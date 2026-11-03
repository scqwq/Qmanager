// Package appservice coordinates source selection, observation, preview and backup.
package appservice

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/discovery"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/mediacleanup"
	"qqarchive/internal/storage"
	"sort"
	"strings"
	"sync"
	"time"
)

type State struct {
	CheckTotal       int                        `json:"check_total"`
	CheckPass        int                        `json:"check_pass"`
	UpdateRunning    bool                       `json:"update_running"`
	BackgroundStage  string                     `json:"background_stage,omitempty"`
	Stopping         bool                       `json:"stopping"`
	UpdateMode       string                     `json:"update_mode"`
	NeedsFull        bool                       `json:"needs_full"`
	Cleanup          *catalog.CleanupJob        `json:"cleanup,omitempty"`
	Restore          *mediabackup.RestoreResult `json:"restore,omitempty"`
	IndexBytes       int64                      `json:"index_bytes"`
	Phase            string                     `json:"phase"`
	Mode             string                     `json:"mode"`
	Source           *media.Source              `json:"source"`
	Sources          []catalog.SavedSource      `json:"sources"`
	Scanned          int                        `json:"scanned"`
	Bytes            int64                      `json:"bytes"`
	Report           *media.Report              `json:"report"`
	Watching         bool                       `json:"watching"`
	Coverage         string                     `json:"coverage"`
	Revision         int                        `json:"revision"`
	Error            string                     `json:"error"`
	Message          string                     `json:"message"`
	Backup           *mediabackup.Result        `json:"backup"`
	Excluded         int                        `json:"excluded"`
	ExcludedItems    []media.Issue              `json:"excluded_items"`
	Cached           bool                       `json:"cached"`
	LastFullScan     time.Time                  `json:"last_full_scan"`
	ReconcileSeconds int                        `json:"reconcile_seconds"`
	Discovery        *discovery.Result          `json:"discovery"`
}
type Media struct {
	appearanceMu      sync.Mutex
	appearanceDraft   *appearanceImage
	discoveryInterval time.Duration
	recycleMedia      func(context.Context, media.Item, string) error
	cleanupQQ         func() (bool, error)
	op                sync.Mutex
	mu                sync.RWMutex
	state             State
	items             []media.Item
	byID              map[string]media.Item
	catalog           *catalog.Catalog
	private           string
	cancel            context.CancelFunc
	done              chan struct{}
	jobCancel         context.CancelFunc
	jobDone           chan struct{}
	previews          chan struct{}
	streamWorkers     chan struct{}
	archiveHistory    map[string][]catalog.History
	server            *http.Server
	url               string
	token             string
}

func NewMedia(c *catalog.Catalog, private string) (*Media, error) {
	sources, err := c.Sources()
	if err != nil {
		return nil, err
	}
	s := &Media{catalog: c, private: private, state: State{Phase: "idle", Mode: "source", Sources: sources}, byID: map[string]media.Item{}, previews: make(chan struct{}, 2), token: rand.Text()}
	s.discoveryInterval = ReconcileInterval
	s.recycleMedia = mediacleanup.Recycle
	s.cleanupQQ = storage.QQRunning
	s.streamWorkers = make(chan struct{}, 4)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.url = "http://" + listener.Addr().String()
	s.server = &http.Server{Handler: http.HandlerFunc(s.stream), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	go s.server.Serve(listener)
	chosen, err := c.Preference("selected_source")
	if err != nil {
		s.server.Close()
		return nil, err
	}
	if chosen == "" && len(sources) > 0 {
		chosen = sources[0].ID
	}
	for _, saved := range sources {
		if saved.ID == chosen {
			if err = s.activate(savedMedia(saved), sources); err != nil {
				s.server.Close()
				return nil, err
			}
			break
		}
	}
	return s, nil
}
func (s *Media) busy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.jobCancel != nil {
		return true
	}
	return s.state.Phase == "preparing_backup" || s.state.Phase == "backup" || s.state.Phase == "verifying" || s.state.Phase == "discovering" || s.state.Phase == "restoring" || s.state.Phase == "deleting" || s.state.Phase == "preparing_delete" || s.state.Phase == "recycling"
}
func (s *Media) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
		s.cancel = nil
		s.done = nil
	}
}
func (s *Media) Select(path string) (media.Source, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return media.Source{}, errors.New("请等待当前任务结束")
	}
	s.stopLocked()
	source, err := media.Resolve(path)
	if err != nil {
		return source, err
	}
	if media.Overlap(source.Resolved, s.private) {
		return source, errors.New("媒体来源不能与应用数据重叠")
	}
	saved, err := s.catalog.Add(source.Resolved, chooseName(source.Resolved))
	if err != nil {
		return source, err
	}
	source.ID = saved.ID
	source.Name = saved.Name
	sources, err := s.catalog.Sources()
	if err != nil {
		return source, err
	}
	return source, s.activate(source, sources)
}
func (s *Media) Switch(id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("请等待当前任务结束")
	}
	s.stopLocked()
	sources, err := s.catalog.Sources()
	if err != nil {
		return err
	}
	for _, source := range sources {
		if source.ID == id {
			return s.activate(savedMedia(source), sources)
		}
	}
	return errors.New("未找到保存的账号来源")
}
func (s *Media) Rename(name string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.RLock()
	source := s.state.Source
	s.mu.RUnlock()
	if source == nil {
		return errors.New("请先选择来源")
	}
	if err := s.catalog.Rename(source.ID, name); err != nil {
		return err
	}
	sources, err := s.catalog.Sources()
	if err != nil {
		return err
	}
	s.mu.Lock()
	copySource := *source
	copySource.Name = name
	s.state.Source = &copySource
	s.state.Sources = sources
	s.mu.Unlock()
	return nil
}
func (s *Media) Start(parent context.Context) error {
	return s.StartUpdate(parent, "full")
}
func (s *Media) StartUpdate(parent context.Context, mode string) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	if mode != "fast" && mode != "full" {
		return errors.New("请选择快速更新或全盘更新")
	}
	s.stopLocked()
	s.mu.Lock()
	if s.state.Source == nil || s.state.Mode != "source" {
		s.mu.Unlock()
		return errors.New("请先选择媒体目录")
	}
	source := *s.state.Source
	fresh, err := media.ResolveForUpdate(source.Resolved)
	if err != nil {
		s.mu.Unlock()
		return errors.New("源目录暂不可访问，仍可浏览缓存记录")
	}
	if !strings.EqualFold(fresh.Resolved, source.Resolved) {
		s.mu.Unlock()
		return errors.New("来源真实路径变化，请重新选择核实")
	}
	fresh.ID = source.ID
	fresh.Name = source.Name
	source = fresh
	s.state.Source = &fresh
	s.state.Phase = "scanning"
	s.state.Error = ""
	s.state.Message = ""
	s.state.UpdateMode = mode
	s.state.UpdateRunning = true
	s.state.BackgroundStage = ""
	s.state.Stopping = false
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.observe(ctx, source, s.done, mode)
	return nil
}
func (s *Media) Stop() { s.op.Lock(); defer s.op.Unlock(); s.stopLocked() }

// StopUpdate cancels observation only; it never cancels a backup or cleanup job.
func (s *Media) StopUpdate() error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前正在执行其他任务，请使用该任务的取消按钮")
	}
	s.mu.Lock()
	s.state.Stopping = true
	s.mu.Unlock()
	s.stopLocked()
	s.mu.Lock()
	s.state.Stopping = false
	s.state.UpdateRunning = false
	s.state.BackgroundStage = ""
	s.state.Watching = false
	s.state.Phase = "ready"
	s.state.Error = ""
	s.state.Message = "更新已终止，已有索引和观察历史已保留；后台监控已暂停"
	s.mu.Unlock()
	return nil
}
func (s *Media) Cancel() {
	s.mu.RLock()
	cancel := s.jobCancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	} else {
		s.Stop()
	}
}
func (s *Media) fail(err error) {
	s.mu.Lock()
	s.state.Error = err.Error()
	s.state.Phase = "ready"
	s.mu.Unlock()
}
func (s *Media) State() State {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	state.IndexBytes = s.catalog.DiskBytes()
	return state
}
func (s *Media) Page(f media.Filter) media.Page {
	f.HideVideoCovers = true
	s.mu.RLock()
	if s.state.Mode == "backup" {
		p := media.Paginate(s.items, f)
		covers := media.MatchVideoCovers(p.Items, s.items)
		for i := range p.Items {
			p.Items[i].CoverID = covers[p.Items[i].ID].ID
		}
		s.mu.RUnlock()
		return p
	}
	source := s.state.Source
	s.mu.RUnlock()
	if source == nil {
		return media.Page{Items: []media.Item{}}
	}
	p, err := s.catalog.Page(context.Background(), source.ID, f)
	if err != nil {
		s.fail(err)
		return media.Page{Items: []media.Item{}}
	}
	covers, err := s.catalog.VideoCovers(context.Background(), source.ID, p.Items)
	if err != nil {
		s.fail(err)
	}
	for i := range p.Items {
		p.Items[i] = media.Bind(p.Items[i], *source)
		p.Items[i].CoverID = covers[p.Items[i].ID].ID
	}
	return p
}
func (s *Media) History(id string) ([]catalog.History, error) {
	item, ok := s.lookup(id)
	s.mu.RLock()
	if s.state.Mode == "backup" && ok {
		h := append([]catalog.History{}, s.archiveHistory[item.InstanceID]...)
		s.mu.RUnlock()
		return h, nil
	}
	s.mu.RUnlock()
	if !ok {
		return nil, errors.New("文件不在当前视图")
	}
	return s.catalog.History(item.InstanceID)
}
func (s *Media) Estimate(f media.Filter) mediabackup.Preview {
	p, _ := s.BackupEstimate(f)
	return p
}
func (s *Media) BackupEstimate(f media.Filter) (mediabackup.Preview, error) {
	if err := media.ValidateFilter(f); err != nil {
		return mediabackup.Preview{}, err
	}
	s.mu.RLock()
	if s.state.Mode == "backup" {
		p := mediabackup.Plan(s.items, f)
		s.mu.RUnlock()
		return p, nil
	}
	source := s.state.Source
	s.mu.RUnlock()
	if source == nil {
		return mediabackup.Preview{}, errors.New("请先选择媒体来源")
	}
	counts, err := s.catalog.BackupPlan(source.ID, f)
	if err != nil {
		return mediabackup.Preview{}, err
	}
	p := mediabackup.Preview{Files: counts.Files, Bytes: counts.Bytes, Pending: counts.Pending, PendingBytes: counts.PendingBytes, ExcludedReasons: counts.ExcludedReasons}
	for _, count := range counts.ExcludedReasons {
		p.Excluded += count
	}
	if p.Files+p.Pending > 0 {
		p.EstimatedPeak = mediabackup.DirectoryBudgetFor(p.Files+p.Pending+p.Excluded, p.Bytes+p.PendingBytes)
	}
	return p, nil
}

func (s *Media) backupDirectory(target string) (string, error) {
	path, err := media.Canonical(target)
	if err != nil {
		return "", errors.New("备份保存目录不可用，请重新选择")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("请选择有效的备份保存目录")
	}
	roots, err := s.protected()
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if media.Overlap(root, path) {
			return "", errors.New("备份保存目录必须与 QQ 数据目录和 Qman 应用数据分开")
		}
	}
	return path, nil
}
func (s *Media) RememberBackupDirectory(target string) (string, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return "", errors.New("请等待当前任务结束后选择目录")
	}
	path, err := s.backupDirectory(target)
	if err != nil {
		return "", err
	}
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	if settings.BackupDirectory == "" {
		settings.BackupDirectory = path
		if _, err = s.saveSettingsLocked(settings); err != nil {
			return "", err
		}
	}
	return path, s.catalog.SetPreference("backup_directory", path)
}
func (s *Media) protected() ([]string, error) {
	settings, err := s.Settings()
	if err != nil {
		return nil, err
	}
	return s.protectedWithDefault(settings.SourceDirectory)
}
func (s *Media) protectedWithDefault(defaultSource string) ([]string, error) {
	sources, err := s.catalog.Sources()
	if err != nil {
		return nil, err
	}
	roots := []string{s.private}
	if defaultSource != "" {
		if _, e := media.Canonical(defaultSource); e != nil {
			return nil, errors.New("默认 QQ 数据路径发生变化，请在设置中重新选择或清除")
		}
		roots = append(roots, defaultSource)
	}
	if restoreRoot := s.Preference("restore_directory"); restoreRoot != "" {
		if _, e := media.Canonical(restoreRoot); e != nil {
			return nil, errors.New("保存的恢复根发生变化，请重新选择核实")
		}
		roots = append(roots, restoreRoot)
	}
	for _, source := range sources {
		if _, err := media.Canonical(source.Root); err != nil {
			return nil, errors.New("已保存的来源路径发生变化，请重新选择并核实后再备份")
		}
		roots = append(roots, source.Root)
	}
	return roots, nil
}
func (s *Media) Backup(parent context.Context, target string, f media.Filter) error {
	return s.BackupFormat(parent, target, f, "directory")
}
func (s *Media) BackupFormat(parent context.Context, target string, f media.Filter, format string) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	if format != "directory" && format != "zip" {
		return errors.New("备份保存形式无效")
	}
	if err := media.ValidateFilter(f); err != nil {
		return err
	}
	target, err := s.backupDirectory(target)
	if err != nil {
		return err
	}
	estimate, err := s.BackupEstimate(f)
	if err != nil {
		return err
	}
	if estimate.Files+estimate.Pending == 0 {
		return errors.New("当前条件没有可核对备份的文件，请查看排除原因或调整筛选")
	}
	protected, err := s.protected()
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.state.Source == nil || s.state.Mode != "source" {
		s.mu.Unlock()
		return errors.New("请先选择媒体来源")
	}
	source := *s.state.Source
	s.mu.Unlock()
	// Invalid output, empty selections and insufficient space must not stop
	// observation: pending files still need their later stability checks.
	budget := estimate.EstimatedPeak
	if format == "zip" {
		budget *= 3
	}
	free, err := storage.Free(target)
	if err != nil || free < uint64(budget) {
		return errors.New("备份创建与校验的临时空间不足，或无法查询目标空间")
	}
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	if settings.BackupDirectory == "" {
		settings.BackupDirectory = target
		if _, err = s.saveSettingsLocked(settings); err != nil {
			return err
		}
	}
	s.stopLocked()
	s.mu.Lock()
	ctx, cancel := context.WithCancel(parent)
	s.jobCancel = cancel
	s.jobDone = make(chan struct{})
	done := s.jobDone
	s.state.Phase = "preparing_backup"
	s.state.Message = ""
	s.state.Error = ""
	s.state.Backup = nil
	s.state.ExcludedItems = nil
	s.state.CheckPass, s.state.CheckTotal = 0, 0
	s.state.Scanned = 0
	s.state.Bytes = 0
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(done)
		var result mediabackup.Result
		items, options, err := s.prepareBackup(ctx, source, f)
		if err == nil {
			s.mu.Lock()
			s.state.Phase = "backup"
			s.state.Scanned, s.state.Bytes = 0, 0
			s.mu.Unlock()
			result, err = mediabackup.Build(ctx, source, items, f, target, protected, s.catalog.History, func(n int, b int64) { s.mu.Lock(); s.state.Scanned = n; s.state.Bytes = b; s.mu.Unlock() }, options)
		}
		if err == nil && format == "zip" {
			zipPath, e := mediabackup.ZipDirectory(ctx, result.Path)
			if e == nil {
				// The directory was freshly created by this job. Only delete its
				// validated object set after the independent ZIP is complete.
				row, re := s.registerBackup(ctx, result.Path)
				if re == nil {
					row.DeletePath = row.Path
					re = mediabackup.DeleteQuarantined(ctx, row)
				}
				if re != nil {
					err = errors.New("ZIP 已创建并保留，目录副本有清理残留；不要将残留视作完整备份")
				}
				result.Path = zipPath
			} else {
				err = e
			}
		}
		if result.Path != "" {
			if _, e := s.registerBackup(context.WithoutCancel(ctx), result.Path); e != nil && err == nil {
				err = errors.New("备份已保存，但登记失败；请保留产物并重新加载")
			}
		}
		if err == nil {
			_ = s.catalog.SetPreference("backup_directory", target)
		}
		summary, summaryErr := s.catalog.Report(source.ID)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.jobCancel = nil
		s.state.Phase = "ready"
		s.state.Revision++
		if summaryErr == nil {
			s.state.Report = summary
		}
		if err != nil {
			s.state.Error = err.Error()
		} else {
			s.state.Backup = &result
			s.state.Message = "备份已完成并通过独立 SHA-256 校验；监控已暂停，可点击快速更新"
		}
	}()
	return nil
}
func (s *Media) OpenBackup(parent context.Context, path string) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	s.stopLocked()
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.jobCancel = cancel
	s.jobDone = make(chan struct{})
	done := s.jobDone
	s.state.Phase = "verifying"
	s.state.Error = ""
	s.state.Scanned = 0
	s.state.Bytes = 0
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(done)
		m, items, err := mediabackup.Open(ctx, path, func(n int, b int64) { s.mu.Lock(); s.state.Scanned = n; s.state.Bytes = b; s.mu.Unlock() })
		if err == nil {
			payload, e := json.Marshal(m)
			if e == nil {
				e = s.catalog.RestoreEvidence(m.ID, payload)
			}
			err = e
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.jobCancel = nil
		s.state.Phase = "ready"
		if err != nil {
			s.state.Error = err.Error()
			return
		}
		s.items = items
		s.archiveHistory = map[string][]catalog.History{}
		s.state.ExcludedItems = []media.Issue{}
		for _, entry := range m.Entries {
			s.archiveHistory[entry.Item.InstanceID] = entry.History
			if entry.Excluded != "" {
				s.state.ExcludedItems = append(s.state.ExcludedItems, media.Issue{Path: entry.Item.RelativePath, Code: entry.Excluded})
			}
		}
		s.byID = map[string]media.Item{}
		for _, item := range items {
			s.byID[item.ID] = item
		}
		source := media.Source{ID: m.SourceID, Name: m.SourceName, Resolved: path}
		s.state.Source = &source
		s.state.Mode = "backup"
		s.state.Revision++
		s.state.Report = report(items)
		s.state.Excluded = len(m.Entries) - len(items)
		s.state.Message = "备份校验通过，已恢复包内保存的观察证据；可在源断开时查看"
		s.state.Coverage = "独立备份 · 不监听源目录"
	}()
	return nil
}
func report(items []media.Item) *media.Report {
	r := &media.Report{Complete: true, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Issues: []media.Issue{}, Buckets: []media.Bucket{}, Files: len(items)}
	b := map[string]*media.Bucket{}
	for _, item := range items {
		r.Bytes += item.Bytes
		k := item.Kind + item.SourceMonth + item.Role
		if b[k] == nil {
			b[k] = &media.Bucket{Kind: item.Kind, Month: item.SourceMonth, Role: item.Role}
		}
		b[k].Files++
		b[k].Bytes += item.Bytes
	}
	keys := []string{}
	for k := range b {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r.Buckets = append(r.Buckets, *b[k])
	}
	return r
}
func (s *Media) ExportIndex(ctx context.Context, target string) (string, error) {
	protected, err := s.protected()
	if err != nil {
		return "", err
	}
	for _, root := range protected {
		if media.Overlap(root, target) {
			return "", errors.New("索引备份目标与来源或应用数据重叠")
		}
	}
	return s.catalog.Export(ctx, target)
}
func (s *Media) Close() {
	s.op.Lock()
	defer s.op.Unlock()
	s.stopLocked()
	s.mu.RLock()
	cancel, done := s.jobCancel, s.jobDone
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
		<-done
	}
	s.server.Close()
	s.catalog.Close()
}
func (s *Media) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/media/thumbnail/") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/media/thumbnail/")
	item, ok := s.lookup(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if media.VideoCoverPath(item) != "" {
		cover, found := s.videoCover(item)
		if !found {
			http.Error(w, "暂时无法预览", http.StatusUnprocessableEntity)
			return
		}
		item = cover
	}
	select {
	case s.previews <- struct{}{}:
		defer func() { <-s.previews }()
	case <-r.Context().Done():
		return
	}
	b, err := media.Thumbnail(r.Context(), item)
	if err != nil {
		http.Error(w, "暂时无法预览，请重新扫描", 422)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(b)
}
func (s *Media) MediaURL(id string) (string, error) {
	item, ok := s.lookup(id)
	if !ok || item.Status != "recognized" {
		return "", errors.New("该文件不可预览")
	}
	if item.Format != "iso-bmff" && item.Format != "png" && item.Format != "jpeg" && item.Format != "gif" {
		return "", errors.New("此格式暂不支持内置预览")
	}
	return s.url + "/" + s.token + "/" + id, nil
}
func (s *Media) stream(w http.ResponseWriter, r *http.Request) {
	select {
	case s.streamWorkers <- struct{}{}:
		defer func() { <-s.streamWorkers }()
	default:
		http.Error(w, "媒体读取繁忙", http.StatusTooManyRequests)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	prefix := "/" + s.token + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	item, ok := s.lookup(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "iso-bmff": "video/mp4"}[item.Format]
	if mime == "" || item.Status != "recognized" {
		http.NotFound(w, r)
		return
	}
	f, err := media.OpenItem(item)
	if err != nil {
		http.Error(w, "文件发生变化，请重新扫描", 409)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, "media", item.ModifiedAt, f)
}
func (s *Media) DataPath() string { return filepath.Join(s.private, "catalog.sqlite") }
func (s *Media) Buckets(view string, includeBaseline ...bool) []media.Bucket {
	s.mu.RLock()
	if s.state.Mode != "backup" {
		source := s.state.Source
		s.mu.RUnlock()
		if source == nil {
			return []media.Bucket{}
		}
		b, err := s.catalog.Buckets(source.ID, view, includeBaseline...)
		if err != nil {
			s.fail(err)
			return []media.Bucket{}
		}
		return b
	}
	defer s.mu.RUnlock()
	items := make([]media.Item, 0, len(s.items))
	for _, item := range s.items {
		if view == "observed" {
			if (item.Method == "baseline" && !(len(includeBaseline) > 0 && includeBaseline[0])) || item.FirstSeenAt.IsZero() {
				continue
			}
			item.SourceMonth = item.FirstSeenAt.Local().Format("2006-01")
		}
		items = append(items, item)
	}
	return report(items).Buckets
}

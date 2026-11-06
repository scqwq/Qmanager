package appservice

import (
	"context"
	"errors"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/discovery"
	"qqarchive/internal/media"
	"strings"
	"time"
)

const ReconcileInterval = 2 * time.Minute

// The cadence now performs light name discovery, never automatic full audits.

func (s *Media) activate(source media.Source, sources []catalog.SavedSource) error {
	cached, err := s.catalog.Report(source.ID)
	if err != nil {
		return err
	}
	if err = s.catalog.SetPreference("selected_source", source.ID); err != nil {
		return err
	}
	last := s.catalog.LastScan(source.ID)
	cleanup, err := s.catalog.LatestCleanup(source.ID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	revision := s.state.Revision + 1
	s.state = State{Phase: "selected", Mode: "source", Source: &source, Sources: sources, Revision: revision, Report: cached, Cached: cached != nil, ReconcileSeconds: int(ReconcileInterval / time.Second), LastFullScan: last}
	s.state.Cleanup = cleanup
	s.state.UpdateMode = "fast"
	if cached != nil {
		s.state.Message = "已从本地索引加载，尚未核对当前源文件；可开始监控补充变化"
	}
	s.items = nil
	s.byID = map[string]media.Item{}
	return nil
}
func (s *Media) Discover(parent context.Context, path string) (discovery.Result, error) {
	s.op.Lock()
	defer s.op.Unlock()
	var result discovery.Result
	if s.busy() {
		return result, errors.New("请等待当前任务结束")
	}
	s.stopLocked()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.mu.Lock()
	s.jobCancel = cancel
	s.jobDone = done
	s.state.Phase = "discovering"
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.jobCancel = nil
		if s.state.Phase == "discovering" {
			s.state.Phase = "selected"
		}
		s.mu.Unlock()
		close(done)
	}()
	result, err := discovery.Find(ctx, path)
	if err != nil {
		return result, err
	}
	existing, err := s.catalog.Sources()
	if err != nil {
		return result, err
	}
	roots := map[string]bool{}
	for _, saved := range existing {
		roots[strings.ToLower(saved.Root)] = true
	}
	count := len(existing)
	for _, candidate := range result.Candidates {
		if media.Overlap(candidate.Root, s.private) {
			return result, errors.New("发现的来源与应用索引目录重叠")
		}
		if !roots[strings.ToLower(candidate.Root)] {
			count++
			roots[strings.ToLower(candidate.Root)] = true
		}
	}
	if count > 32 {
		return result, errors.New("已保存与新发现的来源合计超过 32 个，本次未添加；请直接选择需要的账号目录")
	}
	var first catalog.SavedSource
	for _, candidate := range result.Candidates {
		if media.Overlap(candidate.Root, s.private) {
			return result, errors.New("发现的来源与应用索引目录重叠")
		}
		saved, e := s.catalog.Add(candidate.Root, candidate.Name)
		if e != nil {
			return result, e
		}
		if first.ID == "" {
			first = saved
		}
	}
	sources, err := s.catalog.Sources()
	if err != nil {
		return result, err
	}
	if err = s.activate(savedMedia(first), sources); err != nil {
		return result, err
	}
	s.mu.Lock()
	s.state.Discovery = &result
	s.state.Message = "已发现并保存媒体来源，请在左侧切换账号；首次扫描仅处理当前选择"
	if !result.Complete {
		s.state.Message += "。目录发现存在跳过项或达到边界，可直接选择缺少的账号目录"
	}
	s.mu.Unlock()
	return result, nil
}
func (s *Media) lookup(id string) (media.Item, bool) {
	s.mu.RLock()
	mode := s.state.Mode
	source := s.state.Source
	if mode == "backup" {
		item, ok := s.byID[id]
		s.mu.RUnlock()
		return item, ok
	}
	s.mu.RUnlock()
	if source == nil {
		return media.Item{}, false
	}
	item, err := s.catalog.Item(source.ID, id)
	if err != nil {
		return item, false
	}
	return media.Bind(item, *source), true
}
func (s *Media) snapshot(source media.Source, f media.Filter) ([]media.Item, error) {
	items, err := s.catalog.Selected(context.Background(), source.ID, f)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = media.Bind(items[i], source)
	}
	return items, nil
}
func savedMedia(source catalog.SavedSource) media.Source {
	return media.Source{ID: source.ID, Name: source.Name, Resolved: source.Root, Selected: source.Root}
}
func chooseName(root string) string {
	if filepath.Base(root) == "nt_data" && filepath.Base(filepath.Dir(root)) == "nt_qq" {
		return filepath.Base(filepath.Dir(filepath.Dir(root)))
	}
	return filepath.Base(root)
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

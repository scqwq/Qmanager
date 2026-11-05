package appservice

import (
	"context"
	"errors"
	"path/filepath"
	"qqarchive/internal/discovery"
	"qqarchive/internal/storage"
	"strings"
	"time"
)

const DirectorySearchTimeout = 30 * time.Second

type DirectorySearchResult struct {
	Status     string   `json:"status"`
	Candidates []string `json:"candidates"`
	Complete   bool     `json:"complete"`
}
type directorySearchJob struct {
	id     string
	cancel context.CancelFunc
}

// Timeout/cancel returns promptly even if a filesystem call is still draining.
// The single worker slot is retained until it exits, preventing abandoned-worker
// accumulation. A late result can never save settings or import a source.
func (s *Media) SearchQQDirectory(parent context.Context, id, hint, backupHint string) (DirectorySearchResult, error) {
	if len(hint) > 32767 || len(backupHint) > 32767 {
		return DirectorySearchResult{Candidates: []string{}}, errors.New("目录路径过长，请选择文件夹")
	}
	return s.searchQQDirectory(parent, id, func(ctx context.Context) (discovery.Result, error) {
		opts, err := s.directorySearchOptions(ctx, hint, backupHint)
		if err != nil {
			return discovery.Result{}, err
		}
		return discovery.Search(ctx, opts)
	})
}

func (s *Media) searchQQDirectory(parent context.Context, id string, work func(context.Context) (discovery.Result, error)) (DirectorySearchResult, error) {
	empty := DirectorySearchResult{Candidates: []string{}}
	if id == "" || len(id) > 128 {
		return empty, errors.New("搜索请求无效")
	}
	ctx, cancel := context.WithTimeout(parent, DirectorySearchTimeout)
	job := &directorySearchJob{id, cancel}
	s.searchMu.Lock()
	if until, ok := s.directorySearchCancelled[id]; ok && until.After(time.Now()) {
		delete(s.directorySearchCancelled, id)
		s.searchMu.Unlock()
		cancel()
		empty.Status = "cancelled"
		return empty, nil
	}
	if ctx.Err() != nil {
		s.searchMu.Unlock()
		cancel()
		empty.Status = "cancelled"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			empty.Status = "timeout"
		}
		return empty, nil
	}
	if s.directorySearch != nil {
		s.searchMu.Unlock()
		cancel()
		return empty, errors.New("上次搜索仍在停止，请稍后重试")
	}
	s.directorySearch = job
	s.searchMu.Unlock()
	defer cancel()
	type answer struct {
		result discovery.Result
		err    error
	}
	ready := make(chan answer, 1)
	go func() {
		defer func() {
			s.searchMu.Lock()
			if s.directorySearch == job {
				s.directorySearch = nil
			}
			s.searchMu.Unlock()
		}()
		result, err := work(ctx)
		ready <- answer{result, err}
	}()
	var a answer
	select {
	case <-ctx.Done():
	case a = <-ready:
	}
	if err := ctx.Err(); err != nil {
		empty.Status = "cancelled"
		if errors.Is(err, context.DeadlineExceeded) {
			empty.Status = "timeout"
		}
		return empty, nil
	}
	if a.err != nil {
		return empty, a.err
	}
	result := DirectorySearchResult{Status: "not_found", Complete: a.result.Complete, Candidates: []string{}}
	for _, c := range a.result.Candidates {
		result.Candidates = append(result.Candidates, c.Root)
	}
	if len(result.Candidates) > 0 {
		result.Status = "found"
	}
	return result, nil
}

func (s *Media) CancelQQDirectorySearch(id string) {
	s.searchMu.Lock()
	defer s.searchMu.Unlock()
	if s.directorySearch != nil && (id == s.directorySearch.id || id == "") {
		s.directorySearch.cancel()
		return
	}
	if id == "" || len(id) > 128 {
		return
	}
	// Cancel RPCs may be scheduled before their corresponding start RPCs.
	// Retain a bounded, expiring set so these requests cannot start later.
	if s.directorySearchCancelled == nil {
		s.directorySearchCancelled = map[string]time.Time{}
	}
	now := time.Now()
	for key, until := range s.directorySearchCancelled {
		if !until.After(now) {
			delete(s.directorySearchCancelled, key)
		}
	}
	if len(s.directorySearchCancelled) >= 64 {
		var oldest string
		var first time.Time
		for key, until := range s.directorySearchCancelled {
			if first.IsZero() || until.Before(first) {
				oldest = key
				first = until
			}
		}
		delete(s.directorySearchCancelled, oldest)
	}
	s.directorySearchCancelled[id] = now.Add(DirectorySearchTimeout)
}

func (s *Media) directorySearchOptions(ctx context.Context, hint, backupHint string) (discovery.SearchOptions, error) {
	opts := discovery.SearchOptions{Excluded: []string{s.private, backupHint}}
	if s.busy() {
		return opts, errors.New("请等待当前任务结束后搜索")
	}
	settings, err := s.Settings()
	if err != nil {
		return opts, err
	}
	opts.Excluded = append(opts.Excluded, settings.BackupDirectory, settings.ExportDirectory, s.Preference("backup_directory"))
	paths, err := s.catalog.BackupPaths(ctx)
	if err != nil {
		return opts, err
	}
	opts.Excluded = append(opts.Excluded, paths...)
	opts.Roots = append(opts.Roots, hint, settings.SourceDirectory)
	sources, err := s.catalog.Sources()
	if err != nil {
		return opts, err
	}
	for _, source := range sources {
		opts.Roots = append(opts.Roots, source.Root)
	}
	if docs, err := storage.DocumentsDirectory(); err == nil {
		opts.Roots = append(opts.Roots, filepath.Join(docs, "Tencent Files"))
		opts.Roots = append(opts.Roots, docs)
	} else if hint == "" && settings.SourceDirectory == "" && len(sources) == 0 {
		return opts, errors.New("无法取得常用目录，请选择文件夹")
	}
	// Prefer the known Tencent Files ancestor without probing sibling accounts.
	for i, path := range opts.Roots {
		if !filepath.IsAbs(path) {
			continue
		}
		for p := path; filepath.Dir(p) != p; p = filepath.Dir(p) {
			if strings.EqualFold(filepath.Base(p), "Tencent Files") {
				opts.Roots[i] = p
				break
			}
		}
	}
	return opts, ctx.Err()
}

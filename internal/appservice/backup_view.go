package appservice

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
)

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

type importState struct {
	Manifest mediabackup.Manifest      `json:"manifest"`
	Result   mediabackup.RestoreResult `json:"result"`
}

func (s *Media) ViewLoadedBackup(parent context.Context, id string) error {
	payload, err := s.catalog.Import(id)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("此备份尚未成功加载")
	}
	if err != nil {
		return err
	}
	var saved importState
	if err = json.Unmarshal(payload, &saved); err != nil {
		return err
	}
	result := make(chan error, 1)
	err = s.backupJob(parent, "verifying", func(ctx context.Context) error {
		err := s.showImport(ctx, saved, true)
		result <- err
		return err
	})
	if err != nil {
		return err
	}
	return <-result
}
func (s *Media) showImport(ctx context.Context, saved importState, requireReadable ...bool) error {
	entries := map[string]media.Item{}
	history := map[string][]catalog.History{}
	for _, e := range saved.Manifest.Entries {
		entries[e.Item.InstanceID] = e.Item
		history[e.Item.InstanceID] = e.History
	}
	items := []media.Item{}
	issues := []media.Issue{}
	for _, p := range saved.Result.Placements {
		if err := ctx.Err(); err != nil {
			return err
		}
		item, ok := entries[p.Instance]
		if !ok || p.Root == "" {
			continue
		}
		attached, err := media.AttachObject(item, p.Root, filepath.FromSlash(p.Path))
		if err != nil {
			issues = append(issues, media.Issue{Path: item.RelativePath, Code: "unreadable"})
			continue
		}
		// Revalidate bytes after a new process starts; the old observation evidence
		// is retained but cannot authorize showing a replaced file as this backup.
		root, err := os.OpenRoot(p.Root)
		if err != nil {
			issues = append(issues, media.Issue{Path: item.RelativePath, Code: "unreadable"})
			continue
		}
		same, e := mediabackup.VerifyPlacement(ctx, root, filepath.FromSlash(p.Path), p.SHA256, item.Bytes)
		root.Close()
		if e != nil || !same {
			issues = append(issues, media.Issue{Path: item.RelativePath, Code: "changed"})
			continue
		}
		attached.ID = rand.Text()
		items = append(items, attached)
	}
	if len(requireReadable) > 0 && requireReadable[0] && len(items) == 0 {
		return errors.New("此前加载的媒体已无法读取或校验，请检查恢复位置")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = items
	s.byID = map[string]media.Item{}
	for _, item := range items {
		s.byID[item.ID] = item
	}
	s.archiveHistory = history
	s.state.Source = &media.Source{ID: saved.Manifest.SourceID, Name: saved.Manifest.SourceName, Resolved: "已加载的备份媒体"}
	s.state.Mode = "backup"
	s.state.Report = report(items)
	s.state.ExcludedItems = issues
	s.state.Excluded = len(saved.Manifest.Entries) - len(items)
	s.state.Coverage = "已加载备份 · 不监听源目录"
	s.state.Cached = false
	s.state.Restore = &saved.Result
	return nil
}

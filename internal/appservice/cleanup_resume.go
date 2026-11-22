package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"strings"
)

func (s *Media) PrepareCleanupResume(parent context.Context, id string) error {
	return s.backupJob(parent, "preparing_delete", func(ctx context.Context) error {
		return s.resumeCleanup(ctx, id)
	})
}

func (s *Media) CleanupAttempts(job, instance string) ([]catalog.CleanupAttempt, error) {
	return s.catalog.CleanupAttempts(job, instance)
}

func (s *Media) resumeCleanup(ctx context.Context, id string) error {
	job, err := s.catalog.Cleanup(id)
	if err != nil {
		return err
	}
	sources, err := s.catalog.Sources()
	if err != nil {
		return err
	}
	var source media.Source
	for _, saved := range sources {
		if saved.ID != job.SourceID {
			continue
		}
		if !strings.EqualFold(saved.Root, job.Root) {
			return errors.New("原来源位置已改变，请重新选择")
		}
		source, err = media.ResolveForUpdate(job.Root)
		if err != nil {
			return errors.New("原来源暂不可访问，请连接磁盘后重试")
		}
		source.ID, source.Name = saved.ID, saved.Name
	}
	if source.ID == "" {
		return errors.New("原来源关联已不存在")
	}
	if media.Overlap(source.Resolved, s.private) {
		return errors.New("原来源与应用数据重叠")
	}
	if err = s.activate(source, sources); err != nil {
		return err
	}
	rows, err := s.catalog.CleanupItems(id, 0, media.MaxFiles)
	if err != nil {
		return err
	}
	items := []media.Item{}
	hashes := map[string]string{}
	for _, row := range rows {
		if row.Status == "ready" || row.Status == "pending" || row.Status == "failed" {
			items = append(items, media.Bind(row.Item, source))
			if row.SHA256 != "" {
				hashes[row.Item.InstanceID] = row.SHA256
			}
		}
	}
	if len(items) == 0 {
		return errors.New("没有可继续处理的文件，结果待核对项请先人工核实")
	}
	job, err = s.catalog.ResumeCleanup(ctx, id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Phase = "preparing_delete"
	s.mu.Unlock()
	return s.checkCleanup(ctx, source, job, items, hashes, nil)
}

func (s *Media) resumableBackupCleanup(ids []string) (string, error) {
	unique := map[string]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	var shared string
	for id := range unique {
		row, err := s.catalog.Backup(id)
		if err != nil {
			return "", err
		}
		if row.SourceCleanupJob == "" {
			return "", nil
		}
		if shared != "" && shared != row.SourceCleanupJob {
			return "", nil
		}
		shared = row.SourceCleanupJob
	}
	if shared == "" {
		return "", nil
	}
	job, err := s.catalog.Cleanup(shared)
	if err != nil {
		return "", err
	}
	if job.Archived || len(job.BackupIDs) != len(unique) || (job.State != "interrupted" && job.State != "partial" && job.State != "prepared") {
		return "", nil
	}
	for _, id := range job.BackupIDs {
		if !unique[id] {
			return "", nil
		}
	}
	if job.Ready+job.Pending+job.Failed+job.Uncertain == 0 {
		return "", nil
	}
	return shared, nil
}

func cleanupPresence(item media.Item) (string, string) {
	switch media.Presence(item.Root, item.Path, item.Bytes) {
	case "missing":
		return "missing", "原文件已不存在，已跳过"
	case "unknown":
		return "unavailable", "原位置无法核实，请检查磁盘和访问权限"
	default:
		return "", ""
	}
}

func markCleanupPresence(row *catalog.CleanupItem) {
	reason, detail := cleanupPresence(row.Item)
	if reason == "" {
		return
	}
	row.Reason, row.Detail = reason, detail
	row.Status = "skipped"
	if reason == "unavailable" {
		row.Status = "failed"
	}
}

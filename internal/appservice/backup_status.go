package appservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/mediacleanup"
	"strings"
	"time"
)

func cleanupBackupStatus(job catalog.CleanupJob) *catalog.BackupCleanupStatus {
	return &catalog.BackupCleanupStatus{State: job.State, At: time.Now().UTC(), Joint: len(job.BackupIDs) > 1,
		Recycled: job.Recycled, Skipped: job.Skipped, Failed: job.Failed, Uncertain: job.Uncertain, Remaining: job.Ready + job.Pending}
}

func (s *Media) CheckBackupOriginals(parent context.Context, id string) error {
	return s.backupJob(parent, "verifying", func(ctx context.Context) error {
		row, err := s.catalog.Backup(id)
		if err != nil {
			return err
		}
		status, err := s.inspectOriginalFiles(ctx, row)
		if err != nil {
			return err
		}
		row.OriginalFiles = status
		if row.RestoreResult == nil {
			result, e := s.catalog.LegacyBackupImportStatus(id)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			row.RestoreResult = result
		}
		if row.CleanupResult == nil && row.SourceCleanupJob != "" {
			job, e := s.catalog.Cleanup(row.SourceCleanupJob)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e == nil {
				row.CleanupResult = cleanupBackupStatus(job)
				row.CleanupResult.At = time.Time{} // old operation time is not known
			}
		}
		if err = s.catalog.SaveBackup(row); err != nil {
			return err
		}
		s.mu.Lock()
		s.state.Message = "已核对本份备份对应的原路径；存在和大小检查未校验内容，不代表 QQ 识别或可安全删除"
		s.mu.Unlock()
		return nil
	})
}

func (s *Media) inspectOriginalFiles(ctx context.Context, row catalog.BackupRecord) (*catalog.BackupFileStatus, error) {
	if len(row.Manifest) > 128*1024*1024 || catalog.ManifestDigest([]byte(row.Manifest)) != row.Digest {
		return nil, errors.New("备份历史清单校验不符")
	}
	var manifest mediabackup.Manifest
	if err := json.Unmarshal([]byte(row.Manifest), &manifest); err != nil || manifest.Format != mediabackup.Format || manifest.Version != mediabackup.Version || manifest.ID != row.ID || len(manifest.Entries) > media.MaxFiles {
		return nil, errors.New("历史清单无效或不受支持")
	}
	status := &catalog.BackupFileStatus{State: "unlinked"}
	for _, entry := range manifest.Entries {
		if entry.Excluded == "" {
			status.Total++
		}
	}
	sources, err := s.catalog.Sources()
	if err != nil {
		return nil, err
	}
	var source media.Source
	linked := false
	for _, saved := range sources {
		if saved.ID == manifest.SourceID {
			linked = true
			source, err = media.ResolveForUpdate(saved.Root)
			source.ID = saved.ID
			source.Name = saved.Name
			break
		}
	}
	accessible := false
	if linked && err == nil && !media.Overlap(source.Resolved, s.private) {
		if _, e := media.Canonical(source.Resolved); e == nil {
			if f, e := media.OpenRelative(source.Resolved, "."); e == nil {
				f.Close()
				accessible = true
			}
		}
	}
	if !accessible {
		status.Unknown = status.Total
		if linked {
			status.State = "unknown"
		}
	} else {
		for _, entry := range manifest.Entries {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if entry.Excluded != "" {
				continue
			}
			item := media.Bind(entry.Item, source)
			state := "unknown"
			if filepath.IsLocal(item.Path) && !strings.ContainsAny(item.Path, ":\x00") {
				if e := mediacleanup.Validate(item, source); e == nil {
					state = media.Presence(item.Root, item.Path, item.Bytes)
				}
			}
			switch state {
			case "present":
				status.Present++
			case "missing":
				status.Missing++
			case "changed":
				status.Changed++
			default:
				status.Unknown++
			}
		}
		status.State = "partial"
		if status.Total == 0 {
			status.State = "empty"
		} else if status.Unknown > 0 {
			status.State = "unknown"
		} else if status.Present == status.Total {
			status.State = "present"
		} else if status.Missing == status.Total {
			status.State = "missing"
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	status.CheckedAt = time.Now().UTC()
	return status, nil
}

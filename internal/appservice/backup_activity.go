package appservice

import (
	"context"
	"errors"
	"fmt"
	"qqarchive/internal/catalog"
	"qqarchive/internal/diagnostics"
	"qqarchive/internal/media"
	"time"
)

func (s *Media) backupEvent(row catalog.BackupRecord, action, detail string) {
	if err := s.catalog.AddBackupEvent(row.ID, row.Digest, catalog.BackupEvent{Action: action, Detail: detail}); err != nil {
		diagnostics.Record(context.Background(), "error", "backup.history_write_failed", "未能保存备份操作记录")
	}
}

func (s *Media) BackupEvents(id string, offset int) ([]catalog.BackupEvent, error) {
	if offset < 0 {
		return nil, errors.New("无效的记录页码")
	}
	row, err := s.catalog.Backup(id)
	if err != nil {
		return nil, err
	}
	return s.catalog.BackupEvents(id, row.Digest, offset)
}

func (s *Media) BackupLogIndex() ([]catalog.BackupRecord, error) { return s.catalog.Backups() }

func (s *Media) InspectBackup(ctx context.Context, id string, explicit ...bool) (catalog.BackupRecord, error) {
	verify := len(explicit) > 0 && explicit[0]
	if verify {
		s.op.Lock()
		defer s.op.Unlock()
		if s.busy() {
			return catalog.BackupRecord{}, errors.New("请等待当前任务结束后检查备份")
		}
		ctx = media.WithProgress(ctx, func(progress media.TaskProgress) {
			s.mu.Lock()
			s.state.Verification = &progress
			s.mu.Unlock()
		})
		media.StartProgress(ctx, "verifying", 0)
		defer func() {
			s.mu.Lock()
			s.state.Verification = nil
			s.mu.Unlock()
		}()
	}
	row, err := s.catalog.Backup(id)
	if err != nil {
		return row, err
	}
	if verify {
		if _, err = s.verifyBackupLocation(ctx, &row); err != nil {
			return row, err
		}
	} else {
		s.backupLocationWithCheck(&row)
	}
	if ctx.Err() != nil && verify {
		row.Display, err = s.catalog.BackupDisplay(id, row.Digest)
		return row, err
	}
	row.OriginalFiles, err = s.inspectOriginalFiles(ctx, row)
	if err != nil {
		if ctx.Err() != nil {
			return row, ctx.Err()
		}
		row.OriginalFiles = &catalog.BackupFileStatus{State: "unknown", CheckedAt: time.Now().UTC()}
	}
	row.Display, err = s.catalog.BackupDisplay(id, row.Digest)
	return row, err
}

func (s *Media) MarkBackup(id string, marks map[string]string) (catalog.BackupDisplay, error) {
	if len(marks) == 0 || len(marks) > 3 {
		return catalog.BackupDisplay{}, errors.New("请选择需要标注的项目")
	}
	for key, value := range marks {
		if (key != "package" && key != "original" && key != "cloud") || (value != "present" && value != "missing") {
			return catalog.BackupDisplay{}, errors.New("无效的状态标注")
		}
	}
	row, err := s.catalog.Backup(id)
	if err != nil {
		return catalog.BackupDisplay{}, err
	}
	labels := map[string]string{"package": "备份包", "original": "本地源文件", "cloud": "云盘文件"}
	detail := ""
	for _, key := range []string{"package", "original", "cloud"} {
		if value := marks[key]; value != "" {
			label := "√"
			if value == "missing" {
				label = "×"
			}
			if detail != "" {
				detail += " · "
			}
			detail += labels[key] + " " + label
		}
	}
	now := time.Now().UTC()
	return s.catalog.UpdateBackupDisplay(id, row.Digest, func(d *catalog.BackupDisplay) {
		if d.Marks == nil {
			d.Marks = map[string]catalog.BackupMark{}
		}
		for key, value := range marks {
			d.Marks[key] = catalog.BackupMark{State: value, At: now}
		}
	}, catalog.BackupEvent{At: now, Action: "手动标注", Detail: detail})
}

func (s *Media) RecordBackupInspection(row catalog.BackupRecord, cloud *catalog.BackupCloud) (catalog.BackupDisplay, error) {
	detail := "已检查备份包、原文件和已登记的云端对象"
	if row.OriginalFiles != nil {
		r := row.OriginalFiles
		detail = fmt.Sprintf("本地源文件：存在 %d，缺失 %d，变化 %d，未知 %d", r.Present, r.Missing, r.Changed, r.Unknown)
	}
	return s.catalog.UpdateBackupDisplay(row.ID, row.Digest, func(d *catalog.BackupDisplay) {
		now := time.Now().UTC()
		clearMark := func(key string, checked time.Time) {
			if mark, ok := d.Marks[key]; ok && !mark.At.After(checked) {
				delete(d.Marks, key)
			}
		}
		if row.LocationCheckedAt != nil && (row.LocalState == "present" || row.LocalState == "missing" || row.LocalState == "damaged") {
			clearMark("package", *row.LocationCheckedAt)
		}
		if r := row.OriginalFiles; r != nil && (r.State == "present" || r.State == "missing" || r.State == "partial") {
			clearMark("original", r.CheckedAt)
		}
		if cloud != nil && d.Cloud != nil && d.Cloud.FileID == cloud.FileID && d.Cloud.FolderID == cloud.FolderID && d.Cloud.AccountID == cloud.AccountID && !d.Cloud.CheckedAt.After(cloud.CheckedAt) {
			d.Cloud = cloud
			if cloud.State != "unknown" {
				clearMark("cloud", now)
			}
		}
	}, catalog.BackupEvent{Action: "刷新文件状态", Detail: detail})
}

func (s *Media) SaveBackupUpload(id, digest string, cloud *catalog.BackupCloud, detail string) error {
	_, err := s.catalog.UpdateBackupDisplay(id, digest, func(d *catalog.BackupDisplay) {
		if cloud != nil {
			d.Cloud = cloud
		}
	}, catalog.BackupEvent{Action: "上传到123云盘", Detail: detail})
	return err
}

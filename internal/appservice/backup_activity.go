package appservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/diagnostics"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
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

func (s *Media) InspectBackup(ctx context.Context, id string) (catalog.BackupRecord, error) {
	row, err := s.catalog.Backup(id)
	if err != nil {
		return row, err
	}
	observeBackupLocation(&row)
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
		if row.LocationCheckedAt != nil && (row.LocalState == "present" || row.LocalState == "missing") {
			clearMark("package", *row.LocationCheckedAt)
		}
		if r := row.OriginalFiles; r != nil && (r.State == "present" || r.State == "missing" || r.State == "partial") {
			clearMark("original", r.CheckedAt)
		}
		if cloud != nil && d.Cloud != nil && d.Cloud.FileID == cloud.FileID && d.Cloud.AccountID == cloud.AccountID && !d.Cloud.CheckedAt.After(cloud.CheckedAt) {
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

func (s *Media) PrepareBackupUpload(ctx context.Context, id string) (catalog.BackupRecord, error) {
	row, err := s.catalog.Backup(id)
	if err != nil {
		return row, err
	}
	observeBackupLocation(&row)
	if row.LocalState != "present" || row.DeletePath != "" || row.Status == "deleted" {
		return row, errors.New("备份包不可用，请核对位置或重新定位")
	}
	return row, ctx.Err()
}

func (s *Media) BackupUploadFile(ctx context.Context, selected catalog.BackupRecord) (string, os.FileInfo, func(), error) {
	// Resolve by registry ID again and never authorize a path from the frontend.
	row, err := s.PrepareBackupUpload(ctx, selected.ID)
	if err != nil {
		return "", nil, nil, err
	}
	if row.Path != selected.Path || row.Digest != selected.Digest {
		return "", nil, nil, errors.New("备份登记已变化，请重新选择")
	}
	root, err := media.Canonical(row.Path)
	if err != nil || root != row.Path {
		return "", nil, nil, errors.New("备份路径无法核实")
	}
	protected, err := s.protected()
	if err != nil {
		return "", nil, nil, err
	}
	for _, path := range protected {
		if media.Overlap(root, path) {
			return "", nil, nil, errors.New("备份位置与应用数据或 QQ 来源重叠")
		}
	}
	before, err := os.Lstat(root)
	if err != nil {
		return "", nil, nil, err
	}
	temporary := filepath.Join(s.private, "temporary")
	if _, err = media.Canonical(temporary); err != nil {
		return "", nil, nil, errors.New("备份暂存目录无法核实")
	}
	if err = os.MkdirAll(temporary, 0700); err != nil {
		return "", nil, nil, err
	}
	path := root
	cleanup := func() {}
	if row.Format == "directory" {
		dir, e := os.MkdirTemp(temporary, "backup-upload-")
		if e != nil {
			return "", nil, nil, e
		}
		path = filepath.Join(dir, filepath.Base(root)+".zip")
		cleanup = func() {
			_ = os.Remove(path)
			_ = os.Remove(dir)
		}
		if _, e = mediabackup.ZipDirectoryTo(ctx, root, path); e != nil {
			cleanup()
			return "", nil, nil, e
		}
	}
	fail := func(e error) (string, os.FileInfo, func(), error) {
		cleanup()
		return "", nil, nil, e
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fail(errors.New("未找到可上传的备份 ZIP"))
	}
	pack, err := mediabackup.OpenPackage(ctx, path, temporary, nil)
	if err != nil {
		return fail(err)
	}
	pack.Cleanup()
	if pack.Manifest.ID != row.ID || catalog.ManifestDigest(pack.Bytes) != row.Digest {
		return fail(errors.New("备份内容与登记清单不符，未上传"))
	}
	source, e := os.Lstat(root)
	if e != nil || !os.SameFile(source, before) || !source.ModTime().Equal(before.ModTime()) || source.Size() != before.Size() {
		return fail(errors.New("备份在校验期间已移动或变化，未上传"))
	}
	after, e := os.Lstat(path)
	if e != nil || !os.SameFile(after, info) || !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		return fail(errors.New("待上传文件已移动或变化"))
	}
	return path, info, cleanup, nil
}

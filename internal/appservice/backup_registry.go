package appservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
)

type BackupPreview struct {
	PreviousPath  string               `json:"previous_path,omitempty"`
	Ticket        string               `json:"ticket,omitempty"`
	Record        catalog.BackupRecord `json:"record"`
	RestorePrefix string               `json:"restore_prefix"`
	Target        string               `json:"target"`
	TargetNote    string               `json:"target_note,omitempty"`
}

func (s *Media) PreviewHistoryBackup(ctx context.Context, id string) (BackupPreview, error) {
	row, err := s.catalog.Backup(id)
	if err != nil {
		return BackupPreview{}, err
	}
	preview, e := s.previewBackup(ctx, row.Path, row.Format != "zip", row.ID)
	preview.PreviousPath = ""
	if e != nil {
		s.op.Lock()
		current, lookupErr := s.catalog.Backup(id)
		if lookupErr == nil && current.DeletePath == "" && current.Status != "deleted" {
			current.Status = "unavailable"
			current.Detail = e.Error()
			_ = s.catalog.SaveBackup(current)
		}
		s.op.Unlock()
	}
	return preview, e
}

func (s *Media) registerBackup(ctx context.Context, path string, expectedID ...string) (catalog.BackupRecord, error) {
	row, err := s.inspectBackupPackage(ctx, path, false, expectedID...)
	if err != nil {
		return row, err
	}
	return row, s.catalog.SaveBackup(row)
}

func (s *Media) inspectBackupPackage(ctx context.Context, path string, previewOnly bool, expectedID ...string) (catalog.BackupRecord, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return catalog.BackupRecord{}, err
	}
	if !info.IsDir() {
		var metadata mediabackup.ZIPMetadata
		if previewOnly {
			metadata, err = mediabackup.PreviewZIP(ctx, path)
		} else {
			var verified mediabackup.ZIPVerification
			verified, err = mediabackup.VerifyZIP(ctx, path)
			metadata = verified.ZIPMetadata
		}
		if err != nil {
			return catalog.BackupRecord{}, err
		}
		return backupRecordFromManifest(path, "zip", metadata.Info.Size(), metadata.Manifest, metadata.Bytes, expectedID...)
	}
	pack, err := mediabackup.OpenPackage(ctx, path, filepath.Join(s.private, "temporary"), nil)
	if err != nil {
		return catalog.BackupRecord{}, err
	}
	defer pack.Cleanup()
	return backupRecordFromManifest(path, "directory", pack.MetadataBytes, pack.Manifest, pack.Bytes, expectedID...)
}

func backupRecordFromManifest(path, format string, size int64, m mediabackup.Manifest, encoded []byte, expectedID ...string) (catalog.BackupRecord, error) {
	if len(expectedID) > 0 && expectedID[0] != "" && m.ID != expectedID[0] {
		return catalog.BackupRecord{}, errors.New("所选备份编号与历史不符，未替换或登记其他备份")
	}
	selection, _ := json.Marshal(m.Selection)
	row := catalog.BackupRecord{
		ID: m.ID, Name: m.SourceName, Path: path, Format: format, CreatedAt: m.CreatedAt,
		Bytes: size, Selection: selection, Manifest: string(encoded),
		Digest: catalog.ManifestDigest(encoded), Status: "available",
	}
	for _, entry := range m.Entries {
		if entry.Excluded == "" {
			row.Files++
			if format == "directory" {
				row.Bytes += entry.Item.Bytes
			}
		}
	}
	return row, nil
}
func (s *Media) Backups() ([]catalog.BackupRecord, error) {
	rows, err := s.catalog.Backups()
	if err != nil {
		return nil, err
	}
	for n := range rows {
		s.backupLocationWithCheck(&rows[n])
	}
	return rows, nil
}

func (s *Media) PreviewBackup(parent context.Context, path string, expectedID ...string) (BackupPreview, error) {
	return s.previewBackup(parent, path, true, expectedID...)
}

func (s *Media) PreviewExternalBackup(parent context.Context, path, expectedID string) (BackupPreview, error) {
	return s.previewBackup(parent, path, false, expectedID)
}

func (s *Media) previewBackup(parent context.Context, path string, register bool, expectedID ...string) (BackupPreview, error) {
	s.op.Lock()
	defer s.op.Unlock()
	var preview BackupPreview
	if s.busy() {
		return preview, errors.New("当前任务尚未结束")
	}
	s.stopLocked()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.mu.Lock()
	s.jobCancel = cancel
	s.jobDone = done
	s.state.Phase = "verifying"
	s.state.Error = ""
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.jobCancel = nil
		s.state.Phase = "ready"
		s.mu.Unlock()
		close(done)
	}()
	path, err := media.Canonical(path)
	if err != nil {
		return preview, err
	}
	// Reject treating application-owned imports or live source trees as a
	// deletable backup package before registering anything.
	protected, err := s.protected()
	if err != nil {
		return preview, err
	}
	for _, root := range protected {
		if media.Overlap(root, path) {
			return preview, errors.New("备份必须与 QQ 来源和应用私有目录分离")
		}
	}
	row, err := s.inspectBackupPackage(ctx, path, !register, expectedID...)
	if err != nil {
		return preview, err
	}
	previous, err := s.catalog.Backup(row.ID)
	if err == nil {
		if previous.Digest != row.Digest {
			return preview, errors.New("同一备份编号对应不同清单，拒绝合并")
		}
		if !register && previous.DeletePath != "" {
			return preview, errors.New("该备份有未完成的删除任务，请先处理")
		}
		if !register {
			preview.PreviousPath = previous.Path
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return preview, err
	}
	if register {
		if err = s.catalog.SaveBackup(row); err != nil {
			return preview, err
		}
	}
	var m mediabackup.Manifest
	_ = json.Unmarshal([]byte(row.Manifest), &m)
	preview.Record, preview.RestorePrefix = row, m.RestorePrefix
	if m.RestorePrefix != "" {
		preview.Target, preview.TargetNote, err = s.RestoreDirectory()
		if err != nil {
			return BackupPreview{}, err
		}
	}
	return preview, nil
}

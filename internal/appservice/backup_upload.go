package appservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/storage"
)

func (s *Media) PrepareBackupUpload(ctx context.Context, id string) (catalog.BackupRecord, error) {
	if !s.catalog.StorageReady() {
		return catalog.BackupRecord{}, errors.New("应用记录迁移尚未完成")
	}
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

func (s *Media) backupUploadLocation(ctx context.Context, selected catalog.BackupRecord) (catalog.BackupRecord, string, error) {
	// Resolve by registry ID again and never authorize a path from the frontend.
	row, err := s.PrepareBackupUpload(ctx, selected.ID)
	if err != nil {
		return row, "", err
	}
	if row.Path != selected.Path || row.Digest != selected.Digest || row.Format != selected.Format {
		return row, "", errors.New("备份登记已变化，请重新选择")
	}
	root, err := media.Canonical(row.Path)
	if err != nil || root != row.Path {
		return row, "", errors.New("备份路径无法核实")
	}
	protected, err := s.protected()
	if err != nil {
		return row, "", err
	}
	for _, path := range protected {
		if media.Overlap(root, path) {
			return row, "", errors.New("备份位置与应用数据或 QQ 来源重叠")
		}
	}
	return row, root, nil
}

func (s *Media) BackupUploadMembers(ctx context.Context, selected catalog.BackupRecord) (string, map[string]string, error) {
	row, root, err := s.backupUploadLocation(ctx, selected)
	if err != nil {
		return "", nil, err
	}
	checked, err := mediabackup.InspectSplit(ctx, root, true)
	if err != nil {
		return "", nil, err
	}
	if checked.Manifest.ID != row.ID || catalog.ManifestDigest(checked.Bytes) != row.Digest {
		return "", nil, errors.New("分包备份内容与登记清单不符")
	}
	files, err := mediabackup.SplitUploadFiles(ctx, root, checked.Manifest)
	return root, files, err
}

func (s *Media) BackupUploadFile(ctx context.Context, selected catalog.BackupRecord) (string, os.FileInfo, func(), error) {
	row, root, err := s.backupUploadLocation(ctx, selected)
	if err != nil {
		return "", nil, nil, err
	}
	before, err := os.Lstat(root)
	if err != nil {
		return "", nil, nil, err
	}
	path := root
	cleanup := func() {}
	var verified mediabackup.ZIPVerification
	if row.Format == "directory" {
		temporary := filepath.Join(s.private, "temporary")
		if _, err = media.Canonical(temporary); err != nil {
			return "", nil, nil, errors.New("备份暂存目录无法核实")
		}
		if err = os.MkdirAll(temporary, 0700); err != nil {
			return "", nil, nil, err
		}
		free, e := storage.Free(temporary)
		if e != nil {
			return "", nil, nil, errors.New("无法核对临时 ZIP 的可用空间")
		}
		if row.Bytes > 0 && free <= uint64(row.Bytes) {
			return "", nil, nil, fmt.Errorf("生成临时 ZIP 空间不足（媒体 %d MiB，暂存盘可用 %d MiB）", (row.Bytes+(1<<20)-1)>>20, free>>20)
		}
		dir, e := os.MkdirTemp(temporary, "backup-upload-")
		if e != nil {
			return "", nil, nil, e
		}
		path = filepath.Join(dir, filepath.Base(root)+".zip")
		cleanup = func() {
			_ = os.Remove(path)
			_ = os.Remove(dir)
		}
		verified, err = mediabackup.WriteVerifiedZIP(ctx, root, path)
	} else {
		verified, err = mediabackup.VerifyZIP(ctx, path)
	}
	fail := func(e error) (string, os.FileInfo, func(), error) {
		cleanup()
		return "", nil, nil, e
	}
	if err != nil {
		return fail(err)
	}
	if verified.Manifest.ID != row.ID || catalog.ManifestDigest(verified.Bytes) != row.Digest {
		return fail(errors.New("备份内容与登记清单不符，未上传"))
	}
	source, e := os.Lstat(root)
	if e != nil || !os.SameFile(source, before) || !source.ModTime().Equal(before.ModTime()) || source.Size() != before.Size() {
		return fail(errors.New("备份在校验期间已移动或变化，未上传"))
	}
	after, e := os.Lstat(path)
	if e != nil || !after.Mode().IsRegular() || !os.SameFile(after, verified.Info) || !after.ModTime().Equal(verified.Info.ModTime()) || after.Size() != verified.Info.Size() {
		return fail(errors.New("待上传文件已移动或变化"))
	}
	return path, verified.Info, cleanup, nil
}

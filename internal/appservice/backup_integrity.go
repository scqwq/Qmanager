package appservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/storage"
	"strings"
	"time"
)

func (s *Media) backupLocationWithCheck(row *catalog.BackupRecord) {
	observeBackupLocation(row)
	if check := row.PackageCheck; check != nil && check.Path == row.Path && check.DeletePath == row.DeletePath &&
		(row.LocalState == "present" || row.LocalState == "pending") {
		row.LocalState, row.LocalDetail, row.LocationCheckedAt = check.State, check.Detail, &check.At
	}
}

// Caller holds the operation lock or is the active deletion worker. Restoration
// only publishes an independently verified package from its journalled sibling.
func (s *Media) verifyBackupLocation(ctx context.Context, row *catalog.BackupRecord) (bool, error) {
	observeBackupLocation(row)
	path := row.Path
	if row.DeletePath != "" {
		if filepath.Dir(row.DeletePath) != filepath.Dir(path) || !strings.HasPrefix(filepath.Base(row.DeletePath), ".qman-delete-") {
			return false, errors.New("删除暂存记录无效")
		}
		path = row.DeletePath
		if _, err := os.Lstat(path); os.IsNotExist(err) && row.LocalState == "pending" {
			// A previous restore may have renamed the package before saving the journal.
			path = row.Path
		}
	}
	restored := false
	if row.LocalState == "present" || row.LocalState == "pending" {
		pinned, err := media.OpenRelative(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			row.LocalState, row.LocalDetail = "unavailable", "备份暂不可读取"
		} else {
			before, statErr := pinned.Stat()
			pinned.Close()
			pack, checkErr := mediabackup.OpenPackage(ctx, path, filepath.Join(s.private, "temporary"), nil)
			if pack != nil {
				if catalog.ManifestDigest(pack.Bytes) != row.Digest {
					checkErr = errors.New("备份与登记清单不符")
				}
				pack.Cleanup()
			}
			current, currentErr := os.Lstat(path)
			var ioErr *os.PathError
			switch {
			case ctx.Err() != nil || statErr != nil || currentErr != nil || !os.SameFile(before, current) ||
				before.Size() != current.Size() || !before.ModTime().Equal(current.ModTime()):
				row.LocalState, row.LocalDetail = "unavailable", "本次未能完成备份校验"
			case (errors.As(checkErr, &ioErr) && !errors.Is(checkErr, os.ErrNotExist)) || errors.Is(checkErr, mediabackup.ErrPackageSpace):
				row.LocalState, row.LocalDetail = "unavailable", "备份暂不可读取或校验空间不足"
			case checkErr != nil:
				row.LocalState, row.LocalDetail = "damaged", "备份包不完整或校验不符"
			case row.DeletePath == "":
				row.LocalState, row.LocalDetail = "present", "备份完整，已通过校验"
			default:
				row.LocalState, row.LocalDetail = "pending", "备份完整，未能恢复原位置"
				if path != row.Path {
					roots, e := s.protected()
					if e != nil {
						break
					}
					for _, root := range roots {
						if media.Overlap(row.Path, root) || media.Overlap(path, root) {
							return false, errors.New("备份位置与媒体来源或私有目录重叠")
						}
					}
					if _, e = os.Lstat(row.Path); e == nil {
						row.LocalDetail = "备份完整，原位置被占用"
						break
					} else if !os.IsNotExist(e) {
						break
					}
					checked, e := os.Lstat(path)
					if e != nil || !os.SameFile(current, checked) {
						break
					}
					if _, e = media.Canonical(path); e != nil {
						break
					}
					if e = storage.Publish(path, row.Path); e != nil {
						break
					}
					restored = true
				}
				row.DeletePath, row.Status, row.Detail = "", "available", "备份完整，已恢复原位置"
				row.LocalState, row.LocalDetail = "present", row.Detail
			}
		}
	} else if row.LocalState == "changed" {
		row.LocalState, row.LocalDetail = "damaged", "备份包类型与登记不符"
	}
	now := time.Now().UTC()
	row.LocationCheckedAt = &now
	row.PackageCheck = &catalog.BackupPackageCheck{Path: row.Path, DeletePath: row.DeletePath, State: row.LocalState, Detail: row.LocalDetail, At: now}
	return restored, s.catalog.SaveBackup(*row)
}

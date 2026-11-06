package appservice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"strings"
	"time"
)

type DeleteResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (s *Media) DeleteBackups(parent context.Context, ids []string) error {
	if len(ids) == 0 || len(ids) > 100 {
		return errors.New("每次请选择 1～100 份备份")
	}
	return s.backupJob(parent, "deleting", func(ctx context.Context) error {
		protected, err := s.protected()
		if err != nil {
			return err
		}
		failures, removed, absent, restored, retained, damaged, interrupted := 0, 0, 0, 0, 0, 0, 0
		seen := map[string]bool{}
		total := map[string]bool{}
		for _, id := range ids {
			total[id] = true
		}
		defer func() {
			summary := fmt.Sprintf("已永久删除 %d 份，尚未处理 %d 份。", removed, len(total)-len(seen))
			for _, count := range []struct {
				label string
				value int
			}{
				{"已恢复原位", restored}, {"完整保留", retained}, {"备份不完整", damaged}, {"删除中断", interrupted}, {"位置已缺失", absent}, {"未能删除", failures},
			} {
				if count.value > 0 {
					summary += fmt.Sprintf("%s %d 份。", count.label, count.value)
				}
			}
			s.mu.Lock()
			s.state.OperationResult = summary
			s.mu.Unlock()
		}()
		for i, id := range ids {
			if err = ctx.Err(); err != nil {
				return err
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			row, e := s.catalog.Backup(id)
			if e != nil {
				return e
			}
			if row.Status == "deleted" {
				observeBackupLocation(&row)
				if row.LocalState == "missing" {
					absent++
					continue
				}
			}
			row.PackageCheck = nil
			row.LocalState = ""
			e = s.deleteBackup(ctx, &row, protected)
			if e != nil && ctx.Err() != nil {
				row.Status, row.Detail = "delete_failed", "删除中断"
				check, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
				recovered, checkErr := s.verifyBackupLocation(check, &row)
				cancel()
				switch {
				case checkErr != nil:
					interrupted++
				case recovered:
					restored++
				case row.LocalState == "present":
					retained++
				case row.LocalState == "damaged":
					damaged++
				default:
					interrupted++
				}
				if checkErr != nil {
					return checkErr
				}
				if _, checkErr = s.RecordBackupInspection(row, nil); checkErr != nil {
					return checkErr
				}
				s.backupEvent(row, "删除本地备份包", row.LocalDetail)
				return ctx.Err()
			}
			if e != nil {
				row.Status = "delete_failed"
				row.Detail = e.Error()
				failures++
			} else {
				row.Status = "deleted"
				if row.LocalState == "missing" {
					absent++
					row.Detail = "登记位置已无备份包，本次未执行磁盘删除；若已移动可重新定位。历史保留，未清理原文件或独立副本"
				} else {
					removed++
					row.Detail = "本地备份包已删除；本操作未连带清理源文件或已恢复副本"
				}
				row.DeletePath = ""
			}
			if err = s.catalog.SaveBackup(row); err != nil {
				return err
			}
			s.backupEvent(row, "删除本地备份包", row.Detail)
			s.backupProgress(i+1, 0)
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		s.state.Message = fmt.Sprintf("备份删除操作结束，%d 项未完成；逐项结果已保存在备份历史", failures)
		s.mu.Unlock()
		if failures > 0 {
			return fmt.Errorf("%d 份备份未删除，请查看备份历史", failures)
		}
		return nil
	})
}
func (s *Media) deleteBackup(ctx context.Context, row *catalog.BackupRecord, protected []string) error {
	path, err := media.Canonical(row.Path)
	if err != nil {
		return err
	}
	for _, root := range protected {
		if media.Overlap(root, path) {
			return errors.New("备份位置与 QQ 来源或应用私有目录重叠，拒绝删除")
		}
	}
	if row.DeletePath != "" {
		if filepath.Dir(row.DeletePath) != filepath.Dir(path) || !strings.HasPrefix(filepath.Base(row.DeletePath), ".qman-delete-") {
			return errors.New("删除日志无效")
		}
		if _, e := media.Canonical(row.DeletePath); e != nil {
			return e
		}
		if _, e := os.Lstat(row.DeletePath); os.IsNotExist(e) {
			if e = accessibleBackupParent(row.DeletePath); e != nil {
				return e
			}
			if _, originalErr := os.Lstat(path); originalErr == nil {
				row.DeletePath = ""
			} else if !os.IsNotExist(originalErr) {
				return originalErr
			} else {
				row.LocalState = "missing"
				return nil
			}
		}
	}
	if row.DeletePath == "" {
		pinned, e := media.OpenRelative(filepath.Dir(path), filepath.Base(path))
		if os.IsNotExist(e) {
			if e = accessibleBackupParent(path); e != nil {
				return e
			}
			row.LocalState = "missing"
			return nil
		}
		if e != nil {
			return e
		}
		defer pinned.Close()
		info, e := pinned.Stat()
		if e != nil {
			return e
		}
		if row.Format == "directory" && !info.IsDir() || row.Format == "zip" && !info.Mode().IsRegular() {
			return errors.New("备份位置对象类型已变化，请重新定位并校验")
		}
		pack, e := mediabackup.OpenPackage(ctx, path, filepath.Join(s.private, "temporary"), nil)
		if e != nil {
			return e
		}
		digest := catalog.ManifestDigest(pack.Bytes)
		pack.Cleanup()
		if digest != row.Digest {
			return errors.New("文件与历史记录身份不符，拒绝删除")
		}
		row.DeletePath = filepath.Join(filepath.Dir(path), ".qman-delete-"+rand.Text())
		row.Status = "deleting"
		if err = s.catalog.SaveBackup(*row); err != nil {
			return err
		}
		parent, e := os.OpenRoot(filepath.Dir(path))
		if e != nil {
			return e
		}
		defer parent.Close()
		// Quarantine only within this exact parent; no recursive path deletion.
		if e = parent.Rename(filepath.Base(path), filepath.Base(row.DeletePath)); e != nil {
			row.DeletePath = ""
			return e
		}
		moved, e := parent.Lstat(filepath.Base(row.DeletePath))
		if e != nil || !os.SameFile(info, moved) {
			return errors.New("备份位置在删除准备时变化，已停止")
		}
	}
	if !strings.HasPrefix(filepath.Base(row.DeletePath), ".qman-delete-") {
		return errors.New("删除日志无效")
	}
	return mediabackup.DeleteQuarantined(ctx, *row)
}

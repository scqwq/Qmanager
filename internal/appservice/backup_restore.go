package appservice

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/storage"
	"time"
)

func (s *Media) RestoreBackup(parent context.Context, id, target string) error {
	return s.restoreBackup(parent, id, target, nil)
}

func (s *Media) RestoreExternalBackup(parent context.Context, candidate catalog.BackupRecord, target string) error {
	return s.restoreBackup(parent, candidate.ID, target, &candidate)
}

func (s *Media) restoreBackup(parent context.Context, id, target string, candidate *catalog.BackupRecord) error {
	return s.backupJob(parent, "restoring", func(ctx context.Context) (returnErr error) {
		row, err := s.catalog.Backup(id)
		var pack *mediabackup.Package
		if candidate != nil {
			if err == nil && (row.Digest != candidate.Digest || row.DeletePath != "") {
				return errors.New("原备份记录已变化，请重新选择")
			}
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			protected, e := s.protected()
			if e != nil {
				return e
			}
			path, e := media.Canonical(candidate.Path)
			if e != nil || path != candidate.Path {
				return errors.New("所选备份路径已变化，请重新选择")
			}
			for _, root := range protected {
				if media.Overlap(root, path) {
					return errors.New("备份位置与应用数据或 QQ 来源重叠")
				}
			}
			before, e := os.Lstat(path)
			if e != nil {
				return errors.New("所选备份已无法访问，请重新选择")
			}
			pack, err = mediabackup.OpenPackage(ctx, path, filepath.Join(s.private, "temporary"), s.backupProgress)
			if err != nil {
				return err
			}
			defer pack.Cleanup()
			if pack.Manifest.ID != candidate.ID || catalog.ManifestDigest(pack.Bytes) != candidate.Digest {
				return errors.New("所选备份已变化，未更新登记位置")
			}
			after, e := os.Lstat(path)
			if e != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				return errors.New("所选备份在核对期间已移动或变化，未更新登记位置")
			}
			previousPath := row.Path
			row = *candidate
			row.Manifest = string(pack.Bytes)
			err = s.catalog.SaveBackup(row)
			if err == nil {
				action := "登记外部备份"
				if previousPath != "" {
					action = "使用已登记备份的副本"
				}
				s.backupEvent(row, action, row.Path)
				row, err = s.catalog.Backup(id)
			}
		}
		if err != nil {
			return err
		}
		if row.Status == "deleted" || row.DeletePath != "" {
			return errors.New("此备份已删除或正在删除，请重新定位完整副本")
		}
		attempt := &catalog.BackupRestoreStatus{State: "running", StartedAt: time.Now().UTC(), Target: target, Failed: row.Files}
		row.RestoreResult = attempt
		if row.OriginalFiles != nil {
			row.OriginalFiles.Stale = true
		}
		// Record a single active ID before publishing running state. Startup can
		// recover that one row without parsing every historical manifest.
		if err = s.catalog.SetPreference("active_backup_restore", id); err != nil {
			return err
		}
		if err = s.catalog.SaveBackup(row); err != nil {
			return err
		}
		defer func() {
			finished := time.Now().UTC()
			attempt.FinishedAt = &finished
			switch {
			case returnErr != nil:
				attempt.State = "failed"
				if errors.Is(returnErr, context.Canceled) {
					attempt.State = "cancelled"
				} else if attempt.Written+attempt.Skipped+attempt.Fallback > 0 {
					attempt.State = "partial"
				}
				attempt.Detail = returnErr.Error()
			case attempt.Failed > 0:
				attempt.State = "partial"
			case attempt.Fallback > 0 && attempt.Target != "":
				attempt.State = "fallback"
			case attempt.Target == "":
				attempt.State = "qman_only"
			default:
				attempt.State = "completed"
			}
			latest, e := s.catalog.Backup(id)
			if e == nil {
				latest.RestoreResult = attempt
				e = s.catalog.SaveBackup(latest)
			}
			if e != nil {
				returnErr = errors.Join(returnErr, errors.New("未能保存本次恢复状态，请保留已加载文件并核对应用索引"))
			} else {
				_ = s.catalog.SetPreference("active_backup_restore", "")
			}
			label := map[string]string{
				"completed": "已完成", "qman_only": "已保存到 Qman", "fallback": "有文件保存到 Qman",
				"partial": "部分完成", "failed": "加载失败", "cancelled": "已取消",
			}[attempt.State]
			s.backupEvent(row, "加载备份", fmt.Sprintf("%s · 补回 %d · 跳过 %d · 冲突 %d · 仅 Qman %d · 未完成 %d", label, attempt.Written, attempt.Skipped, attempt.Conflicts, attempt.Fallback, attempt.Failed))
		}()
		if err = ctx.Err(); err != nil {
			return err
		}
		if pack == nil {
			pack, err = mediabackup.OpenPackage(ctx, row.Path, filepath.Join(s.private, "temporary"), s.backupProgress)
			if err != nil {
				return err
			}
			defer pack.Cleanup()
		}
		if pack.Manifest.ID != row.ID || catalog.ManifestDigest(pack.Bytes) != row.Digest {
			return errors.New("备份身份已变化，请重新选择核对")
		}
		if target != "" {
			target, err = media.Canonical(target)
			if err != nil {
				return err
			}
			if media.Overlap(target, s.private) || media.Overlap(target, row.Path) {
				return errors.New("恢复位置与备份或应用私有目录重叠")
			}
		}
		fallbackReason := ""
		if target != "" {
			running, e := s.cleanupQQ()
			if e != nil || running {
				target = ""
				fallbackReason = "QQ 尚在运行或无法核实进程状态，本次仅保存到 Qman。"
			}
		}
		attempt.Detail = fallbackReason
		fallback := filepath.Join(s.private, "library", row.ID)
		if _, err = media.Canonical(fallback); err != nil {
			return err
		}
		if err = storage.Private(fallback); err != nil {
			return err
		}
		result, restoreErr := mediabackup.Restore(ctx, pack.Manifest, pack.Items, target, fallback, s.backupProgress)
		if restoreErr != nil {
			processed := map[string]bool{}
			for _, p := range result.Placements {
				processed[p.Instance] = true
			}
			for _, item := range pack.Items {
				if !processed[item.InstanceID] {
					result.Failed++
					result.Placements = append(result.Placements, mediabackup.Placement{Instance: item.InstanceID, RelativePath: item.RelativePath, Status: "not_processed", Error: "任务取消或未完成，尚未保存"})
				}
			}
		}
		attempt.Written, attempt.Skipped, attempt.Conflicts, attempt.Fallback, attempt.Failed = result.Written, result.Skipped, result.Conflicts, result.Fallback, result.Failed
		saved := importState{Manifest: pack.Manifest, Result: result}
		payload, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		// Persist partial/cancelled results so copied files are not reported as an
		// all-or-nothing success and can be inspected after restarting.
		if err = s.catalog.SaveImport(id, payload); err != nil {
			return err
		}
		manifest, _ := json.Marshal(pack.Manifest)
		if err = s.catalog.RestoreEvidence(id, manifest); err != nil {
			return err
		}
		if target != "" {
			_ = s.catalog.SetPreference("restore_directory", target)
		}
		row.Status = "available"
		row.Detail = fallbackReason + fmt.Sprintf("补回 %d，相同跳过 %d，冲突另存 %d，仅 Qman %d，未完成 %d", result.Written, result.Skipped, result.Conflicts, result.Fallback, result.Failed)
		if restoreErr != nil {
			row.Detail = "加载未全部完成；" + row.Detail
		}
		if err = s.catalog.SaveBackup(row); err != nil {
			return err
		}
		s.mu.Lock()
		s.state.Restore = &result
		s.mu.Unlock()
		if err = s.showImport(ctx, saved); err != nil {
			return err
		}
		s.mu.Lock()
		s.state.Restore = &result
		s.state.Message = row.Detail + "。不判断 QQ 是否识别。"
		s.mu.Unlock()
		return restoreErr
	})
}

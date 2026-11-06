package appservice

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/diagnostics"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/storage"
	"strings"
	"time"
)

type BackupPreview struct {
	PreviousPath  string               `json:"previous_path,omitempty"`
	Ticket        string               `json:"ticket,omitempty"`
	Record        catalog.BackupRecord `json:"record"`
	RestorePrefix string               `json:"restore_prefix"`
	Target        string               `json:"target"`
	TargetNote    string               `json:"target_note,omitempty"`
}
type importState struct {
	Manifest mediabackup.Manifest      `json:"manifest"`
	Result   mediabackup.RestoreResult `json:"result"`
}

func (s *Media) PreviewHistoryBackup(ctx context.Context, id string) (BackupPreview, error) {
	row, err := s.catalog.Backup(id)
	if err != nil {
		return BackupPreview{}, err
	}
	preview, e := s.PreviewBackup(ctx, row.Path, row.ID)
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

func (s *Media) Preference(key string) string { v, _ := s.catalog.Preference(key); return v }
func (s *Media) registerBackup(ctx context.Context, path string, expectedID ...string) (catalog.BackupRecord, error) {
	row, err := s.inspectBackupPackage(ctx, path, expectedID...)
	if err != nil {
		return row, err
	}
	return row, s.catalog.SaveBackup(row)
}

func (s *Media) inspectBackupPackage(ctx context.Context, path string, expectedID ...string) (catalog.BackupRecord, error) {
	pack, err := mediabackup.OpenPackage(ctx, path, filepath.Join(s.private, "temporary"), nil)
	if err != nil {
		return catalog.BackupRecord{}, err
	}
	defer pack.Cleanup()
	m := pack.Manifest
	if len(expectedID) > 0 && expectedID[0] != "" && m.ID != expectedID[0] {
		return catalog.BackupRecord{}, errors.New("所选备份编号与历史不符，未替换或登记其他备份")
	}
	selection, _ := json.Marshal(m.Selection)
	row := catalog.BackupRecord{ID: m.ID, Name: m.SourceName, Path: path, Format: "directory", CreatedAt: m.CreatedAt, Selection: selection, Manifest: string(pack.Bytes), Digest: catalog.ManifestDigest(pack.Bytes), Status: "available"}
	info, err := os.Stat(path)
	if err != nil {
		return row, err
	}
	if !info.IsDir() {
		row.Format = "zip"
		row.Bytes = info.Size()
	}
	for _, e := range m.Entries {
		if e.Excluded == "" {
			row.Files++
			if row.Format == "directory" {
				row.Bytes += e.Item.Bytes
			}
		}
	}
	if row.Format == "directory" {
		row.Bytes += pack.MetadataBytes
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
	row, err := s.inspectBackupPackage(ctx, path, expectedID...)
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
func (s *Media) backupProgress(n int, b int64) {
	s.mu.Lock()
	s.state.Scanned = n
	s.state.Bytes = b
	s.mu.Unlock()
}
func (s *Media) backupJob(parent context.Context, phase string, fn func(context.Context) error) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	s.stopLocked()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.mu.Lock()
	s.jobCancel = cancel
	s.jobDone = done
	s.state.Phase = phase
	s.state.OperationResult = ""
	s.state.Error = ""
	s.state.Message = ""
	s.state.Scanned = 0
	s.state.Bytes = 0
	s.state.Restore = nil
	s.state.CheckPass, s.state.CheckTotal = 0, 0
	s.state.CheckStage, s.state.CheckWaiting = "", false
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(done)
		taskName := map[string]string{"exporting": "导出媒体", "restoring": "加载备份", "verifying": "核对文件", "deleting": "删除本地备份包", "preparing_delete": "回收前核对", "recycling": "回收源文件"}[phase]
		if taskName == "" {
			taskName = "后台任务"
		}
		diagnostics.Record(ctx, "info", "task.start", taskName+"开始")
		err := fn(ctx)
		if err != nil {
			diagnostics.Record(ctx, "error", "task.failed", taskName+"未完成；具体原因见操作结果")
		} else {
			diagnostics.Record(ctx, "info", "task.end", taskName+"已结束")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.jobCancel = nil
		s.state.Phase = "ready"
		s.state.CheckStage, s.state.CheckWaiting = "", false
		if err != nil {
			s.state.Error = err.Error()
		}
		s.state.Revision++
	}()
	return nil
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

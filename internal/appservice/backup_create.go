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
	"qqarchive/internal/storage"
	"strings"
	"time"
)

func (s *Media) Estimate(f media.Filter) mediabackup.Preview {
	p, _ := s.BackupEstimate(f)
	return p
}
func (s *Media) BackupEstimate(f media.Filter) (mediabackup.Preview, error) {
	if err := media.ValidateFilter(f); err != nil {
		return mediabackup.Preview{}, err
	}
	s.mu.RLock()
	if s.state.Mode == "backup" {
		p := mediabackup.Plan(s.items, f)
		s.mu.RUnlock()
		return p, nil
	}
	source := s.state.Source
	s.mu.RUnlock()
	if source == nil {
		return mediabackup.Preview{}, errors.New("请先选择媒体来源")
	}
	counts, err := s.catalog.BackupPlan(source.ID, f)
	if err != nil {
		return mediabackup.Preview{}, err
	}
	p := mediabackup.Preview{Files: counts.Files, Bytes: counts.Bytes, Pending: counts.Pending, PendingBytes: counts.PendingBytes, ExcludedReasons: counts.ExcludedReasons}
	for _, count := range counts.ExcludedReasons {
		p.Excluded += count
	}
	if p.Files+p.Pending > 0 {
		p.EstimatedPeak = mediabackup.DirectoryBudgetFor(p.Files+p.Pending+p.Excluded, p.Bytes+p.PendingBytes)
	}
	return p, nil
}

func (s *Media) backupDirectory(target string) (string, error) {
	path, err := media.Canonical(target)
	if err != nil {
		return "", errors.New("备份保存目录不可用，请重新选择")
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", errors.New("请选择有效的备份保存目录")
	}
	roots, err := s.protected()
	if err != nil {
		return "", err
	}
	for _, root := range roots {
		if media.Overlap(root, path) {
			return "", errors.New("备份保存目录必须与 QQ 数据目录和 Qman 应用数据分开")
		}
	}
	return path, nil
}
func (s *Media) RememberBackupDirectory(target string) (string, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return "", errors.New("请等待当前任务结束后选择目录")
	}
	path, err := s.backupDirectory(target)
	if err != nil {
		return "", err
	}
	settings, err := s.Settings()
	if err != nil {
		return "", err
	}
	if settings.BackupDirectory == "" {
		settings.BackupDirectory = path
		if _, err = s.saveSettingsLocked(settings); err != nil {
			return "", err
		}
	}
	return path, s.catalog.SetPreference("backup_directory", path)
}

func (s *Media) Backup(parent context.Context, target string, f media.Filter) error {
	return s.BackupFormat(parent, target, f, "directory")
}
func (s *Media) BackupFormat(parent context.Context, target string, f media.Filter, format string) error {
	return s.backupFormat(parent, target, f, format, nil)
}

func (s *Media) BackupSelected(parent context.Context, target string, request CleanupRequest, format string) error {
	return s.backupFormat(parent, target, media.Filter{}, format, &request)
}

func (s *Media) backupFormat(parent context.Context, target string, f media.Filter, format string, request *CleanupRequest) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	if format != "directory" && format != "zip" {
		return errors.New("备份保存形式无效")
	}
	if err := media.ValidateFilter(f); err != nil {
		return err
	}
	target, err := s.backupDirectory(target)
	if err != nil {
		return err
	}
	source, err := s.currentMediaSource()
	if err != nil {
		return err
	}
	var selected []media.Item
	var estimate mediabackup.Preview
	if request == nil {
		estimate, err = s.BackupEstimate(f)
	} else {
		selected, err = s.selectedMedia(parent, source, *request)
		estimate = mediabackup.Plan(selected, f)
	}
	if err != nil {
		return err
	}
	if estimate.Files+estimate.Pending == 0 {
		return errors.New("当前条件没有可核对备份的文件，请查看排除原因或调整筛选")
	}
	protected, err := s.protected()
	if err != nil {
		return err
	}
	// Invalid output, empty selections and insufficient space must not stop
	// observation: pending files still need their later stability checks.
	budget := estimate.EstimatedPeak
	if format == "zip" {
		budget *= 3
	}
	free, err := storage.Free(target)
	if err != nil || free < uint64(budget) {
		return errors.New("备份创建与校验的临时空间不足，或无法查询目标空间")
	}
	settings, err := s.Settings()
	if err != nil {
		return err
	}
	if settings.BackupDirectory == "" {
		settings.BackupDirectory = target
		if _, err = s.saveSettingsLocked(settings); err != nil {
			return err
		}
	}
	s.stopLocked()
	s.mu.Lock()
	ctx, cancel := context.WithCancel(parent)
	s.jobCancel = cancel
	s.jobDone = make(chan struct{})
	done := s.jobDone
	s.state.Phase = "preparing_backup"
	s.state.OperationResult = ""
	s.state.Message = ""
	s.state.Error = ""
	s.state.Backup = nil
	s.state.ExcludedItems = nil
	s.state.CheckPass, s.state.CheckTotal = 0, 0
	s.state.CheckStage, s.state.CheckWaiting = "", false
	s.state.Scanned = 0
	s.state.Bytes = 0
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(done)
		var result mediabackup.Result
		var items []media.Item
		var options mediabackup.BuildOptions
		var err error
		if request == nil {
			items, options, err = s.prepareBackup(ctx, source, f)
		} else {
			items, options, err = s.prepareBackupItems(ctx, source, selected)
		}
		if err == nil {
			s.mu.Lock()
			s.state.Phase = "backup"
			s.state.CheckStage, s.state.CheckWaiting = "", false
			s.state.Scanned, s.state.Bytes = 0, 0
			s.mu.Unlock()
			result, err = mediabackup.Build(ctx, source, items, f, target, protected, s.catalog.History, func(n int, b int64) { s.mu.Lock(); s.state.Scanned = n; s.state.Bytes = b; s.mu.Unlock() }, options)
		}
		if err == nil && format == "zip" {
			zipPath, e := mediabackup.ZipDirectory(ctx, result.Path)
			if e == nil {
				// The directory was freshly created by this job. Only delete its
				// validated object set after the independent ZIP is complete.
				row, re := s.registerBackup(ctx, result.Path)
				if re == nil {
					row.DeletePath = row.Path
					re = mediabackup.DeleteQuarantined(ctx, row)
				}
				if re != nil {
					err = errors.New("ZIP 已创建并保留，目录副本有清理残留；不要将残留视作完整备份")
				}
				result.Path = zipPath
			} else {
				err = e
			}
		}
		if result.Path != "" {
			row, e := s.registerBackup(context.WithoutCancel(ctx), result.Path)
			if e == nil {
				row.SelectedOnly = request != nil
				row.OriginalFiles = &catalog.BackupFileStatus{State: "present", CheckedAt: time.Now().UTC(), Total: result.Files, Present: result.Files}
				if saveErr := s.catalog.SaveBackup(row); saveErr != nil {
					diagnostics.Record(ctx, "error", "backup.presence_write_failed", "未能保存初始源文件状态")
				}
				s.backupEvent(row, "创建备份", fmt.Sprintf("保存 %d 个文件 · 排除 %d 项", result.Files, result.Excluded))
			} else if err == nil {
				err = errors.New("备份已保存，但登记失败；请保留产物并重新加载")
			}
		}
		if err == nil {
			_ = s.catalog.SetPreference("backup_directory", target)
		}
		summary, summaryErr := s.catalog.Report(source.ID)
		s.mu.Lock()
		defer s.mu.Unlock()
		s.jobCancel = nil
		s.state.Phase = "ready"
		s.state.Revision++
		s.state.CheckStage, s.state.CheckWaiting = "", false
		if result.Path != "" {
			s.state.OperationResult = fmt.Sprintf("已备份 %d 项。\n%s", result.Files, result.Path)
			if format == "zip" && !strings.EqualFold(filepath.Ext(result.Path), ".zip") {
				s.state.OperationResult = fmt.Sprintf("ZIP 未生成，已保留 %d 项的目录备份。\n%s", result.Files, result.Path)
			}
		}
		if result.StagingRemainder != "" {
			s.state.OperationResult = "本次未生成备份包。\n暂存可能仍占用空间：" + result.StagingRemainder
		}
		if summaryErr == nil {
			s.state.Report = summary
		}
		if err != nil {
			s.state.Error = err.Error()
		} else {
			s.state.Backup = &result
			s.state.Message = "备份已完成并通过独立 SHA-256 校验；监控已暂停，可点击快速更新"
		}
	}()
	return nil
}

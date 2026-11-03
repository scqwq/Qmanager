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
	"qqarchive/internal/mediacleanup"
	"time"
)

type CleanupRequest struct {
	ExcludeIDs  []string     `json:"exclude_ids"`
	IDs         []string     `json:"ids"`
	AllMatching bool         `json:"all_matching"`
	Filter      media.Filter `json:"filter"`
}

func (s *Media) RecoverCleanup() error {
	if err := s.catalog.RecoverBackupRestores(); err != nil {
		return err
	}
	if err := s.catalog.RecoverCleanup(); err != nil {
		return err
	}
	s.mu.RLock()
	chosen := s.state.Source
	s.mu.RUnlock()
	if chosen != nil {
		job, err := s.catalog.LatestCleanup(chosen.ID)
		if err != nil {
			return err
		}
		if job != nil {
			if err = s.recordBackupSourceCleanup(*job); err != nil {
				return err
			}
		}
		s.mu.Lock()
		s.state.Cleanup = job
		s.mu.Unlock()
	}
	return nil
}

func (s *Media) CleanupJob(id string) (catalog.CleanupJob, error) { return s.catalog.Cleanup(id) }
func (s *Media) CleanupItems(id string, offset, limit int) ([]catalog.CleanupItem, error) {
	if limit > 100 {
		limit = 100
	}
	return s.catalog.CleanupItems(id, offset, limit)
}
func (s *Media) PrepareMediaDelete(parent context.Context, request CleanupRequest) error {
	return s.backupJob(parent, "preparing_delete", func(ctx context.Context) error {
		s.mu.Lock()
		s.state.Cleanup = nil
		s.mu.Unlock()
		s.mu.RLock()
		mode := s.state.Mode
		chosen := s.state.Source
		s.mu.RUnlock()
		if mode != "source" || chosen == nil {
			return errors.New("仅可管理当前账号来源中的媒体，不能删除备份内部对象")
		}
		if _, err := media.Canonical(chosen.Resolved); err != nil {
			return err
		}
		source, err := media.Resolve(chosen.Resolved)
		if err != nil {
			return err
		}
		source.ID = chosen.ID
		source.Name = chosen.Name
		if media.Overlap(source.Resolved, s.private) {
			return errors.New("媒体来源与应用数据重叠")
		}
		if err = media.ValidateFilter(request.Filter); err != nil {
			return err
		}
		var items []media.Item
		if request.AllMatching {
			request.Filter.HideVideoCovers = true
			if len(request.IDs) != 0 {
				return errors.New("选择方式冲突")
			}
			items, err = s.catalog.Selected(ctx, source.ID, request.Filter)
			for i := range items {
				items[i] = media.Bind(items[i], source)
			}
			if len(request.ExcludeIDs) > media.MaxFiles {
				return errors.New("排除项超过上限")
			}
			excluded := map[string]bool{}
			for _, id := range request.ExcludeIDs {
				excluded[id] = true
			}
			kept := items[:0]
			for _, item := range items {
				if !excluded[item.InstanceID] {
					kept = append(kept, item)
				}
			}
			items = kept
		} else {
			if len(request.ExcludeIDs) != 0 {
				return errors.New("选择方式冲突")
			}
			if len(request.IDs) == 0 || len(request.IDs) > media.MaxFiles {
				return errors.New("请选择媒体文件")
			}
			seen := map[string]bool{}
			for _, id := range request.IDs {
				if err = ctx.Err(); err != nil {
					return err
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				item, e := s.catalog.Item(source.ID, id)
				if e != nil {
					return errors.New("部分所选记录已变化，请重新选择")
				}
				items = append(items, media.Bind(item, source))
			}
		}
		if err != nil {
			return err
		}
		return s.prepareCleanup(ctx, source, request.Filter, items, nil, nil, nil)
	})
}

func (s *Media) prepareCleanup(ctx context.Context, source media.Source, f media.Filter, items []media.Item, hashes map[string]string, skips map[string]string, backupIDs []string) error {
	job, err := s.catalog.CreateCleanup(ctx, source, f, items, backupIDs...)
	if err != nil {
		return err
	}
	ctx = diagnostics.WithItem(ctx, job.ID, "")
	diagnostics.Record(ctx, "info", "cleanup.prepare", fmt.Sprintf("开始核对 %d 个文件", len(items)))
	if err = s.recordBackupSourceCleanup(job); err != nil {
		_ = s.catalog.CleanupState(job.ID, "interrupted")
		return err
	}
	s.mu.Lock()
	s.state.Cleanup = &job
	s.mu.Unlock()
	complete := false
	defer func() {
		if !complete {
			_ = s.catalog.CleanupState(job.ID, "interrupted")
			if summary, e := s.catalog.Cleanup(job.ID); e == nil {
				_ = s.recordBackupSourceCleanup(summary)
				s.mu.Lock()
				s.state.Cleanup = &summary
				s.mu.Unlock()
			}
		}
	}()
	var bytes int64
	for i, item := range items {
		if err = ctx.Err(); err != nil {
			return err
		}
		row := catalog.CleanupItem{Item: item, Status: "ready"}
		if reason := skips[item.InstanceID]; reason != "" {
			row.Status = "skipped"
			row.Detail = reason
		} else if e := mediacleanup.Validate(item, source); e != nil {
			row.Status = "skipped"
			row.Detail = e.Error()
		} else {
			row.SHA256, err = mediacleanup.Hash(ctx, item)
			if err != nil {
				row.Status = "skipped"
				row.Detail = "无法读取或已变化，已跳过"
			} else if expected := hashes[item.InstanceID]; expected != "" && row.SHA256 != expected {
				row.Status = "skipped"
				row.Detail = "当前内容与备份实际保存的内容不同，已跳过"
			}
		}
		if err = s.catalog.SaveCleanupItem(ctx, job, row); err != nil {
			diagnostics.Record(diagnostics.WithItem(ctx, job.ID, item.InstanceID), "error", "cleanup.save_failed", "预检结果未能写入数据库")
			return err
		}
		if row.Status == "skipped" {
			diagnostics.Record(diagnostics.WithItem(ctx, job.ID, item.InstanceID), "warning", "cleanup.preflight_skipped", "文件未通过预检；具体原因见清理任务")
		}
		bytes += item.Bytes
		s.backupProgress(i+1, bytes)
	}
	if err = s.catalog.PrepareCleanup(job.ID); err != nil {
		return err
	}
	complete = true
	summary, err := s.catalog.Cleanup(job.ID)
	if err != nil {
		return err
	}
	if err = s.recordBackupSourceCleanup(summary); err != nil {
		return err
	}
	s.mu.Lock()
	s.state.Cleanup = &summary
	s.state.Message = "已核对并固定选择清单，尚未删除文件"
	s.mu.Unlock()
	return nil
}

func (s *Media) ExecuteMediaDelete(parent context.Context, id string) error {
	return s.backupJob(parent, "recycling", func(ctx context.Context) error {
		job, err := s.catalog.Cleanup(id)
		if err != nil {
			return err
		}
		ctx = diagnostics.WithItem(ctx, id, "")
		diagnostics.Record(ctx, "info", "cleanup.execute", "开始执行固定清单回收")
		if job.State == "completed" || job.State == "partial" {
			s.mu.Lock()
			s.state.Cleanup = &job
			s.state.Message = "该删除任务已经结束，未重复执行"
			s.mu.Unlock()
			return nil
		}
		if job.State != "prepared" || time.Now().After(job.ExpiresAt) {
			return errors.New("确认清单已过期或中断，请重新选择并核对")
		}
		s.mu.RLock()
		chosen := s.state.Source
		mode := s.state.Mode
		s.mu.RUnlock()
		if chosen == nil || mode != "source" || chosen.ID != job.SourceID || chosen.Resolved != job.Root {
			return errors.New("来源已切换，请在原账号重新选择")
		}
		running, err := s.cleanupQQ()
		if err != nil {
			diagnostics.Record(ctx, "warning", "cleanup.qq_unknown", "无法确认 QQ 进程状态，未执行回收")
			return errors.New("无法确认 QQ 是否完全退出，本次未执行回收；请检查后重试。")
		}
		if running {
			diagnostics.Record(ctx, "warning", "cleanup.qq_running", "检测到 QQ 进程，未执行回收")
			return errors.New("检测到 QQ 仍在运行，目前不区分账号，请完全退出后回收。")
		}
		if _, err = media.Canonical(job.Root); err != nil {
			return err
		}
		source, err := media.Resolve(job.Root)
		if err != nil {
			return err
		}
		source.ID = job.SourceID
		source.Name = job.SourceName
		rows, err := s.catalog.CleanupItems(id, 0, media.MaxFiles)
		if err != nil {
			return err
		}
		if err = s.catalog.CleanupState(id, "running"); err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = s.catalog.CleanupState(id, "interrupted")
			}
			summary, e := s.catalog.Cleanup(id)
			if e == nil {
				diagnostics.Record(ctx, "info", "cleanup.finished", fmt.Sprintf("回收=%d，跳过=%d，失败=%d，待核对=%d，尚未执行=%d；阶段=%s", summary.Recycled, summary.Skipped, summary.Failed, summary.Uncertain, summary.Ready+summary.Pending, summary.State))
				_ = s.recordBackupSourceCleanup(summary)
				report, _ := s.catalog.Report(job.SourceID)
				if report != nil && summary.Recycled > 0 {
					report.Partial = true
					report.IndexedUpdates = summary.Recycled
					report.FinishedAt = time.Now().UTC()
				}
				s.mu.Lock()
				s.state.Cleanup = &summary
				if report != nil && summary.Recycled > 0 {
					s.state.Report = report
					s.state.Cached = false
				}
				s.mu.Unlock()
			}
		}()
		var bytes int64
		for i, row := range rows {
			if err = ctx.Err(); err != nil {
				return err
			}
			if row.Status != "ready" {
				continue
			}
			running, e := s.cleanupQQ()
			if e != nil {
				diagnostics.Record(ctx, "warning", "cleanup.qq_unknown", "无法确认 QQ 进程状态，停止剩余操作")
				return errors.New("无法确认 QQ 是否完全退出，剩余文件未回收；请检查后重试。")
			}
			if running {
				diagnostics.Record(ctx, "warning", "cleanup.qq_running", "检测到 QQ 进程，停止剩余操作")
				return errors.New("检测到 QQ 仍在运行，目前不区分账号，请完全退出后回收。剩余文件未回收。")
			}
			itemCtx := diagnostics.WithItem(ctx, id, row.Item.InstanceID)
			diagnostics.Record(itemCtx, "info", "cleanup.validate", "开始执行前文件核对")
			row.Item = media.Bind(row.Item, source)
			if e = mediacleanup.Validate(row.Item, source); e != nil {
				row.Status = "skipped"
				row.Detail = e.Error()
			} else {
				row.Status = "processing"
				if err = s.catalog.SaveCleanupItem(ctx, job, row); err != nil {
					return err
				}
				diagnostics.Record(itemCtx, "info", "cleanup.processing_saved", "已保存处理中状态，准备调用 Windows")
				e = s.recycleMedia(itemCtx, row.Item, row.SHA256)
				if e == nil {
					row.Status = "recycled"
					row.Detail = "已移至 Windows 回收站"
				} else {
					row.Status = "failed"
					row.Detail = e.Error()
					if _, statErr := os.Lstat(filepath.Join(source.Resolved, row.Item.Path)); os.IsNotExist(statErr) {
						row.Status = "uncertain"
						row.Detail = "未确认回收结果且原路径已不可访问，请核对回收站；不会自动重试"
					}
				}
			}
			if err = s.catalog.SaveCleanupItem(context.WithoutCancel(ctx), job, row); err != nil {
				diagnostics.Record(itemCtx, "error", "cleanup.save_failed", "回收后的结果未能写入数据库，停止任务")
				return err
			}
			level, result := "info", "已回收"
			switch row.Status {
			case "skipped":
				level, result = "warning", "已跳过"
			case "failed":
				level, result = "error", "回收失败"
			case "uncertain":
				level, result = "error", "回收结果待核对"
			}
			diagnostics.Record(itemCtx, level, "cleanup.result_saved", "逐项结果已保存："+result)
			if row.Status == "uncertain" {
				return errors.New("Windows 未确认本项回收结果，已停止剩余操作；请查看任务和回收站")
			}
			bytes += row.Item.Bytes
			s.backupProgress(i+1, bytes)
		}
		summary, err := s.catalog.Cleanup(id)
		if err != nil {
			return err
		}
		status := "completed"
		if summary.Failed > 0 || summary.Uncertain > 0 {
			status = "partial"
		}
		if err = s.catalog.CleanupState(id, status); err != nil {
			return err
		}
		summary.State = status
		finished = true
		report, err := s.catalog.Report(job.SourceID)
		if err != nil {
			return err
		}
		if report != nil {
			report.Partial = true
			report.IndexedUpdates = summary.Recycled
			report.FinishedAt = time.Now().UTC()
		}
		s.mu.Lock()
		s.state.Cleanup = &summary
		s.state.Report = report
		s.state.Cached = false
		s.state.Message = fmt.Sprintf("删除结束：回收 %d，跳过 %d，失败 %d，待核对 %d。未清空回收站，不代表已释放空间。", summary.Recycled, summary.Skipped, summary.Failed, summary.Uncertain)
		s.mu.Unlock()
		return nil
	})
}

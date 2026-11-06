package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/diagnostics"
)

func (s *Media) busy() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.jobCancel != nil {
		return true
	}
	return s.state.Phase == "exporting" || s.state.Phase == "preparing_backup" || s.state.Phase == "backup" || s.state.Phase == "verifying" || s.state.Phase == "discovering" || s.state.Phase == "restoring" || s.state.Phase == "deleting" || s.state.Phase == "preparing_delete" || s.state.Phase == "recycling"
}
func (s *Media) stopLocked() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
		s.cancel = nil
		s.done = nil
	}
}

func (s *Media) Cancel() {
	s.mu.RLock()
	cancel := s.jobCancel
	s.mu.RUnlock()
	if cancel != nil {
		cancel()
	} else {
		s.Stop()
	}
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

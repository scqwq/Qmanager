package appservice

import "fmt"

// CancelOperation waits for the worker to finish recording the actual outcome.
func (s *Media) CancelOperation(expectedPhase string, revision int) string {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.RLock()
	phase, cancel, done, current := s.state.Phase, s.jobCancel, s.jobDone, s.state.Revision
	s.mu.RUnlock()
	if cancel != nil {
		if current != revision || (phase != expectedPhase && !(phase == "backup" && expectedPhase == "preparing_backup")) {
			return ""
		}
		cancel()
		if done != nil {
			<-done
		}
	} else if phase != "ready" || current != revision+1 {
		return ""
	} else {
		phase = expectedPhase
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch phase {
	case "preparing_backup", "backup":
		if s.state.OperationResult != "" {
			return s.state.OperationResult
		}
		return "本次未生成备份包。"
	case "exporting":
		if s.state.OperationResult != "" {
			return s.state.OperationResult
		}
		return "本次未生成导出文件夹。"
	case "preparing_delete":
		return "核对已停止，尚未开始回收。"
	case "recycling":
		job := s.state.Cleanup
		if job == nil {
			return "尚未开始回收。"
		}
		return fmt.Sprintf("已移入回收站 %d 项，尚未处理 %d 项。", job.Recycled, job.Ready+job.Pending) +
			operationCount("跳过", job.Skipped) + operationCount("失败", job.Failed) + operationCount("待核对", job.Uncertain)
	case "deleting":
		return s.state.OperationResult
	}
	return ""
}

func operationCount(label string, count int) string {
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("%s %d 项。", label, count)
}

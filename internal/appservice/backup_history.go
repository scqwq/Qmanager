package appservice

import (
	"context"
	"database/sql"
	"errors"
)

// DeleteBackupHistory is a metadata-only operation, serialized with package and
// source operations. It never deletes media or follows a moved backup.
func (s *Media) DeleteBackupHistory(ctx context.Context, id string) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	row, err := s.catalog.Backup(id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Status != "deleted" || row.DeletePath != "" {
		return errors.New("请先完成本地备份包删除，再删除历史记录")
	}
	observeBackupLocation(&row)
	if row.LocalState != "missing" {
		return errors.New("本地位置尚未确认不存在，请刷新、检查磁盘或重新处理本地包")
	}
	invalidated, err := s.catalog.DeleteBackupHistory(ctx, id, row.Digest)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Cleanup != nil {
		for _, job := range invalidated {
			if s.state.Cleanup.ID == job {
				current := *s.state.Cleanup
				current.State = "interrupted"
				s.state.Cleanup = &current
			}
		}
	}
	s.state.Error = ""
	s.state.Message = "已删除该备份的历史记录；原文件、已加载媒体和独立操作结果保留"
	s.state.Revision++
	return nil
}

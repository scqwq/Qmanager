package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/catalog"
)

func (s *Media) DatabaseUsage(ctx context.Context) (catalog.DatabaseUsage, error) {
	return s.catalog.Usage(ctx)
}

func (s *Media) CompactDatabase(parent context.Context) (catalog.DatabaseMaintenance, error) {
	if !s.op.TryLock() {
		return catalog.DatabaseMaintenance{}, errors.New("请等待当前任务结束")
	}
	defer s.op.Unlock()
	if s.busy() {
		return catalog.DatabaseMaintenance{}, errors.New("请等待当前任务结束")
	}
	s.stopLocked()
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	s.mu.Lock()
	s.jobCancel, s.jobDone = cancel, done
	s.state.Phase = "organizing"
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.jobCancel = nil
		s.state.Phase = "ready"
		s.state.Revision++
		s.mu.Unlock()
		close(done)
	}()
	return s.catalog.Compact(ctx)
}

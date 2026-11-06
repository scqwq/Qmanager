package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/media"
	"strings"
)

func (s *Media) Start(parent context.Context) error {
	return s.StartUpdate(parent, "full")
}
func (s *Media) StartUpdate(parent context.Context, mode string) error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前任务尚未结束")
	}
	if mode != "fast" && mode != "full" {
		return errors.New("请选择快速更新或全盘更新")
	}
	s.stopLocked()
	s.mu.Lock()
	if s.state.Source == nil || s.state.Mode != "source" {
		s.mu.Unlock()
		return errors.New("请先选择媒体目录")
	}
	source := *s.state.Source
	fresh, err := media.ResolveForUpdate(source.Resolved)
	if err != nil {
		s.mu.Unlock()
		return errors.New("源目录暂不可访问，仍可浏览缓存记录")
	}
	if !strings.EqualFold(fresh.Resolved, source.Resolved) {
		s.mu.Unlock()
		return errors.New("来源真实路径变化，请重新选择核实")
	}
	fresh.ID = source.ID
	fresh.Name = source.Name
	source = fresh
	s.state.Source = &fresh
	s.state.Phase = "scanning"
	s.state.Error = ""
	s.state.Message = ""
	s.state.UpdateMode = mode
	s.state.UpdateRunning = true
	s.state.BackgroundStage = ""
	s.state.Stopping = false
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.observe(ctx, source, s.done, mode)
	return nil
}
func (s *Media) Stop() { s.op.Lock(); defer s.op.Unlock(); s.stopLocked() }

// StopUpdate cancels observation only; it never cancels a backup or cleanup job.
func (s *Media) StopUpdate() error {
	s.op.Lock()
	defer s.op.Unlock()
	if s.busy() {
		return errors.New("当前正在执行其他任务，请使用该任务的取消按钮")
	}
	s.mu.Lock()
	s.state.Stopping = true
	s.mu.Unlock()
	s.stopLocked()
	s.mu.Lock()
	s.state.Stopping = false
	s.state.UpdateRunning = false
	s.state.BackgroundStage = ""
	s.state.Watching = false
	s.state.Phase = "ready"
	s.state.Error = ""
	s.state.Message = "更新已终止，已有索引和观察历史已保留；后台监控已暂停"
	s.mu.Unlock()
	return nil
}

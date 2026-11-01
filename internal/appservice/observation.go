package appservice

import (
	"context"
	"errors"
	"qqarchive/internal/media"
	"qqarchive/internal/watch"
	"time"
)

func (s *Media) observe(ctx context.Context, source media.Source, done chan struct{}, firstMode string) {
	defer close(done)
	defer func() {
		s.mu.Lock()
		s.state.UpdateRunning = false
		s.state.BackgroundStage = ""
		if s.state.Phase == "scanning" {
			s.state.Phase = "ready"
		}
		s.mu.Unlock()
	}()
	interval := s.discoveryInterval
	if interval <= 0 {
		interval = ReconcileInterval
	}
	started := time.Now().UTC()
	previousEnd, _ := s.catalog.Preference("watch_end:" + source.ID)
	previousStart, _ := s.catalog.Preference("watch_start:" + source.ID)
	gapStart, _ := time.Parse(time.RFC3339Nano, previousEnd)
	runStart, _ := time.Parse(time.RFC3339Nano, previousStart)
	reason := "resumed_after_gap"
	if runStart.After(gapStart) {
		gapStart = runStart
		reason = "previous_run_interrupted"
	}
	if gapStart.IsZero() {
		gapStart = s.catalog.LastScan(source.ID)
	}
	if !gapStart.IsZero() {
		if err := s.catalog.Gap(source.ID, reason, gapStart, started); err != nil {
			s.fail(err)
			return
		}
	}
	if err := s.catalog.SetPreference("watch_start:"+source.ID, started.Format(time.RFC3339Nano)); err != nil {
		s.fail(err)
		return
	}
	w, err := watch.Start(ctx, source)
	if err != nil {
		s.fail(err)
		return
	}
	defer w.Close()
	known, err := s.catalog.KnownPaths(ctx, source.ID)
	if err != nil {
		s.fail(err)
		return
	}
	previous := map[string]media.Item{}
	if firstMode == "full" {
		previous, err = s.catalog.ActiveItemsContext(ctx, source.ID)
		if err != nil {
			s.fail(err)
			return
		}
	}
	covered := w.Complete
	needsFull := false
	created := map[string]time.Time{}
	dirty := map[string]bool{}
	force := map[string]bool{}
	pending := map[string]time.Time{}
	initial := true
	lastActivity := time.Now()
	recordGap := func(reason string) {
		covered = false
		needsFull = true
		if e := s.catalog.Gap(source.ID, reason, lastActivity, time.Now().UTC()); e != nil {
			s.fail(e)
		}
	}
	if !covered {
		recordGap("watch_setup_partial")
	}
	defer func() {
		now := time.Now().UTC()
		_ = s.catalog.Gap(source.ID, "coverage_end", now, now)
		_ = s.catalog.SetPreference("watch_end:"+source.ID, now.Format(time.RFC3339Nano))
		s.mu.Lock()
		s.state.Watching = false
		s.state.Coverage = "监控已暂停；可快速补新或手动全盘核对"
		if s.state.Phase == "scanning" {
			s.state.Phase = "ready"
		}
		s.mu.Unlock()
	}()
	consume := func(e watch.Event) {
		lastActivity = time.Now()
		if e.Gap {
			recordGap("native_error_or_overflow")
			return
		}
		if e2 := s.catalog.RawEvent(source.ID, e.Path, e.Action, e.At); e2 != nil {
			recordGap("raw_event_history_unavailable")
			s.fail(e2)
		}
		if len(dirty) < 4096 {
			dirty[e.Path] = true
			force[e.Path] = true
		} else {
			recordGap("event_queue_limit")
		}
		if e.Action == 1 || e.Action == 5 {
			if len(created) < 4096 {
				created[e.Path] = e.At
			} else {
				recordGap("creation_event_limit")
			}
		}
	}
	drain := func() {
		for i := 0; i < 512; i++ {
			select {
			case e := <-w.Events:
				consume(e)
			default:
				return
			}
		}
	}
	refillPending := func() bool {
		if len(pending) >= 128 {
			return true
		}
		paths, e := s.catalog.PendingPaths(ctx, source.ID, 128)
		if e != nil {
			s.fail(e)
			return false
		}
		for _, path := range paths {
			if len(pending) >= 128 {
				break
			}
			if _, ok := pending[path]; !ok {
				pending[path] = time.Now().Add(2 * time.Second)
			}
		}
		return true
	}
	scan := func(targets []string, full bool, stage string) bool {
		root, e := media.OpenRelative(source.Resolved, ".")
		if e != nil {
			recordGap("source_unavailable")
			s.fail(errors.New("源目录暂不可访问，已保留原缓存；请恢复后再更新"))
			return false
		}
		root.Close()
		passForce := map[string]bool{}
		if full {
			passForce = force
			force = map[string]bool{}
			dirty = map[string]bool{}
		} else if targets != nil {
			// A stability check or targeted event consumes only covered hints, never
			// unrelated paths still waiting for debounce. A name pass consumes none.
			for path, value := range force {
				if media.Covers(targets, path) {
					passForce[path] = value
					delete(force, path)
				}
			}
			for path := range dirty {
				if media.Covers(targets, path) {
					delete(dirty, path)
				}
			}
		}
		s.mu.Lock()
		if initial {
			s.state.Phase = "scanning"
		} else {
			s.state.Phase = "ready"
		}
		s.state.UpdateRunning = initial
		s.state.BackgroundStage = stage
		if full {
			s.state.UpdateMode = "full"
		} else {
			s.state.UpdateMode = "fast"
		}
		s.state.Scanned = 0
		s.state.Bytes = 0
		s.mu.Unlock()
		r := media.ScanWith(ctx, source, media.ScanOptions{Previous: previous, Force: passForce, Targets: targets, NewOnly: !full, Known: known}, func(n int, b int64) { s.mu.Lock(); s.state.Scanned = n; s.state.Bytes = b; s.mu.Unlock() })
		drain()
		if w.Dropped() {
			recordGap("event_queue_overflow")
		}
		if ctx.Err() != nil {
			return false
		}
		if !r.Complete {
			recordGap("incomplete_scan")
		}
		if e = s.catalog.Apply(ctx, source.ID, &r, initial, created, covered, time.Now().UTC()); e != nil {
			s.fail(e)
			return false
		}
		initial = false
		if full && r.Complete {
			known = map[string]bool{}
			previous = map[string]media.Item{}
			needsFull = false
		}
		for _, path := range r.RemovedPaths {
			delete(known, path)
			delete(previous, path)
			delete(pending, path)
			delete(created, path)
		}
		for _, item := range r.Items {
			known[item.RelativePath] = true
			previous[item.RelativePath] = item
			delete(created, item.RelativePath)
			delete(pending, item.RelativePath)
		}
		if !refillPending() {
			return false
		}
		summary, e := s.catalog.Report(source.ID)
		if e != nil {
			s.fail(e)
			return false
		}
		s.mu.Lock()
		s.state.Report = summary
		s.state.Phase = "ready"
		s.state.UpdateRunning = false
		s.state.BackgroundStage = ""
		s.state.Revision++
		s.state.Cached = false
		s.state.LastFullScan = s.catalog.LastScan(source.ID)
		s.state.Watching = true
		s.state.UpdateMode = "fast"
		s.state.NeedsFull = needsFull
		s.state.ReconcileSeconds = int(interval / time.Second)
		if r.UpdateKind == "full" {
			s.state.Message = "全盘更新完成；随后仅监听变化并每两分钟轻量补新"
		} else if r.UpdateKind == "fast" {
			s.state.Message = "快速更新完成：旧文件沿用缓存，未全面核对变化和缺失"
		} else {
			s.state.Message = "变化路径已核对；其余旧文件继续沿用缓存"
		}
		s.state.Coverage = "原生变化按路径更新 · 每两分钟快速补新 · 不自动全盘更新"
		if needsFull {
			s.state.Coverage = "发现覆盖缺口；快速补新继续，建议手动全盘更新"
		} else if !covered {
			s.state.Coverage = "部分目录未完整监听；每两分钟快速补新，需要时手动全盘更新"
		}
		s.mu.Unlock()
		return true
	}
	if !scan(nil, firstMode == "full", "") {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastNames, lastTick := time.Now(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-w.Events:
			consume(e)
		case now := <-ticker.C:
			if now.Sub(lastTick) > 5*time.Second || now.Before(lastTick) {
				recordGap("sleep_or_clock_change")
			}
			lastTick = now
			if w.Dropped() {
				recordGap("event_queue_overflow")
			}
			if now.Sub(lastNames) >= interval {
				if !scan(nil, false, "discovery") {
					return
				}
				lastNames = time.Now()
				lastTick = lastNames
				continue
			}
			if len(dirty) > 0 && now.Sub(lastActivity) >= 2*time.Second {
				targets := []string{}
				for path := range dirty {
					targets = append(targets, path)
				}
				if !scan(targets, false, "changes") {
					return
				}
				lastTick = time.Now()
				continue
			}
			targets := []string{}
			for path, at := range pending {
				if !now.Before(at) {
					targets = append(targets, path)
					delete(pending, path)
				}
			}
			if len(targets) > 0 {
				if !scan(targets, false, "stability") {
					return
				}
				lastTick = time.Now()
			}
			s.mu.Lock()
			s.state.NeedsFull = needsFull
			s.mu.Unlock()
		}
	}
}

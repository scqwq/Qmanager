package appservice

import (
	"context"
	"qqarchive/internal/media"
	"qqarchive/internal/watch"
	"time"
)

func (s *Media) observe(ctx context.Context, source media.Source, done chan struct{}) {
	defer close(done)
	started := time.Now().UTC()
	if previous := s.catalog.LastScan(source.ID); !previous.IsZero() {
		if err := s.catalog.Gap(source.ID, "resumed_after_gap", previous, started); err != nil {
			s.fail(err)
			return
		}
	}
	w, err := watch.Start(ctx, source)
	if err != nil {
		s.fail(err)
		return
	}
	defer w.Close()
	covered := w.Complete
	previous, err := s.catalog.ActiveItems(source.ID)
	if err != nil {
		s.fail(err)
		return
	}
	created := map[string]time.Time{}
	dirty := map[string]bool{}
	force := map[string]bool{}
	pending := map[string]time.Time{}
	fullNeeded := false
	initial := true
	lastActivity := time.Now()
	recordGap := func(reason string) {
		covered = false
		fullNeeded = true
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
		s.mu.Lock()
		s.state.Watching = false
		s.state.Coverage = "监控已暂停；下次从已有索引继续核对"
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
		if err := s.catalog.RawEvent(source.ID, e.Path, e.Action, e.At); err != nil {
			recordGap("raw_event_history_unavailable")
			s.fail(err)
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
	scan := func(targets []string) bool {
		// Capture hints before scanning; events arriving during this scan remain in
		// dirty for another pass, including changes to an already-visited directory.
		passForce := force
		force = map[string]bool{}
		dirty = map[string]bool{}
		fullNeeded = false
		s.mu.Lock()
		s.state.Phase = "scanning"
		s.mu.Unlock()
		r := media.ScanWith(ctx, source, media.ScanOptions{Previous: previous, Force: passForce, Targets: targets}, func(n int, b int64) { s.mu.Lock(); s.state.Scanned = n; s.state.Bytes = b; s.mu.Unlock() })
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
		if err := s.catalog.Apply(ctx, source.ID, &r, initial, created, covered, time.Now().UTC()); err != nil {
			s.fail(err)
			return false
		}
		initial = false
		if !r.Partial && r.Complete {
			previous = map[string]media.Item{}
		} else if r.Partial && r.Complete {
			for path := range previous {
				if media.Covers(r.Covered, path) {
					delete(previous, path)
				}
			}
		}
		for _, item := range r.Items {
			previous[item.RelativePath] = item
			delete(created, item.RelativePath)
			delete(pending, item.RelativePath)
			if item.Stability != "stable_heuristic" && item.Status == "recognized" && item.Bytes > 0 && item.Role != "temporary" && len(pending) < 128 {
				pending[item.RelativePath] = time.Now().Add(2 * time.Second)
			}
		}
		summary, err := s.catalog.Report(source.ID)
		if err != nil {
			s.fail(err)
			return false
		}
		lastFull := s.catalog.LastScan(source.ID)
		s.mu.Lock()
		s.state.Report = summary
		s.state.Phase = "ready"
		s.state.Revision++
		s.state.Cached = false
		s.state.LastFullScan = lastFull
		s.state.Watching = true
		s.state.ReconcileSeconds = int(ReconcileInterval / time.Second)
		s.state.Message = ""
		s.state.Coverage = "原生变化增量更新 · 每 2 分钟完整核对"
		if !covered {
			s.state.Coverage = "部分监听或扫描存在缺口 · 每 2 分钟核对补漏"
		}
		s.mu.Unlock()
		return true
	}
	if !scan(nil) {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastFull, lastTick := time.Now(), time.Now()
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
			if now.Sub(lastFull) >= ReconcileInterval {
				if !scan(nil) {
					return
				}
				lastFull = time.Now()
				lastTick = lastFull
				continue
			}
			if len(dirty) > 0 && now.Sub(lastActivity) >= 2*time.Second {
				if fullNeeded {
					if !scan(nil) {
						return
					}
					lastFull = time.Now()
				} else {
					targets := []string{}
					for path := range dirty {
						targets = append(targets, path)
					}
					if !scan(targets) {
						return
					}
				}
				lastTick = time.Now()
				continue
			}
			// Small pending sets get stability probes; a large baseline uses the regular
			// two-minute reconciliation rather than repeatedly walking the whole tree.
			targets := []string{}
			for path, at := range pending {
				if !now.Before(at) {
					targets = append(targets, path)
					delete(pending, path)
				}
			}
			if len(targets) > 0 {
				if !scan(targets) {
					return
				}
				lastTick = time.Now()
			}
		}
	}
}

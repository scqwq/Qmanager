package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"strings"
	"testing"
	"time"
)

func waitSelectionInterval(t *testing.T, s *Media) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := s.State()
		if state.CheckStage == "stability" && state.CheckWaiting {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("missing actual observation interval", s.State().Error)
}

func TestCleanupChecksOnlyFrozenRangeAndPreservesEvidence(t *testing.T) {
	s, c, source := pendingFixture(t, 3000)
	before, err := c.Active(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := media.Filter{Month: "2025-10"}
	start := time.Now()
	if err = s.PrepareMediaDelete(context.Background(), CleanupRequest{AllMatching: true, Filter: f}); err != nil {
		t.Fatal(err)
	}
	waitSelectionInterval(t, s)
	state := s.State()
	if state.Phase != "preparing_delete" || state.CheckTotal != 1 || state.CheckPass != 2 {
		t.Fatal("wrong scope/progress", state.CheckTotal, state.CheckPass)
	}
	if err = testfixture.Image(source.Resolved, "2025-10/Ori/late.png"); err != nil {
		t.Fatal(err)
	}
	expected, _ := testfixture.Snapshot(source.Resolved)
	waitBackupJob(t, s)
	state = s.State()
	if state.Error != "" || state.Cleanup == nil || state.Cleanup.Ready != 1 || state.Cleanup.Total != 1 || state.Cleanup.State != "prepared" {
		t.Fatal("pending selection not prepared", state.Error, state.Cleanup)
	}
	if time.Since(start) < time.Second {
		t.Fatal("stability interval was fabricated")
	}
	rows, err := c.CleanupItems(state.Cleanup.ID, 0, 60)
	if err != nil || len(rows) != 1 || len(rows[0].SHA256) != 64 || rows[0].Item.Stability != "stable_heuristic" {
		t.Fatal("missing hash or stable evidence", err, rows)
	}
	after, err := c.Active(source.ID)
	if err != nil || len(after) != len(before) {
		t.Fatal("expanded frozen selection", err)
	}
	for n, old := range before {
		current := after[n]
		if old.Item.SourceMonth != "2025-10" {
			if !reflect.DeepEqual(old, current) {
				t.Fatal("unrelated evidence was touched")
			}
		} else if current.Samples != 3 || current.Item.InstanceID != old.Item.InstanceID || !current.Item.FirstSeenAt.Equal(old.Item.FirstSeenAt) || current.Item.Method != old.Item.Method {
			t.Fatal("first-seen/instance evidence changed")
		}
	}
	actual, _ := testfixture.Snapshot(source.Resolved)
	if !reflect.DeepEqual(expected, actual) {
		t.Fatal("preflight modified or deleted source")
	}
	if state.CheckWaiting || state.CheckStage != "" {
		t.Fatal("stale preparation progress")
	}
}

func TestCleanupOnDemandChecksRejectChangesAndCancel(t *testing.T) {
	for _, action := range []string{"changed", "missing", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, c, source := pendingFixture(t, 0)
			item := s.Page(media.Filter{Month: "2025-10"}).Items[0]
			if err := s.PrepareMediaDelete(context.Background(), CleanupRequest{IDs: []string{item.InstanceID}}); err != nil {
				t.Fatal(err)
			}
			waitSelectionInterval(t, s)
			path := filepath.Join(source.Resolved, filepath.FromSlash(item.RelativePath))
			switch action {
			case "changed":
				f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write([]byte("synthetic change"))
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				s.Cancel()
			}
			waitBackupJob(t, s)
			state := s.State()
			if state.Cleanup == nil {
				t.Fatal("lost frozen task")
			}
			job, err := c.Cleanup(state.Cleanup.ID)
			if err != nil || job.Ready != 0 || job.Recycled != 0 {
				t.Fatal("unsafe preflight result", err, job)
			}
			if action == "cancel" {
				if job.State != "interrupted" || job.Pending != 1 || state.Error == "" {
					t.Fatal("cancel lost pending task", job, state.Error)
				}
			} else {
				rows, err := c.CleanupItems(job.ID, 0, 60)
				if err != nil || job.Skipped != 1 || !strings.Contains(rows[0].Detail, "与所选记录不符") {
					t.Fatal("missing specific exclusion", err, job)
				}
			}
		})
	}
}

func TestSelectionCheckDoesNotObserveBlockedBackupHistoryItems(t *testing.T) {
	s, c, source := pendingFixture(t, 0)
	item := s.Page(media.Filter{Month: "2025-10"}).Items[0]
	before, _ := c.Item(source.ID, item.ID)
	blocked := map[string]string{item.InstanceID: "原实例不符合备份证据"}
	if err := s.prepareCleanup(context.Background(), source, media.Filter{}, []media.Item{media.Bind(item, source)}, nil, blocked, nil); err != nil {
		t.Fatal(err)
	}
	job := s.State().Cleanup
	rows, err := c.CleanupItems(job.ID, 0, 60)
	if err != nil || job.Ready != 0 || rows[0].Detail != blocked[item.InstanceID] {
		t.Fatal("history exclusion was lost", err, job)
	}
	after, _ := c.Item(source.ID, item.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("blocked history item was observed")
	}
}

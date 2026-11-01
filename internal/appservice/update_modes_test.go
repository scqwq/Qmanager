package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func waitUpdate(t *testing.T, s *Media, revision int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state := s.State()
		if state.Revision > revision && state.Phase == "ready" {
			if state.Error != "" {
				t.Fatal(state.Error)
			}
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("update timeout", s.State().Error)
}

func TestFastUpdateStartsAndRepeatsWithoutAutomaticFullAudit(t *testing.T) {
	s, c, _, _ := managerFixture(t)
	s.discoveryInterval = time.Second
	lastFull := s.State().LastFullScan
	sourceID := s.State().Source.ID
	before, _ := c.Active(sourceID)
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "fast"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	state := s.State()
	if state.Report.UpdateKind != "fast" || state.Report.Inspected != 0 || state.Report.KnownSkipped != len(before) || !state.LastFullScan.Equal(lastFull) {
		t.Fatal("fast checked old files", state.Report)
	}
	revision = state.Revision
	waitUpdate(t, s, revision)
	s.Stop()
	state = s.State()
	if state.Report.UpdateKind != "fast" || state.Report.Inspected != 0 || !state.LastFullScan.Equal(lastFull) {
		t.Fatal("periodic refresh became full", state.Report)
	}
	after, _ := c.Active(sourceID)
	for _, old := range before {
		found := false
		for _, current := range after {
			if old.Item.InstanceID == current.Item.InstanceID {
				found = true
				if !old.Seen.Equal(current.Seen) || !old.Item.FirstSeenAt.Equal(current.Item.FirstSeenAt) {
					t.Fatal("fast rewrote untouched evidence")
				}
			}
		}
		if !found {
			t.Fatal("lost cached instance")
		}
	}
}

func TestFastAddsStoppedGapFilesAndFullChecksCachedChanges(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	old := s.Page(media.Filter{Kind: "image"}).Items[0]
	os.Remove(filepath.Join(source.Resolved, filepath.FromSlash(old.RelativePath)))
	testfixture.Image(source.Resolved, "Pic/2025-10/Ori/new-while-stopped.png")
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "fast"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	s.Stop()
	page := s.Page(media.Filter{Kind: "image"})
	if page.Total != 2 {
		t.Fatal("fast erased unchecked old rows or missed old-month addition", page.Total)
	}
	for _, item := range page.Items {
		if item.RelativePath == "Pic/2025-10/Ori/new-while-stopped.png" && item.Method != "rescan_after_gap" {
			t.Fatal("stop-gap time fabricated")
		}
	}
	revision = s.State().Revision
	if err := s.StartUpdate(context.Background(), "full"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	s.Stop()
	page = s.Page(media.Filter{Kind: "image"})
	if page.Total != 1 || s.State().Report.UpdateKind != "full" {
		t.Fatal("manual full did not check missing rows")
	}
}

func TestFullCanVerifyAllMediaRootsRemoved(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	outside := t.TempDir()
	for _, root := range []string{"Pic", "Emoji", "Ptt"} {
		if err := os.Rename(filepath.Join(source.Resolved, root), filepath.Join(outside, root)); err != nil {
			t.Fatal(err)
		}
	}
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "full"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	s.Stop()
	if s.Page(media.Filter{}).Total != 0 {
		t.Fatal("full could not verify an empty saved source")
	}
}

func TestFastBaselinePendingFilesBecomeStableWithoutFullUpdate(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	testfixture.Image(source.Resolved, "Pic/2026-10/Ori/new.png")
	previous := s.State().LastFullScan
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "fast"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	deadline := time.Now().Add(8 * time.Second)
	stable := false
	for time.Now().Before(deadline) {
		page := s.Page(media.Filter{Kind: "image", Month: "2026-10"})
		if len(page.Items) == 1 && page.Items[0].Stability == "stable_heuristic" {
			stable = true
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	s.Stop()
	if !stable || !s.State().LastFullScan.Equal(previous) {
		t.Fatal("stability depended on a full scan", s.State().Error)
	}
}

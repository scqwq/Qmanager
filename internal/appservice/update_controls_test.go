package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
	"time"
)

func TestStopUpdateKeepsIndexAndStopsUntilExplicitResume(t *testing.T) {
	for _, mode := range []string{"fast", "full"} {
		t.Run(mode, func(t *testing.T) {
			s, c, source, _ := managerFixture(t)
			before, _ := c.Active(source.ID)
			full := s.State().LastFullScan
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := s.StartUpdate(ctx, mode); err != nil {
				t.Fatal(err)
			}
			if err := s.StopUpdate(); err != nil {
				t.Fatal(err)
			}
			state := s.State()
			if state.Watching || state.UpdateRunning || state.Stopping || state.BackgroundStage != "" || state.Error != "" || state.Phase != "ready" {
				t.Fatal(state)
			}
			after, _ := c.Active(source.ID)
			if !reflect.DeepEqual(before, after) || !state.LastFullScan.Equal(full) {
				t.Fatal("cancel changed committed evidence")
			}
			testfixture.Image(source.Resolved, "Pic/2026-10/Ori/after-stop.png")
			time.Sleep(150 * time.Millisecond)
			if s.Page(media.Filter{Month: "2026-10"}).Total != 0 {
				t.Fatal("stopped observer still writes index")
			}
			revision := state.Revision
			if err := s.StartUpdate(context.Background(), "fast"); err != nil {
				t.Fatal(err)
			}
			waitUpdate(t, s, revision)
			if err := s.StopUpdate(); err != nil {
				t.Fatal(err)
			}
			if s.Page(media.Filter{Month: "2026-10"}).Total != 1 {
				t.Fatal("resume did not use retained index")
			}
		})
	}
}

func TestBackgroundStabilityDoesNotShowForegroundScanAndEstimateRefreshes(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	testfixture.Image(source.Resolved, "Pic/2026-10/Ori/new.png")
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "fast"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	f := media.Filter{Month: "2026-10"}
	p, err := s.BackupEstimate(f)
	if err != nil || p.Files != 0 || p.ExcludedReasons["pending"] != 1 || p.EstimatedPeak != 0 {
		t.Fatal("missing pending explanation", p, err)
	}
	target := t.TempDir()
	if _, err = s.RememberBackupDirectory(target); err != nil {
		t.Fatal(err)
	}
	// A rejected empty backup must leave observation running so it can stabilize.
	if err = s.BackupFormat(context.Background(), target, f, "directory"); err == nil {
		t.Fatal("pending backup accepted")
	}
	if !s.State().Watching {
		t.Fatal("failed preflight stopped stabilization")
	}
	deadline := time.Now().Add(9 * time.Second)
	for time.Now().Before(deadline) {
		state := s.State()
		if state.UpdateRunning || state.Phase == "scanning" {
			t.Fatal("background work reused foreground progress", state.BackgroundStage)
		}
		p, err = s.BackupEstimate(f)
		if err != nil {
			t.Fatal(err)
		}
		if p.Files == 1 {
			if p.Excluded != 0 || p.EstimatedPeak == 0 {
				t.Fatal(p)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("estimate did not become eligible", s.State().Error)
}

func TestBackupDestinationMemoryAndSourceIsolation(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	for _, bad := range []string{source.Resolved, filepath.Dir(source.Resolved), s.private} {
		if _, err := s.RememberBackupDirectory(bad); err == nil {
			t.Fatal("unsafe output accepted", bad)
		}
	}
	if s.Preference("backup_directory") != "" {
		t.Fatal("unsafe path remembered")
	}
	target := filepath.Join(t.TempDir(), "new-output")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RememberBackupDirectory(target); err != nil {
		t.Fatal(err)
	}
	if s.Preference("backup_directory") != target {
		t.Fatal("path not remembered independently of backup")
	}
	// Stop-update must not cancel a different job type.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.mu.Lock()
	s.jobCancel = cancel
	s.mu.Unlock()
	if err := s.StopUpdate(); err == nil || ctx.Err() != nil {
		t.Fatal("stop-update canceled backup job")
	}
	s.mu.Lock()
	s.jobCancel = nil
	s.mu.Unlock()
}

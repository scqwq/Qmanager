package appservice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
	"time"
)

func pendingFixture(t *testing.T, backlog int) (*Media, *catalog.Catalog, media.Source) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Pic")
	for _, path := range []string{"2025-10/Ori/chosen.png", "2026-09/Ori/other.png"} {
		if err := testfixture.Image(root, path); err != nil {
			t.Fatal(err)
		}
	}
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Add(root, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	source.ID = saved.ID
	r := media.Scan(context.Background(), source, nil)
	base := r.Items[0]
	for n := 0; n < backlog; n++ {
		item := base
		item.RelativePath = fmt.Sprintf("2024-01/Ori/stale-%05d.png", n)
		item.SourceMonth = "2024-01"
		item.Identity = fmt.Sprintf("synthetic-%d", n)
		r.Items = append(r.Items, item)
	}
	if err = c.Apply(context.Background(), source.ID, &r, true, nil, true, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, c, source
}

func TestQuickDoesNotDrainHistoricalPendingBacklog(t *testing.T) {
	s, c, source := pendingFixture(t, 3000)
	before, _ := c.Active(source.ID)
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "fast"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	finished := s.State().Revision
	time.Sleep(3300 * time.Millisecond)
	state := s.State()
	if state.Revision != finished || state.BackgroundStage != "" || state.NeedsFull || state.Report.Inspected != 0 || state.Report.KnownSkipped != 2 {
		t.Fatal("old backlog was rechecked", state.Report, state.Coverage)
	}
	s.Stop()
	after, _ := c.Active(source.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("quick refresh changed old instances or observation times")
	}
}

func TestBackupChecksOnlyFrozenRangeAndPreservesFirstSeen(t *testing.T) {
	s, c, source := pendingFixture(t, 3000)
	before, _ := c.Active(source.ID)
	f := media.Filter{Month: "2025-10"}
	p, err := s.BackupEstimate(f)
	if err != nil || p.Files != 0 || p.Pending != 1 || p.Excluded != 0 {
		t.Fatal(p, err)
	}
	if err = s.BackupFormat(context.Background(), t.TempDir(), f, "directory"); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state := s.State()
	if state.Error != "" || state.Backup == nil || state.Backup.Files != 1 {
		t.Fatal(state.Error, state.Backup)
	}
	m, _, err := mediabackup.Open(context.Background(), state.Backup.Path, nil)
	if err != nil || len(m.Entries) != 1 || m.Entries[0].Item.SourceMonth != "2025-10" {
		t.Fatal(m, err)
	}
	after, _ := c.Active(source.ID)
	for n, old := range before {
		current := after[n]
		if old.Item.SourceMonth != "2025-10" {
			if !reflect.DeepEqual(old, current) {
				t.Fatal("backup touched unrelated cached evidence")
			}
		} else if !old.Item.FirstSeenAt.Equal(current.Item.FirstSeenAt) || old.Item.InstanceID != current.Item.InstanceID || old.Item.Method != current.Item.Method || current.Samples != 3 {
			t.Fatal("lost first-seen evidence or fabricated stability")
		}
	}
}

func waitFirstBackupObservation(t *testing.T, s *Media, c *catalog.Catalog, source media.Source) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := c.Active(source.ID)
		for _, row := range rows {
			if row.Item.SourceMonth == "2025-10" && row.Samples == 2 {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("first backup observation missing", s.State().Error)
}

func TestBackupRejectsChangedCandidateDuringOnDemandChecks(t *testing.T) {
	s, c, source := pendingFixture(t, 0)
	if err := s.BackupFormat(context.Background(), t.TempDir(), media.Filter{Month: "2025-10"}, "directory"); err != nil {
		t.Fatal(err)
	}
	waitFirstBackupObservation(t, s, c, source)
	f, err := os.OpenFile(filepath.Join(source.Resolved, "2025-10/Ori/chosen.png"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("changed"))
	f.Close()
	waitBackupJob(t, s)
	state := s.State()
	if state.Error == "" || state.Backup != nil || len(state.ExcludedItems) != 1 || state.ExcludedItems[0].Code != "unreadable_or_changed" {
		t.Fatal("changed file accepted", state.Error, state.Backup, state.ExcludedItems)
	}
	history, _ := s.Backups()
	if len(history) != 0 {
		t.Fatal("published invalid backup")
	}
}

func TestCancelOnDemandChecksDoesNotPublishOrModifySource(t *testing.T) {
	s, c, source := pendingFixture(t, 0)
	before, _ := testfixture.Snapshot(source.Resolved)
	target := t.TempDir()
	if err := s.BackupFormat(context.Background(), target, media.Filter{Month: "2025-10"}, "zip"); err != nil {
		t.Fatal(err)
	}
	waitFirstBackupObservation(t, s, c, source)
	s.Cancel()
	waitBackupJob(t, s)
	after, _ := testfixture.Snapshot(source.Resolved)
	files, _ := os.ReadDir(target)
	if !reflect.DeepEqual(before, after) || len(files) != 0 || s.State().Backup != nil {
		t.Fatal("canceled check published or changed files")
	}
}

func TestBackupReportsFailedCandidateWithoutLosingOriginalFilter(t *testing.T) {
	s, c, source := pendingFixture(t, 0)
	if err := s.BackupFormat(context.Background(), t.TempDir(), media.Filter{Status: "recognized"}, "directory"); err != nil {
		t.Fatal(err)
	}
	waitFirstBackupObservation(t, s, c, source)
	if err := os.Remove(filepath.Join(source.Resolved, "2025-10/Ori/chosen.png")); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state := s.State()
	if state.Error != "" || state.Backup == nil || state.Backup.Files != 1 || state.Backup.Excluded != 1 {
		t.Fatal(state.Error, state.Backup)
	}
	m, _, err := mediabackup.Open(context.Background(), state.Backup.Path, nil)
	if err != nil || len(m.Entries) != 2 || m.Selection.Status != "recognized" {
		t.Fatal(m, err)
	}
	found := false
	for _, entry := range m.Entries {
		if entry.Item.SourceMonth == "2025-10" {
			found = entry.Excluded == "unreadable_or_changed" && entry.SHA256 == ""
		}
	}
	if !found {
		t.Fatal("failed candidate silently disappeared from selected manifest")
	}
}

func TestBackupPreparationIssueListIsBounded(t *testing.T) {
	s, _, _ := pendingFixture(t, media.MaxIssues+1)
	if err := s.BackupFormat(context.Background(), t.TempDir(), media.Filter{Month: "2024-01"}, "directory"); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state := s.State()
	if state.Error == "" || state.Backup != nil || len(state.ExcludedItems) != media.MaxIssues || state.CheckTotal != media.MaxIssues+1 {
		t.Fatal("unbounded or incomplete preparation results", len(state.ExcludedItems), state.CheckTotal, state.Error)
	}
}

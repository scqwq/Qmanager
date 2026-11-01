package appservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/testfixture"
	"strings"
	"testing"
	"time"
)

func sourceBackup(t *testing.T, s *Media, f media.Filter) catalog.BackupRecord {
	t.Helper()
	before, _ := s.Backups()
	known := map[string]bool{}
	for _, r := range before {
		known[r.ID] = true
	}
	if err := s.Backup(context.Background(), t.TempDir(), f); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	rows, err := s.Backups()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !known[r.ID] {
			return r
		}
	}
	t.Fatal("no new backup record")
	return catalog.BackupRecord{}
}
func prepBackupCleanup(t *testing.T, s *Media, ids ...string) catalog.CleanupJob {
	t.Helper()
	if err := s.PrepareBackupSourceDelete(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state := s.State()
	if state.Error != "" || state.Cleanup == nil || state.Cleanup.State != "prepared" {
		t.Fatalf("prepare %+v %s", state.Cleanup, state.Error)
	}
	return *state.Cleanup
}

func TestBackupSourceCleanupAfterPackageDeletedUsesOriginalManifest(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	before, _ := testfixture.Snapshot(source.Resolved)
	record := sourceBackup(t, s, media.Filter{Kind: "emoji", MonthFrom: "2025-01", MonthTo: "2025-12"})
	after, _ := testfixture.Snapshot(source.Resolved)
	if len(before) != len(after) {
		t.Fatal("backup changed source file set")
	}
	for path, snapshot := range before {
		if after[path] != snapshot {
			t.Fatal("backup modified original", path)
		}
	}
	// A later file in the same selected month must never expand the old backup.
	testfixture.Image(source.Resolved, "Emoji/emoji-recv/2025-12/Ori/added-later.png")
	// An already-loaded independent copy is also outside original-source cleanup.
	if err := s.RestoreBackup(context.Background(), record.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	loaded := s.Page(media.Filter{}).Items
	if err := s.DeleteBackups(context.Background(), []string{record.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	if _, err := os.Stat(record.Path); !os.IsNotExist(err) {
		t.Fatal("package still present")
	}
	job := prepBackupCleanup(t, s, record.ID)
	if len(job.BackupIDs) != 1 || job.Ready != 2 || job.Skipped != 1 || job.Total != 3 {
		t.Fatalf("wrong frozen scope %+v", job)
	}
	// Preparation is read-only even though it has selected the original source.
	if _, err := os.Stat(filepath.Join(source.Resolved, "Emoji/emoji-recv/2025-01/Ori/a.png")); err != nil {
		t.Fatal("prepared cleanup removed source")
	}
	if err := s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	done, _ := c.Cleanup(job.ID)
	if done.Recycled != 2 || *calls != 2 {
		t.Fatalf("cleanup %+v calls %d", done, *calls)
	}
	for _, path := range []string{"Emoji/emoji-recv/2025-12/Ori/added-later.png", "Emoji/emoji-recv/2025-12/OriTemp/growing.tmp", "Emoji/emoji-recv/2026-09/Ori/c.png", "Pic/2025-10/Ori/p.png", "Ptt/2026-09/Ori/v.amr"} {
		if _, err := os.Stat(filepath.Join(source.Resolved, filepath.FromSlash(path))); err != nil {
			t.Fatal("removed outside backed scope", path)
		}
	}
	for _, item := range loaded {
		f, err := media.OpenItem(item)
		if err != nil {
			t.Fatal("removed independent imported copy", err)
		}
		f.Close()
	}
	saved, _ := c.Backup(record.ID)
	if saved.Status != "deleted" || saved.SourceCleanupJob != job.ID || !strings.Contains(saved.SourceCleanupDetail, "回收 2") {
		t.Fatalf("history not retained %+v", saved)
	}
	// A completed job is idempotent; a fresh plan skips originals no longer present.
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	if *calls != 2 {
		t.Fatal("repeated cleanup")
	}
	again := prepBackupCleanup(t, s, record.ID)
	if again.Ready != 0 || again.Skipped != 3 {
		t.Fatal("missing originals became candidates", again)
	}
}

func TestBackupSourceCleanupChecksArchivedHashAndNewInstances(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	record := sourceBackup(t, s, media.Filter{Kind: "image"})
	item := s.Page(media.Filter{Kind: "image"}).Items[0]
	path := filepath.Join(source.Resolved, filepath.FromSlash(item.RelativePath))
	info, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 1
	os.WriteFile(path, b, 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	job := prepBackupCleanup(t, s, record.ID)
	if job.Ready != 0 || job.Skipped != 1 {
		t.Fatal("accepted different content with unchanged metadata", job)
	}
	if *calls != 0 {
		t.Fatal("preflight deleted a file")
	}
	// Restore bytes, then supersede the catalog instance to model disappearance
	// and reappearance. Same content at the same path must not reuse old consent.
	b[len(b)-1] ^= 1
	os.WriteFile(path, b, 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	offline := filepath.Join(t.TempDir(), "original-offline.png")
	if err := os.Rename(path, offline); err != nil {
		t.Fatal(err)
	}
	report := media.Scan(context.Background(), source, nil)
	if err := c.Apply(context.Background(), source.ID, &report, false, nil, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(offline, path); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		report = media.Scan(context.Background(), source, nil)
		if err := c.Apply(context.Background(), source.ID, &report, false, nil, true, time.Now().Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	job = prepBackupCleanup(t, s, record.ID)
	if job.Ready != 0 || job.Skipped != 1 {
		t.Fatal("reappeared instance selected by old backup", job)
	}
}

func TestBackupSourceCleanupFrozenHashRecheckedAtExecution(t *testing.T) {
	s, _, source, calls := managerFixture(t)
	record := sourceBackup(t, s, media.Filter{Kind: "image"})
	job := prepBackupCleanup(t, s, record.ID)
	if job.Ready != 1 {
		t.Fatal(job)
	}
	item := s.Page(media.Filter{Kind: "image"}).Items[0]
	path := filepath.Join(source.Resolved, filepath.FromSlash(item.RelativePath))
	info, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 1
	os.WriteFile(path, b, 0600)
	os.Chtimes(path, info.ModTime(), info.ModTime())
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	if *calls != 0 {
		t.Fatal("execution ignored archived hash")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("changed source removed")
	}
}

func TestBackupSourceCleanupDeduplicatesAndRejectsMixedSources(t *testing.T) {
	s, c, _, _ := managerFixture(t)
	a := sourceBackup(t, s, media.Filter{Kind: "image"})
	b := sourceBackup(t, s, media.Filter{Kind: "image"})
	job := prepBackupCleanup(t, s, a.ID, b.ID, a.ID)
	if job.Total != 1 || job.Ready != 1 || len(job.BackupIDs) != 2 {
		t.Fatal("overlapping backups duplicated source candidates", job)
	}
	// Re-registering or deleting a local package must not erase source results.
	if _, err := s.registerBackup(context.Background(), a.Path); err != nil {
		t.Fatal(err)
	}
	retained, _ := c.Backup(a.ID)
	if retained.SourceCleanupJob != job.ID {
		t.Fatal("re-registration erased source cleanup link")
	}
	second := filepath.Join(t.TempDir(), "Pic")
	testfixture.Image(second, "2025-10/Ori/second.png")
	src, err := s.Select(second)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		report := media.Scan(context.Background(), src, nil)
		if err = c.Apply(context.Background(), src.ID, &report, false, nil, true, time.Now().Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	other := sourceBackup(t, s, media.Filter{})
	if err = s.PrepareBackupSourceDelete(context.Background(), []string{a.ID, other.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error == "" || s.State().Cleanup != nil {
		t.Fatal("mixed original sources accepted")
	}
}

func TestBackupSourceCleanupSkipsConflictingManifestEvidence(t *testing.T) {
	s, c, _, _ := managerFixture(t)
	a := sourceBackup(t, s, media.Filter{Kind: "image"})
	original, _ := c.Backup(a.ID)
	var m mediabackup.Manifest
	if err := json.Unmarshal([]byte(original.Manifest), &m); err != nil {
		t.Fatal(err)
	}
	m.ID = "synthetic-other-backup"
	m.Entries[0].SHA256 = strings.Repeat("0", 64)
	payload, _ := json.Marshal(m)
	other := original
	other.ID = m.ID
	other.Manifest = string(payload)
	other.Digest = catalog.ManifestDigest(payload)
	if err := c.SaveBackup(other); err != nil {
		t.Fatal(err)
	}
	job := prepBackupCleanup(t, s, a.ID, other.ID)
	if job.Ready != 0 || job.Skipped != 1 {
		t.Fatal("conflicting content evidence accepted", job)
	}
}

func TestBackupSourceCleanupFailureDoesNotTouchPackage(t *testing.T) {
	s, _, _, _ := managerFixture(t)
	record := sourceBackup(t, s, media.Filter{Kind: "image"})
	job := prepBackupCleanup(t, s, record.ID)
	s.recycleMedia = func(context.Context, media.Item, string) error { return errors.New("synthetic recycle refusal") }
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	pack, err := mediabackup.OpenPackage(context.Background(), record.Path, t.TempDir(), nil)
	if err != nil {
		t.Fatal("cleanup damaged package", err)
	}
	pack.Cleanup()
}

package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
)

func checkBackupFiles(t *testing.T, s *Media, id string) catalog.BackupRecord {
	t.Helper()
	if err := s.CheckBackupOriginals(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	row, err := s.catalog.Backup(id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestBackupOriginalPresenceUsesOnlySelectedPathsAndRetainsUnknown(t *testing.T) {
	s, c, source, output, row := locationBackup(t, "directory")
	checked := checkBackupFiles(t, s, row.ID)
	if checked.OriginalFiles == nil || checked.OriginalFiles.State != "present" || checked.OriginalFiles.Present != 3 {
		t.Fatal(checked.OriginalFiles)
	}
	externallyRemoveSyntheticBackup(t, output, row.Path)
	if err := os.Remove(filepath.Join(source, "Pic", "2025-01", "Ori", "a.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "Pic", "2025-12", "Ori", "b.png"), []byte("synthetic changed length"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := testfixture.Snapshot(source)
	checked = checkBackupFiles(t, s, row.ID)
	status := checked.OriginalFiles
	if status.State != "partial" || status.Total != 3 || status.Present != 1 || status.Missing != 1 || status.Changed != 1 || status.Unknown != 0 {
		t.Fatal(status)
	}
	// A read-only history check must not silently apply a full update to cache.
	if page, err := c.Page(context.Background(), s.State().Source.ID, media.Filter{}); err != nil || page.Total != 3 {
		t.Fatal(page.Total, err)
	}
	after, _ := testfixture.Snapshot(source)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("presence check changed source")
	}
	if err := os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	checked = checkBackupFiles(t, s, row.ID)
	if checked.OriginalFiles.State != "unknown" || checked.OriginalFiles.Unknown != 3 || checked.OriginalFiles.Missing != 0 {
		t.Fatal("offline source called deleted", checked.OriginalFiles)
	}
	if err := os.Rename(source+"-offline", source); err != nil {
		t.Fatal(err)
	}
	beforeStatus := checked.OriginalFiles
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.CheckBackupOriginals(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	checked, _ = c.Backup(row.ID)
	if s.State().Error == "" || !reflect.DeepEqual(beforeStatus, checked.OriginalFiles) {
		t.Fatal("cancelled check published a partial snapshot")
	}
}

func TestExternalBackupOriginalPresenceDoesNotGuessAccount(t *testing.T) {
	s, _, _, _, row := locationBackup(t, "directory")
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.PreviewBackup(context.Background(), row.Path); err != nil {
		t.Fatal(err)
	}
	checked := checkBackupFiles(t, other, row.ID)
	if checked.OriginalFiles.State != "unlinked" || checked.OriginalFiles.Unknown != 3 {
		t.Fatal(checked.OriginalFiles)
	}
	if s.State().Source == nil {
		t.Fatal("original service unexpectedly changed")
	}
}

func TestRestoreStatusPersistsIndependentOfPackageOperationsAndRecordsFailure(t *testing.T) {
	s, c, _, output, row := locationBackup(t, "directory")
	ctx := context.Background()
	checkBackupFiles(t, s, row.ID)
	if err := s.RestoreBackup(ctx, row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	saved, _ := c.Backup(row.ID)
	if saved.RestoreResult == nil || saved.RestoreResult.State != "qman_only" || saved.RestoreResult.Fallback != 3 || saved.RestoreResult.FinishedAt == nil || saved.RestoreResult.StartedAt.IsZero() || saved.RestoreResult.FinishedAt.Before(saved.RestoreResult.StartedAt) || !saved.OriginalFiles.Stale {
		t.Fatal(saved.RestoreResult, saved.OriginalFiles)
	}
	if _, err := s.PreviewBackup(ctx, row.Path); err != nil {
		t.Fatal(err)
	}
	refreshed, _ := c.Backup(row.ID)
	if !reflect.DeepEqual(saved.RestoreResult, refreshed.RestoreResult) {
		t.Fatal("re-register erased restore status")
	}
	externallyRemoveSyntheticBackup(t, output, row.Path)
	if err := s.RestoreBackup(ctx, row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	failed, _ := c.Backup(row.ID)
	if s.State().Error == "" || failed.RestoreResult.State != "failed" || failed.RestoreResult.FinishedAt == nil || failed.RestoreResult.StartedAt.IsZero() || failed.RestoreResult.FinishedAt.Before(failed.RestoreResult.StartedAt) || failed.RestoreResult.Fallback != 0 {
		t.Fatal("old success survived failed restore", failed.RestoreResult)
	}
	if _, err := c.Import(row.ID); err != nil {
		t.Fatal("earlier loaded copy evidence lost", err)
	}
	if err := s.DeleteBackups(ctx, []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	deleted, _ := c.Backup(row.ID)
	if deleted.Status != "deleted" || !reflect.DeepEqual(deleted.RestoreResult, failed.RestoreResult) {
		t.Fatal("package deletion erased restore result")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Restore cancellation is recorded for an available row, without deleting
	// the earlier import or pretending an interrupted attempt succeeded.
	deleted.Status = "available"
	if err := c.SaveBackup(deleted); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreBackup(ctx, row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	deleted, _ = c.Backup(row.ID)
	if deleted.RestoreResult.State != "cancelled" {
		t.Fatal(deleted.RestoreResult)
	}
}

func TestBackupCleanupStatusLabelsJointResultsAndInvalidatesPresence(t *testing.T) {
	s, c, _, calls := managerFixture(t)
	first := sourceBackup(t, s, media.Filter{Kind: "image"})
	second := sourceBackup(t, s, media.Filter{Kind: "image"})
	checkBackupFiles(t, s, first.ID)
	job := prepBackupCleanup(t, s, first.ID, second.ID)
	prepared, _ := c.Backup(first.ID)
	if prepared.CleanupResult == nil || prepared.CleanupResult.State != "prepared" || !prepared.CleanupResult.Joint {
		t.Fatal(prepared.CleanupResult)
	}
	if err := s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	saved, _ := c.Backup(first.ID)
	if saved.CleanupResult.State != "completed" || saved.CleanupResult.Recycled != 1 || !saved.CleanupResult.Joint || *calls != 1 || !saved.OriginalFiles.Stale {
		t.Fatal(saved.CleanupResult, saved.OriginalFiles)
	}
	checked := checkBackupFiles(t, s, first.ID)
	if checked.OriginalFiles.State != "missing" || checked.OriginalFiles.Stale {
		t.Fatal(checked.OriginalFiles)
	}
}

func TestRestoreStatusDistinguishesTargetSuccessSkipConflictAndFallback(t *testing.T) {
	s, c, _, _, row := locationBackup(t, "directory")
	s.cleanupQQ = func() (bool, error) { return false, nil }
	target := filepath.Join(t.TempDir(), "Tencent Files")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	restore := func() catalog.BackupRecord {
		t.Helper()
		if err := s.RestoreBackup(context.Background(), row.ID, target); err != nil {
			t.Fatal(err)
		}
		waitBackupJob(t, s)
		if s.State().Error != "" {
			t.Fatal(s.State().Error)
		}
		saved, err := c.Backup(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	saved := restore()
	if saved.RestoreResult.State != "completed" || saved.RestoreResult.Written != 3 || saved.RestoreResult.Fallback != 0 {
		t.Fatal(saved.RestoreResult)
	}
	saved = restore()
	if saved.RestoreResult.State != "completed" || saved.RestoreResult.Skipped != 3 {
		t.Fatal(saved.RestoreResult)
	}
	conflict := filepath.Join(target, "12345678", "nt_qq", "nt_data", "Pic", "2025-01", "Ori", "a.png")
	if err := os.WriteFile(conflict, []byte("synthetic existing different content"), 0600); err != nil {
		t.Fatal(err)
	}
	saved = restore()
	if saved.RestoreResult.State != "completed" || saved.RestoreResult.Conflicts != 1 || saved.RestoreResult.Written != 1 {
		t.Fatal(saved.RestoreResult)
	}
	bytes, err := os.ReadFile(conflict)
	if err != nil || string(bytes) != "synthetic existing different content" {
		t.Fatal("conflict was overwritten", err)
	}
	s.cleanupQQ = func() (bool, error) { return true, nil }
	saved = restore()
	if saved.RestoreResult.State != "fallback" || saved.RestoreResult.Fallback != 3 || saved.RestoreResult.Written != 0 {
		t.Fatal("fallback falsely reported as QQ restoration", saved.RestoreResult)
	}
}

package appservice

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExternalBackupConfirmationPreservesHistoryWhenSelectionDisappears(t *testing.T) {
	s, c, _, target, row := locationBackup(t, "directory")
	ctx := context.Background()
	moved := filepath.Join(target, "selected-copy")
	if err := os.Rename(row.Path, moved); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewExternalBackup(ctx, moved, "")
	if err != nil || preview.PreviousPath != row.Path || preview.Record.ID != row.ID {
		t.Fatalf("duplicate not identified: %v", err)
	}
	if registered, err := c.Backup(row.ID); err != nil || registered.Path != row.Path {
		t.Fatal("preview changed the registered path", err)
	}
	aside := moved + "-away"
	if err = os.Rename(moved, aside); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreExternalBackup(ctx, preview.Record, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if registered, err := c.Backup(row.ID); err != nil || registered.Path != row.Path || s.State().Error == "" {
		t.Fatal("missing candidate replaced original registration", err)
	}
	if err = os.Rename(aside, moved); err != nil {
		t.Fatal(err)
	}
	if err = s.RestoreExternalBackup(ctx, preview.Record, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if registered, err := c.Backup(row.ID); err != nil || registered.Path != moved || s.State().Error != "" {
		t.Fatal("confirmed copy was not loaded", err, s.State().Error)
	}
	events, err := s.BackupEvents(row.ID, 0)
	if err != nil || len(events) < 2 || events[0].Action != "加载备份" {
		t.Fatal("load timeline unavailable", err, events)
	}
}

func TestAutomaticInspectionPreservesMarksUntilExplicitDefiniteResult(t *testing.T) {
	s, _, source, _, row := locationBackup(t, "directory")
	ctx := context.Background()
	if _, err := s.MarkBackup(row.ID, map[string]string{"original": "missing", "package": "missing"}); err != nil {
		t.Fatal(err)
	}
	observed, err := s.InspectBackup(ctx, row.ID)
	if err != nil || observed.OriginalFiles.State != "present" || observed.Display.Marks["original"].State != "missing" {
		t.Fatal("automatic observation changed a manual mark", err)
	}
	if err = os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	observed, err = s.InspectBackup(ctx, row.ID)
	if err != nil || observed.OriginalFiles.State != "unknown" {
		t.Fatal("offline source treated as missing", err)
	}
	display, err := s.RecordBackupInspection(observed, nil)
	if err != nil || display.Marks["original"].State != "missing" || display.Marks["package"].State != "" {
		t.Fatal("refresh did not distinguish definite from unknown", err)
	}
}

func TestBackupUploadUsesVerifiedArchiveAndRejectsMovedPackage(t *testing.T) {
	s, _, _, _, row := locationBackup(t, "directory")
	ctx := context.Background()
	selected, err := s.PrepareBackupUpload(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, info, cleanup, err := s.BackupUploadFile(ctx, selected)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".zip" || info.Size() == 0 {
		t.Fatal("verified directory ZIP missing")
	}
	cleanup()
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("temporary ZIP not removed", err)
	}
	if err = os.Rename(row.Path, row.Path+"-moved"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.BackupUploadFile(ctx, selected); err == nil {
		t.Fatal("moved backup was accepted for upload")
	}
}

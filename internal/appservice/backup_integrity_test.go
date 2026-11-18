package appservice

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestZIPPreviewVerificationAndDeletionWithoutUnpackSpace(t *testing.T) {
	s, c, _, _, row := locationBackup(t, "zip")
	ctx := context.Background()
	temporary := filepath.Join(s.private, "temporary")
	if err := os.Rename(temporary, temporary+"-fixture"); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, []byte("synthetic occupied path"), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewHistoryBackup(ctx, row.ID)
	if err != nil || preview.Record.Digest != row.Digest || preview.PreviousPath != "" {
		t.Fatalf("registered ZIP preview used extraction or lost its identity: %v", err)
	}
	observed, err := s.InspectBackup(ctx, row.ID, true)
	if err != nil || observed.LocalState != "present" || observed.PackageCheck == nil {
		t.Fatalf("integrity check with no unpack directory: %s, %v", observed.LocalState, err)
	}
	if err := s.DeleteBackups(ctx, []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	deleted, err := c.Backup(row.ID)
	if err != nil || deleted.Status != "deleted" || deleted.DeletePath != "" {
		t.Fatalf("ZIP deletion did not finish: %s, %s, %v", deleted.Status, deleted.Detail, err)
	}
	if _, err := os.Lstat(row.Path); !os.IsNotExist(err) {
		t.Fatalf("ZIP still exists: %v", err)
	}
}

func TestZIPPayloadCorruptionBlocksRestoreAndDeletion(t *testing.T) {
	s, c, _, _, row := locationBackup(t, "zip")
	ctx := context.Background()
	offset := zipPayloadOffset(t, row.Path)
	f, err := os.OpenFile(row.Path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	b := []byte{0}
	if _, err = f.ReadAt(b, offset); err == nil {
		b[0] ^= 1
		_, err = f.WriteAt(b, offset)
	}
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewHistoryBackup(ctx, row.ID)
	if err != nil || preview.Record.Digest != row.Digest {
		t.Fatalf("preview should stop at ZIP structure and metadata: %v", err)
	}
	if err := s.RestoreBackup(ctx, row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error == "" {
		t.Fatal("restore accepted a corrupted ZIP payload")
	}
	if err := s.DeleteBackups(ctx, []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	current, err := c.Backup(row.ID)
	if err != nil || current.Status != "delete_failed" || current.DeletePath != "" {
		t.Fatalf("invalid ZIP moved or removed: %s, %s, %v", current.Status, current.DeletePath, err)
	}
	if _, err := os.Lstat(row.Path); err != nil {
		t.Fatal(err)
	}
}

func zipPayloadOffset(t *testing.T, path string) int64 {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if strings.HasPrefix(entry.Name, "Media/") || strings.HasPrefix(entry.Name, "Tencent Files/") {
			offset, err := entry.DataOffset()
			if err != nil {
				t.Fatal(err)
			}
			return offset
		}
	}
	t.Fatal("synthetic ZIP has no payload")
	return 0
}

func TestExplicitBackupCheckRestoresIntactAndKeepsDamagedQuarantine(t *testing.T) {
	for _, format := range []string{"directory", "zip"} {
		t.Run(format, func(t *testing.T) {
			s, c, _, _, row := locationBackup(t, format)
			ctx := context.Background()
			quarantine := func() {
				t.Helper()
				row.DeletePath = filepath.Join(filepath.Dir(row.Path), ".qman-delete-synthetic")
				row.Status = "delete_failed"
				if err := os.Rename(row.Path, row.DeletePath); err != nil {
					t.Fatal(err)
				}
				if err := c.SaveBackup(row); err != nil {
					t.Fatal(err)
				}
			}
			quarantine()
			observed, err := s.InspectBackup(ctx, row.ID, true)
			if err != nil || observed.LocalState != "present" || observed.DeletePath != "" {
				t.Fatalf("intact package should return to the original path: %s, %s, %v", observed.LocalState, observed.LocalDetail, err)
			}
			if _, err = os.Stat(row.Path); err != nil {
				t.Fatal(err)
			}
			row = observed
			quarantine()
			if format == "directory" {
				err = os.Remove(filepath.Join(row.DeletePath, "backup.json"))
			} else {
				err = os.WriteFile(row.DeletePath, []byte("damaged ZIP"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			observed, err = s.InspectBackup(ctx, row.ID, true)
			if err != nil || observed.LocalState != "damaged" || observed.DeletePath == "" {
				t.Fatalf("damaged package should stay quarantined: %s, %s, %v", observed.LocalState, observed.LocalDetail, err)
			}
			if _, err = os.Stat(row.Path); !os.IsNotExist(err) {
				t.Fatal("damaged package should not return to the original path", err)
			}
			rows, err := s.Backups()
			if err != nil || len(rows) != 1 || rows[0].LocalState != "damaged" {
				t.Fatalf("lightweight refresh lost integrity result: %v, %v", rows, err)
			}
		})
	}
}

func TestCancelOperationWaitsForResult(t *testing.T) {
	s, _, _, _ := managerFixture(t)
	if err := s.backupJob(context.Background(), "exporting", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	state := s.State()
	if result := s.CancelOperation(state.Phase, state.Revision); !strings.Contains(result, "未生成导出文件夹") || s.State().Phase != "ready" {
		t.Fatalf("cancel outcome: %q", result)
	}
}

func TestDailyDeleteReminderCounters(t *testing.T) {
	s, c, _, _ := managerFixture(t)
	for _, operation := range []string{"source_delete", "backup_delete"} {
		for i := range 3 {
			got, err := s.OperationReminder(operation, "", false)
			if err != nil || (got == operation) != (i < 2) {
				t.Fatalf("reminder %s #%d: %q, %v", operation, i, got, err)
			}
			again, err := s.OperationReminder(operation, "", false)
			if err != nil || again != got {
				t.Fatalf("query should not consume confirmation: %q, %v", again, err)
			}
			if _, err = s.OperationReminder(operation, "", true); err != nil {
				t.Fatal(err)
			}
		}
		yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
		if err := c.SetPreference("operation_reminder_"+operation, `{"day":"`+yesterday+`","count":2}`); err != nil {
			t.Fatal(err)
		}
		if got, err := s.OperationReminder(operation, "", false); err != nil || got != operation {
			t.Fatalf("expected a fresh count on the next day: %q, %v", got, err)
		}
	}
}

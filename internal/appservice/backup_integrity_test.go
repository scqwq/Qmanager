package appservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

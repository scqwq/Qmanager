package appservice

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
)

func TestDeleteBackupHistoryRequiresAcknowledgedAbsentPackage(t *testing.T) {
	for _, format := range []string{"directory", "zip"} {
		t.Run(format, func(t *testing.T) {
			s, c, source, target, row := locationBackup(t, format)
			ctx := context.Background()
			before, _ := testfixture.Snapshot(source)
			if err := s.DeleteBackupHistory(ctx, row.ID); err == nil {
				t.Fatal("existing package history removed")
			}
			externallyRemoveSyntheticBackup(t, target, row.Path)
			if err := s.DeleteBackupHistory(ctx, row.ID); err == nil {
				t.Fatal("unacknowledged missing location history removed")
			}
			if err := s.DeleteBackups(ctx, []string{row.ID}); err != nil {
				t.Fatal(err)
			}
			waitBackupJob(t, s)
			if s.State().Error != "" {
				t.Fatal(s.State().Error)
			}
			if err := s.DeleteBackupHistory(ctx, row.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Backup(row.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("registry row retained", err)
			}
			if rows, err := s.Backups(); err != nil || len(rows) != 0 {
				t.Fatal("forgotten history still visible", rows, err)
			}
			if err := s.DeleteBackupHistory(ctx, row.ID); err != nil {
				t.Fatal("repeat removal failed", err)
			}
			after, _ := testfixture.Snapshot(source)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source changed")
			}
		})
	}
}

func TestDeleteBackupHistoryRejectsUnavailableAndReappearedLocations(t *testing.T) {
	s, c, _, target, row := locationBackup(t, "directory")
	ctx := context.Background()
	aside := row.Path + "-moved"
	if err := os.Rename(row.Path, aside); err != nil {
		t.Fatal(err)
	}
	s.DeleteBackups(ctx, []string{row.ID})
	waitBackupJob(t, s)
	if err := os.Rename(target, target+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackupHistory(ctx, row.ID); err == nil {
		t.Fatal("offline parent accepted")
	}
	if err := os.Rename(target+"-offline", target); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(aside, row.Path); err != nil {
		t.Fatal(err)
	}
	before, _ := testfixture.Snapshot(row.Path)
	if err := s.DeleteBackupHistory(ctx, row.ID); err == nil {
		t.Fatal("reappeared location accepted")
	}
	if _, err := c.Backup(row.ID); err != nil {
		t.Fatal("history lost on failed removal", err)
	}
	after, _ := testfixture.Snapshot(row.Path)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("package changed")
	}
}

func TestDeleteBackupHistoryRevokesSharedPlanAndPreservesLoadedMedia(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	ctx := context.Background()
	first := sourceBackup(t, s, media.Filter{Kind: "image"})
	second := sourceBackup(t, s, media.Filter{Kind: "image"})
	if err := s.RestoreBackup(ctx, first.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	imported, err := c.Import(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	loaded := s.Page(media.Filter{}).Items
	before, _ := testfixture.Snapshot(source.Resolved)
	s.DeleteBackups(ctx, []string{first.ID})
	waitBackupJob(t, s)
	job := prepBackupCleanup(t, s, first.ID, second.ID)
	if err := s.DeleteBackupHistory(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := c.Backup(second.ID)
	if err != nil || remaining.SourceCleanupDetail == "" {
		t.Fatal("other history lost", err)
	}
	savedJob, err := c.Cleanup(job.ID)
	if err != nil || savedJob.State != "interrupted" || s.State().Cleanup.State != "interrupted" {
		t.Fatal("prepared plan still executable", savedJob.State, err)
	}
	if err := s.ExecuteMediaDelete(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error == "" || *calls != 0 {
		t.Fatal("old confirmation executed", *calls)
	}
	if err := s.RecoverCleanup(); err != nil {
		t.Fatal("audit recovery failed after forgetting history", err)
	}
	if _, err := c.Backup(first.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("audit recreated forgotten history", err)
	}
	retained, err := c.Import(first.ID)
	if err != nil || string(retained) != string(imported) {
		t.Fatal("loaded record lost", err)
	}
	for _, item := range loaded {
		f, err := media.OpenItem(item)
		if err != nil {
			t.Fatal("loaded media removed", err)
		}
		f.Close()
	}
	if err := s.ViewLoadedBackup(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" || s.Page(media.Filter{}).Total != len(loaded) {
		t.Fatal("loaded media no longer viewable", s.State().Error)
	}
	after, _ := testfixture.Snapshot(source.Resolved)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source changed")
	}
}

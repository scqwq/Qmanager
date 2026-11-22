package appservice

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
)

func TestCleanupResumeReusesTaskAndFreezesOriginalSelection(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	record := sourceBackup(t, s, media.Filter{Kind: "emoji", MonthFrom: "2025-01", MonthTo: "2025-12"})
	job := prepBackupCleanup(t, s, record.ID)
	rows, err := c.CleanupItems(job.ID, 0, 60)
	if err != nil {
		t.Fatal(err)
	}
	var recycledPath string
	original := s.recycleMedia
	s.recycleMedia = func(ctx context.Context, item media.Item, hash string) error {
		if err := original(ctx, item, hash); err != nil {
			return err
		}
		recycledPath = filepath.Join(item.Root, item.Path)
		s.Cancel()
		return nil
	}
	if err = s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	interrupted, err := c.Cleanup(job.ID)
	if err != nil || interrupted.State != "interrupted" || interrupted.Recycled != 1 {
		t.Fatalf("unexpected interrupted state: %+v %v", interrupted, err)
	}
	if err = testfixture.Image(source.Resolved, filepath.ToSlash(filepath.Join("Emoji", "emoji-recv", "2025-12", "Ori", "later.png"))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(recycledPath, []byte("reappeared file stays untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	s.recycleMedia = original
	resumed := prepBackupCleanup(t, s, record.ID)
	if resumed.ID != job.ID || resumed.Recycled != 1 || resumed.Ready != 1 || resumed.Total != job.Total {
		t.Fatalf("resume changed the frozen task: %+v", resumed)
	}
	if err = s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	done, err := c.Cleanup(job.ID)
	if err != nil || done.Recycled != 2 || *calls != 2 || done.State != "completed" {
		t.Fatalf("unexpected final state: %+v %v", done, err)
	}
	if _, err = os.Stat(recycledPath); err != nil {
		t.Fatal("already-recycled path was retried", err)
	}
	final, err := c.CleanupItems(job.ID, 0, 60)
	if err != nil || len(final) != len(rows) {
		t.Fatal("audit history lost", err)
	}
}

func TestCleanupMissingAndUnavailableAreSeparated(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	item := s.Page(media.Filter{Kind: "image", Limit: 60}).Items[0]
	if err := os.Remove(filepath.Join(source.Resolved, filepath.FromSlash(item.RelativePath))); err != nil {
		t.Fatal(err)
	}
	job := prepareCleanup(t, s, CleanupRequest{IDs: []string{item.InstanceID}})
	if job.Missing != 1 || job.Skipped != 1 || job.Unavailable != 0 || job.Ready != 0 {
		t.Fatalf("missing file misclassified: %+v", job)
	}
	if *calls != 0 {
		t.Fatal("missing preflight invoked recycler")
	}
	if _, err := c.Item(source.ID, item.InstanceID); err == nil {
		t.Fatal("confirmed missing instance retained in current projection")
	}
	offline := source
	offline.Resolved = filepath.Join(t.TempDir(), "offline")
	reason, _ := cleanupPresence(media.Bind(item, offline))
	if reason != "unavailable" {
		t.Fatalf("offline source called %s", reason)
	}
}

func TestDatabaseCompactionLeavesMediaAndQueryProjectionIntact(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	before, err := testfixture.Snapshot(source.Resolved)
	if err != nil {
		t.Fatal(err)
	}
	page := s.Page(media.Filter{Limit: 60})
	usage, err := s.CompactDatabase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	after, err := testfixture.Snapshot(source.Resolved)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("compaction changed source files", err)
	}
	current := s.Page(media.Filter{Limit: 60})
	if current.Total != page.Total || current.Bytes != page.Bytes || usage.After.Used == 0 {
		t.Fatal("compaction changed query results")
	}
}

func TestCleanupRecoveryDoesNotDuplicateCompletedAudit(t *testing.T) {
	s, c, _, _ := managerFixture(t)
	record := sourceBackup(t, s, media.Filter{Kind: "image"})
	job := prepBackupCleanup(t, s, record.ID)
	if err := s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	before, err := c.Backup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := c.BackupEvents(record.ID, record.Digest, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecoverCleanup(); err != nil {
		t.Fatal(err)
	}
	after, err := c.Backup(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := c.BackupEvents(record.ID, record.Digest, 0)
	if err != nil || len(events) != len(current) || !before.CleanupResult.At.Equal(after.CleanupResult.At) {
		t.Fatal("reading a completed task rewrote its audit timestamp", err)
	}
}

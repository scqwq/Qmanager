package appservice

import (
	"context"
	"encoding/json"
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

func backupService(t *testing.T) (*Media, *catalog.Catalog, string) {
	t.Helper()
	private := filepath.Join(t.TempDir(), "private")
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "Tencent Files", "12345678", "nt_qq", "nt_data")
	for _, path := range []string{"Pic/2025-01/Ori/a.png", "Pic/2025-12/Ori/b.png", "Pic/2026-10/Ori/c.png"} {
		if err = testfixture.Image(root, path); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := c.Add(root, "合成账号")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	source.ID = saved.ID
	for n := 0; n < 3; n++ {
		report := media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), source.ID, &report, false, nil, true, time.Now().Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, c, root
}
func waitBackupJob(t *testing.T, s *Media) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for s.busy() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.busy() {
		t.Fatal("job timeout")
	}
}

func TestBackupHistoryRelocateLoadDeleteAndRetainImportedMedia(t *testing.T) {
	s, c, source := backupService(t)
	before, _ := testfixture.Snapshot(source)
	target := t.TempDir()
	f := media.Filter{Kind: "image", MonthFrom: "2025-01", MonthTo: "2025-12"}
	if err := s.BackupFormat(context.Background(), target, f, "directory"); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	history, err := s.Backups()
	if err != nil || len(history) != 1 || history[0].Files != 2 {
		t.Fatalf("history %+v %v", history, err)
	}
	if s.Preference("backup_directory") != target {
		t.Fatal("export path not saved")
	}
	row := history[0]
	moved := row.Path + "-moved"
	if err = os.Rename(row.Path, moved); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PreviewHistoryBackup(context.Background(), row.ID); err == nil {
		t.Fatal("moved package treated as readable")
	}
	preview, err := s.PreviewBackup(context.Background(), moved)
	if err != nil || preview.Record.ID != row.ID || preview.RestorePrefix != "12345678/nt_qq/nt_data" {
		t.Fatalf("relocate %+v %v", preview, err)
	}
	if err = s.RestoreBackup(context.Background(), row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	page := s.Page(media.Filter{})
	if len(page.Items) != 2 || page.Items[0].Method != "baseline" {
		t.Fatal("loaded records/evidence lost")
	}
	firstSeen := page.Items[0].FirstSeenAt
	if err = s.RestoreBackup(context.Background(), row.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	history, _ = s.Backups()
	if len(history) != 1 || s.Page(media.Filter{}).Items[0].FirstSeenAt != firstSeen {
		t.Fatal("duplicate load duplicated/rewrote evidence")
	}
	imported := s.Page(media.Filter{}).Items
	if err = s.DeleteBackups(context.Background(), []string{row.ID, row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	updated, _ := c.Backup(row.ID)
	if updated.Status != "deleted" {
		t.Fatalf("delete status %+v", updated)
	}
	if _, err = os.Stat(moved); !os.IsNotExist(err) {
		t.Fatal("package not deleted")
	}
	for _, item := range imported {
		if f, e := media.OpenItem(item); e != nil {
			t.Fatal("deleted imported media", e)
		} else {
			f.Close()
		}
	}
	if err = s.ViewLoadedBackup(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.Page(media.Filter{}).Total != 2 {
		t.Fatal("loaded media cannot reopen after deleting package")
	}
	if err = s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal("repeat delete failed", s.State().Error)
	}
	secondCatalog, e := catalog.Open(filepath.Join(s.private, "catalog.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	second, e := NewMedia(secondCatalog, s.private)
	if e != nil {
		secondCatalog.Close()
		t.Fatal(e)
	}
	defer second.Close()
	if err = second.ViewLoadedBackup(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, second)
	if second.Page(media.Filter{}).Total != 2 {
		t.Fatal("fresh service lost persisted placements/history", second.State().Error)
	}
	after, _ := testfixture.Snapshot(source)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source changed during backup/load-only/delete")
	}
}

func TestDeleteRefusesChangedPackageAndResumesJournalBeforeRename(t *testing.T) {
	s, c, _ := backupService(t)
	if err := s.Backup(context.Background(), t.TempDir(), media.Filter{}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	history, _ := s.Backups()
	if len(history) != 1 {
		t.Fatal(s.State().Error)
	}
	row := history[0]
	extra := filepath.Join(row.Path, "keep-user-file.txt")
	os.WriteFile(extra, []byte("keep"), 0600)
	if err := s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if _, err := os.Stat(extra); err != nil {
		t.Fatal("deleted extra user file")
	}
	row, _ = c.Backup(row.ID)
	if row.Status != "delete_failed" {
		t.Fatal("unreported refusal")
	}
	os.Remove(extra)
	row.DeletePath = filepath.Join(filepath.Dir(row.Path), ".qman-delete-synthetic-interrupted")
	row.Status = "deleting"
	if err := c.SaveBackup(row); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	row, _ = c.Backup(row.ID)
	if row.Status != "deleted" {
		t.Fatal("cannot resume before rename", row.Detail)
	}
	if _, err := os.Stat(row.Path); !os.IsNotExist(err) {
		t.Fatal("claimed deletion but original remains")
	}
}

func TestZIPHistoryAndSQLMemoryYearParity(t *testing.T) {
	s, c, _ := backupService(t)
	f := media.Filter{Kind: "image", MonthFrom: "2025-01", MonthTo: "2025-12"}
	page := s.Page(f)
	source := s.State().Source
	items, err := c.Selected(context.Background(), source.ID, media.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || media.Paginate(items, f).Total != 2 {
		t.Fatal("year range SQL/memory mismatch")
	}
	if err = s.BackupFormat(context.Background(), t.TempDir(), f, "zip"); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	rows, _ := s.Backups()
	if len(rows) != 1 || rows[0].Format != "zip" {
		t.Fatalf("zip history %+v", rows)
	}
	if _, err = s.PreviewHistoryBackup(context.Background(), rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteBackups(context.Background(), []string{rows[0].ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
}

func TestInterruptedEmptyQuarantineCanFinish(t *testing.T) {
	s, c, _ := backupService(t)
	if err := s.Backup(context.Background(), t.TempDir(), media.Filter{}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	rows, _ := s.Backups()
	if len(rows) != 1 {
		t.Fatal(s.State().Error)
	}
	row := rows[0]
	row.DeletePath = filepath.Join(filepath.Dir(row.Path), ".qman-delete-empty-synthetic")
	row.Status = "deleting"
	if err := c.SaveBackup(row); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(row.Path, row.DeletePath); err != nil {
		t.Fatal(err)
	}
	// Model a crash after every validated member was removed, before the final
	// empty directory and completion record were removed.
	objectPaths, err := filepath.Glob(filepath.Join(row.DeletePath, "objects", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range objectPaths {
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(filepath.Join(row.DeletePath, "objects"))
	os.Remove(filepath.Join(row.DeletePath, "manifest.json"))
	if err = s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	row, _ = c.Backup(row.ID)
	if row.Status != "deleted" {
		t.Fatal("cannot finish interrupted empty stage", row.Detail)
	}
}

func TestHistoryDoesNotFollowDifferentBackupAtOldPath(t *testing.T) {
	s, _, _ := backupService(t)
	if err := s.Backup(context.Background(), t.TempDir(), media.Filter{}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	rows, _ := s.Backups()
	if len(rows) != 1 {
		t.Fatal(s.State().Error)
	}
	row := rows[0]
	path := filepath.Join(row.Path, "manifest.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m mediabackup.Manifest
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.ID = "different-synthetic-backup"
	b, _ = json.Marshal(m)
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PreviewHistoryBackup(context.Background(), row.ID); err == nil {
		t.Fatal("followed a replaced package")
	}
	rows, _ = s.Backups()
	if len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatal("replacement registered as another history entry")
	}
}

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

func locationBackup(t *testing.T, format string) (*Media, *catalog.Catalog, string, string, catalog.BackupRecord) {
	t.Helper()
	s, c, source := backupService(t)
	parent := t.TempDir()
	target := filepath.Join(parent, "export")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.BackupFormat(context.Background(), target, media.Filter{}, format); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	rows, err := s.Backups()
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	registered, err := c.Backup(rows[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, source, target, registered
}
func externallyRemoveSyntheticBackup(t *testing.T, parent, path string) {
	t.Helper()
	relative, err := filepath.Rel(parent, path)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		t.Fatal("fixture removal escaped explicit temporary output")
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			t.Fatal("unexpected fixture object")
		}
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return
	}
	files, dirs := []string{}, []string{}
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			dirs = append(dirs, p)
		} else if d.Type().IsRegular() {
			files = append(files, p)
		} else {
			t.Fatal("fixture contains link or special entry")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range files {
		if err = os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	for n := len(dirs) - 1; n >= 0; n-- {
		if err = os.Remove(dirs[n]); err != nil {
			t.Fatal(err)
		}
	}
}
func TestExternallyRemovedBackupIsObservedAndAcknowledged(t *testing.T) {
	for _, format := range []string{"directory", "zip"} {
		t.Run(format, func(t *testing.T) {
			s, c, source, target, row := locationBackup(t, format)
			before, _ := testfixture.Snapshot(source)
			externallyRemoveSyntheticBackup(t, target, row.Path)
			rows, err := s.Backups()
			if err != nil || rows[0].LocalState != "missing" || rows[0].LocationCheckedAt == nil {
				t.Fatal(rows, err)
			}
			raw, _ := c.Backup(row.ID)
			if raw.Status != "available" || raw.LocalState != "" {
				t.Fatal("presence observation became deletion proof", raw.Status, raw.LocalState)
			}
			if err = s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
				t.Fatal(err)
			}
			waitBackupJob(t, s)
			raw, _ = c.Backup(row.ID)
			if s.State().Error != "" || raw.Status != "deleted" || raw.Manifest != row.Manifest || raw.Digest != row.Digest || raw.DeletePath != "" {
				t.Fatal("missing location was not acknowledged", s.State().Error, raw.Status)
			}
			if err = s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
				t.Fatal(err)
			}
			waitBackupJob(t, s)
			if s.State().Error != "" {
				t.Fatal(s.State().Error)
			}
			after, _ := testfixture.Snapshot(source)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source changed")
			}
		})
	}
}
func TestMovedBackupIsNotSearchedOrDeletedAndCanRelocate(t *testing.T) {
	s, c, _, _, row := locationBackup(t, "directory")
	moved := row.Path + "-moved"
	if err := os.Rename(row.Path, moved); err != nil {
		t.Fatal(err)
	}
	before, _ := testfixture.Snapshot(moved)
	if err := s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	after, _ := testfixture.Snapshot(moved)
	if s.State().Error != "" || !reflect.DeepEqual(before, after) {
		t.Fatal("moved copy touched", s.State().Error)
	}
	if _, err := s.PreviewBackup(context.Background(), moved, row.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := c.Backup(row.ID)
	if raw.Path != moved || raw.Status != "available" {
		t.Fatal("relocation failed", raw.Status)
	}
}
func TestUnavailableParentDoesNotBecomeSuccessfulDeletion(t *testing.T) {
	s, c, _, target, row := locationBackup(t, "directory")
	aside := target + "-offline"
	if err := os.Rename(target, aside); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Backups()
	if err != nil || rows[0].LocalState != "unavailable" {
		t.Fatal(rows, err)
	}
	if err = s.DeleteBackups(context.Background(), []string{row.ID}); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	raw, _ := c.Backup(row.ID)
	if s.State().Error == "" || raw.Status == "deleted" {
		t.Fatal("offline parent acknowledged as deletion")
	}
	if _, err = os.Stat(filepath.Join(aside, filepath.Base(row.Path))); err != nil {
		t.Fatal("offline copy touched", err)
	}
	if err = os.Rename(aside, target); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Backups()
	if err != nil || rows[0].LocalState != "present" {
		t.Fatal(rows, err)
	}
}
func TestBackupReappearingAtClearedLocationNeedsNormalValidation(t *testing.T) {
	for _, action := range []string{"load", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, c, _, _, row := locationBackup(t, "directory")
			aside := row.Path + "-moved"
			if err := os.Rename(row.Path, aside); err != nil {
				t.Fatal(err)
			}
			s.DeleteBackups(context.Background(), []string{row.ID})
			waitBackupJob(t, s)
			if s.State().Error != "" {
				t.Fatal(s.State().Error)
			}
			if err := os.Rename(aside, row.Path); err != nil {
				t.Fatal(err)
			}
			rows, err := s.Backups()
			if err != nil || rows[0].LocalState != "present" || rows[0].Status != "deleted" {
				t.Fatal(rows, err)
			}
			if action == "load" {
				if _, err = s.PreviewHistoryBackup(context.Background(), row.ID); err != nil {
					t.Fatal(err)
				}
				raw, _ := c.Backup(row.ID)
				if raw.Status != "available" {
					t.Fatal("valid restored package was not registered")
				}
			} else {
				s.DeleteBackups(context.Background(), []string{row.ID})
				waitBackupJob(t, s)
				if s.State().Error != "" {
					t.Fatal(s.State().Error)
				}
				if _, err = os.Stat(row.Path); !os.IsNotExist(err) {
					t.Fatal("reappeared package was silently skipped")
				}
			}
		})
	}
}
func TestReplacedLocationObjectIsNeverDeleted(t *testing.T) {
	s, _, _, _, row := locationBackup(t, "directory")
	aside := row.Path + "-original"
	if err := os.Rename(row.Path, aside); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(row.Path, []byte("unrelated file"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Backups()
	if err != nil || rows[0].LocalState != "changed" {
		t.Fatal(rows, err)
	}
	s.DeleteBackups(context.Background(), []string{row.ID})
	waitBackupJob(t, s)
	b, _ := os.ReadFile(row.Path)
	if s.State().Error == "" || string(b) != "unrelated file" {
		t.Fatal("replacement object was deleted")
	}
}

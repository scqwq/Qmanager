package appservice

import (
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"reflect"
	"testing"
)

func TestBackupLocationOnlyResolvesRegisteredDirectories(t *testing.T) {
	s, c, _ := backupService(t)
	dir := filepath.Join(t.TempDir(), "备份 空格,& 目录")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(dir, "备份.zip")
	if err := os.WriteFile(zip, []byte("location lookup never parses or executes this file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"directory", "zip"} {
		path := dir
		if format == "zip" {
			path = zip
		}
		row := catalog.BackupRecord{ID: "location-" + format, Path: path, Format: format, Status: "available", Selection: []byte("{}"), Manifest: "{}", Digest: catalog.ManifestDigest([]byte("{}"))}
		if err := c.SaveBackup(row); err != nil {
			t.Fatal(err)
		}
		before, err := c.Backup(row.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.BackupLocation(row.ID)
		if err != nil || got != dir {
			t.Fatal("wrong Explorer target", got, err)
		}
		after, err := c.Backup(row.ID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("opening location changed history", err)
		}
	}
	if _, err := s.BackupLocation(dir); err == nil {
		t.Fatal("frontend disk path accepted instead of record ID")
	}
	s.mu.Lock()
	s.state.Phase = "deleting"
	s.mu.Unlock()
	if _, err := s.BackupLocation("location-directory"); err == nil {
		t.Fatal("opened during deletion")
	}
}

func TestBackupLocationRefusesMissingWrongTypeAndPendingDeletion(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-folder.exe")
	if err := os.WriteFile(file, []byte("never execute"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, row := range []catalog.BackupRecord{
		{Path: filepath.Join(dir, "missing"), Format: "directory"},
		{Path: "relative", Format: "directory"},
		{Path: "https://example.invalid", Format: "directory"},
		{Path: file, Format: "directory"},
		{Path: dir, Format: "zip"},
		{Path: dir, Format: "unknown"},
		{Path: dir, Format: "directory", Status: "deleting"},
		{Path: dir, Format: "directory", DeletePath: filepath.Join(dir, "staged")},
	} {
		if _, err := backupLocationDirectory(row); err == nil {
			t.Fatal("unsafe location accepted", row)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Log("symbolic-link creation unavailable")
		return
	}
	if _, err := backupLocationDirectory(catalog.BackupRecord{Path: link, Format: "directory"}); err == nil {
		t.Fatal("link target accepted")
	}
}

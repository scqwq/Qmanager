package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/mediabackup"
	"qqarchive/internal/testfixture"
	"strings"
	"testing"
	"time"
)

func TestSelectedMediaExportAndBackupAcrossDirectories(t *testing.T) {
	s, c, source, _ := managerFixture(t)
	ctx := context.Background()
	const duplicate = "Pic/2025-11/Ori/p.png"
	if err := testfixture.Image(source.Resolved, duplicate); err != nil {
		t.Fatal(err)
	}
	for pass := range 3 {
		report := media.Scan(ctx, source, nil)
		if err := c.Apply(ctx, source.ID, &report, false, nil, true, time.Now().Add(time.Duration(pass)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	items, err := c.Selected(ctx, source.ID, media.Filter{Kind: "image"})
	if err != nil || len(items) != 2 {
		t.Fatalf("image selection: %d, %v", len(items), err)
	}
	request := CleanupRequest{IDs: []string{items[0].InstanceID, items[1].InstanceID}}
	target := t.TempDir()
	if err = s.ExportSelected(ctx, target, request); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state := s.State()
	if state.Error != "" || !strings.Contains(state.Message, "已导出 2 个文件") {
		t.Fatalf("export result: %s / %s", state.Error, state.Message)
	}
	dirs, err := os.ReadDir(target)
	if err != nil || len(dirs) != 1 || !strings.HasPrefix(dirs[0].Name(), "Export-") {
		t.Fatalf("export output: %v, %v", dirs, err)
	}
	files, err := os.ReadDir(filepath.Join(target, dirs[0].Name()))
	if err != nil || len(files) != 2 || files[0].Name() != "p.png" || files[1].Name() != "p_2.png" {
		t.Fatalf("flattened files: %v, %v", files, err)
	}
	for _, name := range []string{"p.png", "p_2.png"} {
		payload, readErr := os.ReadFile(filepath.Join(target, dirs[0].Name(), name))
		if readErr != nil || len(payload) == 0 {
			t.Fatalf("export payload: %s, %v", name, readErr)
		}
	}
	settings, err := s.Settings()
	if err != nil || settings.ExportDirectory != "" {
		t.Fatalf("temporary export changed settings: %+v, %v", settings, err)
	}
	records, err := s.Backups()
	if err != nil || len(records) != 0 {
		t.Fatalf("export registered as backup: %v, %v", records, err)
	}
	if err = s.BackupSelected(ctx, t.TempDir(), request, "directory"); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state = s.State()
	if state.Error != "" || state.Backup == nil {
		t.Fatalf("backup result: %s", state.Error)
	}
	manifest, _, err := mediabackup.Open(ctx, state.Backup.Path, nil)
	if err != nil || len(manifest.Entries) != 2 {
		t.Fatalf("backup entries: %d, %v", len(manifest.Entries), err)
	}
	paths := map[string]bool{}
	for _, entry := range manifest.Entries {
		if entry.Excluded != "" || entry.SHA256 == "" {
			t.Fatalf("selected file was not backed up: %+v", entry)
		}
		paths[entry.Item.RelativePath] = true
	}
	if !paths["Pic/2025-10/Ori/p.png"] || !paths[duplicate] {
		t.Fatalf("unexpected backup paths: %v", paths)
	}
	records, err = s.Backups()
	if err != nil || len(records) != 1 || !records[0].SelectedOnly {
		t.Fatalf("selected backup history: %v, %v", records, err)
	}
}

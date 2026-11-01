package media

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"testing"
)

func TestQuickNamesSkipKnownFilesAndKeepDirectoryHintsLight(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	testfixture.Image(root, "2025-10/Ori/old.png")
	source, _ := Resolve(root)
	known := map[string]bool{"2025-10/Ori/old.png": true}
	testfixture.Image(root, "2025-10/Ori/new.png")
	report := ScanWith(context.Background(), source, ScanOptions{NewOnly: true, Known: known}, nil)
	if !report.Quick || !report.Partial || !report.Complete || report.HeaderReads != 1 || report.KnownSkipped != 1 || report.Inspected != 1 || len(report.Covered) != 0 {
		t.Fatalf("quick %+v", report)
	}
	known["2025-10/Ori/new.png"] = true
	hinted := ScanWith(context.Background(), source, ScanOptions{NewOnly: true, Known: known, Targets: []string{"2025-10/Ori"}}, nil)
	if hinted.KnownSkipped != 2 || hinted.Inspected != 0 || len(hinted.Covered) != 0 {
		t.Fatal("directory hint rechecked old files or claimed absence", hinted)
	}
	forced := ScanWith(context.Background(), source, ScanOptions{NewOnly: true, Known: known, Targets: []string{"2025-10/Ori"}, Force: map[string]bool{"2025-10/Ori/old.png": true}}, nil)
	if forced.Inspected != 1 || forced.HeaderReads != 1 || forced.KnownSkipped != 1 {
		t.Fatal("exact file hint lost", forced)
	}
	os.Remove(filepath.Join(root, "2025-10/Ori/old.png"))
	missing := ScanWith(context.Background(), source, ScanOptions{NewOnly: true, Known: known, Targets: []string{"2025-10/Ori/old.png"}}, nil)
	if !missing.Complete || !Covers(missing.Covered, "2025-10/Ori/old.png") {
		t.Fatal("targeted deletion not verified", missing)
	}
}

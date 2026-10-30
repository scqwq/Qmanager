package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"strings"
	"testing"
)

func TestWindowsJunctionNotFollowed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	if err := testfixture.Image(root, "2026-09/Ori/inside.png"); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := testfixture.Image(outside, "outside.png"); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	quote := func(p string) string { return "'" + strings.ReplaceAll(p, "'", "''") + "'" }
	command := "New-Item -ItemType Junction -Path " + quote(link) + " -Target " + quote(outside) + " -ErrorAction Stop | Out-Null"
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic junction setup failed: %v %s", err, output)
	}
	defer os.Remove(link)
	source, _ := Resolve(root)
	r := Scan(context.Background(), source, nil)
	if r.Files != 1 || r.Complete || r.IssueCount == 0 {
		t.Fatal("junction was followed or silently ignored", r.Files, r.Issues)
	}
	if _, err := OpenRelative(root, "junction/outside.png"); err == nil {
		t.Fatal("junction preview allowed")
	}
}

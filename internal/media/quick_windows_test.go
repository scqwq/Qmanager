package media

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"testing"
)

func TestQuickDoesNotOpenLockedKnownMedia(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	testfixture.Image(root, "2025-10/Ori/a.png")
	source, _ := Resolve(root)
	p, _ := windows.UTF16PtrFromString(filepath.Join(root, "2025-10/Ori/a.png"))
	handle, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	r := ScanWith(context.Background(), source, ScanOptions{NewOnly: true, Known: map[string]bool{"2025-10/Ori/a.png": true}}, nil)
	if !r.Complete || r.KnownSkipped != 1 || r.Inspected != 0 || r.IssueCount != 0 {
		t.Fatal("quick tried to open cached file", r)
	}
}
func TestQuickRejectsKnownPathReplacedByJunction(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	testfixture.Image(root, "2025-10/Ori/a.png")
	source, _ := Resolve(root)
	path := filepath.Join(root, "2025-10/Ori/a.png")
	os.Remove(path)
	outside := t.TempDir()
	testfixture.Image(outside, "keep.png")
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", path, outside).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	r := ScanWith(context.Background(), source, ScanOptions{NewOnly: true, Known: map[string]bool{"2025-10/Ori/a.png": true}}, nil)
	if r.Complete || r.KnownSkipped != 0 || r.Inspected != 0 {
		t.Fatal("followed or silently reused junction", r)
	}
}

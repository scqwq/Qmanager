package media

import (
	"context"
	"golang.org/x/sys/windows"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"testing"
)

func TestLockedFileCannotProduceCompleteScan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	if err := testfixture.Image(root, "2026-09/Ori/locked.png"); err != nil {
		t.Fatal(err)
	}
	path, _ := windows.UTF16PtrFromString(NativePath(filepath.Join(root, "2026-09/Ori/locked.png")))
	h, err := windows.CreateFile(path, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	source, _ := Resolve(root)
	r := Scan(context.Background(), source, nil)
	if r.Complete || r.IssueCount == 0 {
		t.Fatal("locked file silently accepted")
	}
}

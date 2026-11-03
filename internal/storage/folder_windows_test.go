package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// Rejected inputs must never reach ShellExecute, so normal tests open no windows.
func TestOpenFolderRejectsFilesAndInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "archive.zip")
	if err := os.WriteFile(file, []byte("not executed"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, "relative", "https://example.invalid", filepath.Join(dir, "missing")} {
		if err := OpenFolder(path); err == nil {
			t.Fatal("non-directory shell target accepted", path)
		}
	}
}

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRecycleConfirmationAndAbort(t *testing.T) {
	if os.Getenv("QMAN_NATIVE_RECYCLE_TEST") != "1" {
		t.Skip("opt-in: synthetic Recycle Bin test")
	}
	path := filepath.Join(t.TempDir(), "Qman-synthetic-recycle-check.txt")
	content := []byte("Qman disposable synthetic recycle fixture")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := recycleNative(context.Background(), path, func() error { calls++; return errors.New("synthetic pre-delete refusal") })
	if err == nil || calls != 1 {
		t.Fatal("pre-delete check was not enforced", calls, err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(content) {
		t.Fatal("refused item changed")
	}
	location, err := recycleNative(context.Background(), path, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if location == "" {
		t.Fatal("missing actual Recycle Bin item")
	}
	got, err := os.ReadFile(location)
	if err != nil || string(got) != string(content) {
		t.Fatal("recycle payload not verified", err)
	}
	// Intentionally never empty or permanently delete any Recycle Bin content.
}

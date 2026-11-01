package mediacleanup

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
)

func cleanupFixture(t *testing.T) (media.Source, media.Item, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Pic")
	if err := testfixture.Image(root, "2025-10/Ori/synthetic.png"); err != nil {
		t.Fatal(err)
	}
	source, err := media.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	r := media.Scan(context.Background(), source, nil)
	if len(r.Items) != 1 {
		t.Fatal(r.Issues)
	}
	item := r.Items[0]
	item.Stability = "stable_heuristic"
	digest, err := Hash(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	return source, item, digest
}

func TestLockedIdentityRejectsReplacementBeforeRecycling(t *testing.T) {
	source, item, _ := cleanupFixture(t)
	if err := Validate(item, source); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(item.Root, item.Path)
	if err := os.Rename(original, original+".old"); err != nil {
		t.Fatal(err)
	}
	if err := testfixture.Image(item.Root, item.Path); err != nil {
		t.Fatal(err)
	}
	if guard, err := media.LockForRecycle(item); err == nil {
		guard.Close()
		t.Fatal("accepted a different file at same path")
	}
}

func TestNativeRecycleSyntheticOnly(t *testing.T) {
	if os.Getenv("QMAN_NATIVE_RECYCLE_TEST") != "1" {
		t.Skip("opt-in: creates one synthetic Windows Recycle Bin entry")
	}
	_, item, digest := cleanupFixture(t)
	if err := Recycle(context.Background(), item, digest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(item.Root, item.Path)); !os.IsNotExist(err) {
		t.Fatal("source still exists after confirmed recycle", err)
	}
}

package mediabackup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
)

func TestRestoreRejectsInternalJunction(t *testing.T) {
	source, items, c := fixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := OpenPackage(context.Background(), out.Path, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pack.Cleanup()
	pack.Manifest.RestorePrefix = "12345678/nt_qq/nt_data"
	target := filepath.Join(t.TempDir(), "Tencent Files")
	parent := filepath.Join(target, "12345678", "nt_qq", "nt_data")
	os.MkdirAll(parent, 0700)
	outside := t.TempDir()
	testfixture.File(outside, "keep.txt", []byte("keep"))
	before, _ := testfixture.Snapshot(outside)
	if result, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(parent, "Pic"), outside).CombinedOutput(); err != nil {
		t.Fatalf("junction fixture: %v %s", err, result)
	}
	restored, err := Restore(context.Background(), pack.Manifest, pack.Items, target, t.TempDir(), nil)
	if err != nil || restored.Fallback != 2 || restored.Written != 0 {
		t.Fatalf("junction %+v %v", restored, err)
	}
	after, _ := testfixture.Snapshot(outside)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("followed junction outside media path")
	}
}

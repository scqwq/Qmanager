package mediabackup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
)

func TestTreeReadAndDeleteRejectInternalJunction(t *testing.T) {
	source, items, c := accountTreeFixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := OpenPackage(context.Background(), out.Path, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pack.Cleanup()
	m := pack.Manifest
	b, _ := json.Marshal(m)
	pic := filepath.Join(out.Path, "Tencent Files", "12345678", "nt_qq", "nt_data", "Pic")
	moved := filepath.Join(t.TempDir(), "known-payload")
	if err = os.Rename(pic, moved); err != nil {
		t.Fatal(err)
	}
	if output, e := exec.Command("cmd", "/c", "mklink", "/J", pic, moved).CombinedOutput(); e != nil {
		t.Fatalf("junction fixture: %v %s", e, output)
	}
	before, _ := testfixture.Snapshot(moved)
	if _, _, e := Open(context.Background(), out.Path, nil); e == nil {
		t.Fatal("reader followed junction")
	}
	row := catalog.BackupRecord{ID: m.ID, Path: out.Path, DeletePath: out.Path, Format: "directory", Manifest: string(b), Digest: catalog.ManifestDigest(b)}
	if e := DeleteQuarantined(context.Background(), row); e == nil {
		t.Fatal("delete followed junction")
	}
	after, _ := testfixture.Snapshot(moved)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("junction target modified")
	}
}

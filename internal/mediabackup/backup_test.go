package mediabackup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
	"time"
)

func fixture(t *testing.T) (media.Source, []media.Item, *catalog.Catalog) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "nt_data")
	for _, p := range []string{"Pic/2026-09/Ori/图一.jpg", "Pic/2026-09/Ori/图二.png"} {
		if err := testfixture.Image(root, p); err != nil {
			t.Fatal(err)
		}
	}
	testfixture.File(root, "Pic/2026-09/Ori/unknown", []byte("unknown"))
	source, err := media.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	saved, err := c.Add(root, "测试来源")
	if err != nil {
		t.Fatal(err)
	}
	source.ID = saved.ID
	source.Name = saved.Name
	var r media.Report
	for i := 0; i < 3; i++ {
		r = media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), saved.ID, &r, false, nil, true, time.Now().UTC().Add(time.Duration(i)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return source, r.Items, c
}
func TestIndependentBackupDedupAndTamper(t *testing.T) {
	source, items, c := fixture(t)
	before, _ := testfixture.Snapshot(source.Resolved)
	out, err := Build(context.Background(), source, items, media.Filter{Month: "2026-09"}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Files != 2 || out.UniqueObjects != 1 || out.Excluded != 1 {
		t.Fatalf("unexpected result %+v", out)
	}
	after, _ := testfixture.Snapshot(source.Resolved)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("source changed")
	}
	if err = os.Rename(source.Resolved, source.Resolved+"-offline"); err != nil {
		t.Fatal(err)
	}
	m, reopened, err := Open(context.Background(), out.Path, nil)
	if err != nil || len(reopened) != 2 {
		t.Fatal("independent open failed", err)
	}
	for _, item := range reopened {
		if item.Method != "baseline" || item.FirstSeenAt.IsZero() {
			t.Fatal("evidence lost")
		}
		if _, err = media.Thumbnail(context.Background(), item); err != nil {
			t.Fatal("backup preview failed", err)
		}
	}
	fresh, err := catalog.Open(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	payload, _ := json.Marshal(m)
	if err = fresh.RestoreEvidence(m.ID, payload); err != nil {
		t.Fatal(err)
	}
	for _, entry := range m.Entries {
		if entry.SHA256 != "" {
			if err = os.WriteFile(filepath.Join(out.Path, "objects", entry.SHA256), []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if _, _, err = Open(context.Background(), out.Path, nil); err == nil {
		t.Fatal("tampered object accepted")
	}
}
func TestCancelIsolationAndUnsafeManifest(t *testing.T) {
	source, items, c := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	parent := t.TempDir()
	if _, err := Build(ctx, source, items, media.Filter{}, parent, nil, c.History, nil); err == nil {
		t.Fatal("cancel ignored")
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 0 {
		t.Fatal("cancel left staged output")
	}
	if _, err := Build(context.Background(), source, items, media.Filter{}, source.Resolved, nil, c.History, nil); err == nil {
		t.Fatal("source output allowed")
	}
	out, err := Build(context.Background(), source, items, media.Filter{}, parent, nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(out.Path, "manifest.json")
	b, _ := os.ReadFile(path)
	var m Manifest
	json.Unmarshal(b, &m)
	m.Entries[0].Item.RelativePath = "../escape"
	b, _ = json.Marshal(m)
	os.WriteFile(path, b, 0600)
	if _, _, err = Open(context.Background(), out.Path, nil); err == nil {
		t.Fatal("unsafe metadata accepted")
	}
	for _, bad := range []string{"C:/x", "//server/a", "a:stream", "CON", "a/../b", "a/aux.txt", "a\\b", "a./b"} {
		if safeRelative(bad) {
			t.Fatal("unsafe path", bad)
		}
	}
}

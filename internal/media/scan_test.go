package media

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"reflect"
	"strings"
	"testing"
)

func TestInventoryAndPreviewLeaveSourceUnchanged(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	for _, p := range []string{"Pic/2026-09/Ori/中文 😀.jpg", "Pic/2026-09/Thumb/preview.png", "Video/2026-10/Thumb/cover.png", "Emoji/emoji-recv/2026-10/Ori/emoji.jpg"} {
		if err := testfixture.Image(root, p); err != nil {
			t.Fatal(err)
		}
	}
	for p, b := range map[string][]byte{"Ptt/2026-10/Ori/sound.amr": []byte("\x02#!SILK_V3test"), "Pic/2026-09/Ori/empty": {}, "Pic/2026-09/Ori/unknown.dat": []byte("unknown"), "Pic/2026-09/Ori/broken.png": []byte("\x89PNG\r\n\x1a\n"), "Pic/2026-09/OriTemp/part.tmp": []byte("part"), "msf/private": []byte("must not scan")} {
		if err := testfixture.File(root, p, b); err != nil {
			t.Fatal(err)
		}
	}
	before, err := testfixture.Snapshot(root)
	if err != nil {
		t.Fatal(err)
	}
	source, err := Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	r := Scan(context.Background(), source, nil)
	if !r.Complete || r.Files != 9 {
		t.Fatalf("files=%d complete=%v issues=%+v", r.Files, r.Complete, r.Issues)
	}
	states := map[string]bool{}
	for _, item := range r.Items {
		states[item.Status] = true
		if strings.Contains(item.RelativePath, "msf") {
			t.Fatal("escaped allowlist")
		}
		if strings.Contains(item.RelativePath, "中文") {
			if item.Format != "png" || item.SourceMonth != "2026-09" || item.CreatedAt == nil {
				t.Fatalf("metadata %+v", item)
			}
			b, e := Thumbnail(context.Background(), item)
			if e != nil || len(b) == 0 {
				t.Fatalf("thumbnail %v", e)
			}
		}
	}
	for _, s := range []string{"recognized", "empty", "unknown", "invalid_header"} {
		if !states[s] {
			t.Fatal("missing status", s)
		}
	}
	after, err := testfixture.Snapshot(root)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("source changed", err)
	}
	p := Paginate(r.Items, Filter{Kind: "image", Month: "2026-09", Limit: 2})
	if p.Total != 6 || len(p.Items) != 2 {
		t.Fatalf("pagination %+v", p)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = Scan(ctx, source, nil)
	if r.Complete || !r.Cancelled {
		t.Fatal("cancel not propagated")
	}
}
func TestRejectInternalLinkAndChangedPreview(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	if err := testfixture.Image(root, "2026-09/Ori/image.jpg"); err != nil {
		t.Fatal(err)
	}
	source, _ := Resolve(root)
	r := Scan(context.Background(), source, nil)
	item := r.Items[0]
	if err := testfixture.File(root, "2026-09/Ori/image.jpg", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if _, err := Thumbnail(context.Background(), item); err == nil {
		t.Fatal("stale preview allowed")
	}
	if _, err := OpenRelative(root, "../outside"); err == nil {
		t.Fatal("traversal accepted")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Log("symlink creation unavailable; native junction verification is separate")
		return
	}
	r = Scan(context.Background(), source, nil)
	if r.Complete || r.IssueCount == 0 {
		t.Fatal("link not reported")
	}
}
func TestLongUnicodePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	path := "2026-09/Ori/" + strings.Repeat("长路径", 75) + ".png"
	if err := testfixture.Image(root, path); err != nil {
		t.Fatal(err)
	}
	source, _ := Resolve(root)
	r := Scan(context.Background(), source, nil)
	if !r.Complete || r.Files != 1 || !r.Items[0].Preview {
		t.Fatalf("long path failed %+v", r.Issues)
	}
}

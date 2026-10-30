package media

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"testing"
)

func TestCachedFormatReuseAndTargetedInventory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	for _, p := range []string{"Pic/2026-09/Ori/a.jpg", "Pic/2026-10/Ori/b.jpg", "Video/2026-10/Thumb/c.jpg"} {
		if err := testfixture.Image(root, p); err != nil {
			t.Fatal(err)
		}
	}
	source, _ := Resolve(root)
	first := Scan(context.Background(), source, nil)
	if first.HeaderReads != 3 {
		t.Fatal(first.HeaderReads)
	}
	previous := map[string]Item{}
	for _, i := range first.Items {
		previous[i.RelativePath] = i
	}
	second := ScanWith(context.Background(), source, ScanOptions{Previous: previous}, nil)
	if second.Reused != 3 || second.HeaderReads != 0 {
		t.Fatalf("cache reuse failed reads=%d reused=%d", second.HeaderReads, second.Reused)
	}
	partial := ScanWith(context.Background(), source, ScanOptions{Previous: previous, Force: map[string]bool{"Pic/2026-09/Ori/a.jpg": true}, Targets: []string{"Pic/2026-09/Ori/a.jpg"}}, nil)
	if !partial.Partial || partial.Files != 1 || partial.HeaderReads != 1 || !Covers(partial.Covered, "Pic/2026-09/Ori/a.jpg") {
		t.Fatal("targeted scan", partial)
	}
	os.Remove(filepath.Join(root, "Pic/2026-09/Ori/a.jpg"))
	deleted := ScanWith(context.Background(), source, ScanOptions{Previous: previous, Targets: []string{"Pic/2026-09/Ori/a.jpg"}}, nil)
	if !deleted.Complete || deleted.Files != 0 || !Covers(deleted.Covered, "Pic/2026-09/Ori/a.jpg") {
		t.Fatal("missing target not covered")
	}
}

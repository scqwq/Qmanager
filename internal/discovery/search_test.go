package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
)

func searchFixture(t *testing.T, root string) string {
	t.Helper()
	data := filepath.Join(root, "synthetic-account", "nt_qq", "nt_data")
	if err := os.MkdirAll(filepath.Join(data, "Pic", "2026-11", "Ori"), 0700); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDirectorySearchCandidatesAndBackupExclusions(t *testing.T) {
	docs := t.TempDir()
	first := filepath.Join(docs, "Tencent Files")
	searchFixture(t, first)
	searchFixture(t, filepath.Join(first, "nested-account-copy"))
	second := filepath.Join(docs, "迁移", "Tencent Files")
	searchFixture(t, second)
	backup := filepath.Join(docs, "backup-package")
	searchFixture(t, filepath.Join(backup, "Tencent Files"))
	if err := os.MkdirAll(filepath.Join(backup, ".qman"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "backup.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(docs, "private")
	searchFixture(t, filepath.Join(private, "Tencent Files"))
	output := filepath.Join(docs, "output")
	searchFixture(t, filepath.Join(output, "Tencent Files"))
	if err := os.MkdirAll(filepath.Join(docs, "ordinary", "Pic"), 0700); err != nil {
		t.Fatal(err)
	}
	before, _ := testfixture.Snapshot(docs)
	r, err := Search(context.Background(), SearchOptions{Roots: []string{docs}, Excluded: []string{private, output}})
	if err != nil || !r.Complete || len(r.Candidates) != 2 {
		t.Fatal("unexpected candidates", r, err)
	}
	got := map[string]bool{}
	for _, c := range r.Candidates {
		got[c.Root] = true
	}
	if !got[first] || !got[second] {
		t.Fatal("backup/copy confused with source", got)
	}
	after, _ := testfixture.Snapshot(docs)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("search modified source")
	}
	r, err = Search(context.Background(), SearchOptions{Roots: []string{filepath.Join(backup, "Tencent Files")}})
	if err != nil || len(r.Candidates) != 0 {
		t.Fatal("started inside a backup", r, err)
	}
}

func TestDirectorySearchStopsAtMediaAndPrefersKnownLocation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tencent Files")
	data := searchFixture(t, root)
	for n := 0; n < 40; n++ {
		if err := os.MkdirAll(filepath.Join(data, "Pic", "deep", fmt.Sprint(n)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Search(context.Background(), SearchOptions{Roots: []string{data, filepath.Join(t.TempDir(), "missing")}})
	if err != nil || len(r.Candidates) != 1 || r.Candidates[0].Root != root || r.Directories != 1 {
		t.Fatal("read media tree or missed direct source", r, err)
	}
	r, err = Search(context.Background(), SearchOptions{Roots: []string{filepath.Join(data, "Pic")}})
	if err != nil || r.Directories != 0 || len(r.Candidates) != 0 {
		t.Fatal("enumerated an explicitly supplied media subtree", r, err)
	}
	empty := t.TempDir()
	r, err = Search(context.Background(), SearchOptions{Roots: []string{empty, empty, empty}})
	if err != nil || r.Directories != 1 {
		t.Fatal("did not deduplicate roots", r, err)
	}
}

func TestDirectorySearchSharedLimitsAndCancellation(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, root := range []string{first, second} {
		for n := 0; n < MaxDirectories/2+2; n++ {
			if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("folder-%04d", n)), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	r, err := Search(context.Background(), SearchOptions{Roots: []string{first, second}})
	if err != nil || r.Complete || r.Directories > MaxDirectories {
		t.Fatal("per-root budget reset", r, err)
	}
	deep := t.TempDir()
	path := deep
	for n := 0; n < MaxDepth+2; n++ {
		path = filepath.Join(path, "level")
	}
	searchFixture(t, filepath.Join(path, "Tencent Files"))
	r, err = Search(context.Background(), SearchOptions{Roots: []string{deep}})
	if err != nil || r.Complete || len(r.Candidates) != 0 {
		t.Fatal("depth limit ignored", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Search(ctx, SearchOptions{Roots: []string{first}}); err != context.Canceled {
		t.Fatal("cancellation ignored", err)
	}
}

func TestDirectorySearchRefusesLinksAndDiskRoots(t *testing.T) {
	root := t.TempDir()
	volume := filepath.VolumeName(root) + string(filepath.Separator)
	r, err := Search(context.Background(), SearchOptions{Roots: []string{volume, "relative"}})
	if err != nil || r.Directories != 0 || r.Complete {
		t.Fatal("disk root or relative scope accepted", r, err)
	}
	outside := t.TempDir()
	searchFixture(t, filepath.Join(outside, "Tencent Files"))
	if err = os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Log("symbolic-link creation unavailable")
		return
	}
	r, err = Search(context.Background(), SearchOptions{Roots: []string{root}})
	if err != nil || len(r.Candidates) != 0 {
		t.Fatal("followed internal link", r, err)
	}
}

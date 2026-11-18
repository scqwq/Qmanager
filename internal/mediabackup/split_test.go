package mediabackup

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
)

func TestSplitZIPPlansBeforeWriting(t *testing.T) {
	const path = "Media/Pic/长文件名.jpg"
	n := SplitZIPLimit - splitZIPFooter - zipEntryBound(path, 0)
	tree := treeSet{Files: map[string]treeFile{path: {Size: n}, "Media/Pic/其他.jpg": {Size: 1}}}
	parts, err := planSplit(tree, SplitZIPLimit)
	if err != nil || len(parts) != 2 || len(parts[0]) != 1 || n < 1<<32 {
		t.Fatalf("unexpected ZIP64-sized plan: %v, %v", parts, err)
	}
	tree.Files[path] = treeFile{Size: n + 1}
	if _, err := planSplit(tree, SplitZIPLimit); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("oversized single media accepted or unnamed: %v", err)
	}
}

func TestSplitZIPRoundTripAndDelete(t *testing.T) {
	ctx := context.Background()
	source, items, c := fixture(t)
	origin, err := Build(ctx, source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readMetadata(ctx, origin.Path)
	if err != nil {
		t.Fatal(err)
	}
	limit := int64(0)
	for _, e := range m.Entries {
		if e.Excluded != "" {
			continue
		}
		path, err := PayloadPath(m, e)
		if err != nil {
			t.Fatal(err)
		}
		limit = max(limit, splitZIPFooter+zipEntryBound(path, e.Item.Bytes))
	}
	path := filepath.Join(t.TempDir(), "分包备份")
	got, err := writeSplit(ctx, origin.Path, path, m, limit)
	if err != nil || len(got.Manifest.Parts) != 2 || got.Manifest.Layout != SplitZIP {
		t.Fatalf("unexpected split result: %v, %v", got.Manifest.Parts, err)
	}
	for _, part := range got.Manifest.Parts {
		r, e := zip.OpenReader(filepath.Join(path, part.Name))
		if e != nil {
			t.Fatal(e)
		}
		if len(r.File) != 1 || part.Size > limit || r.File[0].Method != zip.Store {
			t.Errorf("independent ZIP has unexpected entries or size: %v", part)
		}
		r.Close()
	}
	temporary := t.TempDir()
	pack, err := OpenPackage(ctx, path, temporary, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Items) != 2 || len(pack.Manifest.Entries) != 3 || catalog.ManifestDigest(pack.Bytes) != catalog.ManifestDigest(got.Bytes) {
		t.Fatal("split package lost media or history")
	}
	pack.Cleanup()

	first := filepath.Join(path, got.Manifest.Parts[0].Name)
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPackage(ctx, path, temporary, nil); err == nil {
		t.Fatal("missing part accepted")
	}
	if err := os.WriteFile(first, data, 0600); err != nil {
		t.Fatal(err)
	}
	row := catalog.BackupRecord{
		ID: m.ID, Format: SplitZIP, Path: path + "-original", DeletePath: path,
		Manifest: string(got.Bytes), Digest: catalog.ManifestDigest(got.Bytes),
	}
	data[0] ^= 1
	if err := os.WriteFile(first, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectSplit(ctx, path, true); err == nil {
		t.Fatal("corrupt part accepted")
	}
	if err := DeleteQuarantined(ctx, row); err == nil {
		t.Fatal("changed part deleted")
	}
	data[0] ^= 1
	if err := os.WriteFile(first, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteQuarantined(ctx, row); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("split backup still exists: %v", err)
	}
}

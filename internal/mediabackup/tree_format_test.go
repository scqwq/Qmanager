package mediabackup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"strings"
	"testing"
	"time"
)

func accountTreeFixture(t *testing.T) (media.Source, []media.Item, *catalog.Catalog) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Tencent Files", "12345678", "nt_qq", "nt_data")
	for _, p := range []string{"Pic/2024-08/Ori/same.png", "Pic/2024-09/Ori/same.png", "Emoji/emoji-recv/2024-09/Ori/same.png"} {
		if err := testfixture.Image(root, p); err != nil {
			t.Fatal(err)
		}
	}
	c, err := catalog.Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	saved, err := c.Add(root, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	source.ID = saved.ID
	var r media.Report
	for n := 0; n < 3; n++ {
		r = media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), source.ID, &r, false, nil, true, time.Now().Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return source, r.Items, c
}
func TestTreeCopiesEveryOccurrenceAndRestoresAllPaths(t *testing.T) {
	source, items, c := accountTreeFixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Files != 3 || out.PayloadFiles != 3 || out.Bytes != items[0].Bytes*3 {
		t.Fatal("copies were deduplicated", out)
	}
	pack, err := OpenPackage(context.Background(), out.Path, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pack.Cleanup()
	if len(pack.Items) != 3 {
		t.Fatal("occurrences merged")
	}
	var previous os.FileInfo
	hash := ""
	seen := map[string]bool{}
	for _, e := range pack.Manifest.Entries {
		rel, err := PayloadPath(pack.Manifest, e)
		if err != nil {
			t.Fatal(err)
		}
		want := "Tencent Files/12345678/nt_qq/nt_data/" + e.Item.RelativePath
		if rel != want {
			t.Fatal(rel, want)
		}
		info, err := os.Stat(filepath.Join(out.Path, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && os.SameFile(previous, info) {
			t.Fatal("hard-linked occurrences")
		}
		previous = info
		if hash != "" && hash != e.SHA256 {
			t.Fatal("fixture not identical")
		}
		hash = e.SHA256
		if seen[e.Item.InstanceID] {
			t.Fatal("instances merged")
		}
		seen[e.Item.InstanceID] = true
	}
	target := filepath.Join(t.TempDir(), "Tencent Files")
	os.Mkdir(target, 0700)
	restored, err := Restore(context.Background(), pack.Manifest, pack.Items, target, t.TempDir(), nil)
	if err != nil || restored.Written != 3 {
		t.Fatal(restored, err)
	}
	again, err := Restore(context.Background(), pack.Manifest, pack.Items, target, t.TempDir(), nil)
	if err != nil || again.Skipped != 3 {
		t.Fatal(again, err)
	}
	fallback := t.TempDir()
	independent, err := Restore(context.Background(), pack.Manifest, pack.Items, "", fallback, nil)
	if err != nil || independent.Fallback != 3 {
		t.Fatal(independent, err)
	}
	for _, p := range independent.Placements {
		if strings.Contains(p.Path, "objects/") {
			t.Fatal("fallback flattened paths")
		}
	}
	zipped, err := ZipDirectory(context.Background(), out.Path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenPackage(context.Background(), zipped, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Cleanup()
	if len(reopened.Items) != 3 {
		t.Fatal("ZIP merged occurrences")
	}
}
func TestCompactIndexSharesDirectoryAndRetainsEvidence(t *testing.T) {
	source, items, c := accountTreeFixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := readMetadata(context.Background(), out.Path)
	if err != nil {
		t.Fatal(err)
	}
	base := m.Entries[0]
	m.Entries = []Entry{base}
	for n := 0; n < 9999; n++ {
		e := base
		e.Item.RelativePath = filepath.ToSlash(filepath.Dir(base.Item.RelativePath)) + fmt.Sprintf("/excluded-%05d.png", n)
		e.Item.InstanceID = fmt.Sprintf("synthetic-%05d", n)
		e.Excluded = "unreadable"
		e.SHA256 = ""
		m.Entries = append(m.Entries, e)
	}
	root := t.TempDir()
	if err = writeMetadata(context.Background(), root, &m); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, headerName))
	if err != nil || len(b) > 4096 || strings.Contains(string(b), "excluded-") {
		t.Fatal("header grew with file count", len(b), err)
	}
	index, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(indexName)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(index), `"directory"`) != 1 {
		t.Fatal("directory prefixes repeated")
	}
	old, _ := json.Marshal(struct{ Entries []Entry }{m.Entries})
	if len(index) >= len(old) {
		t.Fatal("index did not shrink", len(index), len(old))
	}
	decoded, err := readMetadata(context.Background(), root)
	if err != nil || len(decoded.Entries) != 10000 {
		t.Fatal(err, len(decoded.Entries))
	}
	if decoded.Entries[0].Item.FirstSeenAt != base.Item.FirstSeenAt || decoded.Entries[0].Item.Identity != base.Item.Identity || len(decoded.Entries[0].History) != len(base.History) {
		t.Fatal("evidence lost")
	}
}
func mutateIndex(t *testing.T, root string, change func([]byte) []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(indexName))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b = change(b)
	os.WriteFile(p, b, 0600)
	data, err := os.ReadFile(filepath.Join(root, headerName))
	if err != nil {
		t.Fatal(err)
	}
	var h header
	json.Unmarshal(data, &h)
	hash := sha256.Sum256(b)
	h.IndexSHA256 = hex.EncodeToString(hash[:])
	data, _ = json.Marshal(h)
	os.WriteFile(filepath.Join(root, headerName), data, 0600)
}
func TestTreeIndexAndInventoryRejectMalformedInputs(t *testing.T) {
	for _, variant := range []string{"hash", "bad_directory_ref", "duplicate_case", "extra_directory", "large_line", "missing_index", "header_extra", "reserved_name"} {
		t.Run(variant, func(t *testing.T) {
			source, items, c := accountTreeFixture(t)
			out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
			if err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "hash":
				f, _ := os.OpenFile(filepath.Join(out.Path, filepath.FromSlash(indexName)), os.O_APPEND|os.O_WRONLY, 0)
				f.Write([]byte("\n"))
				f.Close()
			case "bad_directory_ref":
				mutateIndex(t, out.Path, func(b []byte) []byte {
					return []byte(strings.Replace(string(b), `"file":`, `"file":`, 1) + `{"d":99999,"file":{"n":"escape"}}` + "\n")
				})
			case "duplicate_case":
				m, _ := readMetadata(context.Background(), out.Path)
				e := m.Entries[0]
				e.Item.RelativePath = strings.ToUpper(e.Item.RelativePath)
				e.Item.InstanceID = "different-instance"
				m.Entries = append(m.Entries, e)
				rewriteMetadata(t, out.Path, m)
			case "extra_directory":
				os.Mkdir(filepath.Join(out.Path, "unexpected-empty-directory"), 0700)
			case "large_line":
				mutateIndex(t, out.Path, func(b []byte) []byte { return append(b, []byte(strings.Repeat(" ", maxLine+1))...) })
			case "missing_index":
				os.Remove(filepath.Join(out.Path, filepath.FromSlash(indexName)))
			case "header_extra":
				p := filepath.Join(out.Path, headerName)
				b, _ := os.ReadFile(p)
				var h map[string]any
				json.Unmarshal(b, &h)
				h["unknown"] = "field"
				b, _ = json.Marshal(h)
				os.WriteFile(p, b, 0600)
			case "reserved_name":
				m, _ := readMetadata(context.Background(), out.Path)
				m.Entries[0].Item.RelativePath = "Pic/2024-08/Ori/aux.txt"
				rewriteMetadata(t, out.Path, m)
			}
			if _, _, err = Open(context.Background(), out.Path, nil); err == nil {
				t.Fatal("malformed input accepted", variant)
			}
		})
	}
}

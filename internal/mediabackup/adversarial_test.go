package mediabackup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"testing"
)

func rewriteMetadata(t *testing.T, path string, m Manifest) {
	t.Helper()
	for _, p := range []string{indexName, headerName, ".qman"} {
		if err := os.Remove(filepath.Join(path, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeMetadata(context.Background(), path, &m); err != nil {
		t.Fatal(err)
	}
}
func TestRejectVersionDuplicateMissingAndExtraObjects(t *testing.T) {
	for _, variant := range []string{"version", "duplicate", "missing", "extra", "wrong_length"} {
		t.Run(variant, func(t *testing.T) {
			source, items, c := fixture(t)
			out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
			if err != nil {
				t.Fatal(err)
			}
			m, err := readMetadata(context.Background(), out.Path)
			if err != nil {
				t.Fatal(err)
			}
			included := -1
			for i, e := range m.Entries {
				if e.Excluded == "" {
					included = i
					break
				}
			}
			if included < 0 {
				t.Fatal("no payload")
			}
			rel, _ := PayloadPath(m, m.Entries[included])
			switch variant {
			case "version":
				h := m.head()
				h.Version = 99
				b, _ := json.Marshal(h)
				os.WriteFile(filepath.Join(out.Path, headerName), b, 0600)
			case "duplicate":
				m.Entries = append(m.Entries, m.Entries[0])
				rewriteMetadata(t, out.Path, m)
			case "missing":
				if err = os.Remove(filepath.Join(out.Path, filepath.FromSlash(rel))); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err = os.WriteFile(filepath.Join(out.Path, "unexpected"), []byte("extra"), 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong_length":
				m.Entries[included].Item.Bytes++
				rewriteMetadata(t, out.Path, m)
			}
			if _, _, err = Open(context.Background(), out.Path, nil); err == nil {
				t.Fatal("accepted invalid tree", variant)
			}
		})
	}
}

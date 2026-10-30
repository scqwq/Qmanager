package mediabackup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"testing"
)

func TestRejectVersionDuplicateMissingAndExtraObjects(t *testing.T) {
	for _, variant := range []string{"version", "duplicate", "missing", "extra", "wrong_length"} {
		t.Run(variant, func(t *testing.T) {
			source, items, c := fixture(t)
			out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(out.Path, "manifest.json")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var m Manifest
			if err = json.Unmarshal(original, &m); err != nil {
				t.Fatal(err)
			}
			included := -1
			for i, entry := range m.Entries {
				if entry.SHA256 != "" {
					included = i
					break
				}
			}
			if included < 0 {
				t.Fatal("fixture has no copied objects")
			}
			switch variant {
			case "version":
				m.Version = 99
			case "duplicate":
				m.Entries = append(m.Entries, m.Entries[0])
			case "missing":
				if err = os.Remove(filepath.Join(out.Path, "objects", m.Entries[included].SHA256)); err != nil {
					t.Fatal(err)
				}
			case "extra":
				os.WriteFile(filepath.Join(out.Path, "objects", "unexpected"), []byte("extra"), 0600)
			case "wrong_length":
				m.Entries[included].Item.Bytes++
			}
			changed, _ := json.Marshal(m)
			if err = os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err = Open(context.Background(), out.Path, nil); err == nil {
				t.Fatal("accepted invalid backup", variant)
			}
		})
	}
}

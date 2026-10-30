package archive

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"qqarchive/internal/domain"
	"qqarchive/internal/source/plain"
	"testing"
)

func shaOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "源数据")
	if e := plain.Demo(context.Background(), source); e != nil {
		t.Fatal(e)
	}
	return source, filepath.Join(root, "归档.qqarc")
}
func makeArchive(t *testing.T) (string, string) {
	t.Helper()
	source, out := fixture(t)
	s, e := domain.Dates("demo-chat", "2026-10-03", "2026-10-03", "Asia/Shanghai")
	if e != nil {
		t.Fatal(e)
	}
	result, e := Build(context.Background(), filepath.Join(source, "source.db"), s, out)
	if e != nil {
		t.Fatal(e)
	}
	if result.Messages != 5 || result.Status != "READY_WITH_WARNINGS" {
		t.Fatal(result)
	}
	return source, out
}
func TestIndependentSearchAndNoOverwrite(t *testing.T) {
	source, out := makeArchive(t)
	ctx := context.Background()
	s, _ := domain.Dates("demo-chat", "2026-10-03", "2026-10-03", "Asia/Shanghai")
	if _, e := Build(ctx, filepath.Join(source, "source.db"), s, out); e == nil {
		t.Fatal("existing package overwritten")
	}
	if e := os.Rename(source, source+"-unavailable"); e != nil {
		t.Fatal(e)
	}
	o, e := Open(ctx, out)
	if e != nil {
		t.Fatal(e)
	}
	defer o.Close()
	rows, e := o.Search(ctx, "重复", 10, 0)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 2 || rows[0].Key == rows[1].Key {
		t.Fatal("duplicate text lost")
	}
	if rows[0].Sender != "900719925474099312345" {
		t.Fatal("ID precision lost")
	}
	rows, e = o.Search(ctx, "%", 10, 0)
	if e != nil || len(rows) != 0 {
		t.Fatal("wildcard interpreted")
	}
	rows, e = o.Search(ctx, "' OR 1=1 --", 10, 0)
	if e != nil || len(rows) != 0 {
		t.Fatal("query injection")
	}
	rows, e = o.Search(ctx, "重复", 1, 1)
	if e != nil || len(rows) != 1 {
		t.Fatal("pagination failed")
	}
}
func TestCancelAndIsolation(t *testing.T) {
	source, out := fixture(t)
	s, _ := domain.Dates("demo-chat", "2026-10-03", "2026-10-03", "Asia/Shanghai")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Build(ctx, filepath.Join(source, "source.db"), s, out); e == nil {
		t.Fatal("cancel ignored")
	}
	if _, e := os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("cancel published package")
	}
	if _, e := Build(context.Background(), filepath.Join(source, "source.db"), s, filepath.Join(source, "bad.qqarc")); e == nil {
		t.Fatal("source overlap accepted")
	}
}
func rewrite(t *testing.T, input, output string, edit func(map[string][]byte)) {
	t.Helper()
	z, e := zip.OpenReader(input)
	if e != nil {
		t.Fatal(e)
	}
	data := map[string][]byte{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b := make([]byte, f.UncompressedSize64)
		n := 0
		for n < len(b) {
			c, e := r.Read(b[n:])
			n += c
			if e != nil {
				break
			}
		}
		r.Close()
		data[f.Name] = b
	}
	z.Close()
	edit(data)
	file, e := os.Create(output)
	if e != nil {
		t.Fatal(e)
	}
	w := zip.NewWriter(file)
	for n, b := range data {
		f, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	file.Close()
}
func TestRejectCorruptAndUnsupported(t *testing.T) {
	_, good := makeArchive(t)
	for _, tt := range []struct {
		name string
		edit func(map[string][]byte)
	}{{"hash", func(m map[string][]byte) { m["reports/export.json"] = []byte("changed") }}, {"extra", func(m map[string][]byte) { m["extra.txt"] = []byte("unlisted") }}, {"missing", func(m map[string][]byte) { delete(m, "messages.db") }}, {"path", func(m map[string][]byte) { m["../escape"] = []byte("bad") }}, {"version", func(m map[string][]byte) {
		var manifest map[string]any
		json.Unmarshal(m["manifest.json"], &manifest)
		manifest["major"] = 99
		m["manifest.json"], _ = json.Marshal(manifest)
		var sums map[string]Checksum
		json.Unmarshal(m["checksums.json"], &sums)
		hash := shaOf(m["manifest.json"])
		sums["manifest.json"] = Checksum{int64(len(m["manifest.json"])), hash}
		m["checksums.json"], _ = json.Marshal(sums)
	}}} {
		t.Run(tt.name, func(t *testing.T) {
			bad := filepath.Join(t.TempDir(), "bad.qqarc")
			rewrite(t, good, bad, tt.edit)
			if o, e := Open(context.Background(), bad); e == nil {
				o.Close()
				t.Fatal("bad package accepted")
			}
		})
	}
}
func TestUnsafeNames(t *testing.T) {
	for _, n := range []string{"../x", "C:/x", "\\\\server\\x", "media/a:stream", "CON.txt", "a/../b", "a./b", "a\\b", "/abs", "LPT1"} {
		if safeName(n) {
			t.Fatalf("unsafe name %q", n)
		}
	}
}

func TestPublicationRejectsExistingDestination(t *testing.T) {
	root := t.TempDir()
	staged, out := filepath.Join(root, "stage.partial"), filepath.Join(root, "existing.qqarc")
	if e := os.WriteFile(staged, []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(out, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := publishNoReplace(staged, out); e == nil {
		t.Fatal("existing file replaced")
	}
	for path, want := range map[string]string{staged: "new", out: "old"} {
		got, e := os.ReadFile(path)
		if e != nil || string(got) != want {
			t.Fatalf("publication altered %s: %v", filepath.Base(path), e)
		}
	}
}

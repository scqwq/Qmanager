package mediabackup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"strings"
	"testing"
)

func TestRestoreMergeRepeatConflictAndEvidence(t *testing.T) {
	source, items, c := fixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := OpenPackage(context.Background(), out.Path, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pack.Cleanup()
	pack.Manifest.RestorePrefix = "12345678/nt_qq/nt_data"
	target := filepath.Join(t.TempDir(), "Tencent Files")
	os.Mkdir(target, 0700)
	fallback := t.TempDir()
	before, _ := testfixture.Snapshot(source.Resolved)
	first, err := Restore(context.Background(), pack.Manifest, pack.Items, target, fallback, nil)
	if err != nil || first.Written != 2 || first.Fallback != 0 {
		t.Fatalf("first %+v %v", first, err)
	}
	for _, p := range first.Placements {
		if strings.Contains(p.Path, "Tencent Files") || !strings.HasPrefix(p.Path, "12345678/nt_qq/nt_data/Pic/") {
			t.Fatalf("nested/wrong path %s", p.Path)
		}
	}
	if _, err = os.Stat(filepath.Join(target, "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("manifest copied to source")
	}
	again, err := Restore(context.Background(), pack.Manifest, pack.Items, target, fallback, nil)
	if err != nil || again.Skipped != 2 || again.Written != 0 {
		t.Fatalf("duplicate %+v %v", again, err)
	}
	collision := filepath.Join(target, filepath.FromSlash(first.Placements[0].Path))
	os.WriteFile(collision, []byte("preserve different current content"), 0600)
	third, err := Restore(context.Background(), pack.Manifest, pack.Items, target, fallback, nil)
	if err != nil || third.Conflicts != 1 || third.Written != 1 {
		t.Fatalf("conflict %+v %v", third, err)
	}
	kept, _ := os.ReadFile(collision)
	if string(kept) != "preserve different current content" {
		t.Fatal("overwrote destination")
	}
	fourth, err := Restore(context.Background(), pack.Manifest, pack.Items, target, fallback, nil)
	if err != nil || fourth.Written != 0 || fourth.Skipped != 2 {
		t.Fatalf("conflict idempotence %+v %v", fourth, err)
	}
	after, _ := testfixture.Snapshot(source.Resolved)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("backup/read source changed")
	}
	if pack.Items[0].FirstSeenAt != items[0].FirstSeenAt || pack.Items[0].Method != "baseline" {
		t.Fatal("evidence changed")
	}
}

func TestFallbackLegacyUnsafeMappingAndCancellation(t *testing.T) {
	source, items, c := fixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := OpenPackage(context.Background(), out.Path, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer pack.Cleanup()
	target := filepath.Join(t.TempDir(), "Tencent Files")
	os.Mkdir(target, 0700)
	fallback := t.TempDir()
	result, err := Restore(context.Background(), pack.Manifest, pack.Items, target, fallback, nil)
	if err != nil || result.Fallback != 2 || result.Written != 0 {
		t.Fatalf("legacy fallback %+v %v", result, err)
	}
	for _, prefix := range []string{"../other", "12345678/nt_qq/nt_data/msf", "12345678/nt_qq/nt_data/../msf", "C:/escape"} {
		m := pack.Manifest
		m.RestorePrefix = prefix
		if _, err = mappedPath(m, m.Entries[0]); err == nil {
			t.Fatalf("allowed %s", prefix)
		}
	}
	pack.Manifest.RestorePrefix = "12345678/nt_qq/nt_data"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Restore(ctx, pack.Manifest, pack.Items, target, t.TempDir(), nil); err == nil {
		t.Fatal("cancel ignored")
	}
	entries, _ := os.ReadDir(target)
	if len(entries) != 0 {
		t.Fatal("cancel/legacy wrote source")
	}
}

func TestZIPPortablePackageRoundTripAndMaliciousEntries(t *testing.T) {
	source, items, c := fixture(t)
	out, err := Build(context.Background(), source, items, media.Filter{}, t.TempDir(), nil, c.History, nil)
	if err != nil {
		t.Fatal(err)
	}
	zipped, err := ZipDirectory(context.Background(), out.Path)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := OpenPackage(context.Background(), zipped, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Items) != 2 || pack.Manifest.SourceID != source.ID {
		t.Fatal("zip lost media/evidence")
	}
	verified, err := VerifyZIP(context.Background(), zipped)
	if err != nil || !bytes.Equal(verified.Bytes, pack.Bytes) || verified.Info.Name() != filepath.Base(zipped) {
		t.Fatal("streaming verification did not reproduce the package evidence", err)
	}
	tampered := filepath.Join(t.TempDir(), "tampered.zip")
	rewriteZIPWithChangedPayload(t, zipped, tampered)
	preview, err := PreviewZIP(context.Background(), tampered)
	if err != nil || !bytes.Equal(preview.Bytes, pack.Bytes) {
		t.Fatal("ZIP preview should read the unchanged manifest without verifying media contents", err)
	}
	if _, err = VerifyZIP(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatal("streaming verification accepted altered media with a valid ZIP CRC", err)
	}
	temporary := pack.Root
	pack.Cleanup()
	if _, err = os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatal("unpack leaked")
	}
	for _, name := range []string{"../escape", "objects/../escape", "C:/escape", "objects/fake", "manifest.json"} {
		path := filepath.Join(t.TempDir(), "bad.zip")
		var buffer bytes.Buffer
		writer := zip.NewWriter(&buffer)
		entry, _ := writer.Create("manifest.json")
		b, _ := json.Marshal(pack.Manifest)
		entry.Write(b)
		entry, _ = writer.Create(name)
		entry.Write([]byte("bad"))
		writer.Close()
		os.WriteFile(path, buffer.Bytes(), 0600)
		if p, e := OpenPackage(context.Background(), path, t.TempDir(), nil); e == nil {
			p.Cleanup()
			t.Fatalf("accepted dangerous/duplicate %s", name)
		}
	}
}

func rewriteZIPWithChangedPayload(t *testing.T, from, to string) {
	t.Helper()
	archive, err := zip.OpenReader(from)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	changed := false
	for _, file := range archive.File {
		input, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !changed && file.Name != headerName && file.Name != indexName && len(b) != 0 {
			b[0] ^= 1
			changed = true
		}
		entry, err := writer.Create(file.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil || !changed {
		t.Fatal("did not create tampered ZIP", err)
	}
	if err := os.WriteFile(to, out.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPortablePrefixFromMediaSubroot(t *testing.T) {
	for _, suffix := range []string{"", "Pic", "Video", "Emoji/emoji-recv", "Ptt"} {
		root := filepath.Join(t.TempDir(), "Tencent Files", "12345678", "nt_qq", "nt_data", filepath.FromSlash(suffix))
		want := "12345678/nt_qq/nt_data"
		if suffix != "" {
			want += "/" + suffix
		}
		if got := restorePrefix(root); got != want {
			t.Fatalf("prefix %q want %q", got, want)
		}
	}
}

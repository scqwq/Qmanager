package catalog

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestTempRenameGrowthAndIndexBackup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	temp := "2026-09/OriTemp/part.png"
	if err := testfixture.Image(root, temp); err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(root, "")
	now := time.Now().UTC()
	scan := func(offset int) media.Item {
		r := media.Scan(context.Background(), source, nil)
		if err := c.Apply(context.Background(), saved.ID, &r, false, nil, true, now.Add(time.Duration(offset)*time.Second)); err != nil {
			t.Fatal(err)
		}
		return r.Items[0]
	}
	first := scan(0)
	scan(2)
	item := scan(4)
	if item.Stability != "pending" {
		t.Fatal("temporary file marked stable")
	}
	target := filepath.Join(root, "2026-09/Ori/complete.png")
	os.MkdirAll(filepath.Dir(target), 0700)
	if err := os.Rename(filepath.Join(root, filepath.FromSlash(temp)), target); err != nil {
		t.Fatal(err)
	}
	item = scan(6)
	if item.InstanceID != first.InstanceID || item.Role != "original" || item.Stability != "pending" {
		t.Fatal("identity rename or role stability failed")
	}
	scan(8)
	item = scan(10)
	if item.Stability != "stable_heuristic" {
		t.Fatal("no stable observation")
	}
	f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("growing again"))
	f.Close()
	changed := scan(12)
	if changed.InstanceID == item.InstanceID || changed.Stability != "pending" {
		t.Fatal("continued growth kept stable instance")
	}
	export, err := c.Export(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	copy, err := Open(export)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	records, err := copy.Load(saved.ID)
	if err != nil || len(records) != 2 {
		t.Fatal("index evidence backup lost", err, len(records))
	}
}

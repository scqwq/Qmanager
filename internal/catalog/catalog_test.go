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

func TestEvidenceVersionsGapsAndPersistence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	if err := testfixture.Image(root, "Pic/2026-09/Ori/old.jpg"); err != nil {
		t.Fatal(err)
	}
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Add(root, "主账号")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	source.ID = saved.ID
	now := time.Date(2026, 10, 30, 4, 0, 0, 0, time.UTC)
	apply := func(at time.Time, created map[string]time.Time) media.Report {
		r := media.Scan(context.Background(), source, nil)
		if err := c.Apply(context.Background(), saved.ID, &r, false, created, true, at); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := apply(now, nil)
	first := r.Items[0]
	if first.Method != "baseline" || first.Stability != "pending" {
		t.Fatal(first)
	}
	apply(now.Add(2*time.Second), nil)
	r = apply(now.Add(4*time.Second), nil)
	if r.Items[0].Stability != "stable_heuristic" || !r.Items[0].FirstSeenAt.Equal(first.FirstSeenAt) {
		t.Fatal("stability overwrote first evidence")
	}
	rel := "Pic/2026-09/Ori/new.jpg"
	if err := testfixture.Image(root, rel); err != nil {
		t.Fatal(err)
	}
	r = apply(now.Add(6*time.Second), map[string]time.Time{rel: now.Add(5 * time.Second)})
	var added media.Item
	for _, item := range r.Items {
		if item.RelativePath == rel {
			added = item
		}
	}
	if added.Method != "live_event" || added.SourceMonth != "2026-09" || !media.Matches(added, media.Filter{View: "observed", Month: "2026-10"}) || media.Matches(first, media.Filter{View: "observed"}) {
		t.Fatal("dual dates invalid", added)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
	apply(now.Add(8*time.Second), nil)
	if err := testfixture.Image(root, rel); err != nil {
		t.Fatal(err)
	}
	r = apply(now.Add(10*time.Second), nil)
	for _, item := range r.Items {
		if item.RelativePath == rel && (item.InstanceID == added.InstanceID || item.Method != "rescan_after_gap") {
			t.Fatal("reappearance history collapsed")
		}
	}
	if err := testfixture.File(root, rel, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	apply(now.Add(12*time.Second), nil)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c, err = Open(c.path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	records, err := c.Load(saved.ID)
	if err != nil || len(records) != 4 {
		t.Fatalf("history records=%d err=%v", len(records), err)
	}
	h, err := c.History(added.InstanceID)
	if err != nil || len(h) < 2 {
		t.Fatal("missing persisted events", err)
	}
}
func TestEmptyBaselineAndPartialScan(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	os.MkdirAll(root, 0700)
	source, _ := media.Resolve(root)
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(root, "")
	now := time.Now().UTC()
	r := media.Scan(context.Background(), source, nil)
	if err = c.Apply(context.Background(), saved.ID, &r, true, nil, false, now); err != nil {
		t.Fatal(err)
	}
	testfixture.Image(root, "2026-09/Ori/new.png")
	r = media.Scan(context.Background(), source, nil)
	c.Apply(context.Background(), saved.ID, &r, false, nil, false, now.Add(time.Second))
	if r.Items[0].Method != "rescan_after_gap" {
		t.Fatal("empty baseline forgotten")
	}
	r = media.Report{Complete: false}
	if err = c.Apply(context.Background(), saved.ID, &r, false, nil, false, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	records, _ := c.Load(saved.ID)
	if !records[0].Present {
		t.Fatal("partial scan marked missing")
	}
}

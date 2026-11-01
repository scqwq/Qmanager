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

func TestQuickKeepsUnverifiedOldRowsAndFullFindsDeletion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	testfixture.Image(root, "Pic/2025-10/Ori/a.png")
	testfixture.Image(root, "Pic/2026-09/Ori/b.png")
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(root, "synthetic")
	source, _ := media.Resolve(root)
	source.ID = saved.ID
	at := time.Now().UTC().Add(-time.Hour)
	for n := 0; n < 3; n++ {
		r := media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), saved.ID, &r, false, nil, true, at.Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := c.Active(saved.ID)
	last := c.LastScan(saved.ID)
	known, _ := c.KnownPaths(context.Background(), saved.ID)
	os.Remove(filepath.Join(root, "Pic/2025-10/Ori/a.png"))
	testfixture.Image(root, "Pic/2025-10/Ori/new.png")
	quick := media.ScanWith(context.Background(), source, media.ScanOptions{NewOnly: true, Known: known}, nil)
	if err = c.Apply(context.Background(), saved.ID, &quick, false, nil, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if quick.KnownSkipped != 1 || quick.Inspected != 1 || quick.IndexedUpdates != 1 || !c.LastScan(saved.ID).Equal(last) {
		t.Fatal("quick became full", quick)
	}
	rows, _ := c.Active(saved.ID)
	if len(rows) != 3 {
		t.Fatal("quick removed unverified absence")
	}
	for _, old := range before {
		for _, current := range rows {
			if old.Item.InstanceID == current.Item.InstanceID && (!old.Item.FirstSeenAt.Equal(current.Item.FirstSeenAt) || !old.Seen.Equal(current.Seen)) {
				t.Fatal("quick rewrote untouched old evidence")
			}
		}
	}
	dates, err := c.Page(context.Background(), saved.ID, media.Filter{View: "observed"})
	if err != nil || dates.Total != 1 || dates.Items[0].Method != "rescan_after_gap" {
		t.Fatal("new observation dates", dates, err)
	}
	previous, _ := c.ActiveItems(saved.ID)
	full := media.ScanWith(context.Background(), source, media.ScanOptions{Previous: previous}, nil)
	if err = c.Apply(context.Background(), saved.ID, &full, false, nil, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows, _ = c.Active(saved.ID)
	if len(rows) != 2 || len(full.RemovedPaths) != 1 || c.LastScan(saved.ID).Equal(last) {
		t.Fatal("full did not reconcile deletion", full)
	}
}

func TestEmptyQuickBaselineDoesNotRelabelLaterFilesAsBaseline(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	os.MkdirAll(root, 0700)
	source, _ := media.Resolve(root)
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(root, "synthetic")
	source.ID = saved.ID
	first := media.ScanWith(context.Background(), source, media.ScanOptions{NewOnly: true}, nil)
	if err = c.Apply(context.Background(), saved.ID, &first, true, nil, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if !c.LastScan(saved.ID).IsZero() {
		t.Fatal("name baseline fabricated full verification")
	}
	testfixture.Image(root, "2025-10/Ori/new.png")
	next := media.ScanWith(context.Background(), source, media.ScanOptions{NewOnly: true}, nil)
	if err = c.Apply(context.Background(), saved.ID, &next, false, nil, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].Method != "rescan_after_gap" {
		t.Fatal("later appearance became old baseline", next.Items)
	}
}

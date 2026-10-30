package catalog

import (
	"context"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestCacheQueriesStableUpdateAndLegacyProjection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	testfixture.Image(root, "Pic/2026-09/Ori/a.jpg")
	testfixture.Image(root, "Pic/2026-10/Ori/b.jpg")
	source, _ := media.Resolve(root)
	index := filepath.Join(t.TempDir(), "index.sqlite")
	c, err := Open(index)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := c.Add(root, "")
	at := time.Now().UTC().Add(-20 * time.Second)
	var r media.Report
	for n := 0; n < 3; n++ {
		r = media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), saved.ID, &r, false, nil, true, at.Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	original := r.Items[0]
	previous, _ := c.ActiveItems(saved.ID)
	r = media.ScanWith(context.Background(), source, media.ScanOptions{Previous: previous}, nil)
	if err = c.Apply(context.Background(), saved.ID, &r, false, nil, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if r.IndexedUpdates != 0 || r.HeaderReads != 0 {
		t.Fatal("unchanged files were re-indexed", r.IndexedUpdates, r.HeaderReads)
	}
	p, err := c.Page(context.Background(), saved.ID, media.Filter{Month: "2026-09", Limit: 1})
	if err != nil || p.Total != 1 || len(p.Items) != 1 {
		t.Fatal("SQL page", err, p.Total)
	}
	if p.Items[0].Method != "baseline" || !p.Items[0].LastSeenAt.After(at.Add(4*time.Second)) {
		t.Fatal("evidence or last seen lost")
	}
	if _, err = c.db.Exec("DROP TABLE media_cache;DELETE FROM cache_meta;DROP TABLE source_reports;"); err != nil {
		t.Fatal(err)
	}
	c.Close()
	c, err = Open(index)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err = c.Page(context.Background(), saved.ID, media.Filter{})
	if err != nil || p.Total != 2 {
		t.Fatal("legacy projection recovery", err)
	}
	found := false
	for _, i := range p.Items {
		if i.InstanceID == original.InstanceID {
			found = true
			if !i.FirstSeenAt.Equal(original.FirstSeenAt) {
				t.Fatal("migration changed first seen")
			}
		}
	}
	if !found {
		t.Fatal("instance lost")
	}
}
func TestPartialDeletionKeepsOtherMonths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	testfixture.Image(root, "Pic/2026-09/Ori/a.jpg")
	testfixture.Image(root, "Pic/2026-10/Ori/b.jpg")
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(root, "")
	source, _ := media.Resolve(root)
	r := media.Scan(context.Background(), source, nil)
	if err = c.Apply(context.Background(), saved.ID, &r, true, nil, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	missing := media.Report{Partial: true, Complete: true, Covered: []string{"Pic/2026-09/Ori/a.jpg"}, FinishedAt: time.Now().UTC(), Items: []media.Item{}}
	if err = c.Apply(context.Background(), saved.ID, &missing, false, nil, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	p, err := c.Page(context.Background(), saved.ID, media.Filter{})
	if err != nil || p.Total != 1 || p.Items[0].SourceMonth != "2026-10" {
		t.Fatal("partial update removed unrelated month")
	}
}

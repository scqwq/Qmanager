package appservice

import (
	"context"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestCachedAccountReopenOfflineAndResume(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	testfixture.Image(root, "Pic/2026-09/Ori/a.jpg")
	testfixture.Image(root, "Pic/2026-10/Ori/b.jpg")
	private := t.TempDir()
	index := filepath.Join(private, "index.sqlite")
	c, err := catalog.Open(index)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := c.Add(root, "主账号")
	source, _ := media.Resolve(root)
	at := time.Now().UTC().Add(-20 * time.Second)
	for n := 0; n < 3; n++ {
		r := media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), saved.ID, &r, false, nil, true, at.Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	c.SetPreference("selected_source", saved.ID)
	c.Close()
	if err = os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	c, err = catalog.Open(index)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.State().Cached || s.State().Watching || s.State().Source.ID != saved.ID || s.State().ReconcileSeconds != 120 {
		t.Fatal("cache not restored before source scan")
	}
	page := s.Page(media.Filter{})
	if page.Total != 2 {
		t.Fatal("offline cached list missing")
	}
	originalID := page.Items[0].InstanceID
	if err = s.Start(context.Background()); err == nil {
		t.Fatal("offline source started monitoring")
	}
	if s.Page(media.Filter{}).Total != 2 {
		t.Fatal("offline start erased cache")
	}
	if err = os.Rename(root+"-offline", root); err != nil {
		t.Fatal(err)
	}
	testfixture.Image(root, "Pic/2026-09/Ori/new.jpg")
	revision := s.State().Revision
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for s.State().Revision == revision && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.State().Revision == revision {
		t.Fatal("resume did not update index", s.State().Error)
	}
	s.Stop()
	page = s.Page(media.Filter{})
	if page.Total != 3 {
		t.Fatal("new item not appended")
	}
	found := false
	for _, i := range page.Items {
		if i.InstanceID == originalID {
			found = true
		}
		if i.RelativePath == "Pic/2026-09/Ori/new.jpg" && i.Method != "rescan_after_gap" {
			t.Fatal("offline new file falsely reported as live download")
		}
	}
	if !found {
		t.Fatal("old cache instance replaced")
	}
	r := s.State().Report
	if r.HeaderReads != 1 || r.Reused != 2 || r.IndexedUpdates != 1 {
		t.Fatalf("resume did not reuse cache reads=%d reused=%d updates=%d", r.HeaderReads, r.Reused, r.IndexedUpdates)
	}
}
func TestDiscoverMultipleAccountsDoesNotIndexAll(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tencent Files")
	for _, p := range []string{"10001/nt_qq/nt_data/Pic/2026-09/Ori/a.jpg", "10002/nt_qq/nt_data/Video/2026-10/Thumb/b.jpg"} {
		testfixture.Image(root, p)
	}
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Candidates) != 2 || len(s.State().Sources) != 2 || s.State().Report != nil || s.State().Watching {
		t.Fatal("discovery scanned media or did not save accounts")
	}
	for _, source := range s.State().Sources {
		if err = s.Switch(source.ID); err != nil {
			t.Fatal(err)
		}
		if s.State().Source.ID != source.ID {
			t.Fatal("account switch failed")
		}
	}
}

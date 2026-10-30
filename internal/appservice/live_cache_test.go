package appservice

import (
	"context"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestNativeAdditionUpdatesCacheWithoutRescanningOtherMonths(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nt_data")
	testfixture.Image(root, "Pic/2026-09/Ori/old.jpg")
	testfixture.Image(root, "Pic/2026-10/Ori/other.jpg")
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
	if _, err = s.Select(root); err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for s.State().Report == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.State().Report == nil {
		t.Fatal("baseline did not finish")
	}
	lastFull := s.State().LastFullScan
	if err = testfixture.Image(root, "Pic/2026-09/Ori/new.jpg"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Page(media.Filter{})
		if p.Total == 3 {
			r := s.State().Report
			if !r.Partial || !s.State().LastFullScan.Equal(lastFull) {
				t.Fatal("native update incorrectly used a full scan")
			}
			for _, item := range p.Items {
				if item.RelativePath == "Pic/2026-09/Ori/new.jpg" && item.Method != "live_event" {
					t.Fatal("native evidence lost")
				}
			}
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("native incremental update did not reach cache", s.State().Error)
}

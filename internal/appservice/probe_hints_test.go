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

// Put the new-file notification just before an older pending file's stability
// probe: the probe is due before the new notification's debounce expires.
func TestStabilityProbeRetainsUnrelatedNativeHint(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	if err := testfixture.Image(root, "2026-09/Ori/old.png"); err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
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
	deadline := time.Now().Add(5 * time.Second)
	for s.State().Report == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.State().Report == nil {
		t.Fatal("baseline missing")
	}
	time.Sleep(1300 * time.Millisecond)
	if err = testfixture.Image(root, "2026-10/Ori/new.png"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		p := s.Page(media.Filter{})
		if p.Total == 2 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("stability probe discarded new-file hint", s.State().Error)
}

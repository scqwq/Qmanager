package appservice

import (
	"context"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

// Put the new-file notification just before an older pending file's stability
// probe: the probe is due before the new notification's debounce expires.
func TestStabilityProbeRetainsUnrelatedNativeHint(t *testing.T) {
	s, _, source, _ := managerFixture(t)
	if err := testfixture.Image(source.Resolved, "Pic/2025-10/Ori/newly-indexed.png"); err != nil {
		t.Fatal(err)
	}
	revision := s.State().Revision
	if err := s.StartUpdate(context.Background(), "fast"); err != nil {
		t.Fatal(err)
	}
	waitUpdate(t, s, revision)
	// The new cache entry, rather than the historical backlog, has a follow-up
	// observation due shortly before this second notification's debounce.
	time.Sleep(1300 * time.Millisecond)
	if err := testfixture.Image(source.Resolved, "Pic/2025-10/Ori/native-hint.png"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if s.Page(media.Filter{Kind: "image"}).Total == 3 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("stability probe discarded new-file hint", s.State().Error)
}

package watch

import (
	"context"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestOldMonthAndStartupBuffer(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	if err := testfixture.Image(root, "2026-09/Ori/baseline.jpg"); err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	w, err := Start(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if !w.Complete {
		t.Fatal("watch root failed")
	}
	if err := testfixture.Image(root, "2026-09/Ori/new.jpg"); err != nil {
		t.Fatal(err)
	}
	r := media.Scan(context.Background(), source, nil)
	if !r.Complete || r.Files != 2 {
		t.Fatal("baseline scan", r.Issues)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case e := <-w.Events:
			if e.Gap {
				t.Fatal("unexpected native gap")
			}
			if e.Path == "2026-09/Ori/new.jpg" && e.Action == 1 {
				return
			}
		case <-deadline.C:
			t.Fatal("old-month creation not buffered before enumeration")
		}
	}
}
func TestBoundedQueueReportsLoss(t *testing.T) {
	w := &Watch{Events: make(chan Event, 2)}
	for i := 0; i < 10; i++ {
		w.emit(Event{Action: 1})
	}
	if len(w.Events) != 2 || !w.Dropped() || w.Dropped() {
		t.Fatal("queue loss not reported/reset")
	}
}

package appservice

import (
	"testing"
	"time"
)

func TestObservationClockDistinguishesWorkAndIdleGap(t *testing.T) {
	start := time.Now()
	end := start.Add(12 * time.Second)
	if observationGap(start, end, false) {
		t.Fatal("own work misreported as sleep")
	}
	if !observationGap(start, end, true) {
		t.Fatal("unobserved idle pause lost")
	}
	if !observationGap(start, start.Add(-time.Second), false) {
		t.Fatal("backwards clock lost")
	}
	// After work, a queued signal's scheduled time may precede end. The loop
	// must use the actual handling time after end as its next idle observation.
	actual := end.Add(time.Millisecond)
	if observationGap(end, actual, true) {
		t.Fatal("queued timer signal caused false gap")
	}
}

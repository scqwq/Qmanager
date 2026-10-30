package media

import (
	"testing"
	"time"
)

func TestObservationDatesIncludeWholeDSTEndDay(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = loc
	defer func() { time.Local = previous }()
	for _, day := range []string{"2026-03-08", "2026-11-01", "2026-12-31"} {
		start, _ := time.ParseInLocation("2006-01-02", day, loc)
		end := start.AddDate(0, 0, 1)
		for _, at := range []time.Time{start, end.Add(-time.Nanosecond)} {
			if !Matches(Item{FirstSeenAt: at, Method: "live_event"}, Filter{View: "observed", From: day, To: day}) {
				t.Fatal("excluded local end day", day, at)
			}
		}
		if Matches(Item{FirstSeenAt: end, Method: "live_event"}, Filter{View: "observed", From: day, To: day}) {
			t.Fatal("included next day")
		}
	}
}

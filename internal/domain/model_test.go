package domain

import "testing"

func TestCalendarBoundaries(t *testing.T) {
	for _, tt := range []struct {
		day, zone string
		hours     int
	}{{"2026-03-08", "America/New_York", 23}, {"2026-11-01", "America/New_York", 25}, {"2026-12-31", "Asia/Shanghai", 24}} {
		s, e := Dates("chat", tt.day, tt.day, tt.zone)
		if e != nil {
			t.Fatal(e)
		}
		if int(s.End.Sub(s.Start).Hours()) != tt.hours {
			t.Fatalf("%s: wrong local day duration", tt.day)
		}
	}
	if _, e := Dates("chat", "2026-10-04", "2026-10-03", "Asia/Shanghai"); e == nil {
		t.Fatal("reversed range accepted")
	}
}

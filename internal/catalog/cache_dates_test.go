package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"qqarchive/internal/media"
	"testing"
	"time"
)

func TestSQLObservationFiltersMatchLocalDayBoundaries(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	previous := time.Local
	time.Local = loc
	defer func() { time.Local = previous }()
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(t.TempDir(), "")
	r := media.Report{Complete: true}
	created := map[string]time.Time{}
	for _, day := range []string{"2026-03-08", "2026-11-01", "2026-12-31"} {
		start, _ := time.ParseInLocation("2006-01-02", day, loc)
		end := start.AddDate(0, 0, 1)
		for j, at := range []time.Time{start.Add(-time.Nanosecond), start, end.Add(-time.Nanosecond), end} {
			path := fmt.Sprintf("Pic/2026-09/Ori/%s-%d.png", day, j)
			r.Items = append(r.Items, media.Item{RelativePath: path, Identity: path, Kind: "image", SourceMonth: "2026-09", Role: "original", Format: "png", Status: "recognized", Bytes: 1, ModifiedAt: at.UTC()})
			created[path] = at.UTC()
		}
	}
	if err = c.Apply(context.Background(), saved.ID, &r, true, created, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for _, day := range []string{"2026-03-08", "2026-11-01", "2026-12-31"} {
		f := media.Filter{View: "observed", From: day, To: day, Limit: 100}
		p, err := c.Page(context.Background(), saved.ID, f)
		if err != nil {
			t.Fatal(err)
		}
		expected := media.Paginate(r.Items, f)
		if p.Total != expected.Total || p.Total != 2 {
			t.Fatalf("SQL date filter differs: %s SQL=%d memory=%d", day, p.Total, expected.Total)
		}
	}
	if p, err := c.Page(context.Background(), saved.ID, media.Filter{View: "observed", Month: "__unknown__"}); err != nil || p.Total != 0 {
		t.Fatal("unknown observation month should be empty", err)
	}
}

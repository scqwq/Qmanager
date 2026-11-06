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

func TestSQLRecordHourAndSourceMonthIntersection(t *testing.T) {
	ctx := context.Background()
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, err := c.Add(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 12, 14, 0, 0, 0, time.Local)
	records := []struct {
		month string
		at    time.Time
	}{
		{"2026-08", start.Add(-time.Nanosecond)},
		{"2026-08", start},
		{"2026-08", start.Add(time.Hour - time.Nanosecond)},
		{"2026-08", start.Add(time.Hour)},
		{"2026-09", start},
		{"", start},
	}
	report := media.Report{Complete: true}
	created := map[string]time.Time{}
	for i, row := range records {
		path := fmt.Sprintf("Pic/%s/Ori/sample-%d.png", row.month, i)
		report.Items = append(report.Items, media.Item{RelativePath: path, Identity: path, Kind: "image", SourceMonth: row.month, Role: "original", Format: "png", Status: "recognized", Bytes: 1, ModifiedAt: row.at.UTC()})
		created[path] = row.at.UTC()
	}
	if err = c.Apply(ctx, saved.ID, &report, true, created, true, start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		filter media.Filter
		want   int
	}{
		{"observation and source month", media.Filter{View: "observed", Month: "2026-10", SourceMonth: "2026-08"}, 4},
		{"one hour inclusive", media.Filter{View: "observed", From: "2026-10-12T14", To: "2026-10-12T14", SourceMonth: "2026-08"}, 2},
		{"blank end hour includes the day", media.Filter{View: "observed", From: "2026-10-12T14", To: "2026-10-12", SourceMonth: "2026-08"}, 3},
		{"blank start hour begins at midnight", media.Filter{View: "observed", From: "2026-10-12", To: "2026-10-12T14", SourceMonth: "2026-08"}, 3},
		{"unknown source month", media.Filter{View: "observed", Month: "2026-10", SourceMonth: "__unknown__"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			page, err := c.Page(ctx, saved.ID, test.filter)
			if err != nil || page.Total != test.want {
				t.Fatalf("page total=%d, want %d, error=%v", page.Total, test.want, err)
			}
			if memory := media.Paginate(report.Items, test.filter); memory.Total != test.want {
				t.Fatalf("loaded backup total=%d, want %d", memory.Total, test.want)
			}
			items, err := c.Selected(ctx, saved.ID, test.filter)
			if err != nil || len(items) != test.want {
				t.Fatalf("matching items=%d, want %d, error=%v", len(items), test.want, err)
			}
			plan, err := c.BackupPlan(saved.ID, test.filter)
			if err != nil || plan.Files+plan.Pending != test.want {
				t.Fatalf("backup candidates=%d, want %d, error=%v", plan.Files+plan.Pending, test.want, err)
			}
		})
	}
	buckets, err := c.ObservedBuckets(saved.ID, false, "2026-08")
	if err != nil || len(buckets) != 1 || buckets[0].Month != "2026-10" || buckets[0].Files != 4 {
		t.Fatalf("source-filtered observed buckets: %+v, %v", buckets, err)
	}
	if _, err := c.Page(ctx, saved.ID, media.Filter{View: "observed", From: "2026-10-12T15", To: "2026-10-12T14"}); err == nil {
		t.Fatal("reversed hours accepted")
	}
}

package mediabackup

import (
	"context"
	"fmt"
	"qqarchive/internal/media"
	"reflect"
	"testing"
)

func TestEstimateExplainsAllExclusionsAndMatchesIndexedQuery(t *testing.T) {
	source, items, c := fixture(t)
	base := items[0]
	items = nil
	for i, status := range []string{"recognized", "unknown", "empty", "invalid_header", "unreadable", "changed", "recognized", "recognized", "recognized"} {
		item := base
		item.RelativePath = fmt.Sprintf("Pic/2026-09/Ori/synthetic-%d.png", i)
		item.Identity = fmt.Sprintf("synthetic-%d", i)
		item.Status = status
		item.Bytes = 104
		item.Role = "original"
		item.Stability = "stable_heuristic"
		switch i {
		case 6:
			item.Role = "temporary"
		case 7:
			item.Stability = "pending"
		case 8:
			item.Bytes = 0
		}
		items = append(items, item)
	}
	// Add synthetic observations through the normal catalogue entry point.
	r := media.Report{Items: items, Complete: true, Partial: true}
	if err := c.Apply(context.Background(), source.ID, &r, false, nil, true, base.LastSeenAt); err != nil {
		t.Fatal(err)
	}
	// Apply correctly recalculates stability, so compare the actual projection.
	current, err := c.Selected(context.Background(), source.ID, media.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	p := Estimate(current, media.Filter{})
	files, size, reasons, err := c.BackupStats(source.ID, media.Filter{})
	if err != nil || p.Files != files || p.Bytes != size || !reflect.DeepEqual(p.ExcludedReasons, reasons) {
		t.Fatal(p, files, size, reasons, err)
	}
	for _, status := range []string{"unknown", "empty", "invalid_header", "unreadable", "changed", "temporary", "pending"} {
		if p.ExcludedReasons[status] == 0 {
			t.Fatal("missing reason", status, p)
		}
	}
	zero := Estimate([]media.Item{{Status: "recognized", Bytes: 5, Stability: "pending"}}, media.Filter{})
	if zero.Files != 0 || zero.EstimatedPeak != 0 || zero.ExcludedReasons["pending"] != 1 {
		t.Fatal(zero)
	}
}

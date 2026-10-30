package catalog

import (
	"context"
	"path/filepath"
	"qqarchive/internal/media"
	"reflect"
	"testing"
	"time"
)

func TestInitialImportDateCanBeIncludedWithoutChangingEvidence(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(t.TempDir(), "测试账号")
	imported := time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local).UTC()
	r := media.Report{Complete: true, FinishedAt: imported, Items: []media.Item{{RelativePath: "Pic/2026-09/Ori/old.png", Identity: "fixture-old", Kind: "image", SourceMonth: "2026-09", Role: "original", Status: "recognized", Format: "png", Bytes: 20, ModifiedAt: imported}}}
	if err = c.Apply(context.Background(), saved.ID, &r, true, nil, true, imported); err != nil {
		t.Fatal(err)
	}
	original := r.Items[0]
	before, err := c.Load(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := media.Filter{View: "observed", Month: "2026-10", From: "2026-10-30", To: "2026-10-30"}
	p, err := c.Page(context.Background(), saved.ID, f)
	if err != nil || p.Total != 0 || p.BaselineFiles != 1 {
		t.Fatalf("initial import must be explained when excluded: %+v %v", p, err)
	}
	f.IncludeBaseline = true
	p, err = c.Page(context.Background(), saved.ID, f)
	if err != nil || p.Total != 1 || p.BaselineFiles != 1 || len(p.Items) != 1 {
		t.Fatal("include initial import did not find yesterday", err)
	}
	item := p.Items[0]
	if item.Method != "baseline" || item.SourceMonth != "2026-09" || !item.FirstSeenAt.Equal(original.FirstSeenAt) || item.InstanceID != original.InstanceID {
		t.Fatal("filter rewrote date or provenance")
	}
	inMemory := media.Paginate(r.Items, f)
	if inMemory.Total != p.Total || inMemory.BaselineFiles != p.BaselineFiles {
		t.Fatal("backup/memory view differs from SQL")
	}
	normal, err := c.Buckets(saved.ID, "observed")
	if err != nil || len(normal) != 0 {
		t.Fatal("baseline leaked into default discovered months")
	}
	all, err := c.Buckets(saved.ID, "observed", true)
	if err != nil || len(all) != 1 || all[0].Month != "2026-10" {
		t.Fatal("initial import discovery month missing", err)
	}
	f.Month = "2026-09"
	p, err = c.Page(context.Background(), saved.ID, f)
	if err != nil || p.Total != 0 || p.BaselineFiles != 0 {
		t.Fatal("source month confused with discovery month")
	}
	f.Month = ""
	f.From = "2026-10-31"
	f.To = "2026-10-31"
	p, err = c.Page(context.Background(), saved.ID, f)
	if err != nil || p.Total != 0 {
		t.Fatal("import date falsely changed to today")
	}
	after, err := c.Load(saved.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("read-only filtering changed stored observations")
	}
}

func TestBaselineHintRespectsOtherFiltersAndMixedResults(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	source, _ := c.Add(t.TempDir(), "")
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.Local).UTC()
	old := media.Item{RelativePath: "Pic/2026-09/Ori/old.png", Identity: "old", Kind: "image", SourceMonth: "2026-09", Role: "original", Format: "png", Status: "recognized", Bytes: 10, ModifiedAt: at}
	r := media.Report{Complete: true, Items: []media.Item{old}}
	c.Apply(context.Background(), source.ID, &r, true, nil, true, at)
	newItem := media.Item{RelativePath: "Video/2026-09/Ori/new.mp4", Identity: "new", Kind: "video", SourceMonth: "2026-09", Role: "original", Format: "iso-bmff", Status: "recognized", Bytes: 30, ModifiedAt: at}
	r = media.Report{Complete: true, Items: []media.Item{old, newItem}}
	if err = c.Apply(context.Background(), source.ID, &r, false, map[string]time.Time{newItem.RelativePath: at.Add(time.Hour)}, true, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	f := media.Filter{View: "observed", From: "2026-10-30", To: "2026-10-30"}
	p, err := c.Page(context.Background(), source.ID, f)
	if err != nil || p.Total != 1 || p.BaselineFiles != 1 {
		t.Fatal("mixed discovery count", err, p)
	}
	f.Kind = "video"
	p, err = c.Page(context.Background(), source.ID, f)
	if err != nil || p.Total != 1 || p.BaselineFiles != 0 {
		t.Fatal("baseline explanation ignored media type")
	}
	f.IncludeBaseline = true
	f.Kind = ""
	p, err = c.Page(context.Background(), source.ID, f)
	if err != nil || p.Total != 2 {
		t.Fatal("mixed include baseline", err)
	}
	expected := media.Paginate(r.Items, f)
	if expected.Total != p.Total || expected.BaselineFiles != p.BaselineFiles {
		t.Fatal("in-memory parity")
	}
}

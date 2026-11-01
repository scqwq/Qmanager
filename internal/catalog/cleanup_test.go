package catalog

import (
	"context"
	"path/filepath"
	"qqarchive/internal/media"
	"testing"
)

func TestOpeningCatalogDoesNotInterruptActiveCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.sqlite")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	source, err := first.Add(t.TempDir(), "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	item := media.Item{InstanceID: "synthetic-instance", RelativePath: "Pic/2025-10/Ori/a.png", Kind: "image", Bytes: 10}
	job, err := first.CreateCleanup(context.Background(), media.Source{ID: source.ID, Name: source.Name, Resolved: source.Root}, media.Filter{}, []media.Item{item})
	if err != nil {
		t.Fatal(err)
	}
	if err = first.SaveCleanupItem(context.Background(), job, CleanupItem{Item: item, Status: "processing"}); err != nil {
		t.Fatal(err)
	}
	if err = first.CleanupState(job.ID, "running"); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	active, err := second.Cleanup(job.ID)
	if err != nil || active.State != "running" {
		t.Fatal("opening another connection interrupted active work", err)
	}
	if err = first.RecoverCleanup(); err != nil {
		t.Fatal(err)
	}
	recovered, err := first.Cleanup(job.ID)
	if err != nil || recovered.State != "interrupted" || recovered.Uncertain != 1 {
		t.Fatal("startup recovery lost uncertain operation", err)
	}
}

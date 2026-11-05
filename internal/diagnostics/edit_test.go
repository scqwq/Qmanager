package diagnostics

import (
	"context"
	"testing"
)

func TestDeleteRunEntryPreservesOtherRowsAndAppend(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Record(context.Background(), "info", "test.keep", "保留")
	l.Record(context.Background(), "error", "test.remove", "删除")
	before, err := l.Snapshot()
	if err != nil || len(before.Entries) != 2 {
		t.Fatal(err, before.Entries)
	}
	if err = l.deleteEntry(before.Entries[0].ID); err != nil {
		t.Fatal(err)
	}
	l.Record(context.Background(), "info", "test.new", "追加")
	after, err := l.Snapshot()
	if err != nil || len(after.Entries) != 2 || after.Entries[0].Event != "test.new" || after.Entries[1].Event != "test.keep" {
		t.Fatal("delete changed other entries or broke the writer", err, after.Entries)
	}
}

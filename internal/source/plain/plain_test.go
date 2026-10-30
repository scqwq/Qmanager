package plain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadOnlySource(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "fixture")
	if e := Demo(ctx, dir); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "source.db")
	before, e := FileHash(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Open(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.DB.ExecContext(ctx, "DELETE FROM messages"); e == nil {
		t.Fatal("source writes allowed")
	}
	r.Close()
	after, _ := FileHash(ctx, p)
	if before != after {
		t.Fatal("source changed")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		side := p + suffix
		if e = os.WriteFile(side, []byte("sidecar"), 0600); e != nil {
			t.Fatal(e)
		}
		if db, e := ReadOnly(ctx, p); e == nil {
			db.Close()
			t.Fatal("sidecar silently ignored")
		}
		os.Remove(side)
	}
}

package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"qqarchive/internal/media"
	"testing"
	"time"
)

func TestCachedPageWithFiveThousandMetadataRecords(t *testing.T) {
	root := t.TempDir()
	index := filepath.Join(t.TempDir(), "index.sqlite")
	c, err := Open(index)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	source, _ := c.Add(root, "合成索引性能样本")
	r := media.Report{Complete: true, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()}
	for i := 0; i < 5000; i++ {
		r.Items = append(r.Items, media.Item{RelativePath: fmt.Sprintf("Pic/2026-%02d/Ori/%06d.jpg", i%10+1, i), Identity: fmt.Sprintf("fixture-%d", i), Kind: "image", SourceMonth: fmt.Sprintf("2026-%02d", i%10+1), Role: "original", Format: "png", Status: "recognized", Bytes: 4096, ModifiedAt: time.Now().UTC()})
	}
	start := time.Now()
	if err = c.Apply(context.Background(), source.ID, &r, true, nil, true, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	initial := time.Since(start)
	start = time.Now()
	page, err := c.Page(context.Background(), source.ID, media.Filter{Limit: 60})
	elapsed := time.Since(start)
	if err != nil || page.Total != 5000 || len(page.Items) != 60 {
		t.Fatal("cached page", err, page.Total)
	}
	info, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic metadata only: records=5000 page=60 index_bytes=%d initial_index=%s cached_query=%s", info.Size(), initial, elapsed)
}

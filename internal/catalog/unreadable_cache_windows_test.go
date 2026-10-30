package catalog

import (
	"context"
	"golang.org/x/sys/windows"
	"path/filepath"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestTemporaryReadFailureKeepsCachedFirstSeen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Pic")
	rel := "2026-09/Ori/a.jpg"
	testfixture.Image(root, rel)
	source, _ := media.Resolve(root)
	c, err := Open(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	saved, _ := c.Add(root, "")
	at := time.Now().UTC().Add(-20 * time.Second)
	var original media.Item
	for n := 0; n < 3; n++ {
		r := media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), saved.ID, &r, false, nil, true, at.Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
		original = r.Items[0]
	}
	previous, _ := c.ActiveItems(saved.ID)
	p, _ := windows.UTF16PtrFromString(media.NativePath(filepath.Join(root, filepath.FromSlash(rel))))
	handle, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	r := media.ScanWith(context.Background(), source, media.ScanOptions{Previous: previous}, nil)
	windows.CloseHandle(handle)
	if r.Complete || len(r.Items) != 1 || r.Items[0].Status != "unreadable" {
		t.Fatal("read failure not detected", r.Issues)
	}
	if err = c.Apply(context.Background(), saved.ID, &r, false, nil, false, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	page, err := c.Page(context.Background(), saved.ID, media.Filter{})
	if err != nil || page.Total != 1 || page.Items[0].InstanceID != original.InstanceID || !page.Items[0].FirstSeenAt.Equal(original.FirstSeenAt) || page.Items[0].Stability != "pending" {
		t.Fatal("temporary lock replaced cached observation history", err)
	}
	records, _ := c.Active(saved.ID)
	if records[0].Samples != 0 {
		t.Fatal("unreadable file contributed a stable sample")
	}
}

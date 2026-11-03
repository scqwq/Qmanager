package appservice

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediacleanup"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
	"time"
)

func videoCoverFixture(t *testing.T, count int) (*Media, *catalog.Catalog, media.Source) {
	t.Helper()
	root, private := filepath.Join(t.TempDir(), "nt_data"), t.TempDir()
	video := make([]byte, 4096)
	copy(video, "\x00\x00\x00\x18ftypisom")
	for n := 0; n < count; n++ {
		id := fmt.Sprintf("%032x", n)
		if err := testfixture.File(root, "Video/2026-09/Ori/"+id+".mp4", video); err != nil {
			t.Fatal(err)
		}
		if err := testfixture.Image(root, "Video/2026-09/Thumb/"+id+"_0.png"); err != nil {
			t.Fatal(err)
		}
	}
	// Orphan cover and a video whose only cover belongs to another month.
	testfixture.Image(root, "Video/2026-08/Thumb/ffffffffffffffffffffffffffffffff_0.png")
	testfixture.Image(root, "Video/2026-09/Thumb/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee_0.png")
	testfixture.File(root, "Video/2026-09/Ori/ffffffffffffffffffffffffffffffff.mp4", video)
	testfixture.File(root, "Video/2026-09/OriTemp/part.tmp", []byte("part"))
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Add(root, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	source, err := media.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	source.ID = saved.ID
	for n := 0; n < 3; n++ {
		r := media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), source.ID, &r, false, nil, true, time.Now().Add(time.Duration(n-3)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, c, source
}

func TestVideoCoversPaginationCountsAndControlledPreview(t *testing.T) {
	s, c, source := videoCoverFixture(t, 61)
	// The same identifier in another account must not fill an unmatched video.
	otherRoot := filepath.Join(t.TempDir(), "nt_data")
	if err := testfixture.Image(otherRoot, "Video/2026-09/Thumb/ffffffffffffffffffffffffffffffff_0.png"); err != nil {
		t.Fatal(err)
	}
	other, err := c.Add(otherRoot, "other synthetic source")
	if err != nil {
		t.Fatal(err)
	}
	otherSource, err := media.Resolve(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	r := media.Scan(context.Background(), otherSource, nil)
	if err = c.Apply(context.Background(), other.ID, &r, true, nil, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, _ := testfixture.Snapshot(source.Resolved)
	f := media.Filter{Kind: "video", Limit: 60}
	p := s.Page(f)
	if p.Total != 63 || len(p.Items) != 60 {
		t.Fatal("hidden covers must be excluded before pagination", p.Total, len(p.Items))
	}
	for _, item := range p.Items {
		if item.Role == "cover" || item.CoverID == "" {
			t.Fatal("bad first page", item.Role, item.CoverID)
		}
	}
	f.Offset = 60
	last := s.Page(f)
	if last.Total != 63 || len(last.Items) != 3 {
		t.Fatal("bad final page", last.Total, len(last.Items))
	}
	if last.Items[0].CoverID == "" || last.Items[1].CoverID != "" {
		t.Fatal("missing cover should remain a video placeholder")
	}
	if s.Page(media.Filter{}).Total != 63 {
		t.Fatal("all types exposed covers")
	}
	if s.Page(media.Filter{Kind: "video", Role: "cover"}).Total != 63 {
		t.Fatal("explicit cover view lost orphans")
	}
	filtered := s.Page(media.Filter{Kind: "video", Role: "original", MinBytes: 1024})
	if filtered.Total != 62 || filtered.Items[0].CoverID == "" {
		t.Fatal("cover lookup incorrectly inherited role/size/page filters")
	}
	for _, f := range []media.Filter{{Kind: "video"}, {View: "observed", IncludeBaseline: true}} {
		total := 0
		for _, b := range s.FilterBuckets(f) {
			total += b.Files
		}
		if total != 63 {
			t.Fatal("navigation count includes hidden covers", total)
		}
	}
	thumb := httptest.NewRecorder()
	s.ServeHTTP(thumb, httptest.NewRequest("GET", "/media/thumbnail/"+p.Items[0].ID, nil))
	if thumb.Code != 200 || thumb.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("video thumbnail failed", thumb.Code)
	}
	items, err := c.Selected(context.Background(), source.ID, media.Filter{})
	if err != nil || len(items) != 126 {
		t.Fatal("display filtering leaked into physical selection", err, len(items))
	}
	for _, item := range items {
		if item.CoverID != "" {
			t.Fatal("derived relation persisted")
		}
	}
	after, _ := testfixture.Snapshot(source.Resolved)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview changed source")
	}
	// A replacement fails the existing identity/size/mtime checks.
	for _, item := range items {
		if item.ID == p.Items[0].CoverID {
			testfixture.File(source.Resolved, item.RelativePath, []byte("replacement"))
		}
	}
	thumb = httptest.NewRecorder()
	s.ServeHTTP(thumb, httptest.NewRequest("GET", "/media/thumbnail/"+p.Items[0].ID, nil))
	if thumb.Code != 422 {
		t.Fatal("changed cover was served", thumb.Code)
	}
}

func TestVideoCoverBackupViewAndCleanupStayIndependent(t *testing.T) {
	s, c, source := videoCoverFixture(t, 1)
	f := media.Filter{Kind: "video"}
	plan, err := s.BackupEstimate(f)
	if err != nil || plan.Files+plan.Pending != 5 {
		t.Fatal("backup omitted hidden covers", plan, err)
	}
	originals, err := s.BackupEstimate(media.Filter{Kind: "video", Role: "original"})
	if err != nil || originals.Files+originals.Pending != 2 {
		t.Fatal("explicit role expanded backup", originals, err)
	}
	if err = s.Backup(context.Background(), t.TempDir(), f); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	rows, err := s.Backups()
	if err != nil || len(rows) != 1 || rows[0].Files != 5 {
		t.Fatal("physical backup count", err, rows)
	}
	// Inspect the independent backup view through the normal verified entry point.
	if err = s.OpenBackup(context.Background(), rows[0].Path); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	page := s.Page(f)
	if s.State().Error != "" || page.Total != 2 || page.Items[0].CoverID == "" {
		t.Fatal("backup display differs", s.State().Error, page.Total)
	}
	thumb := httptest.NewRecorder()
	s.ServeHTTP(thumb, httptest.NewRequest("GET", "/media/thumbnail/"+page.Items[0].ID, nil))
	if thumb.Code != 200 || s.Page(media.Filter{Role: "cover"}).Total != 3 {
		t.Fatal("backup covers unavailable", thumb.Code)
	}
	if err = s.Switch(source.ID); err != nil {
		t.Fatal(err)
	}
	s.cleanupQQ = func() (bool, error) { return false, nil }
	recycled := []string{}
	target := t.TempDir()
	s.recycleMedia = func(ctx context.Context, item media.Item, expected string) error {
		h, err := mediacleanup.Hash(ctx, item)
		if err != nil {
			return err
		}
		if h != expected {
			return fmt.Errorf("changed synthetic file")
		}
		recycled = append(recycled, item.Role)
		return os.Rename(filepath.Join(item.Root, item.Path), filepath.Join(target, item.InstanceID))
	}
	job := prepareCleanup(t, s, CleanupRequest{AllMatching: true, Filter: f})
	if job.Total != 3 || job.Ready != 2 {
		t.Fatal("all matching included hidden covers", job)
	}
	if err = s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if len(recycled) != 2 {
		t.Fatal("unexpected deletion count", recycled, s.State().Error)
	}
	for _, role := range recycled {
		if role != "original" {
			t.Fatal("cover deletion linked to video")
		}
	}
	if s.Page(media.Filter{Role: "cover"}).Total != 3 {
		t.Fatal("cover records removed with video")
	}
	remaining, err := c.Selected(context.Background(), source.ID, media.Filter{Role: "cover"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range remaining {
		if _, err = os.Stat(filepath.Join(source.Resolved, item.RelativePath)); err != nil {
			t.Fatal("cover file removed", err)
		}
	}
}

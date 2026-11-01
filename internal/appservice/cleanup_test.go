package appservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/mediacleanup"
	"qqarchive/internal/testfixture"
	"strings"
	"testing"
	"time"
)

func managerFixture(t *testing.T) (*Media, *catalog.Catalog, media.Source, *int) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "nt_data")
	for _, path := range []string{"Pic/2025-10/Ori/p.png", "Emoji/emoji-recv/2025-01/Ori/a.png", "Emoji/emoji-recv/2025-12/Ori/b.png", "Emoji/emoji-recv/2026-09/Ori/c.png"} {
		if err := testfixture.Image(root, path); err != nil {
			t.Fatal(err)
		}
	}
	testfixture.File(root, "Ptt/2026-09/Ori/v.amr", []byte("\x02#!SILK_V3synthetic"))
	testfixture.File(root, "Emoji/emoji-recv/2025-12/OriTemp/growing.tmp", []byte("pending"))
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Add(root, "合成媒体账号")
	if err != nil {
		t.Fatal(err)
	}
	source, _ := media.Resolve(root)
	source.ID = saved.ID
	source.Name = saved.Name
	for n := 0; n < 3; n++ {
		r := media.Scan(context.Background(), source, nil)
		if err = c.Apply(context.Background(), source.ID, &r, false, nil, true, time.Now().Add(time.Duration(n)*2*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.cleanupQQ = func() (bool, error) { return false, nil }
	calls := new(int)
	recycle := t.TempDir()
	s.recycleMedia = func(ctx context.Context, item media.Item, expected string) error {
		actual, e := mediacleanup.Hash(ctx, item)
		if e != nil {
			return e
		}
		if actual != expected {
			return errors.New("synthetic content changed")
		}
		*calls++
		return os.Rename(filepath.Join(item.Root, item.Path), filepath.Join(recycle, item.InstanceID))
	}
	return s, c, source, calls
}
func prepareCleanup(t *testing.T, s *Media, request CleanupRequest) catalog.CleanupJob {
	t.Helper()
	if err := s.PrepareMediaDelete(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	state := s.State()
	if state.Error != "" || state.Cleanup == nil || state.Cleanup.State != "prepared" {
		t.Fatal("prepare failed", state.Error, state.Cleanup)
	}
	return *state.Cleanup
}

func TestCleanupYearFreezeAcrossPagesExclusionsAndRepeatedDelete(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	page := s.Page(media.Filter{Kind: "emoji", MonthFrom: "2025-01", MonthTo: "2025-12"})
	if page.Total != 3 {
		t.Fatal("year fixture")
	}
	exclude := ""
	for _, item := range page.Items {
		if strings.HasSuffix(item.RelativePath, "a.png") {
			exclude = item.InstanceID
		}
	}
	job := prepareCleanup(t, s, CleanupRequest{AllMatching: true, Filter: media.Filter{Kind: "emoji", MonthFrom: "2025-01", MonthTo: "2025-12"}, ExcludeIDs: []string{exclude}})
	if job.Total != 2 || job.Ready != 1 || job.Skipped != 1 {
		t.Fatalf("wrong scope %+v", job)
	}
	testfixture.Image(source.Resolved, "Emoji/emoji-recv/2025-12/Ori/new-after-confirm.png")
	if err := s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if s.State().Error != "" {
		t.Fatal(s.State().Error)
	}
	done, _ := c.Cleanup(job.ID)
	if done.Recycled != 1 || *calls != 1 || done.Ready != 0 || done.Bytes != 0 {
		t.Fatalf("wrong result %+v calls %d", done, *calls)
	}
	for _, path := range []string{"Emoji/emoji-recv/2025-01/Ori/a.png", "Emoji/emoji-recv/2026-09/Ori/c.png", "Emoji/emoji-recv/2025-12/Ori/new-after-confirm.png", "Ptt/2026-09/Ori/v.amr", "Pic/2025-10/Ori/p.png"} {
		if _, err := os.Stat(filepath.Join(source.Resolved, filepath.FromSlash(path))); err != nil {
			t.Fatal("deleted outside snapshot", path, err)
		}
	}
	if err := s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	if *calls != 1 {
		t.Fatal("repeat execution deleted again")
	}
	history, err := c.History(func() string {
		rows, _ := c.CleanupItems(job.ID, 0, 60)
		for _, r := range rows {
			if r.Status == "recycled" {
				return r.Item.InstanceID
			}
		}
		return ""
	}())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range history {
		if event.Kind == "user_recycled" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing observation-preserving cleanup event")
	}
}
func TestCleanupQQGateReplacementAndSourceSwitch(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	page := s.Page(media.Filter{Kind: "voice", Month: "2026-09"})
	if page.Total != 1 {
		t.Fatal("voice fixture")
	}
	item := page.Items[0]
	job := prepareCleanup(t, s, CleanupRequest{IDs: []string{item.InstanceID, item.InstanceID}})
	if job.Total != 1 {
		t.Fatal("duplicate selection")
	}
	s.cleanupQQ = func() (bool, error) { return true, nil }
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	if *calls != 0 || s.State().Error == "" {
		t.Fatal("ignored running QQ")
	}
	s.cleanupQQ = func() (bool, error) { return false, nil }
	path := filepath.Join(source.Resolved, filepath.FromSlash(item.RelativePath))
	os.Rename(path, path+".old")
	testfixture.File(source.Resolved, item.RelativePath, []byte("\x02#!SILK_V3replacement"))
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	done, _ := c.Cleanup(job.ID)
	if *calls != 0 || done.Failed != 1 {
		t.Fatalf("replaced file not protected %+v", done)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("replacement deleted")
	}
	second := prepareCleanup(t, s, CleanupRequest{AllMatching: true, Filter: media.Filter{Kind: "image"}})
	other := filepath.Join(t.TempDir(), "Pic")
	testfixture.Image(other, "2026-09/Ori/x.png")
	if _, err := s.Select(other); err != nil {
		t.Fatal(err)
	}
	s.ExecuteMediaDelete(context.Background(), second.ID)
	waitBackupJob(t, s)
	if *calls != 0 || s.State().Error == "" {
		t.Fatal("accepted old plan in another source")
	}
}
func TestCleanupHashChecksSameSizeAndTimestampChanges(t *testing.T) {
	s, c, source, calls := managerFixture(t)
	item := s.Page(media.Filter{Kind: "image"}).Items[0]
	job := prepareCleanup(t, s, CleanupRequest{IDs: []string{item.InstanceID}})
	path := filepath.Join(source.Resolved, filepath.FromSlash(item.RelativePath))
	before, _ := os.Stat(path)
	b, _ := os.ReadFile(path)
	b[len(b)-1] ^= 1
	os.WriteFile(path, b, 0600)
	os.Chtimes(path, before.ModTime(), before.ModTime())
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	done, _ := c.Cleanup(job.ID)
	if *calls != 0 || done.Failed != 1 {
		t.Fatalf("content-only replacement removed %+v", done)
	}
}
func TestCleanupCancelAndInterruptedRecoveryNeverAutoRuns(t *testing.T) {
	s, c, _, calls := managerFixture(t)
	job := prepareCleanup(t, s, CleanupRequest{AllMatching: true, Filter: media.Filter{Kind: "image"}})
	rows, _ := c.CleanupItems(job.ID, 0, 60)
	row := rows[0]
	row.Status = "processing"
	if err := c.SaveCleanupItem(context.Background(), job, row); err != nil {
		t.Fatal(err)
	}
	c.CleanupState(job.ID, "running")
	if err := s.RecoverCleanup(); err != nil {
		t.Fatal(err)
	}
	recovered, _ := c.Cleanup(job.ID)
	if recovered.State != "interrupted" || recovered.Uncertain != 1 {
		t.Fatal("lost interrupted result")
	}
	s.ExecuteMediaDelete(context.Background(), job.ID)
	waitBackupJob(t, s)
	if *calls != 0 {
		t.Fatal("restarted interrupted destructive work")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.PrepareMediaDelete(ctx, CleanupRequest{AllMatching: true, Filter: media.Filter{Kind: "image"}})
	waitBackupJob(t, s)
	if *calls != 0 {
		t.Fatal("cancelled preparation deleted files")
	}
}

func TestCleanupUnconfirmedRemovalStopsRemainingItems(t *testing.T) {
	s, c, source, _ := managerFixture(t)
	job := prepareCleanup(t, s, CleanupRequest{AllMatching: true, Filter: media.Filter{Kind: "emoji", MonthFrom: "2025-01", MonthTo: "2025-12"}})
	calls := 0
	isolation := t.TempDir()
	s.recycleMedia = func(ctx context.Context, item media.Item, expected string) error {
		calls++
		if err := os.Rename(filepath.Join(item.Root, item.Path), filepath.Join(isolation, item.InstanceID)); err != nil {
			return err
		}
		return errors.New("synthetic missing post-delete confirmation")
	}
	if err := s.ExecuteMediaDelete(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	waitBackupJob(t, s)
	result, err := c.Cleanup(job.ID)
	if err != nil || result.State != "interrupted" || result.Uncertain != 1 || result.Ready != 1 || calls != 1 {
		t.Fatalf("continued after an uncertain result: %+v, calls=%d, err=%v", result, calls, err)
	}
	rows, err := c.CleanupItems(job.ID, 0, 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Status == "ready" {
			if _, err = os.Stat(filepath.Join(source.Resolved, filepath.FromSlash(row.Item.RelativePath))); err != nil {
				t.Fatal("unprocessed file changed", err)
			}
		}
	}
}

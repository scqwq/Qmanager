package appservice

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/discovery"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"reflect"
	"testing"
	"time"
)

func TestDirectorySearchOnlyReturnsCandidateWithoutImporting(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tencent Files")
	rel := "synthetic-account/nt_qq/nt_data/Pic/2026-11/Ori/image.png"
	if err := testfixture.Image(root, rel); err != nil {
		t.Fatal(err)
	}
	private := t.TempDir()
	c, err := catalog.Open(filepath.Join(private, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := media.Resolve(filepath.Join(root, "synthetic-account", "nt_qq", "nt_data"))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := c.Add(source.Resolved, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	source.ID = saved.ID
	r := media.Scan(context.Background(), source, nil)
	if err = c.Apply(context.Background(), source.ID, &r, true, nil, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	settings := PathSettings{SourceDirectory: root, BackupDirectory: t.TempDir()}
	payload, _ := json.Marshal(settings)
	if err = c.SetPreference("path_defaults", string(payload)); err != nil {
		t.Fatal(err)
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	before, _ := c.Active(source.ID)
	filesBefore, _ := testfixture.Snapshot(root)
	result, err := s.SearchQQDirectory(context.Background(), "read-only-search", root, settings.BackupDirectory)
	if err != nil || result.Status != "found" || len(result.Candidates) != 1 || result.Candidates[0] != root {
		t.Fatal("missing synthetic candidate", err)
	}
	after, _ := c.Active(source.ID)
	current, _ := s.Settings()
	sources, _ := c.Sources()
	filesAfter, _ := testfixture.Snapshot(root)
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(filesBefore, filesAfter) || current != settings || len(sources) != 1 || s.State().Source.ID != source.ID {
		t.Fatal("search changed settings, source selection or media evidence")
	}
}

func waitSearchWorker(t *testing.T, s *Media) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.searchMu.Lock()
		idle := s.directorySearch == nil
		s.searchMu.Unlock()
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("search worker did not release its slot")
}

func TestDirectorySearchCancelOnlyMatchingRequest(t *testing.T) {
	s := &Media{}
	started := make(chan struct{})
	finished := make(chan DirectorySearchResult, 1)
	go func() {
		r, _ := s.searchQQDirectory(context.Background(), "current", func(ctx context.Context) (discovery.Result, error) {
			close(started)
			<-ctx.Done()
			return discovery.Result{}, ctx.Err()
		})
		finished <- r
	}()
	<-started
	s.CancelQQDirectorySearch("old-request")
	select {
	case <-finished:
		t.Fatal("stale request cancelled current search")
	case <-time.After(10 * time.Millisecond):
	}
	s.CancelQQDirectorySearch("current")
	select {
	case r := <-finished:
		if r.Status != "cancelled" || len(r.Candidates) != 0 {
			t.Fatal("cancel returned a result", r)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not return promptly")
	}
	waitSearchWorker(t, s)
}

func TestDirectorySearchCancelBeforeStartNeverRuns(t *testing.T) {
	s := &Media{}
	s.CancelQQDirectorySearch("not-started")
	r, err := s.searchQQDirectory(context.Background(), "not-started", func(context.Context) (discovery.Result, error) {
		t.Error("cancelled request started filesystem work")
		return discovery.Result{}, nil
	})
	if err != nil || r.Status != "cancelled" {
		t.Fatal("early cancel was lost", r, err)
	}
	for n := 0; n < 100; n++ {
		s.CancelQQDirectorySearch(fmt.Sprint(n))
	}
	if len(s.directorySearchCancelled) > 64 {
		t.Fatal("unbounded cancelled-request cache")
	}
}

func TestDirectorySearchTimeoutBoundsBlockedWorker(t *testing.T) {
	if DirectorySearchTimeout != 30*time.Second {
		t.Fatal("production search limit changed")
	}
	s := &Media{}
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r, err := s.searchQQDirectory(ctx, "first", func(context.Context) (discovery.Result, error) {
		<-release
		return discovery.Result{Complete: true, Candidates: []discovery.Candidate{{Root: "late-result"}}}, nil
	})
	if err != nil || r.Status != "timeout" || len(r.Candidates) != 0 {
		t.Fatal("late result escaped deadline", r, err)
	}
	if _, err = s.searchQQDirectory(context.Background(), "second", func(context.Context) (discovery.Result, error) {
		t.Error("launched another blocked worker")
		return discovery.Result{}, nil
	}); err == nil {
		t.Fatal("worker slot lost after timeout")
	}
	close(release)
	waitSearchWorker(t, s)
	r, err = s.searchQQDirectory(context.Background(), "next", func(context.Context) (discovery.Result, error) {
		return discovery.Result{Complete: true, Candidates: []discovery.Candidate{{Root: "synthetic"}}}, nil
	})
	if err != nil || r.Status != "found" || len(r.Candidates) != 1 {
		t.Fatal("could not search after worker cleanup", r, err)
	}
	waitSearchWorker(t, s)
}

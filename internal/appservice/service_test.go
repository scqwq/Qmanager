package appservice

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"qqarchive/internal/catalog"
	"qqarchive/internal/media"
	"qqarchive/internal/testfixture"
	"testing"
	"time"
)

func TestMultipleSourcesAndControlledRangePreview(t *testing.T) {
	private := filepath.Join(t.TempDir(), "private")
	c, err := catalog.Open(filepath.Join(private, "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewMedia(c, private)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := filepath.Join(t.TempDir(), "nt_data")
	if err = testfixture.Image(root, "Pic/2026-09/Ori/中文.jpg"); err != nil {
		t.Fatal(err)
	}
	source, err := s.Select(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for s.State().Report == nil && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if s.State().Report == nil {
		t.Fatal("scan failed", s.State().Error)
	}
	p := s.Page(media.Filter{})
	if len(p.Items) != 1 {
		t.Fatal("missing item")
	}
	url, err := s.MediaURL(p.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", url, nil)
	request.Header.Set("Range", "bytes=0-7")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 206 || string(body) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("range preview status=%d body=%q", response.StatusCode, body)
	}
	response, err = http.Get(s.url + "/unauthorized/path")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("unauthorized URL accepted")
	}
	thumb := httptest.NewRecorder()
	s.ServeHTTP(thumb, httptest.NewRequest("GET", "/media/thumbnail/"+p.Items[0].ID, nil))
	if thumb.Code != 200 || thumb.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatal("thumbnail failed", thumb.Code)
	}
	s.Stop()
	other := filepath.Join(t.TempDir(), "Pic")
	testfixture.Image(other, "2026-10/Ori/other.png")
	second, err := s.Select(other)
	if err != nil {
		t.Fatal(err)
	}
	if source.ID == second.ID || len(s.State().Sources) != 2 {
		t.Fatal("sources merged")
	}
	if err = s.Switch(source.ID); err != nil {
		t.Fatal(err)
	}
	if s.State().Source.ID != source.ID {
		t.Fatal("source switch failed")
	}
	if _, err = s.Select(private); err == nil {
		t.Fatal("private data accepted as source")
	}
}

package appservice

import (
	"errors"
	"net/http"
	"qqarchive/internal/media"
	"strings"
)

func (s *Media) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/media/thumbnail/") {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/media/thumbnail/")
	item, ok := s.lookup(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if media.VideoCoverPath(item) != "" {
		cover, found := s.videoCover(item)
		if !found {
			http.Error(w, "暂时无法预览", http.StatusUnprocessableEntity)
			return
		}
		item = cover
	}
	select {
	case s.previews <- struct{}{}:
		defer func() { <-s.previews }()
	case <-r.Context().Done():
		return
	}
	b, err := media.Thumbnail(r.Context(), item)
	if err != nil {
		http.Error(w, "暂时无法预览，请重新扫描", 422)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(b)
}
func (s *Media) MediaURL(id string) (string, error) {
	item, ok := s.lookup(id)
	if !ok || item.Status != "recognized" {
		return "", errors.New("该文件不可预览")
	}
	if item.Format != "iso-bmff" && item.Format != "png" && item.Format != "jpeg" && item.Format != "gif" {
		return "", errors.New("此格式暂不支持内置预览")
	}
	return s.url + "/" + s.token + "/" + id, nil
}
func (s *Media) stream(w http.ResponseWriter, r *http.Request) {
	select {
	case s.streamWorkers <- struct{}{}:
		defer func() { <-s.streamWorkers }()
	default:
		http.Error(w, "媒体读取繁忙", http.StatusTooManyRequests)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if r.Method != "GET" && r.Method != "HEAD" {
		http.NotFound(w, r)
		return
	}
	prefix := "/" + s.token + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	item, ok := s.lookup(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif", "iso-bmff": "video/mp4"}[item.Format]
	if mime == "" || item.Status != "recognized" {
		http.NotFound(w, r)
		return
	}
	f, err := media.OpenItem(item)
	if err != nil {
		http.Error(w, "文件发生变化，请重新扫描", 409)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	http.ServeContent(w, r, "media", item.ModifiedAt, f)
}

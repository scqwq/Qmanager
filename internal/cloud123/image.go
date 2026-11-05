package cloud123

import (
	"net/http"
	"strings"
	"time"
)

// ServeQR serves only the active, short-lived generated image. It never reads a
// path from disk and uses the same origin already allowed by the app's CSP.
func (s *Service) ServeQR(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/cloud-qr/")
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed || s.qr == nil || id == "" || id != s.qr.public.ID || time.Now().After(s.qr.public.ExpiresAt) || len(s.qr.image) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(s.qr.image)
}

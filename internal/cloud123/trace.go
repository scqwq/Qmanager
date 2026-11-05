package cloud123

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Enabled only by an explicitly configured local test build. Never captures
// request bodies, Cookie headers, passwords, OTPs, or real session credentials.
type responseTrace struct {
	mu       sync.Mutex
	path     string
	lastPoll string
}

func (t *responseTrace) record(path string, status int, body []byte) {
	if t == nil || t.path == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	info, e := os.Lstat(t.path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		v = map[string]any{"format": "响应不是JSON", "bytes": len(body)}
	}
	if path == "/b/api/user/info" {
		if top, ok := v.(map[string]any); ok {
			if data, ok := top["data"].(map[string]any); ok {
				minimal := map[string]any{}
				for _, name := range []string{"UID", "Passport", "Mail", "Nickname"} {
					if value, exists := data[name]; exists {
						minimal[name] = value
					}
				}
				top["data"] = minimal
			}
		}
	}
	safe := redact(v, "")
	if path == "/api/user/qr-code/generate" {
		t.lastPoll = ""
	}
	if path == "/api/user/qr-code/result" {
		if top, ok := safe.(map[string]any); ok {
			fingerprint, _ := json.Marshal(map[string]any{"http": status, "code": top["code"], "data": top["data"], "message": top["message"]})
			if string(fingerprint) == t.lastPoll {
				return
			}
			t.lastPoll = string(fingerprint)
		}
	}
	b, e := json.MarshalIndent(safe, "", "  ")
	if e != nil {
		return
	}
	f, e := os.OpenFile(filepath.Clean(t.path), os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n## " + time.Now().Format(time.RFC3339) + " · " + path + "\n\nHTTP " + jsonNumber(status) + "\n\n```json\n" + string(b) + "\n```\n")
}
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
func redact(v any, key string) any {
	k := strings.ToLower(key)
	if k == "traceless" {
		if obj, ok := v.(map[string]any); ok {
			out := map[string]any{}
			for _, field := range []string{"code", "message", "msg"} {
				if value, exists := obj[field]; exists {
					out[field] = redact(value, field)
				}
			}
			return out
		}
	}
	if k == "key" || strings.HasSuffix(k, "_key") || strings.HasSuffix(k, "key") {
		return "<已隐藏>"
	}
	for _, s := range []string{"token", "secret", "password", "cookie", "passport", "phone", "mail", "uniid", "certify", "traceless", "signature", "credential", "device", "uid", "url", "nickname", "wxcode", "wechat", "v_code", "vcode", "serial"} {
		if strings.Contains(k, s) {
			if v == nil {
				return nil
			}
			if str, ok := v.(string); ok && str == "" {
				return ""
			}
			return "<已隐藏>"
		}
	}
	switch x := v.(type) {
	case map[string]any:
		r := map[string]any{}
		for k, v := range x {
			r[k] = redact(v, k)
		}
		return r
	case []any:
		r := make([]any, 0, len(x))
		for _, v := range x {
			r = append(r, redact(v, key))
		}
		return r
	case string:
		return safeMessage(x)
	default:
		return v
	}
}

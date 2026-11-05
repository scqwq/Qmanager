package cloud123

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func reply(value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(b))}
}
func ok(data any) *http.Response { return reply(map[string]any{"code": 0, "data": data}) }
func testService(t *testing.T, transport transportFunc) *Service {
	t.Helper()
	c := newClient(nil, "test-uuid")
	c.http.Transport = transport
	c.setToken("test-session-only")
	s := &Service{client: c, private: filepath.Join(t.TempDir(), "private"), status: Status{LoggedIn: true}, folders: map[string]bool{"0": true}, files: map[string]chosenFile{}, credential: filepath.Join(t.TempDir(), "session.dpapi")}
	t.Cleanup(s.Close)
	return s
}
func TestAuthFailureEvenWithHTTP200(t *testing.T) {
	c := newClient(nil, "test-uuid")
	c.setToken("test-session-only")
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		return reply(map[string]any{"code": 20101, "message": "未登录"}), nil
	})
	_, _, e := c.user(context.Background())
	if !errors.Is(e, ErrLogin) {
		t.Fatalf("not classified as expired login: %v", e)
	}
}
func TestUploadStreamsPartsUsesFinalIDAndNeverSendsSessionToObjectHost(t *testing.T) {
	const size = 600 << 10
	var mu sync.Mutex
	var putSizes []int64
	var finalQueried bool
	s := testService(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut {
			if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
				t.Error("credential leaked to object host")
			}
			n, e := io.Copy(io.Discard, r.Body)
			if e != nil {
				return nil, e
			}
			if n != r.ContentLength {
				t.Error("incorrect part length")
			}
			mu.Lock()
			putSizes = append(putSizes, n)
			mu.Unlock()
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		if r.Header.Get("Authorization") != "Bearer test-session-only" {
			t.Error("missing session")
		}
		var body map[string]json.RawMessage
		if r.Body != nil {
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
		}
		switch r.URL.Path {
		case "/b/api/file/list/new":
			return ok(map[string]any{"Next": "-1", "InfoList": []any{}}), nil
		case "/b/api/file/upload_request":
			return ok(map[string]any{"Reuse": false, "FileId": 11, "Bucket": "test-bucket", "Key": "test-key", "UploadId": "test-upload", "StorageNode": "test-node", "SliceSize": "262144"}), nil
		case "/b/api/file/s3_upload_object/auth":
			var start, end int
			_ = json.Unmarshal(body["partNumberStart"], &start)
			_ = json.Unmarshal(body["partNumberEnd"], &end)
			if end != start+1 {
				t.Error("unexpected part range")
			}
			return ok(map[string]any{"presignedUrls": map[string]string{jsonNumber(start): "https://unit.123pan.com/object?part=" + jsonNumber(start)}}), nil
		case "/b/api/file/upload_complete/v2":
			var multipart bool
			_ = json.Unmarshal(body["isMultipart"], &multipart)
			if !multipart {
				t.Error("multipart completion missing")
			}
			return ok(map[string]any{"file_info": map[string]any{"FileId": 22}}), nil
		case "/b/api/file/info":
			var list []struct {
				ID int64 `json:"fileId"`
			}
			_ = json.Unmarshal(body["fileIdList"], &list)
			if len(list) != 1 || list[0].ID != 22 {
				t.Error("did not query final ID")
			}
			mu.Lock()
			finalQueried = true
			mu.Unlock()
			return ok(map[string]any{"infoList": []any{map[string]any{"FileId": 22, "FileName": "payload.bin", "Type": 0, "Size": size, "Etag": "6f3ec34dc6ce3bb6f20ee72b0c0fd985", "ParentFileId": 0}}}), nil
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			return ok(nil), nil
		}
	})
	path := filepath.Join(t.TempDir(), "payload.bin")
	if e := os.WriteFile(path, make([]byte, size), 0600); e != nil {
		t.Fatal(e)
	}
	local, e := s.SelectFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.StartUpload("0", local.Ticket); e != nil {
		t.Fatal(e)
	}
	s.mu.RLock()
	done := s.done
	s.mu.RUnlock()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("upload hung")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(putSizes) != 3 || putSizes[0] != 262144 || putSizes[1] != 262144 || putSizes[2] != 90112 || !finalQueried {
		t.Fatalf("unexpected transfer: %v final=%v", putSizes, finalQueried)
	}
	// The test fixture has a separately checked MD5; assert terminal success too.
	if st := s.Status().Upload; st.Phase != "complete" {
		t.Fatalf("upload failed: %s", st.Detail)
	}
}
func TestChangedSelectionAndPrivateFileRejected(t *testing.T) {
	s := testService(t, func(*http.Request) (*http.Response, error) { t.Error("network must not be used"); return ok(nil), nil })
	if e := os.MkdirAll(s.private, 0700); e != nil {
		t.Fatal(e)
	}
	secret := filepath.Join(s.private, "session")
	_ = os.WriteFile(secret, []byte("private"), 0600)
	if _, e := s.SelectFile(secret); e == nil {
		t.Fatal("private file accepted")
	}
	path := filepath.Join(t.TempDir(), "sample.txt")
	_ = os.WriteFile(path, []byte("old"), 0600)
	local, e := s.SelectFile(path)
	if e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(path, []byte("changed"), 0600)
	if e = s.StartUpload("0", local.Ticket); e != nil {
		t.Fatal(e)
	}
	s.mu.RLock()
	done := s.done
	s.mu.RUnlock()
	<-done
	if s.Status().Upload.Phase != "failed" {
		t.Fatal("changed file uploaded")
	}
}
func TestCollisionStopsBeforeInitialization(t *testing.T) {
	c := newClient(nil, "unit")
	c.setToken("unit")
	c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" {
			t.Fatal("created an object despite collision")
		}
		return ok(map[string]any{"Next": "-1", "InfoList": []any{map[string]any{"FileName": "same.zip", "Type": 0}}}), nil
	})
	if e := c.ensureNameAbsent(context.Background(), 0, "same.zip"); e == nil {
		t.Fatal("collision not detected")
	}
}
func TestObjectURLBoundary(t *testing.T) {
	for _, raw := range []string{"http://a.123pan.com/key", "https://123pan.com.evil.test/key", "https://a.123pan.com:444/key", "https://user:password@a.123pan.com/key", "https://127.0.0.1/key", "https://a.123pan.com/key#fragment"} {
		if _, e := trustedObjectURL(raw); e == nil {
			t.Errorf("accepted unsafe URL %s", raw)
		}
	}
	if _, e := trustedObjectURL("https://m8012.123pan.com/key?X-Amz-Signature=unit"); e != nil {
		t.Fatal(e)
	}
}
func TestResponseTraceRedactsCredentialsIncludingNestedObjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.md")
	_ = os.WriteFile(path, []byte("# trace\n"), 0600)
	trace := &responseTrace{path: path}
	trace.record("/api/user/sign_in", 200, []byte(`{"code":200,"message":"ok","data":{"token":"real-secret-token","passport":"13800000000","traceless":{"deviceToken":"private-proof"},"uniID":"qr-secret","url":"https://example.test/signed"}}`))
	b, _ := os.ReadFile(path)
	for _, secret := range []string{"real-secret-token", "13800000000", "private-proof", "qr-secret", "https://example.test"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Error("response log retained private data")
		}
	}
	if !bytes.Contains(b, []byte(`"code": 200`)) {
		t.Fatal("lost useful response code")
	}
}
func TestVerificationResponseLogKeepsCodeButNeverDeviceProof(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.md")
	_ = os.WriteFile(path, []byte("# trace\n"), 0600)
	tr := &responseTrace{path: path}
	tr.record("/api/user/sign_in", 200, []byte(`{"code":0,"data":{"traceless":{"code":900,"message":"无痕验证失败","deviceToken":"synthetic-proof-secret"},"info_key":"synthetic-binding-secret"}}`))
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte(`"code": 900`)) {
		t.Fatal("verification diagnostic code was hidden")
	}
	if bytes.Contains(b, []byte("synthetic-proof-secret")) || bytes.Contains(b, []byte("synthetic-binding-secret")) {
		t.Fatal("verification secret was logged")
	}
}
func TestEncryptedCredentialRestoreAndLogout(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI")
	}
	private := t.TempDir()
	s, e := New(private, "")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	c := newClient(nil, "unit")
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return ok(map[string]any{"UID": 42, "Passport": 13800000000}), nil
	})
	if e = s.accept(context.Background(), c, "test-secret-session-token", 0); e != nil {
		var cause *credentialSaveError
		if errors.As(e, &cause) {
			t.Fatalf("credential stage=%s cause=%v", cause.stage, cause.cause)
		}
		t.Fatal(e)
	}
	b, e := os.ReadFile(s.credential)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte("test-secret-session-token")) {
		t.Fatal("plaintext credential persisted")
	}
	restored, e := New(private, "")
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	if !restored.Status().NeedsCheck || restored.Status().LoggedIn || restored.client.getToken() != "test-secret-session-token" {
		t.Fatal("restore did not require revalidation")
	}
	if e = restored.Logout(); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(s.credential); !os.IsNotExist(e) {
		t.Fatal("logout left saved credential")
	}
}
func TestQRCodeUsesSameOriginAndExpiredImagesAreUnavailable(t *testing.T) {
	s := testService(t, func(r *http.Request) (*http.Response, error) {
		return ok(map[string]any{"url": "https://www.123pan.com/qr-synthetic", "uniID": "synthetic-session"}), nil
	})
	s.loginFactory = func() *Client { return s.client }
	qr, e := s.NewQR(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(qr.Image, "/cloud-qr/") || strings.Contains(qr.Image, "data:") {
		t.Fatal("QR bypassed same-origin image policy")
	}
	rec := httptest.NewRecorder()
	s.ServeQR(rec, httptest.NewRequest("GET", qr.Image, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(rec.Body.Bytes(), []byte{137, 80, 78, 71}) {
		t.Fatal("QR image was not a PNG")
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("QR must not persist in cache")
	}
	s.CancelQR()
	old := httptest.NewRecorder()
	s.ServeQR(old, httptest.NewRequest("GET", qr.Image, nil))
	if old.Code != 404 {
		t.Fatal("cancelled QR still available")
	}
}
func TestQRPayloadContainsTheLoginSessionAndAppRouting(t *testing.T) {
	base, _ := url.Parse("https://yun.123pan.cn/wx-app-login.html")
	text, e := qrPayload(base, "synthetic-uni-id")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(text)
	q := u.Query()
	if q.Get("env") != "production" || q.Get("uniID") != "synthetic-uni-id" || q.Get("source") != "123pan" || q.Get("type") != "login" {
		t.Fatal("QR would be treated as a plain/offline-download URL")
	}
	if base.RawQuery != "" {
		t.Fatal("mutated shared QR base")
	}
	if _, e = qrPayload(base, ""); e == nil {
		t.Fatal("accepted missing login session")
	}
}
func TestPasswordLoginEncryptsBothFieldsAndKeepsPhoneEmailModes(t *testing.T) {
	for _, account := range []string{"138 0000 0000", "synthetic@example.invalid"} {
		c := newClient(nil, "synthetic-uuid")
		c.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/user/sign_in" {
				t.Fatal("wrong sign-in endpoint")
			}
			var body map[string]any
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Fatal(e)
			}
			key := "passport"
			wantType := float64(1)
			if strings.Contains(account, "@") {
				key = "mail"
				wantType = 2
			}
			if body["type"] != wantType || body["gray"] != true || body["remember"] != true {
				t.Fatal("incorrect login mode")
			}
			for _, field := range []string{key, "password"} {
				v, ok := body[field].(string)
				if !ok {
					t.Fatal("missing encrypted login field")
				}
				b, e := base64.StdEncoding.DecodeString(v)
				if e != nil || len(b) != 256 || strings.Contains(v, "synthetic-password") {
					t.Fatal("login field not RSA encrypted")
				}
			}
			return reply(map[string]any{"code": 200, "data": map[string]any{"token": "synthetic-session-result"}}), nil
		})
		if token, e := c.passwordLogin(context.Background(), account, "synthetic-password"); e != nil || token != "synthetic-session-result" {
			t.Fatalf("token mapping failed: %v", e)
		}
	}
	c := newClient(nil, "synthetic-uuid")
	c.http.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		return reply(map[string]any{"code": 11000, "message": "需要验证"}), nil
	})
	if _, e := c.passwordLogin(context.Background(), "13800000000", "synthetic-password"); e == nil || !strings.Contains(e.Error(), "安全验证") {
		t.Fatalf("verification requirement not surfaced: %v", e)
	}
}

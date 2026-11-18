package cloud123

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestPartAuthorizationRecoversMissingPartWithoutUsingAnotherURL(t *testing.T) {
	calls := 0
	s := testService(t, func(req *http.Request) (*http.Response, error) {
		calls++
		var body struct {
			Start int64 `json:"partNumberStart"`
			End   int64 `json:"partNumberEnd"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		if req.URL.Path != "/b/api/file/s3_repare_upload_parts_batch" || body.Start != 1 || body.End != 3 {
			t.Errorf("unexpected authorization request: %+v", body)
		}
		urls := map[string]string{"1": "https://unit.123pan.com/first"}
		if calls == 2 {
			urls["2"] = "https://unit.123pan.com/second"
		}
		return ok(map[string]any{"presignedUrls": urls}), nil
	})
	addresses, err := s.client.authorizeParts(context.Background(), uploadInit{}, 1, 2, 2)
	if err != nil || calls != 2 || len(addresses) != 2 || addresses[0].Path != "/first" || addresses[1].Path != "/second" {
		t.Fatal("did not recover exact part", err, calls)
	}
}

func TestPartAuthorizationDistinguishesMissingAndUnsafeAddresses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		url   string
		want  string
		calls int
	}{
		{"missing", "1", "https://unit.123pan.com/other?signature=private", "缺少第 2/2 片的上传地址（返回分片编号：1）", 2},
		{"non-https", "2", "http://unit.123pan.com/target?signature=private", "缺少有效的 HTTPS 主机", 1},
		{"host", "2", "https://example.test/target?signature=private", "未识别的上传主机", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := testService(t, func(req *http.Request) (*http.Response, error) {
				calls++
				return ok(map[string]any{"presignedUrls": map[string]string{tc.key: tc.url}}), nil
			})
			if _, err := s.client.authorizeParts(context.Background(), uploadInit{}, 2, 2, 2); err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "signature") || calls != tc.calls {
				t.Fatal("invalid result for authorization failure", err, calls)
			}
		})
	}
}

func TestUploadRateLimitUsesRetryAfterAndPreventsImmediateRequest(t *testing.T) {
	for _, code := range []int{100011, http.StatusTooManyRequests} {
		calls := 0
		s := testService(t, func(req *http.Request) (*http.Response, error) {
			calls++
			resp := reply(map[string]any{"code": code, "message": "请勿频繁操作"})
			if code == http.StatusTooManyRequests {
				resp.StatusCode = code
			}
			resp.Header.Set("Retry-After", "12")
			return resp, nil
		})
		ctx := context.Background()
		now := time.Now()
		_, err := s.client.post(ctx, "/b/api/file/upload_request", nil)
		var limited rateLimitError
		if !errors.As(err, &limited) || limited.until.Before(now.Add(12*time.Second)) || limited.until.After(time.Now().Add(12*time.Second)) {
			t.Fatal("retry deadline was not read", code, err)
		}
		if _, err = s.client.post(ctx, "/b/api/file/upload_request", nil); !errors.As(err, &limited) || calls != 1 {
			t.Fatal("request was sent during cooldown", code, err, calls)
		}
	}
}

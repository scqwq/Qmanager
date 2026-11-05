package cloud123

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode"

	qrcode "github.com/skip2/go-qrcode"
)

// Public login key published by the login-center client. This is not a secret.
const loginPublicKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAsnhu3abShvzh4gRWdHEK
BDzpZ1riNxyVxWbeNAWQQXPgSJ+Y6FXhj4zUcbzKrvvz1pUArEi3MFZHcHl7GO2E
YY2YecNOUkIuK5I6VsLR3Giz+tgevRMyTGssTZ54FU/HFP7I8pFpOMyuhRvi3aX4
Os+bZxzYhgExI+ZIA68BBw30Z4latsXxDi7DZktd9l8RSJkwWLnBJpsniSoc3nrd
6D6tmFKdfwMGFbcBdeAkw1P4XIe9eIkqyF+2HMAAKwnHREMIKAp1YhrtH8MKXhbf
wje0QTHsqoyWpQ+RBiHZNd1HYQ6ETTA/2tTRARTq+u0ol2H5uy/Tu/dH6+hEbSx+
gQIDAQAB
-----END PUBLIC KEY-----`

func encryptLogin(s string) (string, error) {
	b, _ := pem.Decode([]byte(loginPublicKey))
	if b == nil {
		return "", errors.New("登录加密公钥无效")
	}
	k, e := x509.ParsePKIXPublicKey(b.Bytes)
	if e != nil {
		return "", errors.New("登录加密公钥无法解析")
	}
	pub, ok := k.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("登录加密算法无法识别")
	}
	if len([]byte(s)) > pub.Size()-11 {
		return "", errors.New("账号或密码过长")
	}
	out, e := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(s))
	if e != nil {
		return "", errors.New("登录加密失败")
	}
	return base64.StdEncoding.EncodeToString(out), nil
}
func (c *Client) signIn(ctx context.Context, body map[string]any) (string, error) {
	body["gray"] = true
	r, e := c.call(ctx, true, http.MethodPost, "/api/user/sign_in", nil, body)
	if e != nil {
		var provider *apiError
		if errors.As(e, &provider) && provider.code == 11000 {
			return "", fmt.Errorf("%w；需要额外安全验证，请改用扫码登录", e)
		}
		return "", e
	}
	var d struct {
		Token string `json:"token"`
		Bind  *bool  `json:"bind"`
	}
	if json.Unmarshal(r.Data, &d) != nil || d.Token == "" {
		return "", errors.New("登录尚未完成，可能需要补充绑定或安全验证，请改用扫码")
	}
	if d.Bind != nil && !*d.Bind {
		return "", errors.New("该账号需要先在云盘官方页面完成绑定")
	}
	return d.Token, nil
}
func (c *Client) passwordLogin(ctx context.Context, account, password string) (string, error) {
	account = strings.TrimSpace(account)
	if account == "" || password == "" {
		return "", errors.New("请输入账号和密码")
	}
	if len(password) > 128 || len(account) > 254 {
		return "", errors.New("账号或密码过长")
	}
	if strings.Contains(account, "@") {
		address, e := mail.ParseAddress(account)
		if e != nil || address.Address != account || address.Name != "" {
			return "", errors.New("请输入正确的手机号或邮箱")
		}
	} else {
		account = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, account)
		if !phoneOK(account) {
			return "", errors.New("请输入正确的手机号或邮箱")
		}
	}
	a, e := encryptLogin(account)
	if e != nil {
		return "", e
	}
	p, e := encryptLogin(password)
	if e != nil {
		return "", e
	}
	body := map[string]any{"type": 1, "passport": a, "password": p, "remember": true}
	if strings.Contains(account, "@") {
		delete(body, "passport")
		body["mail"] = a
		body["type"] = 2
		body["operateType"] = 2
	}
	return c.signIn(ctx, body)
}
func phoneOK(phone string) bool {
	if len(phone) != 11 || phone[0] != '1' {
		return false
	}
	for _, r := range phone {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Service) NewQR(ctx context.Context) (QR, error) {
	if !s.authGate.TryLock() {
		return QR{}, errors.New("正在处理登录，请稍候")
	}
	defer s.authGate.Unlock()
	if !s.operation.TryLock() {
		return QR{}, errors.New("请等待当前云盘操作结束")
	}
	defer s.operation.Unlock()
	s.mu.Lock()
	if s.activeLocked() {
		s.mu.Unlock()
		return QR{}, errors.New("请先取消或等待上传结束")
	}
	s.qr = nil
	s.generation++
	g := s.generation
	s.mu.Unlock()
	c := s.newLoginClient()
	r, e := c.call(ctx, true, http.MethodGet, "/api/user/qr-code/generate", nil, nil)
	if e != nil {
		return QR{}, e
	}
	var d struct {
		URL   string `json:"url"`
		UniID string `json:"uniID"`
	}
	if json.Unmarshal(r.Data, &d) != nil || d.URL == "" || len(d.URL) > 4096 || d.UniID == "" || len(d.UniID) > 256 {
		return QR{}, errors.New("二维码响应不完整，请刷新")
	}
	u, e := url.Parse(d.URL)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return QR{}, errors.New("二维码内容无法识别")
	}
	allowed := false
	for _, host := range []string{"123pan.cn", "123pan.com"} {
		if u.Hostname() == host || strings.HasSuffix(u.Hostname(), "."+host) {
			allowed = true
		}
	}
	if !allowed {
		return QR{}, errors.New("二维码服务地址未识别")
	}
	payload, e := qrPayload(u, d.UniID)
	if e != nil {
		return QR{}, e
	}
	b, e := qrcode.Encode(payload, qrcode.Medium, 232)
	if e != nil {
		return QR{}, errors.New("无法生成二维码图像")
	}
	q := QR{ID: randomID(), ExpiresAt: time.Now().Add(3 * time.Minute)}
	q.Image = "/cloud-qr/" + q.ID
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != g {
		return QR{}, errors.New("登录方式已切换，请重新获取二维码")
	}
	s.qr = &qrSession{public: q, uniID: d.UniID, client: c, generation: g, image: b}
	return q, nil
}
func qrPayload(base *url.URL, uniID string) (string, error) {
	if uniID == "" || len(uniID) > 256 {
		return "", errors.New("二维码登录会话无效")
	}
	u := *base
	q := u.Query()
	q.Set("env", "production")
	q.Set("uniID", uniID)
	q.Set("source", "123pan")
	q.Set("type", "login")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
func (s *Service) CancelQR() { s.mu.Lock(); s.qr = nil; s.generation++; s.mu.Unlock() }
func (s *Service) PollQR(ctx context.Context, id string) (QRResult, error) {
	if !s.authGate.TryLock() {
		return QRResult{State: "waiting", Status: s.Status()}, nil
	}
	defer s.authGate.Unlock()
	s.mu.Lock()
	q := s.qr
	if q == nil || q.public.ID != id {
		s.mu.Unlock()
		return QRResult{State: "expired", Status: s.Status()}, nil
	}
	if time.Now().After(q.public.ExpiresAt) {
		s.qr = nil
		s.mu.Unlock()
		return QRResult{State: "expired", Status: s.Status()}, nil
	}
	if time.Since(q.lastPoll) < time.Second {
		s.mu.Unlock()
		return QRResult{State: "waiting", Status: s.Status()}, nil
	}
	q.lastPoll = time.Now()
	s.mu.Unlock()
	r, e := q.client.call(ctx, true, http.MethodGet, "/api/user/qr-code/result", url.Values{"uniID": {q.uniID}, "remember": {"true"}, "gray": {"true"}}, nil)
	if e != nil {
		return QRResult{}, e
	}
	var d struct {
		State     int    `json:"loginStatus"`
		Platform  int    `json:"scanPlatform"`
		LoginType int    `json:"login_type"`
		Token     string `json:"token"`
	}
	if json.Unmarshal(r.Data, &d) != nil {
		return QRResult{}, errors.New("扫码状态响应无法识别")
	}
	if r.Code == 200 {
		d.State = 3
		if d.Platform == 0 {
			d.Platform = d.LoginType
		}
	}
	s.mu.RLock()
	valid := s.qr == q && s.generation == q.generation
	s.mu.RUnlock()
	if !valid {
		return QRResult{State: "expired", Status: s.Status()}, nil
	}
	state := "waiting"
	switch d.State {
	case 1:
		state = "scanned"
	case 2:
		state = "rejected"
	case 4:
		state = "expired"
	case 3:
		token := d.Token
		if d.Platform == 4 || d.LoginType == 4 {
			wx, e := q.client.call(ctx, true, http.MethodPost, "/api/user/qr-code/wx_code", nil, map[string]any{"uniID": q.uniID})
			if e != nil {
				return QRResult{}, e
			}
			var w struct {
				Code string `json:"wxCode"`
			}
			_ = json.Unmarshal(wx.Data, &w)
			if w.Code == "" {
				return QRResult{}, errors.New("微信扫码尚未完成，请刷新二维码")
			}
			token, e = q.client.signIn(ctx, map[string]any{"from": "web", "wechat_code": w.Code, "type": 4, "remember": true})
			if e != nil {
				return QRResult{}, e
			}
		}
		if token == "" {
			return QRResult{}, errors.New("扫码结果缺少登录凭据，请重新扫码")
		}
		if e = s.accept(ctx, q.client, token, q.generation); e != nil {
			return QRResult{}, e
		}
		state = "logged_in"
	}
	return QRResult{State: state, Status: s.Status()}, nil
}
func (s *Service) PasswordLogin(ctx context.Context, account, password string) error {
	if !s.authGate.TryLock() {
		return errors.New("正在处理登录，请稍候")
	}
	defer s.authGate.Unlock()
	if !s.operation.TryLock() {
		return errors.New("请等待当前云盘操作结束")
	}
	defer s.operation.Unlock()
	s.mu.Lock()
	if s.activeLocked() {
		s.mu.Unlock()
		return errors.New("请先等待上传结束")
	}
	s.generation++
	g := s.generation
	s.qr = nil
	s.mu.Unlock()
	c := s.newLoginClient()
	token, e := c.passwordLogin(ctx, account, password)
	if e != nil {
		return e
	}
	return s.accept(ctx, c, token, g)
}

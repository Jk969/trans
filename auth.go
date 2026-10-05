package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

/* 可选 PIN：设置后，除静态页/探活/登录外的所有接口都要带 cookie 令牌。
   WebDAV 额外支持 Basic Auth（用户名任意，密码=PIN），方便文档类 App 挂载。 */

const authCookie = "trans_token"

type pinAuth struct {
	mu    sync.Mutex
	pin   string
	token string
}

func newPinAuth(pin string) *pinAuth {
	pa := &pinAuth{pin: strings.TrimSpace(pin)}
	pa.token = newToken()
	return pa
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (pa *pinAuth) Enabled() bool { return pa != nil && pa.pin != "" }

// SetPin 设置/清除 PIN 后换 token（旧会话全部失效）
func (pa *pinAuth) SetPin(pin string) {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	pa.pin = strings.TrimSpace(pin)
	pa.token = newToken()
}

func (pa *pinAuth) Check(pin string) bool {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return pa.pin != "" && pin == pa.pin
}

func (pa *pinAuth) CurrentToken() string {
	pa.mu.Lock()
	defer pa.mu.Unlock()
	return pa.token
}

// checkRequest cookie 令牌或（仅 WebDAV 用）Basic 密码
func (pa *pinAuth) checkRequest(r *http.Request, allowBasic bool) bool {
	if !pa.Enabled() {
		return true
	}
	if c, err := r.Cookie(authCookie); err == nil && c.Value != "" && c.Value == pa.CurrentToken() {
		return true
	}
	if allowBasic {
		if u, p, ok := r.BasicAuth(); ok && pa.Check(p) && (u == "" || strings.EqualFold(u, "trans")) {
			return true
		}
	}
	return false
}

// POST /api/auth {pin} → 下发 30 天 cookie
func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct{ Pin string }
	if err := jsonDecode(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if !s.pin.Check(body.Pin) {
		jsonErr(w, http.StatusUnauthorized, "PIN 不正确")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: authCookie, Value: s.pin.CurrentToken(), Path: "/",
		MaxAge: 30 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	jsonOK(w, map[string]any{"ok": true})
}

// authGate 在 wrap 里调用；放行的路径：静态页/探活/登录/二维码
func (s *Server) authAllowedNoAuth(path string) bool {
	switch {
	case path == "/", path == "/index.html", path == "/app.js", path == "/style.css":
		return true
	case path == "/api/ping", path == "/api/auth", path == "/api/net", path == "/api/qr":
		return true
	}
	return false
}

var _ = time.Now

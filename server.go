package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	qrcode "github.com/skip2/go-qrcode"
)

//go:embed web
var webFS embed.FS

//go:embed assets/icon.ico
var iconBytes []byte

type Server struct {
	cfg    *Config
	stream *Stream
	paused atomic.Bool
	mux    *http.ServeMux
	ln     net.Listener
	up     websocket.Upgrader
	pin    *pinAuth
	clip   *ClipWatcher
}

func NewServer(cfg *Config) *Server {
	s := &Server{cfg: cfg, stream: NewStream(cfg.StageDir), mux: http.NewServeMux()}
	s.up = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	s.pin = newPinAuth(cfg.PIN)
	s.clip = NewClipWatcher(s)
	s.routes()
	return s
}

func (s *Server) Stream() *Stream { return s.stream }
func (s *Server) Clip() *ClipWatcher { return s.clip }

func (s *Server) Start() error {
	_ = os.MkdirAll(s.cfg.StageDir, 0o755)
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.Port))
	if err != nil {
		return err
	}
	s.ln = ln
	go func() {
		if err := http.Serve(ln, s.mux); err != nil {
			log.Println("HTTP 服务退出:", err)
		}
	}()
	return nil
}

func (s *Server) TogglePause() bool {
	p := !s.paused.Load()
	s.SetPaused(p)
	return p
}

func (s *Server) SetPaused(p bool) {
	if s.paused.Swap(p) == p {
		return
	}
	if p {
		s.stream.Hub().Broadcast(map[string]any{"type": "pause", "paused": true})
		s.stream.Hub().CloseAll()
	} else {
		s.stream.Hub().Broadcast(map[string]any{"type": "pause", "paused": false})
	}
	log.Println("服务暂停:", p)
}

func (s *Server) routes() {
	h := func(path string, fn http.HandlerFunc) { s.mux.HandleFunc(path, s.wrap(fn)) }
	h("/", s.serveStatic)
	h("/api/ping", s.handlePing)
	h("/api/net", s.handleNet)
	h("/api/settings", s.handleSettings)
	h("/api/qr", s.handleQR)
	h("/api/stream", s.handleStreamRoot)
	h("/api/stream/", s.handleStreamSub)
	h("/ws", s.handleWS)
	h("/f/", s.handleFileByID)
	h("/b/", s.handleBrowseFile)
	h("/api/fs/list", s.handleFsList)
	h("/api/fs/search", s.handleFsSearch)
	h("/api/favorites", s.handleFavorites)
	h("/api/upload", s.handleUpload)
	h("/api/space", s.handleSpace)
	h("/api/clipboard", s.handleClipboard)
	h("/api/cache/clear", s.handleCacheClear)
	h("/api/internal/stage", s.handleInternalStage)
	h("/api/internal/reveal", s.handleInternalReveal)
	h("/api/internal/clip-stage", s.handleClipStage)
	h("/api/clip/pause", s.handleClipPause)
	// v0.2 / v0.3
	h("/api/auth", s.handleAuth)
	h("/api/zip", s.handleZipCreate)
	h("/api/zip/", s.handleZipDownload)
	h("/api/thumb", s.handleThumb)
	h("/api/meta", s.handleMeta)
	h("/api/sub", s.handleSub)
	h("/api/media", s.handleMedia)
	h("/t/", s.handleTranscode)
	h("/api/upload/init", s.handleUploadInit)
	h("/api/upload/chunk", s.handleUploadChunk)
	h("/api/upload/complete", s.handleUploadComplete)
	h("/api/upload/status", s.handleUploadStatus)
	h("/dav/", s.handleDav)
}

func (s *Server) wrap(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 内部接口只接受本机回环（SendTo 转发 / 打开所在文件夹 / 复制文件确认上架）
		if strings.HasPrefix(r.URL.Path, "/api/internal") && !isLoopback(r) {
			jsonErr(w, http.StatusForbidden, "forbidden")
			return
		}
		// 暂停时只保留探活
		if s.paused.Load() && r.URL.Path != "/api/ping" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"paused"}`))
			return
		}
		// 可选 PIN：静态页/探活/登录放行，其余校验 cookie（WebDAV 有自己的 Basic 校验）
		if !strings.HasPrefix(r.URL.Path, "/dav") && !s.authAllowedNoAuth(r.URL.Path) {
			if !s.pin.checkRequest(r, false) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"pin"}`))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api") {
			log.Println(r.Method, r.URL.Path)
		}
		fn(w, r)
	}
}

func isLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

/* ---------- 基础端点 ---------- */

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	sub, _ := fs.Sub(webFS, "web")
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{"ok": true, "paused": s.paused.Load(), "version": version, "pinRequired": s.pin.Enabled()})
}

type netIf struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
	URL  string `json:"url"`
}

func (s *Server) handleNet(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, map[string]any{"port": s.cfg.Port, "interfaces": listAddrs(s.cfg.Port)})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			Autostart  *bool    `json:"autostart"`
			ShareRoots []string `json:"shareRoots"`
			ClipWatch  *bool    `json:"clipWatch"` // true=开启监听
			PIN        *string  `json:"pin"`       // ""=清除
		}
		if err := jsonDecode(r, &body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad body")
			return
		}
		if body.Autostart != nil {
			if err := setAutostart(*body.Autostart); err != nil {
				jsonErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			log.Println("开机自启:", *body.Autostart)
		}
		if body.ClipWatch != nil {
			s.cfg.ClipWatchOff = !*body.ClipWatch
			s.cfg.Save()
			if *body.ClipWatch {
				s.clip.Start()
			}
			log.Println("剪贴板监听:", *body.ClipWatch)
		}
		if body.PIN != nil {
			pin := strings.TrimSpace(*body.PIN)
			if len(pin) > 0 && len(pin) > 32 {
				jsonErr(w, http.StatusBadRequest, "PIN 太长")
				return
			}
			s.cfg.PIN = pin
			s.cfg.Save()
			s.pin.SetPin(pin) // 换 token，旧会话失效
			log.Println("访问 PIN 已更新")
		}
		if body.ShareRoots != nil {
			roots := []string{}
			for _, p := range body.ShareRoots {
				p = filepath.Clean(strings.Trim(strings.TrimSpace(p), "\"' "))
				if !filepath.IsAbs(p) {
					jsonErr(w, http.StatusBadRequest, "必须是绝对路径: "+p)
					return
				}
				if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
					jsonErr(w, http.StatusBadRequest, "目录不存在: "+p)
					return
				}
				roots = append(roots, p)
			}
			if len(roots) == 0 {
				jsonErr(w, http.StatusBadRequest, "至少保留一个共享位置")
				return
			}
			s.cfg.ShareRoots = roots
			s.cfg.Save()
			log.Println("共享位置更新:", roots)
		}
		jsonOK(w, map[string]any{"ok": true})
		return
	}
	_, ffprobe := ffmpegPath()
	jsonOK(w, map[string]any{
		"port":            s.cfg.Port,
		"shareRoots":      s.cfg.ShareRoots,
		"favorites":       s.favoritesView(),
		"stageDir":        s.cfg.StageDir,
		"cacheLimitGB":    s.cfg.CacheLimitGB,
		"cacheUsed":       s.stream.CacheUsed(),
		"autostart":       autostartEnabled(),
		"sendtoInstalled": sendtoInstalled(),
		"clipWatch":       !s.cfg.ClipWatchOff,
		"clipPausedUntil": s.clip.PausedUntil(),
		"clipPending":     s.clip.PendingCount(),
		"pinSet":          s.pin.Enabled(),
		"ffmpeg":          ffprobe != "",
		"version":         version,
	})
}

// handleFavorites POST {path: 虚拟路径, remove: bool} —— 收藏存绝对路径，根顺序变化也不失效
func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct {
		Path   string `json:"path"`
		Remove bool   `json:"remove"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	abs, _, _, err := s.resolveVirtual(body.Path)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	fi, err := os.Stat(abs)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "路径不存在")
		return
	}
	if body.Remove {
		keep := s.cfg.Favorites[:0]
		for _, f := range s.cfg.Favorites {
			if !strings.EqualFold(f, abs) {
				keep = append(keep, f)
			}
		}
		s.cfg.Favorites = keep
	} else {
		dup := false
		for _, f := range s.cfg.Favorites {
			if strings.EqualFold(f, abs) {
				dup = true
				break
			}
		}
		if !dup {
			s.cfg.Favorites = append(s.cfg.Favorites, abs)
		}
	}
	s.cfg.Save()
	jsonOK(w, map[string]any{"ok": true, "favorites": s.favoritesView()})
	_ = fi
}

// favoritesView 收藏清单 → 虚拟路径（根被移除时该条自动隐藏）
func (s *Server) favoritesView() []map[string]any {
	out := []map[string]any{}
	for _, abs := range s.cfg.Favorites {
		vp := s.virtualOf(abs)
		if vp == "" {
			continue
		}
		isDir := false
		if fi, err := os.Stat(abs); err == nil {
			isDir = fi.IsDir()
		}
		out = append(out, map[string]any{
			"path": vp,
			"abs":  abs,
			"name": filepath.Base(strings.TrimRight(abs, "\\/")),
			"dir":  isDir,
		})
	}
	return out
}

func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" || len(u) > 512 || (!strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://")) {
		jsonErr(w, http.StatusBadRequest, "bad url")
		return
	}
	size := 280
	if v, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil && v >= 120 && v <= 600 {
		size = v
	}
	png, err := qrcode.Encode(u, qrcode.Medium, size)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (s *Server) handleSpace(w http.ResponseWriter, r *http.Request) {
	_ = os.MkdirAll(s.cfg.StageDir, 0o755)
	jsonOK(w, map[string]any{"free": freeSpace(s.cfg.StageDir), "cacheUsed": s.stream.CacheUsed()})
}

func (s *Server) handleClipboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct{ Text string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if err := writeSystemClipboard(body.Text); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.clip.SuppressBy(body.Text) // 防回环：监听器跳过这条
	jsonOK(w, map[string]any{"ok": true})
}

func (s *Server) handleCacheClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	freed, removed := s.stream.ClearCopies()
	jsonOK(w, map[string]any{"ok": true, "freed": freed, "removed": removed})
}

func (s *Server) handleInternalStage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct{ Paths []string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	n := s.stream.StagePaths(body.Paths, "sendto")
	jsonOK(w, map[string]any{"ok": true, "added": n})
}

func (s *Server) handleInternalReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct{ ID string }
	if err := jsonDecode(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	e, ok := s.stream.Get(body.ID)
	if !ok || e.Kind != "file" {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	if err := revealInExplorer(e.Path); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"ok": true})
}

// handleClipStage PC 主页点「上架」：把监听到的复制文件入传输流
func (s *Server) handleClipStage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	paths := s.clip.TakePending()
	n := s.stream.StagePaths(paths, "sendto")
	jsonOK(w, map[string]any{"ok": true, "added": n})
}

// handleClipPause POST {ms: 0=恢复, >0=暂停毫秒数}
func (s *Server) handleClipPause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct{ MS int64 `json:"ms"` }
	if err := jsonDecode(r, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if body.MS > 0 {
		s.clip.PauseFor(time.Duration(body.MS) * time.Millisecond)
	} else {
		s.clip.PauseFor(-time.Hour) // 立即恢复
		s.stream.Hub().Broadcast(map[string]any{"type": "clip-pause", "until": 0})
	}
	jsonOK(w, map[string]any{"ok": true, "until": s.clip.PausedUntil()})
}

/* ---------- 传输流 API ---------- */

func (s *Server) handleStreamRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	jsonOK(w, map[string]any{"entries": s.stream.Snapshot()})
}

func (s *Server) handleStreamSub(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/text"):
		var body struct{ Text, Origin string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad body")
			return
		}
		body.Text = strings.TrimSpace(body.Text)
		if body.Text == "" {
			jsonErr(w, http.StatusBadRequest, "empty")
			return
		}
		if len(body.Text) > 1<<20 {
			jsonErr(w, http.StatusRequestEntityTooLarge, "too large")
			return
		}
		jsonOK(w, map[string]any{"entry": s.stream.AddText(body.Text, body.Origin)})

	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/ref"):
		var body struct{ Path string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonErr(w, http.StatusBadRequest, "bad body")
			return
		}
		abs, _, _, err := s.resolveVirtual(body.Path)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid path")
			return
		}
		if e := s.stream.AddFile(abs, "ref", "fs"); e != nil {
			jsonOK(w, map[string]any{"entry": e})
		} else {
			jsonErr(w, http.StatusNotFound, "file not found")
		}

	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/clear"):
		jsonOK(w, map[string]any{"ok": true, "removed": s.stream.ClearAll()})

	case r.Method == http.MethodDelete:
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/stream/"), "/")
		if id == "" || strings.Contains(id, "/") {
			jsonErr(w, http.StatusNotFound, "not found")
			return
		}
		if s.stream.Remove(id) {
			jsonOK(w, map[string]any{"ok": true})
		} else {
			jsonErr(w, http.StatusNotFound, "not found")
		}

	default:
		jsonErr(w, http.StatusNotFound, "not found")
	}
}

/* ---------- WebSocket ---------- */

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.paused.Load() {
		jsonErr(w, http.StatusServiceUnavailable, "paused")
		return
	}
	conn, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ch := s.stream.Hub().register()
	closed := make(chan struct{})
	// 写协程：广播下发；暂停时通道被关闭 → 顺手关连接
	go func() {
		defer conn.Close()
		for msg := range ch {
			_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if conn.WriteMessage(websocket.TextMessage, msg) != nil {
				return
			}
		}
	}()
	// 心跳
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-closed:
				return
			case <-t.C:
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	defer close(closed)
	defer s.stream.Hub().unregister(ch)
	conn.SetReadLimit(512)
	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

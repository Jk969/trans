package main

import (
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/webdav"
)

/* WebDAV：/dav/<根序号>/... 每个共享位置一个 webdav.Handler。
   文档类 App（如 iOS 文件、Documents）可直接挂载 http://电脑IP:端口/dav/0 */

func (s *Server) handleDav(w http.ResponseWriter, r *http.Request) {
	if !s.pin.checkRequest(r, true) { // DAV 额外允许 Basic Auth（密码=PIN）
		w.Header().Set("WWW-Authenticate", `Basic realm="Trans"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/dav")
	rest = strings.Trim(rest, "/")
	idxStr, _, _ := strings.Cut(rest, "/")
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(s.cfg.ShareRoots) {
		http.Error(w, "用法：/dav/<共享位置序号>/，序号见主页共享位置列表（从 0 开始）", http.StatusBadRequest)
		return
	}
	h := &webdav.Handler{
		FileSystem: webdav.Dir(s.cfg.ShareRoots[idx]),
		LockSystem: webdav.NewMemLS(),
	}
	prefix := "/dav/" + idxStr
	r.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, prefix)
	r.Header.Set("X-Forwarded-Prefix", prefix)
	h.ServeHTTP(w, r)
}

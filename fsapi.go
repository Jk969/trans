package main

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var errForbidden = errors.New("path outside root")

// safeJoin 把相对路径限制在单个根目录内（防 ../ 穿越、防盘符注入）
func safeJoin(root, rel string) (string, string, error) {
	rel = strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "/")
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." {
		rel = ""
	}
	// Windows 文件名本就不允许 ':'，出现在这里必是盘符注入（如 D:/x 被拼进 Join）
	if rel != "" && (filepath.IsAbs(filepath.FromSlash(rel)) || strings.Contains(rel, ":")) {
		return "", "", errForbidden
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	p := filepath.Join(rootAbs, filepath.FromSlash(rel))
	r, err := filepath.Rel(rootAbs, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", "", errForbidden
	}
	if rel != "" && (r == "." || r == "") {
		return "", "", errForbidden
	}
	return p, filepath.ToSlash(r), nil
}

// resolveVirtual 虚拟路径 "/<根序号>/<相对路径>" → 绝对路径。
// 文件浏览、上传目标、收藏、/b/ 文件服务统一走这套寻址，手机端永远见不到真实路径。
func (s *Server) resolveVirtual(p string) (abs string, idx int, rel string, err error) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	seg, rest, _ := strings.Cut(p, "/")
	i, e := strconv.Atoi(seg)
	if e != nil || i < 0 || i >= len(s.cfg.ShareRoots) {
		return "", 0, "", errForbidden
	}
	root := s.cfg.ShareRoots[i]
	if rest == "" {
		a, e := filepath.Abs(root)
		if e != nil {
			return "", 0, "", e
		}
		return a, i, "", nil
	}
	abs, rel, err = safeJoin(root, rest)
	if err != nil {
		return "", 0, "", err
	}
	return abs, i, rel, nil
}

// virtualOf 绝对路径 → 虚拟路径；不在任何共享位置内返回 ""
func (s *Server) virtualOf(abs string) string {
	a, err := filepath.Abs(abs)
	if err != nil {
		return ""
	}
	for i, r := range s.cfg.ShareRoots {
		ra, e := filepath.Abs(r)
		if e != nil {
			continue
		}
		if strings.EqualFold(a, ra) {
			return "/" + strconv.Itoa(i)
		}
		if rel, e := filepath.Rel(ra, a); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			vp := "/" + strconv.Itoa(i)
			if rel != "." {
				vp += "/" + filepath.ToSlash(rel)
			}
			return vp
		}
	}
	return ""
}

func mimeOf(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv":
		return "video/x-matroska"
	case ".flac":
		return "audio/flac"
	}
	return "application/octet-stream"
}

func (s *Server) isStageDir(p string) bool {
	stageAbs, err := filepath.Abs(s.cfg.StageDir)
	if err != nil {
		return false
	}
	pAbs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	return strings.EqualFold(stageAbs, pAbs)
}

type fsItem struct {
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Path  string `json:"path"`          // 虚拟绝对路径，前端直接使用
	Sub   string `json:"sub,omitempty"` // 次要说明（共享位置显示完整路径）
}

// rootLabel 共享位置的显示名：盘根 → "C 盘"，其余取目录名
func rootLabel(root string) string {
	r := strings.TrimRight(root, "\\/")
	if len(r) == 2 && r[1] == ':' {
		return strings.ToUpper(r[:1]) + " 盘"
	}
	b := filepath.Base(filepath.Clean(r))
	if b == "" || b == "." || b == string(filepath.Separator) {
		return root
	}
	return b
}

func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	q := "/" + strings.Trim(r.URL.Query().Get("path"), "/")
	items := []fsItem{}

	if q == "/" {
		// 虚拟根：列出所有共享位置
		for i, root := range s.cfg.ShareRoots {
			items = append(items, fsItem{Name: rootLabel(root), Dir: true, Path: "/" + strconv.Itoa(i), Sub: root})
		}
		jsonOK(w, map[string]any{"path": "/", "entries": items})
		return
	}

	abs, _, _, err := s.resolveVirtual(q)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	prefix := strings.TrimSuffix(q, "/")
	for _, de := range des {
		if strings.HasPrefix(de.Name(), ".") {
			continue
		}
		if s.isStageDir(filepath.Join(abs, de.Name())) {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		items = append(items, fsItem{
			Name: de.Name(), Dir: de.IsDir(), Size: fi.Size(), Mtime: fi.ModTime().UnixMilli(),
			Path: prefix + "/" + de.Name(),
		})
	}
	sort.Slice(items, func(a, b int) bool {
		if items[a].Dir != items[b].Dir {
			return items[a].Dir
		}
		return strings.ToLower(items[a].Name) < strings.ToLower(items[b].Name)
	})
	jsonOK(w, map[string]any{"path": prefix, "entries": items})
}

var errStopWalk = errors.New("stop")

type searchHit struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

// handleFsSearch 跨所有共享位置按文件名搜索
func (s *Server) handleFsSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := []searchHit{}
	if q == "" {
		jsonOK(w, map[string]any{"results": out, "truncated": false})
		return
	}
	const maxResults = 200
	deadline := time.Now().Add(5 * time.Second)
	stopped := false
	for i, root := range s.cfg.ShareRoots {
		if stopped {
			break
		}
		rootAbs, _ := filepath.Abs(root)
		_ = filepath.WalkDir(rootAbs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // 无权限的目录直接跳过
			}
			if len(out) >= maxResults || time.Now().After(deadline) {
				stopped = true
				return errStopWalk
			}
			if d.IsDir() {
				name := d.Name()
				// 跳过隐藏目录、依赖缓存和暂存目录，减少噪音、加速遍历
				if strings.HasPrefix(name, ".") || name == "node_modules" {
					return fs.SkipDir
				}
				if s.isStageDir(p) {
					return fs.SkipDir
				}
				return nil
			}
			if strings.Contains(strings.ToLower(d.Name()), q) {
				if fi, e := d.Info(); e == nil {
					rel, e := filepath.Rel(rootAbs, p)
					if e == nil {
						out = append(out, searchHit{"/" + strconv.Itoa(i) + "/" + filepath.ToSlash(rel), d.Name(), fi.Size(), fi.ModTime().UnixMilli()})
					}
				}
			}
			return nil
		})
	}
	jsonOK(w, map[string]any{"results": out, "truncated": stopped})
}

// /b/<虚拟路径> 文件浏览原始文件（Range 由 ServeFile 处理）
func (s *Server) handleBrowseFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/b/")
	abs, _, _, err := s.resolveVirtual(rel)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("dl") == "1" {
		setAttachment(w, filepath.Base(abs))
	}
	http.ServeFile(w, r, abs)
}

// /f/<id>/<name> 传输流条目文件
func (s *Server) handleFileByID(w http.ResponseWriter, r *http.Request) {
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/f/"), "/")
	e, ok := s.stream.Get(id)
	if !ok || e.Kind != "file" {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	if _, err := os.Stat(e.Path); err != nil {
		jsonErr(w, http.StatusGone, "missing") // 原文件已不在
		return
	}
	if r.URL.Query().Get("dl") == "1" {
		setAttachment(w, e.Name)
	}
	http.ServeFile(w, r, e.Path)
}

func setAttachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(name))
}

func stringsEqualFold(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b)
}

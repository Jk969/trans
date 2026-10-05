package main

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// GET /api/sub?vpath=/0/a/b.mp4 或 ?id=<条目id>
// 查找同目录同主名的 .vtt / .srt 字幕，srt 转 vtt 后下发，供 <track> 使用
func (s *Server) handleSub(w http.ResponseWriter, r *http.Request) {
	var abs string
	if id := r.URL.Query().Get("id"); id != "" {
		e, ok := s.stream.Get(id)
		if !ok || e.Kind != "file" {
			jsonErr(w, http.StatusNotFound, "not found")
			return
		}
		abs = e.Path
	} else {
		a, _, _, err := s.resolveVirtual(r.URL.Query().Get("vpath"))
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid path")
			return
		}
		abs = a
	}
	dir := filepath.Dir(abs)
	stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	for _, cand := range []string{stem + ".vtt", stem + ".srt", stem + ".SRT", stem + ".VTT"} {
		b, err := os.ReadFile(filepath.Join(dir, cand))
		if err != nil {
			continue
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		if strings.EqualFold(filepath.Ext(cand), ".vtt") {
			w.Write(b)
			return
		}
		w.Write(srtToVtt(b))
		return
	}
	jsonErr(w, http.StatusNotFound, "no subtitle")
}

// srtToVtt 基本转换：块解析 + 时间戳逗号改点
func srtToVtt(b []byte) []byte {
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	text := strings.ReplaceAll(string(b), "\r\n", "\n")
	blocks := strings.Split(strings.TrimSpace(text), "\n\n")
	for _, blk := range blocks {
		lines := strings.Split(blk, "\n")
		if len(lines) < 2 {
			continue
		}
		// 跳过序号行（不含 --> 的第一行）
		i := 0
		if !strings.Contains(lines[0], "-->") {
			i = 1
		}
		if i >= len(lines) || !strings.Contains(lines[i], "-->") {
			continue
		}
		timing := strings.ReplaceAll(lines[i], ",", ".")
		fmt.Fprintf(&out, "%s\n", timing)
		for _, l := range lines[i+1:] {
			out.WriteString(l)
			out.WriteString("\n")
		}
		out.WriteString("\n")
	}
	return []byte(out.String())
}

/* ---------- 媒体扫描（相册 Tab） ---------- */

type mediaItem struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
	Kind  string `json:"kind"` // image | video
}

type mediaGroup struct {
	Dir   string      `json:"dir"`
	Count int         `json:"count"`
	Items []mediaItem `json:"items"`
}

func mediaKind(name string) string {
	e := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	for _, x := range []string{"jpg", "jpeg", "png", "gif", "webp", "bmp", "avif"} {
		if e == x {
			return "image"
		}
	}
	for _, x := range []string{"mp4", "webm", "m4v", "mov", "mkv", "avi"} {
		if e == x {
			return "video"
		}
	}
	return ""
}

// GET /api/media → 跨所有共享位置扫描图片+视频，按目录分组（限额 1500 个 / 8 秒）
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	const maxItems = 1500
	const perGroup = 200
	deadline := time.Now().Add(8 * time.Second)
	groups := map[string]*mediaGroup{}
	total := 0
	latest := map[string]int64{}
	stopped := false

	for i, root := range s.cfg.ShareRoots {
		if stopped {
			break
		}
		rootAbs, _ := filepath.Abs(root)
		prefix := "/" + strconv.Itoa(i)
		_ = filepath.WalkDir(rootAbs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if total >= maxItems || time.Now().After(deadline) {
				stopped = true
				return errStopWalk
			}
			if d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" {
					return fs.SkipDir
				}
				// 跳过系统/缓存目录：相册只收用户自己的照片视频，不被 AppData 里的图标淹没
				if isNoiseDir(name) {
					return fs.SkipDir
				}
				if s.isStageDir(p) || s.isThumbDir(p) {
					return fs.SkipDir
				}
				return nil
			}
			k := mediaKind(d.Name())
			if k == "" {
				return nil
			}
			fi, e := d.Info()
			if e != nil {
				return nil
			}
			rel, e := filepath.Rel(rootAbs, p)
			if e != nil || rel == "." {
				return nil
			}
			vdir := prefix + "/" + filepath.ToSlash(filepath.Dir(rel))
			g := groups[vdir]
			if g == nil {
				g = &mediaGroup{Dir: vdir}
				groups[vdir] = g
			}
			if len(g.Items) < perGroup {
				g.Items = append(g.Items, mediaItem{
					Path: prefix + "/" + filepath.ToSlash(rel), Name: d.Name(),
					Size: fi.Size(), Mtime: fi.ModTime().UnixMilli(), Kind: k,
				})
			}
			g.Count++
			total++
			if fi.ModTime().UnixMilli() > latest[vdir] {
				latest[vdir] = fi.ModTime().UnixMilli()
			}
			return nil
		})
	}

	out := []mediaGroup{}
	for dir, g := range groups {
		if g.Count == 0 {
			continue
		}
		items := g.Items
		sort.SliceStable(items, func(a, b int) bool { return items[a].Mtime > items[b].Mtime })
		out = append(out, mediaGroup{Dir: dir, Count: g.Count, Items: items})
	}
	sort.SliceStable(out, func(a, b int) bool { return latest[out[a].Dir] > latest[out[b].Dir] })
	jsonOK(w, map[string]any{"groups": out, "truncated": stopped})
}

func (s *Server) isThumbDir(p string) bool {
	return stringsEqualFold(filepath.Clean(p), filepath.Clean(thumbDir()))
}

// isNoiseDir 相册扫描要跳过的系统/缓存目录名
func isNoiseDir(name string) bool {
	switch strings.ToLower(name) {
	case "appdata", "windows", "program files", "program files (x86)", "programdata",
		"$recycle.bin", "system volume information", "node_modules", "winxsx":
		return true
	}
	return false
}

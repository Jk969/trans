package main

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	_ "image/gif"
	_ "image/png"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var thumbDir = func() string { return filepath.Join(configDir(), "thumb_cache") }

var thumbMu sync.Mutex // 串行生成即可，个人低并发

// GET /api/thumb?vpath=/0/a.jpg 或 ?id=<条目id> &w=宽度
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
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
	width := 256
	if v := r.URL.Query().Get("w"); v == "160" || v == "120" {
		width, _ = strconv.Atoi(v)
	}

	fi, err := os.Stat(abs)
	if err != nil || fi.IsDir() || fi.Size() > 80<<20 {
		sendThumbPlaceholder(w)
		return
	}
	key := hashKey(abs + "|" + fi.ModTime().String() + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.Itoa(width))
	cache := filepath.Join(thumbDir(), key+".jpg")

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")

	if b, err := os.ReadFile(cache); err == nil {
		w.Write(b)
		return
	}
	thumbMu.Lock()
	defer thumbMu.Unlock()
	if b, err := os.ReadFile(cache); err == nil { // 双检
		w.Write(b)
		return
	}
	img, err := decodeImage(abs)
	if err != nil {
		sendThumbPlaceholder(w)
		return
	}
	small := scaleDown(img, width)
	var buf bytes.Buffer
	if jpeg.Encode(&buf, small, &jpeg.Options{Quality: 78}) != nil {
		sendThumbPlaceholder(w)
		return
	}
	_ = os.MkdirAll(thumbDir(), 0o755)
	_ = os.WriteFile(cache, buf.Bytes(), 0o644)
	w.Write(buf.Bytes())
}

func decodeImage(p string) (image.Image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// scaleDown 简单盒采样缩小（个人用，够清晰），不放大小图
func scaleDown(img image.Image, maxW int) image.Image {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sw <= maxW {
		return img
	}
	dh := sh * maxW / sw
	if dh < 1 {
		dh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, maxW, dh))
	xRatio := float64(sw) / float64(maxW)
	yRatio := float64(sh) / float64(dh)
	for y := 0; y < dh; y++ {
		sy0 := int(float64(y) * yRatio)
		sy1 := int(float64(y+1) * yRatio)
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < maxW; x++ {
			sx0 := int(float64(x) * xRatio)
			sx1 := int(float64(x+1) * xRatio)
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			var rs, gs, bs, n uint32
			for sy := sy0; sy < sy1 && sy < sh; sy++ {
				for sx := sx0; sx < sx1 && sx < sw; sx++ {
					cr, cg, cb, _ := img.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					rs += cr >> 8
					gs += cg >> 8
					bs += cb >> 8
					n++
				}
			}
			if n == 0 {
				n = 1
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i] = uint8(rs / n)
			dst.Pix[i+1] = uint8(gs / n)
			dst.Pix[i+2] = uint8(bs / n)
			dst.Pix[i+3] = 255
		}
	}
	return dst
}

// 1x1 像素占位，前端用图标兜底
var thumbPlaceholder = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0}

func sendThumbPlaceholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/jpeg")
	w.Write(thumbPlaceholder)
}

/* ---------- 视频时长（ffprobe，缺失则前端 preload=metadata 兜底） ---------- */

var (
	durMu    sync.Mutex
	durCache = map[string]float64{}
)

// GET /api/meta?vpath=... 或 ?id=... → {duration: 秒|null}
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
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
	fi, err := os.Stat(abs)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "not found")
		return
	}
	key := abs + "|" + fi.ModTime().String()
	durMu.Lock()
	d, hit := durCache[key]
	durMu.Unlock()
	if !hit {
		d = probeDuration(abs)
		durMu.Lock()
		if len(durCache) > 500 { // 防膨胀
			durCache = map[string]float64{}
		}
		durCache[key] = d
		durMu.Unlock()
	}
	if d <= 0 {
		jsonOK(w, map[string]any{"duration": nil})
		return
	}
	jsonOK(w, map[string]any{"duration": d})
}

func probeDuration(file string) float64 {
	_, ffprobe := ffmpegPath()
	if ffprobe == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", file).Output()
	if err != nil {
		return 0
	}
	var d float64
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &d); err != nil {
		return 0
	}
	return d
}

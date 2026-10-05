package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// /t/id/<条目id> 和 /t/v/<虚拟路径> —— ffmpeg 实时转封装（-c:v copy + aac），mkv 等
// 浏览器不认的容器有机会直接播。实验功能：需要系统装有 ffmpeg；HEVC 源仍可能播不了。
func (s *Server) handleTranscode(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/t/")
	kind, arg, _ := strings.Cut(rest, "/")
	if arg == "" {
		jsonErr(w, http.StatusBadRequest, "bad path")
		return
	}
	var abs string
	switch kind {
	case "id":
		e, ok := s.stream.Get(arg)
		if !ok || e.Kind != "file" {
			jsonErr(w, http.StatusNotFound, "not found")
			return
		}
		abs = e.Path
	case "v":
		a, _, _, err := s.resolveVirtual(arg)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid path")
			return
		}
		abs = a
	default:
		jsonErr(w, http.StatusBadRequest, "bad path")
		return
	}
	if _, err := os.Stat(abs); err != nil {
		jsonErr(w, http.StatusGone, "missing")
		return
	}
	ffmpeg, _ := ffmpegPath()
	if ffmpeg == "" {
		jsonErr(w, http.StatusServiceUnavailable, "电脑上未安装 ffmpeg（加入 PATH 后重启 Trans）")
		return
	}

	// 客户端断开（拖动/换片）就杀进程
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// 起播定位（实验：仅支持从头播）
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-nostdin", "-loglevel", "error",
		"-i", abs,
		"-c:v", "copy", "-c:a", "aac", "-b:a", "192k",
		"-movflags", "frag_keyframe+empty_moov",
		"-f", "mp4", "pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		jsonErr(w, http.StatusInternalServerError, "ffmpeg 启动失败: "+err.Error())
		return
	}
	go func() {
		_ = cmd.Wait()
	}()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = r.Context()
	_, _ = io.Copy(w, stdout)
}

var _ = strconv.Itoa
var _ = time.Second

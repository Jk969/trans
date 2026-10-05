package main

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var errConflict = errors.New("conflict")

// POST /api/upload  multipart：dest=stream|dir, dir=目标目录(相对), resolve=rename|overwrite|skip, origin, file
// 单文件一次请求（进度/重试/冲突都按文件粒度处理）
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "not multipart")
		return
	}

	var dest, dir, resolve, origin string
	var dirIdx int
	var dirRel string
	var filePart *multipart.Part
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if part.FileName() == "" {
			val, _ := io.ReadAll(io.LimitReader(part, 4<<10))
			switch part.FormName() {
			case "dest":
				dest = string(val)
			case "dir":
				dir = string(val)
			case "resolve":
				resolve = string(val)
			case "origin":
				origin = string(val)
			}
			continue
		}
		filePart = part
		break
	}
	if filePart == nil {
		jsonErr(w, http.StatusBadRequest, "no file")
		return
	}
	defer filePart.Close()
	name := sanitizeName(filePart.FileName())

	var base string
	if dest == "dir" {
		abs, idx, rel, err := s.resolveVirtual("/" + strings.Trim(dir, "/"))
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid dir")
			return
		}
		base, dirIdx, dirRel = abs, idx, rel
	} else {
		dest = "stream"
		base = filepath.Join(s.cfg.StageDir, time.Now().Format("2006-01-02"))
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 磁盘空间预检（有 Content-Length 时精确比对）
	if cl := r.ContentLength; cl > 0 {
		if free := freeSpace(base); free < uint64(cl)+50<<20 {
			w.WriteHeader(http.StatusInsufficientStorage)
			jsonOK(w, map[string]any{"error": "nospace", "free": free})
			return
		}
	}

	// 传输流副本一律自动改名；指定目录按 resolve 处理
	saveResolve := "rename"
	if dest == "dir" {
		saveResolve = resolve
	}
	saved, skipped, err := saveFile(base, name, filePart, saveResolve)
	if errors.Is(err, errConflict) {
		w.WriteHeader(http.StatusConflict)
		jsonOK(w, map[string]any{"error": "conflict", "existing": name})
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if skipped {
		jsonOK(w, map[string]any{"skipped": name})
		return
	}

	if dest == "dir" {
		vp := "/" + strconv.Itoa(dirIdx)
		if dirRel != "" {
			vp += "/" + dirRel
		}
		jsonOK(w, map[string]any{"saved": saved, "path": vp + "/" + saved})
		return
	}
	if origin == "" {
		origin = "phone"
	}
	e := s.stream.AddFile(filepath.Join(base, saved), "copy", origin)
	if e == nil {
		jsonErr(w, http.StatusInternalServerError, "save failed")
		return
	}
	jsonOK(w, map[string]any{"entry": e})
}

// saveFile 落盘；同名按 resolve 处理，返回最终文件名
func saveFile(dir, name string, src io.Reader, resolve string) (string, bool, error) {
	target := filepath.Join(dir, name)
	if fileExists(target) {
		switch resolve {
		case "skip":
			return name, true, nil
		case "overwrite":
			_ = os.Remove(target)
		case "rename":
			name = nextName(dir, name)
			target = filepath.Join(dir, name)
		default:
			return "", false, errConflict
		}
	}
	tmp := target + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", false, err
	}
	_, err = io.Copy(f, src)
	f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", false, err
	}
	return name, false, nil
}

func nextName(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 500; i++ {
		cand := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if !fileExists(filepath.Join(dir, cand)) {
			return cand
		}
	}
	return fmt.Sprintf("%s-%d%s", stem, time.Now().UnixMilli(), ext)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

var nameReplacer = strings.NewReplacer(
	"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
)

func sanitizeName(n string) string {
	n = nameReplacer.Replace(filepath.Base(strings.TrimSpace(n)))
	n = strings.TrimRight(n, ". ")
	if n == "" || n == "." || n == ".." {
		n = "file"
	}
	if len(n) > 180 {
		ext := filepath.Ext(n)
		if len(ext) > 20 {
			ext = ""
		}
		n = n[:180-len(ext)] + ext
	}
	return n
}

/* ================= 分片上传 + 断点续传 ================= */

type chunkUpload struct {
	mu       sync.Mutex
	partPath string
	name     string
	size     int64
	dest     string // stream | dir
	dirIdx   int
	dirRel   string
	created  time.Time
}

var (
	chunkMu    sync.Mutex
	chunkUploads = map[string]*chunkUpload{}
)

func chunkTmpDir() string { return filepath.Join(configDir(), "upload_tmp") }

func chunkCleanup() {
	chunkMu.Lock()
	defer chunkMu.Unlock()
	for id, u := range chunkUploads {
		if time.Since(u.created) > 24*time.Hour {
			_ = os.Remove(u.partPath)
			delete(chunkUploads, id)
		}
	}
}

// POST /api/upload/init {name,size,dest,dir} → {id, received}
// 同名同大小的续传会拿到同一个 id（part 文件还在就能接着传）
func (s *Server) handleUploadInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		Dest string `json:"dest"`
		Dir  string `json:"dir"`
	}
	if err := jsonDecode(r, &body); err != nil || body.Name == "" || body.Size <= 0 {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	name := sanitizeName(body.Name)
	dest := body.Dest
	var base string
	var dirIdx int
	var dirRel string
	if dest == "dir" {
		abs, idx, rel, err := s.resolveVirtual("/" + strings.Trim(body.Dir, "/"))
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid dir")
			return
		}
		base, dirIdx, dirRel = abs, idx, rel
	} else {
		dest = "stream"
		base = filepath.Join(s.cfg.StageDir, time.Now().Format("2006-01-02"))
	}
	if free := freeSpace(base); free < uint64(body.Size)+50<<20 {
		w.WriteHeader(http.StatusInsufficientStorage)
		jsonOK(w, map[string]any{"error": "nospace", "free": free})
		return
	}
	_ = os.MkdirAll(chunkTmpDir(), 0o755)
	id := hashKey(name + "|" + strconv.FormatInt(body.Size, 10) + "|" + dest + "|" + body.Dir)
	partPath := filepath.Join(chunkTmpDir(), id+".part")

	chunkMu.Lock()
	chunkUploads[id] = &chunkUpload{
		partPath: partPath, name: name, size: body.Size,
		dest: dest, dirIdx: dirIdx, dirRel: dirRel, created: time.Now(),
	}
	chunkMu.Unlock()

	var received int64
	if fi, err := os.Stat(partPath); err == nil && fi.Size() <= body.Size {
		received = fi.Size()
	} else {
		_ = os.Remove(partPath) // 损坏的 part（超出声明大小）重来
	}
	jsonOK(w, map[string]any{"id": id, "received": received})
}

// PUT /api/upload/chunk?id=&offset=  body 为裸字节
func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	id := r.URL.Query().Get("id")
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	chunkMu.Lock()
	u := chunkUploads[id]
	chunkMu.Unlock()
	if u == nil {
		jsonErr(w, http.StatusNotFound, "upload not found（过期？重新开始）")
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	fi, err := os.Stat(u.partPath)
	var cur int64
	if err == nil {
		cur = fi.Size()
	}
	if cur != offset { // 与服务端已有字节不一致 → 返回实际进度让客户端对齐
		w.WriteHeader(http.StatusConflict)
		jsonOK(w, map[string]any{"error": "offset", "received": cur})
		return
	}
	f, err := os.OpenFile(u.partPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := io.Copy(f, r.Body)
	f.Close()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"received": cur + n})
}

// GET /api/upload/status?id=
func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	chunkMu.Lock()
	u := chunkUploads[id]
	chunkMu.Unlock()
	if u == nil {
		jsonErr(w, http.StatusNotFound, "upload not found")
		return
	}
	var cur int64
	if fi, err := os.Stat(u.partPath); err == nil {
		cur = fi.Size()
	}
	jsonOK(w, map[string]any{"received": cur, "size": u.size})
}

// POST /api/upload/complete?id=&resolve=
func (s *Server) handleUploadComplete(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	resolve := r.URL.Query().Get("resolve")
	origin := r.URL.Query().Get("origin")
	chunkMu.Lock()
	u := chunkUploads[id]
	delete(chunkUploads, id)
	chunkMu.Unlock()
	if u == nil {
		jsonErr(w, http.StatusNotFound, "upload not found")
		return
	}
	fi, err := os.Stat(u.partPath)
	if err != nil || fi.Size() != u.size {
		_ = os.Remove(u.partPath)
		jsonErr(w, http.StatusBadRequest, "分片不完整，请重试")
		return
	}
	var base string
	if u.dest == "dir" {
		if abs, _, _, err := s.resolveVirtual("/" + strconv.Itoa(u.dirIdx) + "/" + u.dirRel); err == nil {
			base = abs
		} else {
			_ = os.Remove(u.partPath)
			jsonErr(w, http.StatusBadRequest, "invalid dir")
			return
		}
	} else {
		base = filepath.Join(s.cfg.StageDir, time.Now().Format("2006-01-02"))
	}
	_ = os.MkdirAll(base, 0o755)

	saveResolve := "rename"
	if u.dest == "dir" {
		saveResolve = resolve
	}
	saved, skipped, err := moveFile(base, u.name, u.partPath, saveResolve)
	if errors.Is(err, errConflict) {
		// part 保留 10 分钟供改选后重试：重新登记
		chunkMu.Lock()
		chunkUploads[id] = u
		chunkMu.Unlock()
		w.WriteHeader(http.StatusConflict)
		jsonOK(w, map[string]any{"error": "conflict", "existing": u.name})
		return
	}
	if err != nil {
		_ = os.Remove(u.partPath)
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if skipped {
		_ = os.Remove(u.partPath)
		jsonOK(w, map[string]any{"skipped": u.name})
		return
	}
	if u.dest == "dir" {
		vp := "/" + strconv.Itoa(u.dirIdx)
		if u.dirRel != "" {
			vp += "/" + u.dirRel
		}
		jsonOK(w, map[string]any{"saved": saved, "path": vp + "/" + saved})
		return
	}
	if origin == "" {
		origin = "phone"
	}
	e := s.stream.AddFile(filepath.Join(base, saved), "copy", origin)
	if e == nil {
		jsonErr(w, http.StatusInternalServerError, "save failed")
		return
	}
	jsonOK(w, map[string]any{"entry": e})
}

// moveFile 把已收完的 part 移到目标位置（沿用同名冲突策略）
func moveFile(dir, name, partPath, resolve string) (string, bool, error) {
	target := filepath.Join(dir, name)
	if fileExists(target) {
		switch resolve {
		case "skip":
			return name, true, nil
		case "overwrite":
			_ = os.Remove(target)
		case "rename":
			name = nextName(dir, name)
			target = filepath.Join(dir, name)
		default:
			return "", false, errConflict
		}
	}
	if err := os.Rename(partPath, target); err != nil {
		return "", false, err
	}
	return name, false, nil
}

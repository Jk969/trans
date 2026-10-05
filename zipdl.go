package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// zip 打包下载：POST 建任务返回下载 URL，GET 流式下发（不落盘打包）
type zipJob struct {
	absList []string
	created time.Time
}

var (
	zipMu    sync.Mutex
	zipJobs  = map[string]*zipJob{}
	zipSeq   uint64
)

const (
	zipMaxFiles = 5000
	zipMaxBytes = 20 << 30
	zipJobTTL   = 10 * time.Minute
)

func (s *Server) handleZipCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method")
		return
	}
	var body struct{ Paths []string `json:"paths"` }
	if err := jsonDecode(r, &body); err != nil || len(body.Paths) == 0 {
		jsonErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if len(body.Paths) > 200 {
		jsonErr(w, http.StatusBadRequest, "太多路径")
		return
	}
	absList := []string{}
	for _, p := range body.Paths {
		abs, _, _, err := s.resolveVirtual(p)
		if err != nil {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			absList = append(absList, abs)
		}
	}
	if len(absList) == 0 {
		jsonErr(w, http.StatusBadRequest, "无有效路径")
		return
	}
	zipMu.Lock()
	defer zipMu.Unlock()
	// 清理过期任务
	for id, j := range zipJobs {
		if time.Since(j.created) > zipJobTTL {
			delete(zipJobs, id)
		}
	}
	zipSeq++
	id := hashKey(time.Now().String() + string(rune('a'+zipSeq%26)))
	zipJobs[id] = &zipJob{absList: absList, created: time.Now()}
	jsonOK(w, map[string]any{"url": "/api/zip/" + id + "/trans.zip", "count": len(absList)})
}

func (s *Server) handleZipDownload(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/zip/")
	id, _, _ := strings.Cut(rest, "/")
	zipMu.Lock()
	job := zipJobs[id]
	zipMu.Unlock()
	if job == nil {
		jsonErr(w, http.StatusNotFound, "打包任务不存在或已过期")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''trans.zip`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	zw := zip.NewWriter(w)
	defer zw.Close()

	var files, bytes int64
	addFile := func(abs, name string) error {
		if files >= zipMaxFiles || bytes >= zipMaxBytes {
			return nil
		}
		f, err := os.Open(abs)
		if err != nil {
			return nil
		}
		defer f.Close()
		hdr := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Store}
		if fi, err := f.Stat(); err == nil {
			hdr.Modified = fi.ModTime()
		}
		zf, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		n, err := io.Copy(zf, f)
		files++
		bytes += n
		return err
	}

	for _, abs := range job.absList {
		fi, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			_ = addFile(abs, baseName(abs))
			continue
		}
		rootName := baseName(abs)
		_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			rel, e := filepath.Rel(abs, p)
			if e != nil {
				return nil
			}
			return addFile(p, filepath.Join(rootName, rel))
		})
	}
}

func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

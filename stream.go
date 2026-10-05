package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry 传输流条目。Path 只存在于服务端，绝不下发给浏览器（json:"-"）。
type Entry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // text | file
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`
	Size    int64  `json:"size"`
	MIME    string `json:"mime,omitempty"`
	Storage string `json:"storage,omitempty"` // ref 路径引用 | copy 暂存副本
	Origin  string `json:"origin,omitempty"`  // pc-drag | phone | sendto | pc | fs
	Created int64  `json:"created"`
	Missing bool   `json:"missing,omitempty"` // 引用失效：原文件不在了
	Path    string `json:"-"`
}

// persistEntry stream.json 落盘结构（含 Path）
type persistEntry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`
	Size    int64  `json:"size"`
	MIME    string `json:"mime,omitempty"`
	Storage string `json:"storage,omitempty"`
	Origin  string `json:"origin,omitempty"`
	Created int64  `json:"created"`
	Path    string `json:"path"`
}

// Hub WebSocket 广播器
type Hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{clients: map[chan []byte]struct{}{}} }

func (h *Hub) register() chan []byte {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unregister(ch chan []byte) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
}

// Broadcast 非阻塞投递；慢客户端丢弃（客户端重连后全量同步兜底）
func (h *Hub) Broadcast(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- b:
		default:
		}
	}
}

func (h *Hub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		close(ch)
		delete(h.clients, ch)
	}
}

const (
	maxTextEntries = 200
	maxFileEntries = 100
)

// Stream 传输流：内存条目 + stream.json 持久化
type Stream struct {
	mu       sync.Mutex
	entries  []*Entry
	file     string
	stageDir string
	hub      *Hub
}

func NewStream(stageDir string) *Stream {
	s := &Stream{
		file:     filepath.Join(configDir(), "stream.json"),
		stageDir: stageDir,
		hub:      NewHub(),
	}
	_ = os.MkdirAll(stageDir, 0o755)
	s.load()
	return s
}

func (s *Stream) Hub() *Hub { return s.hub }

func (s *Stream) load() {
	data, err := os.ReadFile(s.file)
	if err != nil {
		return
	}
	var list []persistEntry
	if json.Unmarshal(data, &list) == nil {
		for _, p := range list {
			s.entries = append(s.entries, &Entry{
				ID: p.ID, Kind: p.Kind, Text: p.Text, Name: p.Name, Size: p.Size,
				MIME: p.MIME, Storage: p.Storage, Origin: p.Origin, Created: p.Created, Path: p.Path,
			})
		}
	}
}

// persist 需持有 s.mu 调用
func (s *Stream) persist() {
	list := make([]persistEntry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, persistEntry{
			ID: e.ID, Kind: e.Kind, Text: e.Text, Name: e.Name, Size: e.Size,
			MIME: e.MIME, Storage: e.Storage, Origin: e.Origin, Created: e.Created, Path: e.Path,
		})
	}
	data, _ := json.MarshalIndent(list, "", " ")
	if err := os.WriteFile(s.file, data, 0o644); err != nil {
		log.Println("stream.json 写入失败:", err)
	}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Stream) Add(e *Entry) *Entry {
	if e.ID == "" {
		e.ID = newID()
	}
	if e.Created == 0 {
		e.Created = time.Now().UnixMilli()
	}
	s.mu.Lock()
	s.entries = append(s.entries, e)
	s.trimLocked()
	s.persist()
	s.mu.Unlock()
	s.hub.Broadcast(map[string]any{"type": "add", "entry": e})
	return e
}

func (s *Stream) AddText(text, origin string) *Entry {
	return s.Add(&Entry{Kind: "text", Text: text, Origin: origin})
}

// AddFile stat 失败返回 nil
func (s *Stream) AddFile(path, storage, origin string) *Entry {
	path = filepath.Clean(path)
	fi, err := os.Stat(path)
	if err != nil {
		return nil
	}
	name := filepath.Base(path)
	return s.Add(&Entry{
		Kind: "file", Name: name, Size: fi.Size(),
		MIME: mimeOf(name), Storage: storage, Origin: origin, Path: path,
	})
}

// StagePaths SendTo：按绝对路径上架为引用（目录暂不支持）
func (s *Stream) StagePaths(paths []string, origin string) int {
	n := 0
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			if s.AddFile(p, "ref", origin) != nil {
				n++
			}
		} else if err == nil {
			log.Println("暂不支持目录上架:", p)
		}
	}
	return n
}

func (s *Stream) Get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.ID == id {
			return *e, true
		}
	}
	return Entry{}, false
}

func (s *Stream) Remove(id string) bool {
	s.mu.Lock()
	found := false
	keep := s.entries[:0]
	for _, e := range s.entries {
		if e.ID == id {
			found = true
			if e.Storage == "copy" && e.Path != "" {
				_ = os.Remove(e.Path)
			}
			continue
		}
		keep = append(keep, e)
	}
	if found {
		s.entries = keep
		s.persist()
	}
	s.mu.Unlock()
	if found {
		s.hub.Broadcast(map[string]any{"type": "remove", "id": id})
	}
	return found
}

func (s *Stream) ClearAll() int {
	s.mu.Lock()
	n := len(s.entries)
	for _, e := range s.entries {
		if e.Storage == "copy" && e.Path != "" {
			_ = os.Remove(e.Path)
		}
	}
	s.entries = nil
	s.persist()
	s.mu.Unlock()
	s.hub.Broadcast(map[string]any{"type": "bulk-remove"})
	return n
}

// ClearCopies 只清副本条目（引用与文本保留）
func (s *Stream) ClearCopies() (freed int64, removed int) {
	s.mu.Lock()
	keep := s.entries[:0]
	for _, e := range s.entries {
		if e.Kind == "file" && e.Storage == "copy" {
			if fi, err := os.Stat(e.Path); err == nil {
				freed += fi.Size()
			}
			_ = os.Remove(e.Path)
			removed++
		} else {
			keep = append(keep, e)
		}
	}
	s.entries = keep
	s.persist()
	s.mu.Unlock()
	if removed > 0 {
		s.hub.Broadcast(map[string]any{"type": "bulk-remove"})
	}
	return freed, removed
}

// Snapshot 新的在前；引用条目做失效检测
func (s *Stream) Snapshot() []Entry {
	s.mu.Lock()
	list := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, *e)
	}
	s.mu.Unlock()
	for i := range list {
		if list[i].Kind == "file" {
			if _, err := os.Stat(list[i].Path); err != nil {
				list[i].Missing = true
			}
		}
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].Created > list[b].Created })
	return list
}

func (s *Stream) CacheUsed() int64 {
	var total int64
	_ = filepath.WalkDir(s.stageDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, e := d.Info(); e == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

// trimLocked 需持有 s.mu；超限删最旧（副本文件一并清理）
func (s *Stream) trimLocked() {
	var texts, files int
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := s.entries[i]
		if e.Kind == "text" {
			texts++
			if texts > maxTextEntries {
				s.removeAtLocked(i)
			}
		} else {
			files++
			if files > maxFileEntries {
				s.removeAtLocked(i)
			}
		}
	}
}

func (s *Stream) removeAtLocked(i int) {
	e := s.entries[i]
	s.entries = append(s.entries[:i], s.entries[i+1:]...)
	if e.Storage == "copy" && e.Path != "" {
		_ = os.Remove(e.Path)
	}
}

package main

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"strings"
	"sync"
	"time"
)

// ClipWatcher 剪贴板监听：PC 复制的文本自动进传输流；
// PC 复制的文件不自动上架，只在 PC 主页弹轻提示一键确认。
type ClipWatcher struct {
	srv  *Server
	mu   sync.Mutex
	last string // 上一条文本（快速跳过未变化）

	recent   [128][20]byte // 近期已上墙文本的 hash 环，防交替复制重复上墙
	recentIx int

	suppressHash [20]byte // 我们自己写入系统剪贴板的内容，监听要跳过（防回环）
	suppressAt   time.Time

	lastFileFp string // 上一批文件路径的指纹

	pausedUntil time.Time
	pending     []string // 检测到复制未确认的文件
	pendingAt   time.Time
}

const (
	clipMaxText   = 100 * 1024 // 文本上墙上限，超长跳过
	clipPollEvery = time.Second
	clipPendingTTL = time.Minute
)

func NewClipWatcher(srv *Server) *ClipWatcher {
	return &ClipWatcher{srv: srv}
}

func (c *ClipWatcher) Start() {
	if c.srv.cfg.ClipWatchOff {
		log.Println("剪贴板监听：已关闭（设置里可开启）")
		return
	}
	go c.loop()
	log.Println("剪贴板监听：已启动（文本自动进流；复制文件后主页一键上架）")
}

func (c *ClipWatcher) loop() {
	t := time.NewTicker(clipPollEvery)
	defer t.Stop()
	for range t.C {
		func() {
			defer func() { recover() }() // 剪贴板 API 偶发失败绝不能带崩进程
			c.pollText()
			c.pollFiles()
		}()
	}
}

func (c *ClipWatcher) pollText() {
	txt := readClipboardText()
	c.mu.Lock()
	defer c.mu.Unlock()
	if txt == "" || txt == c.last {
		return
	}
	c.last = txt
	h := sha1.Sum([]byte(txt))
	// 防回环：这是“写入系统剪贴板”端点刚写的内容
	if h == c.suppressHash && time.Since(c.suppressAt) < 5*time.Second {
		c.suppressAt = time.Time{} // 只吞一次
		return
	}
	// 去重：近期已上墙过的内容不再上墙
	for _, r := range c.recent {
		if r == h {
			return
		}
	}
	if c.Paused() || len(txt) > clipMaxText {
		return
	}
	c.recent[c.recentIx] = h
	c.recentIx = (c.recentIx + 1) % len(c.recent)
	c.srv.Stream().AddText(txt, "clip") // AddText 会解锁后再广播，不会死锁
}

// SuppressBy 由“写入系统剪贴板”调用：登记内容，防止被监听当成新条目再上墙
func (c *ClipWatcher) SuppressBy(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.suppressHash = sha1.Sum([]byte(text))
	c.suppressAt = time.Now()
	c.last = text
}

// PauseFor 暂停监听（防密码/验证码上墙）
func (c *ClipWatcher) PauseFor(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pausedUntil = time.Now().Add(d)
	c.srv.Stream().Hub().Broadcast(map[string]any{"type": "clip-pause", "until": c.pausedUntil.UnixMilli()})
	log.Println("剪贴板监听已暂停至", c.pausedUntil.Format("15:04:05"))
}

func (c *ClipWatcher) Paused() bool { return time.Now().Before(c.pausedUntil) }

func (c *ClipWatcher) PausedUntil() int64 {
	if c.Paused() {
		return c.pausedUntil.UnixMilli()
	}
	return 0
}

func (c *ClipWatcher) pollFiles() {
	paths := readClipboardFiles()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(paths) == 0 {
		return
	}
	fp := strings.Join(paths, "\x00")
	if fp == c.lastFileFp {
		return
	}
	c.lastFileFp = fp
	if c.Paused() {
		return
	}
	// 不自动上架：登记待确认，广播给 PC 主页弹提示
	c.pending = paths
	c.pendingAt = time.Now()
	c.srv.Stream().Hub().Broadcast(map[string]any{"type": "clip-files", "count": len(paths), "names": firstNames(paths, 3)})
}

// TakePending 取走待确认的复制文件（点“上架”时调用）
func (c *ClipWatcher) TakePending() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pending
	c.pending = nil
	return p
}

func (c *ClipWatcher) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil || time.Since(c.pendingAt) > clipPendingTTL {
		return 0
	}
	return len(c.pending)
}

func firstNames(paths []string, n int) []string {
	out := []string{}
	for i, p := range paths {
		if i >= n {
			break
		}
		out = append(out, baseName(p))
	}
	return out
}

func baseName(p string) string {
	p = strings.TrimRight(p, "\\/")
	if i := strings.LastIndexAny(p, "\\/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func hashKey(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

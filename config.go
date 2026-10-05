package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Port         int      `json:"port"`
	ShareRoots   []string `json:"shareRoots"`   // 多个共享位置（手机可浏览的范围）
	Favorites    []string `json:"favorites"`    // 收藏的绝对路径（服务端保存，全设备同步）
	StageDir     string   `json:"stageDir"`
	CacheLimitGB int      `json:"cacheLimitGB"`
	AutoOpen     bool     `json:"autoOpen"`
	FirstRunDone bool     `json:"firstRunDone"`
	ClipWatchOff bool     `json:"clipWatchOff"` // 剪贴板监听开关（反向存储：缺省 false=开启）
	PIN          string   `json:"pin"`          // 可选访问 PIN（空=不启用）

	LegacyShareRoot string `json:"shareRoot,omitempty"` // 旧版单根字段，仅读取迁移
}

func configDir() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".trans")
		}
		return ".trans"
	}
	return filepath.Join(appData, "Trans")
}

func configPath() string { return filepath.Join(configDir(), "config.json") }

func defaultConfig() *Config {
	return &Config{
		Port:         8020,
		StageDir:     filepath.Join(configDir(), "stage_cache"),
		CacheLimitGB: 20,
		AutoOpen:     true,
	}
}

func loadConfig() *Config {
	c := defaultConfig()
	if data, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(data, c)
	}
	c.normalize()
	return c
}

func (c *Config) normalize() {
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = 8020
	}
	// 旧版单根迁移；全新安装默认：用户主目录 + 所有本地固定硬盘
	if len(c.ShareRoots) == 0 {
		roots := []string{}
		if c.LegacyShareRoot != "" {
			roots = append(roots, c.LegacyShareRoot)
		} else if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, home)
		}
		roots = append(roots, fixedDrives()...)
		c.ShareRoots = roots
	}
	if c.StageDir == "" {
		c.StageDir = filepath.Join(configDir(), "stage_cache")
	}
	if c.CacheLimitGB <= 0 {
		c.CacheLimitGB = 20
	}
	// 去重 + 剔除不存在的目录
	seen := map[string]bool{}
	out := []string{}
	for _, r := range c.ShareRoots {
		r = filepath.Clean(strings.TrimRight(r, "\"' "))
		if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
			continue
		}
		k := strings.ToLower(r)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	if len(out) > 0 {
		c.ShareRoots = out
	}
	if c.Favorites == nil {
		c.Favorites = []string{}
	}
}

func (c *Config) Save() {
	data, _ := json.MarshalIndent(c, "", "  ")
	_ = os.WriteFile(configPath(), data, 0o644)
}

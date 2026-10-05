package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "0.3.1"

func main() {
	quiet := flag.Bool("quiet", false, "启动后不自动打开浏览器（开机自启用）")
	noTray := flag.Bool("no-tray", false, "无托盘模式运行（调试）")
	portFlag := flag.Int("port", 0, "覆盖配置端口")
	doInstall := flag.Bool("install-sendto", false, "安装“发送到 Trans”右键菜单")
	doUninstall := flag.Bool("uninstall-sendto", false, "卸载“发送到 Trans”右键菜单")
	flag.Parse()

	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		fmt.Println("无法创建配置目录:", err)
		os.Exit(1)
	}
	initLog()

	if *doInstall {
		if err := installSendtoShortcut(); err != nil {
			log.Println("安装失败:", err)
			os.Exit(1)
		}
		log.Println("已安装“发送到 Trans”右键菜单")
		return
	}
	if *doUninstall {
		uninstallSendtoShortcut()
		log.Println("已卸载“发送到 Trans”右键菜单")
		return
	}

	cfg := loadConfig()
	if *portFlag > 0 && *portFlag != cfg.Port {
		cfg.Port = *portFlag
		cfg.Save()
	}

	// SendTo 热路径：已有实例在运行 → 转发路径后立即退出（单实例）
	paths := flag.Args()
	if len(paths) > 0 && forwardStage(cfg.Port, paths) {
		return
	}

	// SendTo 快捷方式：每次启动自检（exe 换位置自动重建），失败不影响其它功能
	if err := installSendtoShortcut(); err != nil {
		log.Println("SendTo 安装失败（不影响其它功能）:", err)
	}
	if !cfg.FirstRunDone {
		cfg.FirstRunDone = true
		cfg.Save()
	}

	// 已有实例在跑：直接打开主页退出（双击场景防端口冲突静默失败）
	if len(paths) == 0 && alreadyRunning(cfg.Port) {
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", cfg.Port))
		return
	}

	srv := NewServer(cfg)
	if err := srv.Start(); err != nil {
		log.Fatalf("启动失败（端口 %d 可能被占用）: %v", cfg.Port, err)
	}
	log.Printf("trans %s 已启动 :%d  共享位置 [%s]  暂存 %s", version, cfg.Port, strings.Join(cfg.ShareRoots, ", "), cfg.StageDir)

	// v0.2/v0.3 模块
	srv.Clip().Start()       // 剪贴板监听
	go chunkCleanupLoop()    // 分片上传临时文件清理
	mdnsStart(cfg.Port)      // mDNS: trans.local

	if !*quiet && cfg.AutoOpen {
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", cfg.Port))
	}

	// SendTo 冷路径：服务本来没在跑 → 启动完成后把带来的文件上架
	if len(paths) > 0 {
		if n := srv.Stream().StagePaths(paths, "sendto"); n > 0 {
			log.Printf("已上架 %d 个文件", n)
		}
	}

	if *noTray {
		log.Println("无托盘模式，Ctrl+C 退出")
		select {}
	}
	runTray(srv)
}

// alreadyRunning 探测本端口是否已是 trans 在服务
func alreadyRunning(port int) bool {
	resp, err := (&http.Client{Timeout: time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return strings.Contains(string(b), `"ok":true`)
}

func chunkCleanupLoop() {
	for {
		time.Sleep(time.Hour)
		chunkCleanup()
	}
}

func initLog() {
	f, err := os.OpenFile(filepath.Join(configDir(), "trans.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stdout, f))
	log.SetFlags(log.LstdFlags)
}

// forwardStage 已有实例在监听则把 SendTo 的文件路径转发给它
func forwardStage(port int, paths []string) bool {
	body, _ := json.Marshal(map[string]any{"paths": paths})
	url := fmt.Sprintf("http://127.0.0.1:%d/api/internal/stage", port)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Post(url, "application/json", strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusOK {
		log.Printf("已转发 %d 个路径到运行中的实例", len(paths))
		return true
	}
	return false
}

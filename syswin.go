package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/atotto/clipboard"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const autostartName = "Trans"

func runKeyPath() string { return `Software\Microsoft\Windows\CurrentVersion\Run` }

// setAutostart 登录后自启（注册表 Run 键，非 Windows Service）
func setAutostart(on bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(autostartName, `"`+exe+`" --quiet`)
	}
	return k.DeleteValue(autostartName)
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath(), registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(autostartName)
	return err == nil
}

func sendtoShortcutPath() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "SendTo", "Trans.lnk")
}

func sendtoInstalled() bool {
	_, err := os.Stat(sendtoShortcutPath())
	return err == nil
}

// installSendtoShortcut 安装/自愈：exe 位置变了（换目录、dev→正式版）就重建快捷方式
func installSendtoShortcut() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	marker := filepath.Join(configDir(), "sendto_exe.txt")
	if sendtoInstalled() {
		if data, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(data)) == exe {
			return nil
		}
	}
	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; $l = $ws.CreateShortcut('%s'); $l.TargetPath = '%s'; $l.WorkingDirectory = '%s'; $l.Save()`,
		psEscape(sendtoShortcutPath()), psEscape(exe), psEscape(filepath.Dir(exe)))
	if err := exec.Command("powershell", "-NoProfile", "-Command", ps).Run(); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(exe), 0o644)
}

func uninstallSendtoShortcut() { _ = os.Remove(sendtoShortcutPath()) }

func psEscape(s string) string { return strings.ReplaceAll(s, "'", "''") }

// fixedDrives 枚举所有本地固定硬盘（C:\ D:\ ...），默认作为共享位置
func fixedDrives() []string {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	getLogical := k32.NewProc("GetLogicalDrives")
	getType := k32.NewProc("GetDriveTypeW")
	mask, _, _ := getLogical.Call()
	var out []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		vol := string(rune('A'+i)) + `:\`
		t, _, _ := getType.Call(uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(vol))))
		if t == 3 { // DRIVE_FIXED
			out = append(out, vol)
		}
	}
	return out
}

// freeSpace 指定目录所在磁盘剩余空间
func freeSpace(dir string) uint64 {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	var free, total, avail uint64
	r1, _, _ := proc.Call(
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(dir))),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&avail)),
	)
	if r1 == 0 {
		return 0
	}
	return free
}

func openBrowser(url string) error {
	return exec.Command("cmd", "/c", "start", "", url).Start()
}

func revealInExplorer(path string) error {
	return exec.Command("explorer", "/select,", path).Start()
}

func writeSystemClipboard(text string) error {
	return clipboard.WriteAll(text)
}

func readClipboardText() string {
	t, err := clipboard.ReadAll()
	if err != nil {
		return ""
	}
	return t
}

const cf_HDROP = 15

// readClipboardFiles 读剪贴板里的文件列表（资源管理器 Ctrl+C），失败/没有返回空
func readClipboardFiles() (out []string) {
	// 剪贴板 API 出任何问题都不让进程挂掉
	defer func() { recover() }()
	user32 := syscall.NewLazyDLL("user32.dll")
	shell32 := syscall.NewLazyDLL("shell32.dll")
	openClipboard := user32.NewProc("OpenClipboard")
	closeClipboard := user32.NewProc("CloseClipboard")
	getClipboardData := user32.NewProc("GetClipboardData")
	dragQueryFile := shell32.NewProc("DragQueryFileW")
	if r, _, _ := openClipboard.Call(0); r == 0 {
		return nil
	}
	defer closeClipboard.Call()
	h, _, _ := getClipboardData.Call(cf_HDROP)
	if h == 0 {
		return nil
	}
	n, _, _ := dragQueryFile.Call(h, 0xFFFFFFFF, 0, 0)
	for i := uintptr(0); i < n && i < 100; i++ {
		sz, _, _ := dragQueryFile.Call(h, i, 0, 0)
		buf := make([]uint16, sz+1)
		dragQueryFile.Call(h, i, uintptr(unsafe.Pointer(&buf[0])), sz+1)
		out = append(out, windows.UTF16ToString(buf))
	}
	return out
}

// ffmpegAvailable 探测 ffmpeg/ffprobe（转码播放与视频时长用，缺失不报错）
func ffmpegPath() (ffmpeg string, ffprobe string) {
	ffmpeg, _ = exec.LookPath("ffmpeg")
	ffprobe, _ = exec.LookPath("ffprobe")
	return
}

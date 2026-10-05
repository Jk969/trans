// Trans 安装器 / 卸载器（单文件，内嵌 trans.exe）
//
// 用法：
//   Trans-x.y.z-setup.exe                     交互安装（问桌面快捷方式/开机自启/立即运行）
//   Trans-x.y.z-setup.exe /SILENT             静默安装（全默认：无桌面快捷方式、不自启）
//   Trans-x.y.z-setup.exe /DIR=D:\Trans       指定安装目录
//   Trans-x.y.z-setup.exe /DESKTOP=1 /AUTOSTART=1   静默安装并开桌面快捷方式/自启
//   Trans-x.y.z-setup.exe /UNINSTALL          交互卸载（问是否删数据）
//   Trans-x.y.z-setup.exe /UNINSTALL /SILENT  静默卸载（保留用户数据）
//
// 安装到 %LOCALAPPDATA%\Programs\Trans（免管理员），注册到「应用与功能」，
// 快捷方式：开始菜单 + 右键「发送到 Trans」(+可选桌面)，卸载器随安装落地。
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "embed"

	"golang.org/x/sys/windows/registry"
)

//go:embed payload/trans.exe
var payload []byte

var version = "0.3.1"

const (
	appName   = "Trans"
	uninstKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\Trans`
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
)

var stdin = bufio.NewReader(os.Stdin)

func main() {
	args := os.Args[1:]
	silent := hasFlag(args, "/SILENT") || hasFlag(args, "/VERYSILENT") || hasFlag(args, "/S")
	switch {
	case stringFlag(args, "/FINALIZE") != "":
		doFinalize(stringFlag(args, "/FINALIZE"))
	case hasFlag(args, "/UNINSTALL"):
		doUninstall(silent)
	default:
		doInstall(args, silent)
	}
}

/* ================= 安装 ================= */

func doInstall(args []string, silent bool) {
	dir := stringFlag(args, "/DIR")
	if dir == "" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			fatal("无法定位 LOCALAPPDATA")
		}
		dir = filepath.Join(local, "Programs", appName)
	}
	dir = filepath.Clean(strings.Trim(strings.TrimSpace(dir), `"`))
	desktop := stringFlag(args, "/DESKTOP") == "1"
	autostart := stringFlag(args, "/AUTOSTART") == "1"

	if !silent {
		fmt.Printf("Trans %s 局域网传输站 安装向导\n=====================================\n", version)
		fmt.Printf("安装目录: %s\n", dir)
		desktop = askYN("创建桌面快捷方式?", false)
		autostart = askYN("开机自动启动(登录后)?", false)
	}

	killRunning()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("创建目录失败: " + err.Error())
	}
	target := filepath.Join(dir, "trans.exe")
	if err := os.WriteFile(target, payload, 0o755); err != nil {
		fatal("写入 trans.exe 失败: " + err.Error())
	}

	// 快捷方式：开始菜单 + SendTo（固定），桌面（可选）
	must(createShortcut(startMenuLnk(), target, ""))
	must(createShortcut(sendToLnk(), target, ""))
	if desktop {
		must(createShortcut(desktopLnk(), target, ""))
	}

	// 自启（注册表 Run，与主程序设置互通）
	setAutostart(autostart, target)

	// 注册到「应用与功能」
	must(writeUninstallRegistry(dir, target))

	// 卸载器落地
	self, err := os.Executable()
	if err == nil {
		_ = copyFile(self, filepath.Join(dir, "uninstall.exe"))
	}

	fmt.Println()
	fmt.Printf("安装完成: %s\n", target)
	fmt.Println("手机与电脑连同一路由器，双击 Trans 托盘常驻后扫码即用。")
	fmt.Println("（首次运行 Windows 防火墙弹窗需点「允许」）")

	if !silent && askYN("立即运行 Trans?", true) {
		_ = exec.Command(target, "--quiet").Start()
	}
}

/* ================= 卸载 ================= */

func doUninstall(silent bool) {
	fmt.Println("正在卸载 Trans…")
	killRunning()

	for _, lnk := range []string{startMenuLnk(), sendToLnk(), desktopLnk()} {
		_ = os.Remove(lnk)
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstKey)
	_ = registry.DeleteKey(registry.CURRENT_USER, runKey) // runKey 下只有我们自己的值

	delData := false
	if !silent {
		delData = askYN("同时删除配置与数据("+filepath.Join(os.Getenv("APPDATA"), "Trans")+")?", false)
	}
	if delData {
		_ = os.RemoveAll(filepath.Join(os.Getenv("APPDATA"), "Trans"))
	}

	self, err := os.Executable()
	if err != nil {
		fatal(err.Error())
	}
	dir := filepath.Dir(self)

	// 安装目录里含正在运行的卸载器自身 → 交给临时副本异步收尾删除
	tmpCopy := filepath.Join(os.TempDir(), "trans-uninstall.exe")
	_ = os.Remove(tmpCopy)
	if err := copyFile(self, tmpCopy); err != nil {
		fatal("卸载收尾失败: " + err.Error())
	}
	_ = exec.Command(tmpCopy, "/FINALIZE="+dir).Start()
	fmt.Println("卸载完成。")
}

func doFinalize(dir string) {
	time.Sleep(800 * time.Millisecond)
	_ = os.RemoveAll(dir)
	// 临时副本自删：cmd 子进程比本进程活得久，ping 延时等本进程退出后 del
	// （路径作为独立参数传，Go 会在含空格时自动加引号，避免拼接破坏解析）
	_ = exec.Command("cmd", "/c", "ping -n 3 127.0.0.1 >nul & del", filepath.Join(os.TempDir(), "trans-uninstall.exe")).Start()
}

/* ================= 注册表 / 快捷方式 ================= */

func writeUninstallRegistry(dir, target string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	uninst := filepath.Join(dir, "uninstall.exe")
	_ = k.SetStringValue("DisplayName", "Trans 局域网传输站")
	_ = k.SetStringValue("DisplayVersion", version)
	_ = k.SetStringValue("DisplayIcon", target)
	_ = k.SetStringValue("UninstallString", `"`+uninst+`" /UNINSTALL`)
	_ = k.SetStringValue("InstallLocation", dir)
	_ = k.SetStringValue("Publisher", "Trans")
	_ = k.SetDWordValue("EstimatedSize", uint32(len(payload)/1024))
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return nil
}

func setAutostart(on bool, target string) {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if on {
		_ = k.SetStringValue(appName, `"`+target+`" --quiet`)
	} else {
		_ = k.DeleteValue(appName)
	}
}

func startMenuLnk() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Trans.lnk")
}
func desktopLnk() string {
	return filepath.Join(os.Getenv("USERPROFILE"), "Desktop", "Trans.lnk")
}
func sendToLnk() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "SendTo", "Trans.lnk")
}

func createShortcut(lnk, target, args string) error {
	if _, err := os.Stat(target); err != nil {
		return err
	}
	ps := fmt.Sprintf(
		`$ws = New-Object -ComObject WScript.Shell; $l = $ws.CreateShortcut('%s'); $l.TargetPath = '%s'; $l.Arguments = '%s'; $l.WorkingDirectory = '%s'; $l.Save()`,
		psEscape(lnk), psEscape(target), psEscape(args), psEscape(filepath.Dir(target)))
	return exec.Command("powershell", "-NoProfile", "-Command", ps).Run()
}

func psEscape(s string) string { return strings.ReplaceAll(s, "'", "''") }

/* ================= 工具 ================= */

func killRunning() {
	_ = exec.Command("cmd", "/c", "taskkill /IM trans.exe /F >nul 2>&1").Run()
}

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if strings.EqualFold(a, name) {
			return true
		}
	}
	return false
}

func stringFlag(args []string, name string) string {
	prefix := strings.ToLower(name) + "="
	for _, a := range args {
		if strings.HasPrefix(strings.ToLower(a), prefix) {
			return a[len(prefix):]
		}
	}
	return ""
}

func askYN(prompt string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Printf("%s %s ", prompt, hint)
	line, _ := stdin.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

func fatal(msg string) {
	fmt.Println("错误:", msg)
	os.Exit(1)
}

func must(err error) {
	if err != nil {
		fatal(err.Error())
	}
}

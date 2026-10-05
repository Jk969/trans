# Trans — 局域网传输站

个人用的局域网文件/文本传输服务：电脑跑一个托盘常驻程序，手机扫码打开网页，像「文件传输助手」一样互传。

- **传输流**：文件 + 文本混合时间流，双向实时同步（WebSocket）
- **剪贴板闭环**：电脑复制的**文字自动进流**（手机随时取）；手机发的文字电脑一键「写入系统剪贴板」；复制**文件**后主页弹轻提示一键上架
- **多共享位置 + 收藏**：默认用户主目录 + 所有本地硬盘；任意目录可收藏（服务端保存，换设备也在）
- **三种上架方式**：电脑拖文件/整个文件夹进网页（递归展开）/ 右键「发送到 Trans」/ 手机 ⊕ 上传
- **视频在线看**：Range 流式播放、倍速、连播、srt 字幕、进度记忆；mkv 可 ffmpeg 转码播放
- **相册 Tab**：跨共享位置扫描图片/视频瀑布流（自动跳过系统目录），点目录名直达
- **多选打包**：文件页多选 → 流式 zip 下载（文件夹直接打包）
- **缩略图 + 视频时长**：图片有缩略图缓存，视频显示时长（需 ffprobe）
- **大文件分片上传 + 断点续传**（>20MB 自动分片，中断后重试续传）
- **可选增强**：访问 PIN（cookie 门禁）、WebDAV（`/dav/<位置序号>/`）、mDNS（`trans.local`）

免安装、免登录、单文件 exe（`trans.exe`）。ffmpeg 非必需：装了（并加入 PATH）才有 mkv 转码播放和视频时长显示。

免安装、免登录、单文件 exe（`trans.exe`）。

## 快速开始

**方式一（推荐）：安装包**

双击 `dist\Trans-0.3.1-setup.exe`（源码构建用 `build.bat` 一键产出）：

- 安装到 `%LOCALAPPDATA%\Programs\Trans`（免管理员），注册进「应用与功能」可正常卸载
- 自动创建开始菜单快捷方式和右键「发送到 Trans」，可选拖面快捷方式/开机自启
- 命令行：`/SILENT` 静默安装、`/DIR=D:\xxx` 指定目录、`/DESKTOP=1 /AUTOSTART=1` 附加项；卸载 `uninstall.exe /UNINSTALL [/SILENT]`（静默卸载保留用户数据）

**方式二：绿色单文件**

直接双击仓库根目录的 `trans.exe`（功能相同，只是不走安装器）。

1. 首次运行 Windows 会弹防火墙提示 → **必须点「允许」**
2. 主页左侧有二维码，手机扫码打开即可
3. 手机端建议「添加到主屏幕」，下次点图标直达

## 连不上？按顺序排查

1. **防火墙**：首次运行的弹窗必须点「允许」。错过了就到 Windows 安全中心 → 防火墙 → 允许应用通过防火墙，勾选 `trans.exe`
2. **公用网络**：当前网络若被识别为「公用网络」，放行了也可能连不上。
   设置 → 网络和 Internet → 点当前网络 → 网络配置文件选「专用」
3. **同一路由器**：手机和电脑连同一个 Wi-Fi（或同一路由器的 2.4G/5G）
4. **IP 变了**：电脑重启后 IP 可能变化，主页会自动重新生成二维码；
   一劳永逸的办法是**在路由器后台给电脑的 MAC 地址绑定静态 IP**

## 共享位置与收藏

**手机能浏览哪些目录**由「共享位置」决定，默认 = 用户主目录 + 所有本地固定硬盘。
在电脑端主页左侧控制台的「共享位置」里可以：

- **＋ 添加**：输入任意绝对路径，如 `D:\` 或 `D:\Movies`（只共享某个子目录，浏览更快）
- **✕ 移除**：不想暴露的盘/目录直接移除（至少保留一个）

**收藏**：手机文件页进入某个目录后，点右上「☆ 收藏」，之后顶部出现 ⭐ chips，一键跳回。
收藏保存在服务端（`config.json`），换手机/换浏览器也在；收藏的是绝对路径，共享位置顺序变化不受影响，位置被移除后收藏自动隐藏。

注意：共享位置的调整目前只在电脑端控制台（宽屏显示）；手机浏览器窗口窄，不做管理界面。

## 右键「发送到 Trans」

首次运行会自动安装。没装的话命令行执行：

```
trans.exe --install-sendto
```

之后在资源管理器里右键任意文件 → 发送到 → Trans，文件立刻出现在手机传输流里（零拷贝引用，不占空间；移动/删除原文件后条目会标红失效）。

## 托盘菜单

右键右下角托盘图标：**打开主页 / 暂停服务 / 暂停剪贴板监听 10 分钟 / 退出**。

- 暂停服务：手机端显示暂停页，恢复即自动回来
- 暂停监听：复制密码/验证码前用，期间电脑复制的内容不会上墙
- 「登录后自动启动」在主页左侧控制台勾选（注册表 Run 键，**不是** Windows Service——服务在 Session 0 拿不到剪贴板/托盘）

## PIN / WebDAV（可选，默认全关）

- **PIN**：控制台「访问 PIN → 设置」。启用后本局域网所有访问要输 PIN（浏览器输一次记 30 天）
- **WebDAV**：文档类 App 挂载 `http://电脑IP:8020/dav/0/`（序号 = 主页共享位置列表的第几项，从 0 开始）。设了 PIN 时用户名 `trans`、密码 = PIN
- **mDNS**：自动广播，iOS Safari 可试 `http://trans.local:8020/`（安卓浏览器支持不稳）

## 命令行

```
trans.exe                    正常启动（托盘常驻）
trans.exe --quiet            不自动弹浏览器（开机自启用）
trans.exe --port 9000        换端口
trans.exe --no-tray          调试：无托盘控制台模式
trans.exe 文件1 文件2         上架指定文件（SendTo 内部也走这个）
trans.exe --install-sendto   安装右键菜单
trans.exe --uninstall-sendto 卸载右键菜单
```

## 数据目录

配置、传输流清单、暂存副本、日志都在 `%APPDATA%\Trans\`：

| 文件 | 说明 |
|---|---|
| `config.json` | 端口 / 共享根目录（默认用户主目录）/ 缓存上限 |
| `stream.json` | 传输流清单（重启恢复） |
| `stage_cache\` | 拖拽/上传产生的副本，上限默认 20GB，主页可一键清理 |
| `trans.log` | 日志 |

## 浏览器限制说明（http 局域网的正常现象）

- 手机「添加到主屏幕」只是快捷方式，**不支持离线/后台**
- 手机锁屏或切后台时上传可能中断，页面里有分文件重试
- 复制操作都是点击触发（浏览器安全限制，非 bug）

## 从源码构建

需要 Go 1.22+（国内网络先 `go env -w GOPROXY=https://goproxy.cn,direct`）：

```
build.bat                      一键全流程（推荐）：
                               图标 -> trans.exe(内嵌图标+版本信息) -> dist\Trans-x.y.z-setup.exe
go run ./tools/makeicon        只重新生成图标（installer/trans.ico + assets/icon.ico）
go run ./tools/mkversion       只重新生成 exe 版本资源（resource.syso）
go build -o trans-dev.exe .    调试版（带控制台日志）
```

图标与版本信息说明：`tools/makeicon` 用超采样绘制渐变方块+双箭头图标并合成多尺寸 .ico；`tools/mkversion` 生成 `resource.syso`（版本号从根目录 **VERSION** 文件注入），`go build` 自动打进 exe；安装器是 `installer/setup`（Go 自研，内嵌 trans.exe，支持静默安装/完整卸载）。

**版本号单一来源**：升版本只改根目录 `VERSION` 文件（如 `0.3.2`），`build.bat`、exe 资源信息、安装器全部自动跟随。

## 架构一览

```
main.go      入口：单实例探测/SendTo 参数/自启/托盘
config.go    %APPDATA%\Trans\config.json
stream.go    传输流存储 + WebSocket 广播 + stream.json 持久化
server.go    HTTP 路由/静态页/二维码/网卡枚举/WS/暂停/设置
fsapi.go     文件浏览/搜索/条目文件服务（Range；safeJoin 防穿越）
upload.go    multipart 上传 + 分片断点续传（冲突处理/空间预检）
clipwatch.go 剪贴板监听（文本自动进流 + 复制文件轻提示 + 防回环/去重/暂停）
syswin.go    注册表自启/SendTo 快捷方式/磁盘空间/剪贴板/资源管理器
thumb.go     图片缩略图缓存 + ffprobe 视频时长
subapi.go    srt→vtt 字幕 + 相册媒体扫描
zipdl.go     多选/文件夹流式 zip 打包下载
transcode.go ffmpeg 转封装流式播放（mkv 实验支持）
dav.go       WebDAV（x/net/webdav，每共享位置一个挂载点）
auth.go      可选 PIN（cookie 门禁 + DAV Basic）
mdns.go      mDNS 广播 trans.local
tray.go      托盘（getlantern/systray）
web/         前端单页（原生 JS，无构建、无外部依赖）
```

安全边界（个人轻量）：文件浏览严格限制在共享根目录内；传输流引用走随机 ID，真实路径不下发给浏览器；`/api/internal/*` 仅接受本机回环。

package main

import (
	"fmt"
	"os"
	"time"

	systray "github.com/getlantern/systray"
)

func runTray(s *Server) {
	systray.Run(func() {
		systray.SetIcon(iconBytes)
		systray.SetTitle("Trans")
		systray.SetTooltip("Trans 局域网传输")
		mOpen := systray.AddMenuItem("打开主页", "在浏览器打开")
		mPause := systray.AddMenuItem("暂停服务", "暂停后手机将无法访问")
		mClipPause := systray.AddMenuItem("暂停剪贴板监听 10 分钟", "复制密码/验证码前用")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "")
		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					_ = openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", s.cfg.Port))
				case <-mPause.ClickedCh:
					p := s.TogglePause()
					if p {
						mPause.SetTitle("恢复服务")
						mPause.SetTooltip("恢复对局域网的服务")
					} else {
						mPause.SetTitle("暂停服务")
						mPause.SetTooltip("暂停后手机将无法访问")
					}
				case <-mClipPause.ClickedCh:
					s.Clip().PauseFor(10 * time.Minute)
				case <-mQuit.ClickedCh:
					systray.Quit()
				}
			}
		}()
	}, func() {
		os.Exit(0)
	})
}

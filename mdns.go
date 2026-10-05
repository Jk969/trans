package main

import (
	"context"
	"log"
	"time"

	"github.com/grandcat/zeroconf"
)

// mdnsStart 广播 trans.local（_http._tcp），iOS Safari 可直接访问；
// 安卓浏览器对 mDNS 支持不稳，仅作补充，失败不影响任何功能。
func mdnsStart(port int) {
	go func() {
		defer func() { recover() }()
		var ips []string
		for _, n := range listAddrs(port) {
			ips = append(ips, n.IP)
		}
		if len(ips) == 0 {
			return
		}
		server, err := zeroconf.RegisterProxy("trans", "_http._tcp", "local.", port, "trans.local", ips, nil, nil)
		if err != nil {
			log.Println("mDNS 广播失败（不影响其它功能）:", err)
			return
		}
		log.Printf("mDNS 已广播：http://trans.local:%d/", port)
		<-context.Background().Done()
		server.Shutdown()
	}()
}

var _ = time.Second

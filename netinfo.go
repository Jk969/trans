package main

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// listAddrs 枚举网卡，筛私网 IPv4，生成候选访问地址
func listAddrs(port int) []netIf {
	var out []netIf
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, ifi := range ifaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !isPrivate(ip4) {
				continue
			}
			out = append(out, netIf{
				Name: ifi.Name,
				IP:   ip4.String(),
				URL:  fmt.Sprintf("http://%s:%d/", ip4.String(), port),
			})
		}
	}
	sortIfs(out)
	return out
}

func isPrivate(ip net.IP) bool {
	return ip[0] == 10 ||
		(ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) ||
		(ip[0] == 192 && ip[1] == 168)
}

// sortIfs 物理网卡（有线/Wi-Fi）排前面，虚拟/VPN 网卡排后面
func sortIfs(ifs []netIf) {
	score := func(name string) int {
		l := strings.ToLower(name)
		switch {
		case strings.Contains(l, "wi-fi"), strings.Contains(l, "wireless"), strings.Contains(l, "wlan"),
			strings.Contains(l, "以太网"), strings.Contains(l, "ethernet"), strings.Contains(l, "网络"):
			return 0
		case strings.Contains(l, "virtual"), strings.Contains(l, "vmware"), strings.Contains(l, "vethernet"),
			strings.Contains(l, "wsl"), strings.Contains(l, "hyper"), strings.Contains(l, "vpn"),
			strings.Contains(l, "tap"), strings.Contains(l, "loopback"):
			return 2
		default:
			return 1
		}
	}
	sort.SliceStable(ifs, func(a, b int) bool { return score(ifs[a].Name) < score(ifs[b].Name) })
}

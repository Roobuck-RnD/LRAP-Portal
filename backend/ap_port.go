package main

import (
	"strconv"
	"strings"
)

// ap_port.go 按 AP 接入的 AC 物理 LAN 口给它一个稳定身份/编号。物理口由布线决定,
// 不随 DHCP IP 变化或 reboot 改变,比 IP 稳。映射链:
//   IP --(/proc/net/arp)--> MAC --(bridge fdb)--> lanN
// 复用 lan_clients.go 的 parseARPByIP() 和 listLanEdgePortsByMAC()。

// apPortIndexByIP 返回 "AP 的 IP -> 物理口号(1-4)"。解析不出的 IP 不在 map 里。
func apPortIndexByIP() map[string]int {
	arpByIP := parseARPByIP()           // ip -> MAC(大写)
	portByMAC := listLanEdgePortsByMAC() // MAC(大写) -> port("lan1"...)

	out := make(map[string]int)
	for ip, mac := range arpByIP {
		if n := lanPortNumber(portByMAC[mac]); n > 0 {
			out[ip] = n
		}
	}
	return out
}

// lanPortNumber 从 "lanN" 解析出 N(如 "lan3" -> 3);不是 lan 口返回 0。
func lanPortNumber(port string) int {
	p := strings.ToLower(strings.TrimSpace(port))
	if !strings.HasPrefix(p, "lan") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(p, "lan"))
	if err != nil {
		return 0
	}
	return n
}

// apDisplayName 给 AP 一个按物理口编号的显示名:portIndex>0 时 = "Antenna" + 口号
// (如 lan1 -> "Antenna1");解析不出口(portIndex<=0)时回退真实 hostname。
// 只影响前端显示,不改设备真实 hostname。
func apDisplayName(hostname string, portIndex int) string {
	if portIndex <= 0 {
		return hostname
	}
	return "Antenna" + strconv.Itoa(portIndex)
}

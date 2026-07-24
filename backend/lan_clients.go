package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// 返回给前端的数据结构
// 保留 port/mac/ip/hostname，兼容你当前前端
// 新增 type/online，建议前端后续直接用 type 判断主模块/子模块
type LanClient struct {
	ModuleID string `json:"module_id"`
	Port     string `json:"port"`
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`

	Type   string `json:"type,omitempty"`   // "main" / "ap"
	Online bool   `json:"online,omitempty"` // main/AP 是否在线
}

// ------- ubus 辅助 -------

func pickLanIPv4(sid string) string {
	st, err := ubusCallJSONLocal(sid, "network.interface.lan", "status", nil)
	if err != nil {
		log.Printf("lanClients: pickLanIPv4 ubus error: %v", err)
		return ""
	}

	if arr, ok := st["ipv4-address"].([]any); ok && len(arr) > 0 {
		if m, ok2 := arr[0].(map[string]any); ok2 {
			if a, ok3 := m["address"].(string); ok3 && a != "" {
				return a
			}
		}
	}

	return ""
}

func getBridgeMAC(sid string) string {
	st, err := ubusCallJSONLocal(sid, "network.device", "status", map[string]any{
		"name": "br-lan",
	})
	if err != nil {
		log.Printf("lanClients: getBridgeMAC ubus error: %v", err)
		return ""
	}

	if mac, ok := st["macaddr"].(string); ok && mac != "" {
		return strings.ToUpper(mac)
	}

	return ""
}

// 主模块 hostname：优先读取 UCI，失败后降级 system board
func hostnameFromLocal(sid string) string {
	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONLocal(sid, "uci", "get", params)
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hn, ok2 := values["hostname"].(string); ok2 && hn != "" {
				return hn
			}
		}
	}

	b, err := ubusCallJSONLocal(sid, "system", "board", nil)
	if err == nil {
		if hn, ok := b["hostname"].(string); ok && hn != "" {
			return hn
		}
	}

	return ""
}

// 子模块 hostname：继续沿用你的 AnonSID 方案
// 前提：你已经在子模块 ACL 里允许对应 ubus 调用
func resolveRemoteHostname(ip string) string {

	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONAt(ip, AnonSID, "uci", "get", params)
	if err != nil {
		return ""
	}

	if values, ok := res["values"].(map[string]any); ok {
		if hn, ok2 := values["hostname"].(string); ok2 && hn != "" {
			return hn
		}
	}

	return ""
}

// ------- 探测与本地表解析 -------

// 并发 ping，但等待全部 ping 完成后再继续读 ARP
// 修复你之前 go ping 后马上读 ARP 的竞态问题
func probeStaticIPs(ips []string) {
	var wg sync.WaitGroup

	for _, ip := range ips {
		ip := ip
		wg.Add(1)

		go func() {
			defer wg.Done()

			// -c 1: 发 1 个包
			// -W 1: 最多等待 1 秒
			// OpenWrt busybox ping 支持这个写法
			if err := exec.Command("ping", "-c", "1", "-W", "1", ip).Run(); err != nil {
				// 离线是正常情况，不需要打 error log
				return
			}
		}()
	}

	wg.Wait()
}

// 只返回有效 ARP：
// flags == 0x2
// mac != 00:00:00:00:00:00
// device == br-lan
//
// 返回：ip -> mac
func parseARPByIP() map[string]string {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		log.Printf("lanClients: open /proc/net/arp error: %v", err)
		return map[string]string{}
	}
	defer f.Close()

	out := make(map[string]string)

	sc := bufio.NewScanner(f)
	first := true

	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}

		if first {
			first = false
			continue
		}

		fs := strings.Fields(ln)
		if len(fs) < 6 {
			continue
		}

		ip := fs[0]
		flags := strings.ToLower(fs[2])
		mac := strings.ToUpper(fs[3])
		dev := fs[5]

		if flags != "0x2" {
			continue
		}
		if mac == "" || mac == "00:00:00:00:00:00" {
			continue
		}
		if dev != "br-lan" {
			continue
		}

		out[ip] = mac
	}

	if err := sc.Err(); err != nil {
		log.Printf("lanClients: scan /proc/net/arp error: %v", err)
	}

	return out
}

// 读取 bridge fdb，建立 mac -> port 映射
// 比你之前只匹配 lan1-lan4 更宽松
func listLanEdgePortsByMAC() map[string]string {
	out := make(map[string]string)

	raw, err := exec.Command("bridge", "fdb", "show", "br", "br-lan").Output()
	if err != nil {
		log.Printf("lanClients: bridge fdb error: %v", err)
		return out
	}

	// 常见行：
	// 52:53:29:c5:88:a4 dev lan1 master br-lan
	// 52:53:29:c5:88:a4 dev wan master br-lan
	// 52:53:29:c5:88:a4 dev eth0 master br-lan
	reLine := regexp.MustCompile(`(?i)^([0-9a-f:]{17})\s+dev\s+([^\s]+)\b`)

	lines := strings.Split(string(raw), "\n")

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}

		lower := strings.ToLower(ln)

		// 跳过本机 bridge 自身、永久项、vlan 项
		if strings.Contains(lower, " self ") ||
			strings.Contains(lower, " permanent") ||
			strings.Contains(lower, " vlan ") {
			continue
		}

		m := reLine.FindStringSubmatch(ln)
		if len(m) != 3 {
			continue
		}

		mac := strings.ToUpper(m[1])
		port := m[2]

		// 跳过 br-lan 自己
		if port == "br-lan" {
			continue
		}

		// 同一个 MAC 如果出现多次，优先保留第一个物理端口
		if _, exists := out[mac]; !exists {
			out[mac] = port
		}
	}

	return out
}

// ------- HTTP Handler -------

func lanClientsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// 1) 取 sid
	auth := r.Header.Get("Authorization")
	sid := ""

	if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
		sid = auth[7:]
	}

	// sid 来自请求 Bearer(接口已由 withAuth 保证存在)。

	// 2) 主动探测静态 AP IP，并等待 ping 完成
	apIPs := APManagementIPs()
	probeStaticIPs(apIPs)

	// 3) 主模块自己
	main := LanClient{
		ModuleID: managedMainModuleID,
		Port:     "br-lan",
		MAC:      getBridgeMAC(sid),
		IP:       pickLanIPv4(sid),
		Hostname: hostnameFromLocal(sid),
		Type:     "main",
		Online:   true,
	}

	if main.Hostname == "" {
		main.Hostname = "(unknown)"
	}
	if main.IP == "" {
		main.IP = "(unknown)"
	}
	if main.MAC == "" {
		main.MAC = "(unknown)"
	}

	// 4) 读取 ARP 和 FDB
	arpByIP := parseARPByIP()
	portByMAC := listLanEdgePortsByMAC()

	// 5) 组装返回
	finalOut := make([]LanClient, 0, 1+len(apIPs))
	finalOut = append(finalOut, main)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range apIPs {
		mac, online := arpByIP[ip]
		if !online {
			// 按你的要求，离线 AP 不返回
			continue
		}

		port := portByMAC[mac]
		if port == "" {
			port = "unknown"
		}
		portN := lanPortNumber(port)

		client := LanClient{
			ModuleID: managedAntennaModuleID(portN),
			Port:     port,
			MAC:      mac,
			IP:       ip,
			// 显示名按物理口编号(RoobuckAP1..4),稳定不随 IP 变;设备真实 hostname 不改。
			Hostname: apDisplayName("RoobuckAP", portN),
			Type:     "ap",
			Online:   true,
		}

		idx := len(finalOut)
		finalOut = append(finalOut, client)

		// 并发读取真实 hostname,再按物理口编号
		wg.Add(1)
		go func(targetIdx, pN int, targetIP string) {
			defer wg.Done()

			realName := resolveRemoteHostname(targetIP)
			if realName == "" {
				return
			}

			mu.Lock()
			finalOut[targetIdx].Hostname = apDisplayName(realName, pN)
			mu.Unlock()
		}(idx, portN, ip)
	}

	wg.Wait()

	// Product order follows physical identity, never the transient management IP:
	// Router, Antenna1/lan1, ..., Antenna4/lan4. Unknown identities are kept at
	// the end with IP as a deterministic compatibility fallback.
	sort.SliceStable(finalOut, func(i, j int) bool {
		left := managedModuleDisplaySortKey(finalOut[i].ModuleID, finalOut[i].Type, finalOut[i].Port, finalOut[i].IP)
		right := managedModuleDisplaySortKey(finalOut[j].ModuleID, finalOut[j].Type, finalOut[j].Port, finalOut[j].IP)
		if left != right {
			return left < right
		}
		return finalOut[i].IP < finalOut[j].IP
	})

	_ = json.NewEncoder(w).Encode(finalOut)
}

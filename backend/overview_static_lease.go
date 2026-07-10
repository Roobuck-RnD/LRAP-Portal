package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"log"
	"os/exec"
)

// ---------- 小工具：把 Authorization 里的 SID 打个前缀，便于日志定位 ----------
func sid8FromAuth(h string) string {
	if len(h) > 7 && (strings.HasPrefix(h, "Bearer ") || strings.HasPrefix(h, "bearer ")) {
		s := h[7:]
		if len(s) > 8 {
			return s[:8]
		}
		return s
	}
	return ""
}

// ---------- 通用小工具 ----------

func chooseCaller(r *http.Request, remoteIP string) (func(string,string,map[string]any)(map[string]any,error), error) {
	auth := r.Header.Get("Authorization")
	log.Printf("chooseCaller: remoteIP=%q hasAuth=%t sid8=%s", remoteIP, auth != "", sid8FromAuth(auth))

	// 子模块：强制使用 unauth（零SID）
	if remoteIP != "" {
		const zeroSID = "00000000000000000000000000000000"
		log.Printf("chooseCaller: using REMOTE zeroSID -> %s", remoteIP)
		return func(object, method string, params map[string]any) (map[string]any, error) {
			return ubusCallJSONAt(remoteIP, zeroSID, object, method, params)
		}, nil
	}

	// 主模块：优先 Bearer，再本地登录
	sid := ""
	if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
		sid = auth[7:]
	}
	if sid != "" {
		log.Printf("chooseCaller: using BEARER sid=%s...", sid8FromAuth(auth))
		return func(object, method string, params map[string]any) (map[string]any, error) {
			return ubusCallJSONLocal(sid, object, method, params)
		}, nil
	}

	user := envOr("RPC_USER", "root")
	pass := envOr("RPC_PASS", "")
	log.Printf("chooseCaller: FALLBACK local login user=%s PASS_EMPTY=%t", user, pass == "")
	sid, _, err := ubusLoginLocal(user, pass)
	if err != nil || sid == "" {
		log.Printf("chooseCaller: local login FAILED: %v", err)
		return nil, fmt.Errorf("ubus login failed on local: %v", err)
	}
	log.Printf("chooseCaller: local login OK sid8=%s", sid[:8])
	return func(object, method string, params map[string]any) (map[string]any, error) {
		return ubusCallJSONLocal(sid, object, method, params)
	}, nil
}

func reloadDnsmasq(remoteIP string, call func(string, string, map[string]any) (map[string]any, error)) {
	// 🟢 情况 1：本机操作 (主路由)
	if remoteIP == "" {
		log.Printf("reloadDnsmasq [local]: executing SIGHUP...")
		
		// 直接发送信号，不经过 OpenWrt 的任何封装脚本
		// 这就好比外科医生做手术，只动那个神经，不碰周围的血管
		cmd := exec.Command("killall", "-HUP", "dnsmasq")
		
		// 获取输出，万一失败方便看日志
		output, err := cmd.CombinedOutput()
		
		if err != nil {
			// 🛑 重点：如果失败，只打印日志，绝对不 fallback 到 ubus！
			// 因为你的设备对 ubus 重载过敏，一碰就断网
			log.Printf("reloadDnsmasq [local] CRITICAL ERROR: killall failed: %v, output: %s", err, string(output))
			log.Printf("reloadDnsmasq [local]: SKIPPING fallback to avoid WiFi disconnect.")
		} else {
			log.Printf("reloadDnsmasq [local]: SUCCESS. SIGHUP sent.")
		}
		return
	}

	// 🔵 情况 2：远程操作 (子路由)
	// 子路由断网不影响你（主路由），所以可以用标准方法
	if call != nil {
		log.Printf("reloadDnsmasq [remote]: sending config.change to %s", remoteIP)
		_, err := call("service", "event", map[string]any{
			"type": "config.change",
			"data": map[string]any{"package": "dhcp"},
		})
		if err != nil {
			log.Printf("reloadDnsmasq [remote]: failed: %v", err)
		}
	}
}

// ---------- 静态租约 列表 ----------

type StaticEntry struct {
	Section string `json:"section"`
	MAC     string `json:"mac"`
	IP      string `json:"ip"`
	Name    string `json:"name,omitempty"`
}

func staticMapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	remoteIP := strings.TrimSpace(r.URL.Query().Get("ip"))
	auth := r.Header.Get("Authorization")
	log.Printf("static-map: start ip=%q hasAuth=%t sid8=%s", remoteIP, auth != "", sid8FromAuth(auth))

	call, err := chooseCaller(r, remoteIP)
	if err != nil {
		log.Printf("static-map: chooseCaller FAILED: %v", err)
		http.Error(w, `{"error":"`+escapeErr(err)+`"}`, http.StatusUnauthorized)
		return
	}

	// uci get dhcp
	resp, err := call("uci", "get", map[string]any{"config": "dhcp"})
	if err != nil {
		log.Printf("static-map: uci.get(dhcp) FAILED: %v (remoteIP=%q hasAuth=%t)", err, remoteIP, auth != "")
		http.Error(w, `{"error":"uci get dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}

	values, _ := resp["values"].(map[string]any)
	n := 0
	if values != nil { n = len(values) }
	log.Printf("static-map: uci.get(dhcp) OK sections=%d (remoteIP=%q)", n, remoteIP)

	out := make([]StaticEntry, 0, 16)
	for sid, vv := range values {
		m, _ := vv.(map[string]any)
		if m == nil || m[".type"] != "host" {
			continue
		}
		mac, _ := m["mac"].(string)
		ip, _ := m["ip"].(string)
		name, _ := m["name"].(string)
		if mac == "" || ip == "" {
			continue
		}
		out = append(out, StaticEntry{
			Section: sid,
			MAC:     strings.ToUpper(mac),
			IP:      ip,
			Name:    name,
		})
	}

	_ = json.NewEncoder(w).Encode(out)
}

// ---------- 设置/取消 静态租约 ----------

type staticLeaseIn struct {
	Hostname string `json:"hostname"`
	MAC      string `json:"mac"`
	IPAddr   string `json:"ipaddr"`
}

type staticLeaseOut struct {
	Updated bool   `json:"updated"` // true=更新，false=新增
	Section string `json:"section"`
}

func setStaticLeaseHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handleSetStatic(w, r)
	case http.MethodDelete:
		handleUnsetStatic(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleSetStatic(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    // 1. 获取 remoteIP
    remoteIP := strings.TrimSpace(r.URL.Query().Get("ip"))
    
    // 2. 获取 call 函数
    call, err := chooseCaller(r, remoteIP)
    if err != nil {
        http.Error(w, `{"error":"`+escapeErr(err)+`"}`, http.StatusUnauthorized)
        return
    }

    var in staticLeaseIn
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
        return
    }
    in.MAC = strings.ToUpper(strings.TrimSpace(in.MAC))
    in.IPAddr = strings.TrimSpace(in.IPAddr)
    in.Hostname = strings.TrimSpace(in.Hostname)
    if in.MAC == "" || in.IPAddr == "" {
        http.Error(w, `{"error":"mac and ipaddr are required"}`, http.StatusBadRequest)
        return
    }

    // 读现有 dhcp
    resp, err := call("uci", "get", map[string]any{"config": "dhcp"})
    if err != nil {
        http.Error(w, `{"error":"uci get dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
        return
    }
    values, _ := resp["values"].(map[string]any)

    // 找是否已存在（按 MAC）
    var target string
    for sid, vv := range values {
        m, _ := vv.(map[string]any)
        if m == nil || m[".type"] != "host" {
            continue
        }
        if strings.EqualFold(fmt.Sprintf("%v", m["mac"]), in.MAC) {
            target = sid
            break
        }
    }

    updated := true
    if target == "" {
        updated = false
        add, err := call("uci", "add", map[string]any{"config": "dhcp", "type": "host"})
        if err != nil {
            http.Error(w, `{"error":"uci add host failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
            return
        }
        target, _ = add["section"].(string)
        if target == "" {
            http.Error(w, `{"error":"uci add host returned empty section"}`, http.StatusInternalServerError)
            return
        }
    }

    // set 值
    vals := map[string]any{"mac": in.MAC, "ip": in.IPAddr}
    if in.Hostname != "" {
        vals["name"] = in.Hostname
    }
    if _, err := call("uci", "set", map[string]any{
        "config":  "dhcp",
        "section": target,
        "values":  vals,
    }); err != nil {
        http.Error(w, `{"error":"uci set failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
        return
    }
    
    // 提交更改
    if _, err := call("uci", "commit", map[string]any{"config": "dhcp"}); err != nil {
        http.Error(w, `{"error":"uci commit dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
        return
    }

    // 🟢 修正点在这里：必须传入 remoteIP
    // reloadDnsmasq(remoteIP, call)

    _ = json.NewEncoder(w).Encode(staticLeaseOut{Updated: updated, Section: target})
}

func handleUnsetStatic(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")

    // 1. 获取 remoteIP
    remoteIP := strings.TrimSpace(r.URL.Query().Get("ip"))
    
    // 2. 获取 call 函数
    call, err := chooseCaller(r, remoteIP)
    if err != nil {
        http.Error(w, `{"error":"`+escapeErr(err)+`"}`, http.StatusUnauthorized)
        return
    }

    var in struct{ MAC string `json:"mac"` }
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
        return
    }
    in.MAC = strings.ToUpper(strings.TrimSpace(in.MAC))
    if in.MAC == "" {
        http.Error(w, `{"error":"mac is required"}`, http.StatusBadRequest)
        return
    }

    // 找 section
    resp, err := call("uci", "get", map[string]any{"config": "dhcp"})
    if err != nil {
        http.Error(w, `{"error":"uci get dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
        return
    }
    values, _ := resp["values"].(map[string]any)

    var target string
    for sid, vv := range values {
        m, _ := vv.(map[string]any)
        if m == nil || m[".type"] != "host" {
            continue
        }
        if strings.EqualFold(fmt.Sprintf("%v", m["mac"]), in.MAC) {
            target = sid
            break
        }
    }
    if target == "" {
        // 本就不存在，当作成功
        _ = json.NewEncoder(w).Encode(map[string]any{"deleted": false})
        return
    }

    if _, err := call("uci", "delete", map[string]any{"config": "dhcp", "section": target}); err != nil {
        http.Error(w, `{"error":"uci delete failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
        return
    }
    
    // 提交更改
    if _, err := call("uci", "commit", map[string]any{"config": "dhcp"}); err != nil {
        http.Error(w, `{"error":"uci commit dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
        return
    }

    // 🟢 修正点在这里：必须传入 remoteIP
    // reloadDnsmasq(remoteIP, call)

    _ = json.NewEncoder(w).Encode(map[string]any{"deleted": true, "section": target})
}
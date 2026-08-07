// main.go
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

// ---------- 通用类型 ----------

type ubusReq struct {
	Jsonrpc string        `json:"jsonrpc"`
	ID      int           `json:"id"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
}

type ubusResp struct {
	Result []interface{} `json:"result"`
	Error  any           `json:"error"`
}

// ---------- 基础工具 ----------

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func newHTTPClient() *http.Client {
	insecure := os.Getenv("INSECURE_TLS") == "1"
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: insecure},
		DisableKeepAlives:   true,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 128,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

// ---------- 访问日志中间件 ----------

type statusWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(sw, r)
		log.Printf("access %s %s %d %dB %v", clientIP(r), r.URL.Path, sw.status, sw.size, time.Since(start))
	})
}

// ---------- CORS 中间件（补充 DELETE） ----------

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", envOr("CORS_ORIGIN", "*"))
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- ubus 登录本机 ----------

func ubusLoginLocal(user, pass string) (sid string, extra map[string]any, err error) {
	ubusURL := envOr("UBUS_URL", "http://127.0.0.1/ubus")
	body, _ := json.Marshal(ubusReq{
		Jsonrpc: "2.0", ID: 1, Method: "call",
		Params: []any{
			AnonSID,
			"session", "login",
			map[string]string{"username": user, "password": pass},
		},
	})
	resp, err := newHTTPClient().Post(ubusURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var out ubusResp
	_ = json.Unmarshal(raw, &out)
	if len(out.Result) < 1 {
		return "", nil, fmt.Errorf("ubus bad result body=%s", string(raw))
	}
	// 成功应为 [0, {...}]；但也做容错
	if code, _ := out.Result[0].(float64); int(code) != 0 {
		return "", nil, fmt.Errorf("ubus code=%d body=%s", int(code), string(raw))
	}
	if len(out.Result) < 2 {
		return "", nil, fmt.Errorf("no body for login: %s", string(raw))
	}
	m, _ := out.Result[1].(map[string]any)
	sid, _ = m["ubus_rpc_session"].(string)
	if sid == "" {
		return "", nil, fmt.Errorf("no sid body=%s", string(raw))
	}
	return sid, m, nil
}

// ---------- 通用：ubus 调用（本机/远程）+ 统一解析 ----------

// 修复版：既能解析标准的 [0, {...}]，也接受成功但无数据的 [0]
func parseUbusResp(r io.Reader) (map[string]any, error) {
	raw, _ := io.ReadAll(r)
	var out ubusResp
	_ = json.Unmarshal(raw, &out)

	// 情况 A：仅有 result:[0] → 成功但无数据
	if len(out.Result) == 1 {
		if code, _ := out.Result[0].(float64); int(code) == 0 {
			return map[string]any{}, nil
		}
		// 非 0 继续走下面逻辑报错
	}

	// 情况 B：标准返回 result:[0, {...}]
	if len(out.Result) >= 2 {
		if code, _ := out.Result[0].(float64); int(code) != 0 {
			return nil, fmt.Errorf("ubus code=%d body=%s", int(code), string(raw))
		}
		m, _ := out.Result[1].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		return m, nil
	}

	// 其它：视为格式异常
	return nil, fmt.Errorf("ubus bad result body=%s", string(raw))
}

// 本机（使用 UBUS_URL）
func ubusCallJSONLocal(sid, object, method string, params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	ubusURL := envOr("UBUS_URL", "http://127.0.0.1/ubus")
	reqBody, _ := json.Marshal(ubusReq{
		Jsonrpc: "2.0", ID: 2, Method: "call",
		Params: []any{sid, object, method, params},
	})
	resp, err := newHTTPClient().Post(ubusURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseUbusResp(resp.Body)
}

// 远程（http://<ip>/ubus）
func ubusCallJSONAt(ip, sid, object, method string, params map[string]any) (map[string]any, error) {
	if params == nil {
		params = map[string]any{}
	}
	target := (&url.URL{Scheme: "http", Host: ip, Path: "/ubus"}).String()
	body, _ := json.Marshal(ubusReq{
		Jsonrpc: "2.0", ID: 2, Method: "call",
		Params: []any{sid, object, method, params},
	})
	resp, err := newHTTPClient().Post(target, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parseUbusResp(resp.Body)
}

// ---------- 会话 & 健康检查 ----------

func createSessionHandler(w http.ResponseWriter, r *http.Request) {
	type inT struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	type outT struct {
		Token   string `json:"token"`
		Timeout int    `json:"timeout,omitempty"`
		Expires int    `json:"expires,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var in inT
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Username == "" || in.Password == "" {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}

	sid, extra, err := ubusLoginLocal(in.Username, in.Password)
	if err != nil || sid == "" {
		log.Printf("session failed user=%s from=%s err=%v", in.Username, r.RemoteAddr, err)
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	out := outT{Token: sid}
	if v, ok := extra["timeout"].(float64); ok {
		out.Timeout = int(v)
	}
	if v, ok := extra["expires"].(float64); ok {
		out.Expires = int(v)
	}

	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(out)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// ---------- 入口 ----------
// 说明：以下 handler（memory/network/leases/static-lease/static-map/clients）
// 来自你项目里的其他 .go 文件，这里只做路由注册。
func main() {
	log.SetPrefix("[lrapServer] ")
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Enforce the private Antenna DHCP pool before accepting API requests. This
	// also migrates older scalar tag options to the UCI list form required by
	// OpenWrt's dnsmasq generator and asynchronously repairs any Antenna lease
	// that previously escaped into the ordinary client pool.
	repairProtectedDHCPAtStartup()

	mux := http.NewServeMux()

	// ---- 公开端点（无需登录）----
	mux.HandleFunc("/api/sessions", createSessionHandler) // 登录
	mux.HandleFunc("/healthz", healthHandler)
	// AP 拉取固件用，靠一次性能力令牌授权，不走会话鉴权
	mux.HandleFunc("/api/system/firmware/download", firmwareDownloadHandler)

	// ---- 受保护端点：统一用 withAuth 校验 ubus 会话 ----
	// overview_* handlers（存在于其他文件）
	mux.HandleFunc("/api/status/overview/system", withAuth(systemOverviewHandler))
	mux.HandleFunc("/api/status/overview/memory", withAuth(memoryOverviewHandler))
	mux.HandleFunc("/api/status/overview/network", withAuth(networkOverviewHandler))
	mux.HandleFunc("/api/status/connected-clients", withAuth(connectedClientsHandler))

	// DHCP & 静态租约（存在于其他文件）
	mux.HandleFunc("/api/lan/leases", withAuth(lanLeasesHandler))
	mux.HandleFunc("/api/lan/leases/reset", withAuth(resetDhcpLeasesHandler))
	mux.HandleFunc("/api/lan/static-lease", withAuth(setStaticLeaseHandler))
	mux.HandleFunc("/api/lan/static-map", withAuth(staticMapHandler))
	mux.HandleFunc("/api/lan/clients", withAuth(lanClientsHandler))
	// arp
	mux.HandleFunc("/api/net/arp", withAuth(arpHandler))
	mux.HandleFunc("/api/net/routes", withAuth(routesHandler))
	//system page
	mux.HandleFunc("/api/system/general", withAuth(systemGeneralHandler))
	mux.HandleFunc("/api/system/timezones", withAuth(timezonesHandler))
	// firmware
	mux.HandleFunc("/api/system/firmware/flash", withAuth(firmwareFlashHandler))

	// 修改密码接口
	mux.HandleFunc("/api/system/password", withAuth(changePasswordHandler))
	// wifi ssid/password
	mux.HandleFunc("/api/mtk/wifi", withAuth(mtkWifiHandler))
	// reboot
	mux.HandleFunc("/api/system/reboot", withAuth(systemRebootHandler))
	// network/routes
	mux.HandleFunc("/api/net/static-routes", withAuth(staticRoutesConfigHandler))
	mux.HandleFunc("/api/net/interfaces", withAuth(interfacesHandler))
	// firewall
	mux.HandleFunc("/api/net/firewall", withAuth(firewallHandler))
	// static leases config
	mux.HandleFunc("/api/lan/static-leases-config", withAuth(staticLeaseConfigHandler))

	addr := envOr("LISTEN_ADDR", ":9080")
	cert := os.Getenv("TLS_CERT_FILE")
	key := os.Getenv("TLS_KEY_FILE")

	srv := withCORS(withAccessLog(mux))

	log.Printf("listening on %s (UBUS_URL=%s)", addr, envOr("UBUS_URL", "http://127.0.0.1/ubus"))
	if cert != "" && key != "" {
		log.Fatal(http.ListenAndServeTLS(addr, cert, key, srv))
	} else {
		log.Fatal(http.ListenAndServe(addr, srv))
	}
}

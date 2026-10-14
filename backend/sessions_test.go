package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubAccounts stands in for rpcd and passwd: root and admin with known
// passwords, and a privileged session id the handlers should be handed.
func stubAccounts(t *testing.T) map[string]string {
	t.Helper()
	passwords := map[string]string{"root": "staff-secret", "admin": "customer-pass"}
	oldVerify, oldMint, oldSet := verifyPassword, privilegedSID, setPassword
	verifyPassword = func(user, pass string) bool { p, ok := passwords[user]; return ok && p == pass }
	privilegedSID = func() (string, error) { return "privileged-sid", nil }
	setPassword = func(user, pass string) error { passwords[user] = pass; return nil }
	sessions.Lock()
	sessions.m = map[string]*portalSession{}
	sessions.Unlock()
	failures.Lock()
	failures.m = map[string][]time.Time{}
	failures.Unlock()
	t.Cleanup(func() { verifyPassword, privilegedSID, setPassword = oldVerify, oldMint, oldSet })
	return passwords
}

func call(t *testing.T, h http.HandlerFunc, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "10.10.18.100:5555"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h(w, r)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func signInAs(t *testing.T, user, pass string) string {
	t.Helper()
	code, out := call(t, sessionsHandler, http.MethodPost, "/api/sessions", "", `{"username":"`+user+`","password":"`+pass+`"}`)
	if code != http.StatusCreated {
		t.Fatalf("sign in as %s: %d %v", user, code, out)
	}
	return out["token"].(string)
}

// The customer's account signs in, can use what the portal offers, and the
// handler acts with the backend's own ubus session, never the account's.
func TestCustomerSignsInAndHandlersActWithTheBackendsSession(t *testing.T) {
	stubAccounts(t)
	token := signInAs(t, "admin", "customer-pass")
	if !strings.HasPrefix(token, portalTokenPrefix) {
		t.Fatalf("token %q", token)
	}
	if code, out := call(t, sessionsHandler, http.MethodGet, "/api/sessions", token, ""); code != 200 || out["username"] != "admin" || out["role"] != "user" {
		t.Fatalf("who am I: %d %v", code, out)
	}
	var seenAuth, seenUser string
	h := withAuth(func(w http.ResponseWriter, r *http.Request) {
		seenAuth, seenUser = r.Header.Get("Authorization"), sessionUser(r)
	})
	if code, _ := call(t, h, http.MethodPost, "/api/mtk/wifi", token, ""); code != 200 {
		t.Fatalf("protected call: %d", code)
	}
	if seenAuth != "Bearer privileged-sid" || seenUser != "admin" {
		t.Fatalf("handler saw %q as %q", seenAuth, seenUser)
	}
	if code, _ := call(t, sessionsHandler, http.MethodDelete, "/api/sessions", token, ""); code != http.StatusNoContent {
		t.Fatalf("sign out: %d", code)
	}
	if code, _ := call(t, h, http.MethodGet, "/api/mtk/wifi", token, ""); code != http.StatusUnauthorized {
		t.Fatalf("a signed-out token still works: %d", code)
	}
}

func TestWrongPasswordsAreRefusedAndThenLockedOut(t *testing.T) {
	stubAccounts(t)
	for i := 0; i < loginFailures; i++ {
		if code, _ := call(t, sessionsHandler, http.MethodPost, "/api/sessions", "", `{"username":"admin","password":"guess"}`); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code, _ := call(t, sessionsHandler, http.MethodPost, "/api/sessions", "", `{"username":"admin","password":"customer-pass"}`); code != http.StatusTooManyRequests {
		t.Fatalf("after %d wrong passwords: %d", loginFailures, code)
	}
	if code, _ := call(t, sessionsHandler, http.MethodPost, "/api/sessions", "", `{"username":"Admin; reboot","password":"x"}`); code == http.StatusCreated {
		t.Fatal("a name that is not an account signed in")
	}
}

func TestSessionsEndWhenIdle(t *testing.T) {
	stubAccounts(t)
	token := signInAs(t, "admin", "customer-pass")
	sessions.Lock()
	sessions.m[token].seen = time.Now().Add(-sessionIdle - time.Second)
	sessions.Unlock()
	if _, ok := lookupSession(token); ok {
		t.Fatal("an idle session is still alive")
	}
}

// A password change is the signed-in account's own, needs no current password
// and no particular length, and signs every session of the account out.
// root's is not the customer's to change.
func TestPasswordChangeIsForTheSignedInAccount(t *testing.T) {
	passwords := stubAccounts(t)
	token := signInAs(t, "admin", "customer-pass")
	other := signInAs(t, "admin", "customer-pass")
	h := withAuth(changePasswordHandler)
	if code, _ := call(t, h, http.MethodPost, "/api/system/password", token, `{"new_password":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty password: %d", code)
	}
	if code, _ := call(t, h, http.MethodPost, "/api/system/password", token, `{"new_password":"a
b"}`); code != http.StatusBadRequest {
		t.Fatalf("password with a line break: %d", code)
	}
	if code, out := call(t, h, http.MethodPost, "/api/system/password", token, `{"new_password":"abc"}`); code != 200 {
		t.Fatalf("change: %d %v", code, out)
	}
	if passwords["admin"] != "abc" || passwords["root"] != "staff-secret" {
		t.Fatalf("passwords now %v", passwords)
	}
	for _, tok := range []string{token, other} {
		if _, ok := lookupSession(tok); ok {
			t.Fatal("a session survived the password change")
		}
	}
}

// The agent presents a genuine rpcd session, as it always has; it is checked
// with rpcd and passed through untouched, and it belongs to no account.
func TestTheAgentsRpcdSessionStillWorks(t *testing.T) {
	stubAccounts(t)
	rpcd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ubusReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Params[0] == "0123456789abcdef0123456789abcdef" {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":[0,{"values":{}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":2,"result":[6]}`))
	}))
	defer rpcd.Close()
	t.Setenv("UBUS_URL", rpcd.URL)
	var seenAuth, seenUser string
	h := withAuth(func(w http.ResponseWriter, r *http.Request) {
		seenAuth, seenUser = r.Header.Get("Authorization"), sessionUser(r)
	})
	if code, _ := call(t, h, http.MethodGet, "/api/mtk/easymesh", "0123456789abcdef0123456789abcdef", ""); code != 200 {
		t.Fatalf("agent session: %d", code)
	}
	if seenAuth != "Bearer 0123456789abcdef0123456789abcdef" || seenUser != "" {
		t.Fatalf("handler saw %q as %q", seenAuth, seenUser)
	}
	if code, _ := call(t, h, http.MethodGet, "/api/mtk/easymesh", "ffffffffffffffffffffffffffffffff", ""); code != http.StatusUnauthorized {
		t.Fatalf("forged session: %d", code)
	}
	if code, _ := call(t, withAuth(changePasswordHandler), http.MethodPost, "/api/system/password", "0123456789abcdef0123456789abcdef",
		`{"new_password":"y"}`); code != http.StatusForbidden {
		t.Fatalf("the agent changed a password: %d", code)
	}
}

// The API answers the portal's own pages, on the same host, and no other site.
func TestOnlyTheDevicesOwnPagesMayCallTheAPI(t *testing.T) {
	for _, c := range []struct {
		host, origin, configured, want string
	}{
		{"10.10.18.1:9080", "http://10.10.18.1", "*", "http://10.10.18.1"},
		{"10.10.18.1:9080", "https://10.10.18.1", "", "https://10.10.18.1"},
		{"192.168.1.143:9080", "http://192.168.1.143", "*", "http://192.168.1.143"},
		{"10.10.18.1:9080", "http://evil.example", "*", ""},
		{"10.10.18.1:9080", "http://10.10.18.1.evil.example", "*", ""},
		{"10.10.18.1:9080", "file://", "*", ""},
		{"10.10.18.1:9080", "", "*", ""},
		{"10.10.18.1:9080", "http://localhost:5173", "http://localhost:5173", "http://localhost:5173"},
		{"10.10.18.1:9080", "http://10.10.18.1", "http://localhost:5173", ""},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://"+c.host+"/api/mtk/wifi", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := allowedOrigin(r, c.configured); got != c.want {
			t.Errorf("host %s origin %q configured %q: allowed %q, want %q", c.host, c.origin, c.configured, got, c.want)
		}
	}
}

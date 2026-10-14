package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Who signs in, and with what.
//
// The device has two accounts. root is for our own staff. admin is the
// customer's: the same password signs in here and over SSH, where it is an
// ordinary unprivileged user and can look but not change anything. Here it can
// use everything the portal offers.
//
// So a person's password only ever proves who they are. rpcd checks it (its
// login sections point at each account's /etc/shadow entry), the rpcd session
// that login creates is destroyed straight away, and the portal issues a
// session of its own. What the portal then does, it does with a ubus session
// it mints for itself as root (privilegedSID): the admin account's own rpcd
// rights are only enough to sign in, so its password used directly against
// /ubus gets nothing.
//
// The device's own agent (lrap-buoy) is the other caller. It mints a short,
// read-only rpcd session through the ubus socket and presents that, as it
// always has; withAuth still accepts a genuine rpcd session.

// portalTokenPrefix tells a portal session from an rpcd session id (32 hex
// characters) at a glance.
const portalTokenPrefix = "lrap."

const (
	// A session lasts while it is used, and no longer than a working day.
	sessionIdle = 30 * time.Minute
	sessionMax  = 12 * time.Hour

	// Wrong passwords from one address, or for one account, before sign-in is
	// refused for a while: rpcd itself never says no to guessing.
	loginFailures = 5
	loginWindow   = 10 * time.Minute
)

type portalSession struct {
	user          string
	created, seen time.Time
}

var sessions = struct {
	sync.Mutex
	m map[string]*portalSession
}{m: map[string]*portalSession{}}

type userKey struct{}

// sessionUser is the account the request was signed in with, or "" for the
// agent's rpcd session.
func sessionUser(r *http.Request) string {
	u, _ := r.Context().Value(userKey{}).(string)
	return u
}

func roleOf(user string) string {
	if user == "root" {
		return "root"
	}
	return "user"
}

func newPortalToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return portalTokenPrefix + hex.EncodeToString(b)
}

// lookupSession finds a live session and marks it used.
func lookupSession(token string) (string, bool) {
	now := time.Now()
	sessions.Lock()
	defer sessions.Unlock()
	s, ok := sessions.m[token]
	if !ok {
		return "", false
	}
	if now.Sub(s.seen) > sessionIdle || now.Sub(s.created) > sessionMax {
		delete(sessions.m, token)
		return "", false
	}
	s.seen = now
	return s.user, true
}

// endSessionsOf ends every session of user except the one named.
func endSessionsOf(user, except string) {
	sessions.Lock()
	defer sessions.Unlock()
	for t, s := range sessions.m {
		if s.user == user && t != except {
			delete(sessions.m, t)
		}
	}
}

// ---------- the ubus sessions the backend uses itself ----------

// runUbus calls ubus over its local socket as root, where no session is
// needed. Arguments are passed as they are, never through a shell.
var runUbus = func(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ubus", args...).Output()
	return string(out), err
}

// verifyPassword asks rpcd whether the password is right, and destroys the
// session that answer comes with.
var verifyPassword = func(user, pass string) bool {
	sid, _, err := ubusLoginLocal(user, pass)
	if err != nil || sid == "" {
		return false
	}
	destroyUbusSession(sid)
	return true
}

func destroyUbusSession(sid string) {
	body, _ := json.Marshal(map[string]string{"ubus_rpc_session": sid})
	if _, err := runUbus("call", "session", "destroy", string(body)); err != nil {
		log.Printf("could not destroy a sign-in's rpcd session: %v", err)
	}
}

// The privileged session is minted once and reused for a while; rpcd extends
// it on every use. A replaced one is left to expire rather than destroyed, so
// a request already holding it still finishes.
const (
	privilegedTimeout = 30 * 60 // seconds, as rpcd counts them
	privilegedReuse   = 10 * time.Minute
)

var minted = struct {
	sync.Mutex
	sid string
	at  time.Time
}{}

var privilegedSID = func() (string, error) {
	minted.Lock()
	defer minted.Unlock()
	if minted.sid != "" && time.Since(minted.at) < privilegedReuse {
		return minted.sid, nil
	}
	raw, err := runUbus("call", "session", "create", fmt.Sprintf(`{"timeout":%d}`, privilegedTimeout))
	if err != nil {
		return "", fmt.Errorf("mint a ubus session: %w", err)
	}
	var created struct {
		SID string `json:"ubus_rpc_session"`
	}
	if json.Unmarshal([]byte(raw), &created) != nil || created.SID == "" {
		return "", fmt.Errorf("mint a ubus session: unexpected answer %q", raw)
	}
	// Everything the handlers do through rpcd: any ubus call, every uci config
	// both ways, and the file plugin's operations.
	for _, grant := range []string{
		`{"ubus_rpc_session":%q,"scope":"ubus","objects":[["*","*"]]}`,
		`{"ubus_rpc_session":%q,"scope":"uci","objects":[["*","read"],["*","write"]]}`,
		`{"ubus_rpc_session":%q,"scope":"file","objects":[["*","read"],["*","write"],["*","exec"],["*","list"],["*","stat"]]}`,
	} {
		if _, err := runUbus("call", "session", "grant", fmt.Sprintf(grant, created.SID)); err != nil {
			return "", fmt.Errorf("grant the ubus session: %w", err)
		}
	}
	minted.sid, minted.at = created.SID, time.Now()
	return minted.sid, nil
}

// ---------- refusing guesses ----------

var failures = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

func recentFailures(key string, now time.Time) int {
	kept := failures.m[key][:0]
	for _, t := range failures.m[key] {
		if now.Sub(t) < loginWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(failures.m, key)
	} else {
		failures.m[key] = kept
	}
	return len(kept)
}

// loginRefused says whether this address or this account has guessed wrong
// too often lately.
func loginRefused(ip, user string) bool {
	now := time.Now()
	failures.Lock()
	defer failures.Unlock()
	return recentFailures("ip:"+ip, now) >= loginFailures || recentFailures("user:"+user, now) >= loginFailures
}

func recordLoginFailure(ip, user string) {
	now := time.Now()
	failures.Lock()
	defer failures.Unlock()
	failures.m["ip:"+ip] = append(failures.m["ip:"+ip], now)
	failures.m["user:"+user] = append(failures.m["user:"+user], now)
}

func clearLoginFailures(ip, user string) {
	failures.Lock()
	defer failures.Unlock()
	delete(failures.m, "ip:"+ip)
	delete(failures.m, "user:"+user)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// accountName is what a device account can be called; anything else is not
// passed on to rpcd or passwd at all.
var accountName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ---------- /api/sessions ----------

// sessionsHandler signs in (POST), says who is signed in (GET) and signs out
// (DELETE).
func sessionsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodPost:
		signIn(w, r)
	case http.MethodGet:
		user, ok := lookupSession(bearerSID(r))
		if !ok {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"username": user, "role": roleOf(user)})
	case http.MethodDelete:
		sessions.Lock()
		delete(sessions.m, bearerSID(r))
		sessions.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func signIn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || in.Username == "" || in.Password == "" {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	ip := remoteHost(r)
	if loginRefused(ip, in.Username) {
		w.Header().Set("Retry-After", fmt.Sprint(int(loginWindow.Seconds())))
		http.Error(w, `{"error":"too many attempts"}`, http.StatusTooManyRequests)
		return
	}
	if !accountName.MatchString(in.Username) || !verifyPassword(in.Username, in.Password) {
		recordLoginFailure(ip, in.Username)
		log.Printf("sign-in failed user=%q from=%s", in.Username, ip)
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	clearLoginFailures(ip, in.Username)
	now := time.Now()
	token := newPortalToken()
	sessions.Lock()
	sessions.m[token] = &portalSession{user: in.Username, created: now, seen: now}
	sessions.Unlock()
	log.Printf("signed in user=%q from=%s", in.Username, ip)
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":    token,
		"timeout":  int(sessionIdle.Seconds()),
		"username": in.Username,
		"role":     roleOf(in.Username),
	})
}

// isPortalToken tells the two kinds of caller apart.
func isPortalToken(token string) bool { return strings.HasPrefix(token, portalTokenPrefix) }

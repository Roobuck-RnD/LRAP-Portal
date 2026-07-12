package main

import (
	"net/http"
	"strings"
)

// bearerSID extracts the session id from an "Authorization: Bearer <sid>"
// header. It returns "" when the header is missing or malformed.
func bearerSID(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return ""
	}
	return strings.TrimSpace(auth[len("Bearer "):])
}

// ubusSessionValid reports whether sid is a genuine, unexpired ubus session on
// the local device. It does not trust the caller: it asks rpcd to perform an
// authenticated read (`uci get system`) using sid. Only a real login session
// holds the ACLs to do this; the anonymous zero SID and any forged SID are
// denied by rpcd and produce an error, so this returns false for them.
func ubusSessionValid(sid string) bool {
	if sid == "" || sid == AnonSID {
		return false
	}
	_, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "system",
	})
	return err == nil
}

// withAuth is the single enforcement point for authenticated endpoints. It
// rejects any request that does not carry a valid ubus session token before the
// wrapped handler runs, so individual handlers no longer need to (and must not)
// decide on their own whether the Authorization header is trustworthy.
func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !ubusSessionValid(bearerSID(r)) {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

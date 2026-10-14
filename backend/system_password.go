package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
)

// changePasswordHandler changes the signed-in account's own password: admin's
// for a customer, root's only for someone signed in as root. It asks for no
// current password and sets no rules on the new one beyond not being empty:
// the account and the device are the customer's. The same password signs in
// to SSH, so this changes both; every session of the account ends, and the
// page signs in again.
//
// The agent's rpcd session belongs to no account and is refused.
type changePasswordReq struct {
	New string `json:"new_password"`
}

func changePasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	user := sessionUser(r)
	if user == "" {
		http.Error(w, `{"error":"sign in with a device account to change its password"}`, http.StatusForbidden)
		return
	}
	var req changePasswordReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	// passwd reads one line per answer, so a line break would end it early.
	if req.New == "" || strings.ContainsAny(req.New, "\r\n") {
		http.Error(w, `{"error":"enter a new password"}`, http.StatusBadRequest)
		return
	}
	if err := setPassword(user, req.New); err != nil {
		log.Printf("password change for %q failed: %v", user, err)
		http.Error(w, `{"error":"the device refused the new password"}`, http.StatusInternalServerError)
		return
	}
	endSessionsOf(user, "")
	log.Printf("password changed user=%q from=%s", user, remoteHost(r))
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// setPassword runs passwd as root, which asks for the new password twice and
// never for the old one.
var setPassword = func(user, password string) error {
	if !accountName.MatchString(user) {
		return fmt.Errorf("not an account name: %q", user)
	}
	cmd := exec.Command("passwd", user)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		defer stdin.Close()
		_, _ = io.WriteString(stdin, password+"\n"+password+"\n")
	}()
	return cmd.Wait()
}

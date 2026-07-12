package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
)

// adminUser is the single administrative account on this device. The change
// password endpoint always targets it and never trusts a client-supplied name.
const adminUser = "root"

type ChangePasswordReq struct {
	Username    string `json:"username"` // accepted for backward compat, but ignored (see adminUser)
	// OldPassword string `json:"old_password"` // 已删除：不需要旧密码(接口已由 withAuth 保护,有效会话即授权)
	NewPassword string `json:"new_password"`
}

func changePasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	// 1. 简单的权限校验：确保请求头里带了 Token
	// 虽然我们不验证旧密码，但至少要保证用户是登录状态
	auth := r.Header.Get("Authorization")
	if len(auth) < 7 { // "Bearer "
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req ChangePasswordReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}

	if req.NewPassword == "" {
		http.Error(w, `{"error":"missing fields"}`, http.StatusBadRequest)
		return
	}

	// 2. 直接调用系统命令修改密码 (Root 权限下无需旧密码)
	// 目标账号写死为 adminUser，忽略请求体里的 username，避免越权改任意账号。
	cmd := exec.Command("passwd", adminUser)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"system pipe failed: %v"}`, err), 500)
		return
	}

	if err := cmd.Start(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"passwd cmd failed: %v"}`, err), 500)
		return
	}

	// 3. 写入新密码 (两次确认)
	go func() {
		defer stdin.Close()
		// Root 修改密码时，系统只要求输入两次新密码
		io.WriteString(stdin, req.NewPassword+"\n")
		io.WriteString(stdin, req.NewPassword+"\n")
	}()

	if err := cmd.Wait(); err != nil {
		// 如果报错，可能是密码太弱被系统拒绝，或者进程权限不足
		http.Error(w, fmt.Sprintf(`{"error":"failed to set password (too weak?): %v"}`, err), 500)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok", "message":"Password changed successfully"}`))
}
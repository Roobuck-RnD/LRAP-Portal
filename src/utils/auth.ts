// utils/auth.ts
import { apiFetch } from "./http";

// The session token and the account's role ("root" for our own staff, "user"
// for the customer's admin account), or null when sign-in is refused.
export async function authenticateWithOpenWrt(username: string, password: string): Promise<{ token: string; role: string } | null> {
  try {
    const res = await apiFetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
      // 如果你后面改成 HttpOnly Cookie 模式，就加上：
      // credentials: 'include',
    });

    if (!res.ok) return null;             // 401 等都直接返回 null

    // A portal session token (backend/sessions.go), not an rpcd session id.
    const data: { token?: string; role?: string } = await res.json();
    return data.token ? { token: data.token, role: data.role ?? 'user' } : null;
  } catch (e) {
    console.error('Fail to Sign in', e);
    return null;
  }
}

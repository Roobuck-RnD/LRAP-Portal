// utils/auth.ts
import { apiFetch } from "./http";

export async function authenticateWithOpenWrt(username: string, password: string) {
  try {
    const res = await apiFetch('/api/sessions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
      // 如果你后面改成 HttpOnly Cookie 模式，就加上：
      // credentials: 'include',
    });

    if (!res.ok) return null;             // 401 等都直接返回 null

    const data: { token?: string } = await res.json();
    return data.token ?? null;            // token 就是 ubus sid
  } catch (e) {
    console.error('Fail to Sign in', e);
    return null;
  }
}

import { toast } from 'sonner';

export const API_BASE = import.meta.env.VITE_API_BASE || '';

// Endpoints that handle their own auth failures (the login call itself must be
// able to return 401 without triggering the "session expired" flow).
const AUTH_EXEMPT = ['/api/sessions'];

// Guards against a burst of concurrent polls all firing the toast/redirect at
// once. Re-armed shortly after so a later genuine expiry is still caught.
let handlingUnauthorized = false;

function isAuthExempt(path: string): boolean {
  return AUTH_EXEMPT.some((p) => path.startsWith(p));
}

// Attaches the stored bearer token so callers never have to. Skips the login
// endpoint and never overrides a caller-supplied Authorization header. Only
// touches Authorization — Content-Type is left to the caller so that FormData
// (multipart) uploads keep their browser-generated boundary.
function withAuthHeader(path: string, init?: RequestInit): RequestInit | undefined {
  if (isAuthExempt(path)) return init;
  const token = sessionStorage.getItem('token')?.trim();
  if (!token) return init;

  const headers = new Headers(init?.headers);
  if (!headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${token}`);
  }
  return { ...init, headers };
}

export async function apiFetch(path: string, init?: RequestInit) {
  const res = await fetch(`${API_BASE}${path}`, withAuthHeader(path, init));

  if (res.status === 401 && !isAuthExempt(path)) {
    handleUnauthorized();
  }

  return res;
}

function handleUnauthorized() {
  const wasLoggedIn = sessionStorage.getItem('isLoggedIn') === 'true';

  sessionStorage.removeItem('isLoggedIn');
  sessionStorage.removeItem('token');
  sessionStorage.removeItem('username');

  if (handlingUnauthorized) return;
  handlingUnauthorized = true;

  // Only surface a message to a user who thought they were still signed in.
  if (wasLoggedIn) {
    toast.error('Session expired', { description: 'Please sign in again.' });
  }

  // HashRouter keeps the route after the '#', so navigating the hash lands on
  // the login screen without a full page reload (the toast stays visible).
  window.location.hash = '#/login';

  window.setTimeout(() => {
    handlingUnauthorized = false;
  }, 1500);
}

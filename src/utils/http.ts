import { toast } from 'sonner';
import useDevModeStore from '@/states/devModeState';
import { clearPageDataCache } from '@/utils/page-data-cache';

// The portal is served by uhttpd on :80 while the API lives on :9080 of the SAME
// device, so the base has to be absolute — but it must follow whichever device
// the operator actually opened, not a fixed address.
//
// Hardcoding a host here breaks the moment there is more than one AC: in a Mesh
// the Agent serves its own copy of this page, and every call from it went to the
// Root instead, so the Agent's portal silently showed the Root's node role,
// radios and Antennas.
//
// VITE_API_BASE stays available as an override for `npm run dev`, where the page
// is served from a laptop and the device is elsewhere.
function deviceApiBase(): string {
  if (typeof window === 'undefined') return '';
  const { protocol, hostname, port } = window.location;
  // Already served from the API itself (or a proxy in front of it): same origin.
  if (port === '9080') return '';
  return `${protocol}//${hostname}:9080`;
}

export const API_BASE = import.meta.env.VITE_API_BASE || deviceApiBase();

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
  // During the non-cancellable reboot screen the old ubus session will
  // naturally disappear when the AC restarts. Keep the in-memory countdown
  // visible; the reboot page clears auth and navigates to login at its deadline.
  if (
    sessionStorage.getItem('rebootPending') === 'true' ||
    sessionStorage.getItem('wifiApplyPending') === 'true' ||
    sessionStorage.getItem('meshNetworkTransitionPending') === 'true'
  ) {
    return;
  }

  const wasLoggedIn = sessionStorage.getItem('isLoggedIn') === 'true';

  sessionStorage.removeItem('isLoggedIn');
  sessionStorage.removeItem('token');
  sessionStorage.removeItem('username');
  clearPageDataCache();

  // token 过期只改 hash、不整页刷新,内存态不会自动清,所以显式退出开发者模式。
  useDevModeStore.getState().setDevMode(false);

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

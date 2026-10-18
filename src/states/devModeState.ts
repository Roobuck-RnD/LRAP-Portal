import { create } from 'zustand'

type DevModeState = {
  devMode: boolean
}

type DevModeAction = {
  setDevMode: (devMode: DevModeState['devMode']) => void
}

// Developer mode switch. **In memory, deliberately not persisted**: a full reload, a new sign-in or an expired token all return it to the default
// off state (expiry and sign-out also reset it explicitly, see handleUnauthorized in utils/http.ts and
// nav-user's sign-out, because an expired token only changes the hash without a full reload, so memory is not cleared by itself).
// It is offered only to an account signed in as root and needs no password of its own; it is a convenience, not a security boundary.
const useDevModeStore = create<DevModeState & DevModeAction>(
  (set): DevModeState & DevModeAction => ({
    devMode: false,
    setDevMode: (devMode) => set(() => ({ devMode }))
  })
)

export default useDevModeStore

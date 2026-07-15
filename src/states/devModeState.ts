import { create } from 'zustand'

type DevModeState = {
  devMode: boolean
}

type DevModeAction = {
  setDevMode: (devMode: DevModeState['devMode']) => void
}

// 开发者模式开关。**内存态,故意不持久化**:整页刷新、重新登录、token 过期都会回到默认
// 关闭状态(过期/登出时另有显式重置,见 utils/http.ts 的 handleUnauthorized 和
// nav-user 的登出处理——因为 token 过期只改 hash、不整页刷新,内存不会自动清)。
// 校验密码 west20st 只写在前端,属于"软隐藏"防手滑,不是真正的安全边界。
const useDevModeStore = create<DevModeState & DevModeAction>(
  (set): DevModeState & DevModeAction => ({
    devMode: false,
    setDevMode: (devMode) => set(() => ({ devMode }))
  })
)

export default useDevModeStore

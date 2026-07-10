import { create } from 'zustand'

type ModuleType = 'Main Module' | 'Sub Module' | 'Unknown'

type Module = {
  name: string
  ipaddress: string
  mac: string
  port: string
  logo: React.ElementType
  type: ModuleType
}

type CurrentAllModuleState = {
  currentAllModule: Module[]
}

type CurrentAllModuleAction = {
  updateCurrentAllModule: (currentAllModule: CurrentAllModuleState['currentAllModule']) => void
}

const useCurrentAllModuleStore = create<CurrentAllModuleState & CurrentAllModuleAction>(
  (set): CurrentAllModuleState & CurrentAllModuleAction => ({
    currentAllModule: [],
    updateCurrentAllModule: (currentAllModule) => set(() => ({ currentAllModule }))
  })
)
export { useCurrentAllModuleStore }
export type { Module }

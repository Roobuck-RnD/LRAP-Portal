import { create } from 'zustand'
import { Router } from 'lucide-react'

type ModuleType = 'Main Module' | 'Sub Module' | 'Unknown'

type Module = {
  name: string
  ipaddress: string
  mac: string
  port: string
  logo: React.ElementType
  type: ModuleType
}

type CurrentModuleState = {
  currentModule: Module
}

type CurrentModuleAction = {
  updateCurrentModule: (currentModule: CurrentModuleState['currentModule']) => void
}

const useCurrentModuleStore = create<CurrentModuleState & CurrentModuleAction>(
  (set): CurrentModuleState & CurrentModuleAction => ({
    currentModule: {
      name: 'RoobuckAC',
      ipaddress:'10.10.18.1',
      mac: 'Unknown',
      port: 'Unknown',
      logo: Router,
      type: 'Main Module'
    },
    updateCurrentModule: (currentModule) => set(() => ({ currentModule }))
  })
)
export { useCurrentModuleStore }
export type { Module }

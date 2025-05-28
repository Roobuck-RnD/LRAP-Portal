import { create } from 'zustand'
import { Router } from 'lucide-react'

type ModuleType = 'Main Module' | 'Sub Module'

type Module = {
  name: string
  ipaddress: string
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
      name: 'Roobuck AC',
      ipaddress:'10.10.18.1',
      logo: Router,
      type: 'Main Module'
    },
    updateCurrentModule: (currentModule) => set(() => ({ currentModule }))
  })
)
export { useCurrentModuleStore }
export type { Module }

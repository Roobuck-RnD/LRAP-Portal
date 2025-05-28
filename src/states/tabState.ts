import { create } from 'zustand'

type CurrentTabState = {
  currentTab: string
}

type CurrentTabAction = {
  updateCurrentTab: (currentTab: CurrentTabState['currentTab']) => void
}

const useCurrentTabStore = create<CurrentTabState & CurrentTabAction>(
  (set): CurrentTabState & CurrentTabAction => ({
    currentTab: 'Overview',
    updateCurrentTab: (currentTab) => set(() => ({ currentTab }))
  })
)
export default useCurrentTabStore

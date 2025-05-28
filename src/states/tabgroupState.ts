import { create } from 'zustand'

type CurrentTabGroupState = {
  currentTabGroup: string
}

type CurrentTabGroupAction = {
  updateCurrentTabGroup: (currentTabGroup: CurrentTabGroupState['currentTabGroup']) => void
}

const useCurrentTabGroupStore = create<CurrentTabGroupState & CurrentTabGroupAction>(
  (set): CurrentTabGroupState & CurrentTabGroupAction => ({
    currentTabGroup: 'Status',
    updateCurrentTabGroup: (currentTabGroup) => set(() => ({ currentTabGroup }))
  })
)
export default useCurrentTabGroupStore

import type { JSX } from 'react'
import useCurrentTabStore from '@/states/tabState'

function RealtimeGraphs(): JSX.Element {
  const { currentTab } = useCurrentTabStore()
  return (
    <div className="flex items-center justify-center h-full">
      <div className="text-center">
        <div className="text-lg font-semibold">Current tab:</div>
        <div className="text-xl">{currentTab}</div>
      </div>
    </div>
  )
}

export default RealtimeGraphs

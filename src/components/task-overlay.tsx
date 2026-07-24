import * as React from 'react'

import { cn } from '@/lib/utils'

function FullScreenTaskOverlay({
  children,
  className,
  panelClassName,
  ...props
}: React.ComponentProps<'div'> & { panelClassName?: string }) {
  return (
    <div
      role="status"
      aria-live="polite"
      className={cn('fixed inset-0 z-[9999] flex items-center justify-center bg-background/85 p-4 backdrop-blur-sm', className)}
      {...props}
    >
      <div className={cn('bg-card w-full max-w-md rounded-lg border p-8 text-center shadow-2xl', panelClassName)}>
        {children}
      </div>
    </div>
  )
}

export { FullScreenTaskOverlay }

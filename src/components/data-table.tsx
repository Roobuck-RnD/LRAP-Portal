import * as React from 'react'
import { Search } from 'lucide-react'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

function SearchField({ className, ...props }: React.ComponentProps<typeof Input>) {
  return (
    <div className={cn('relative w-full sm:w-64', className)}>
      <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2" />
      <Input className="pl-9" {...props} />
    </div>
  )
}

function DataTableShell({ className, children, ...props }: React.ComponentProps<'div'>) {
  return (
    <div className={cn('overflow-hidden rounded-md border', className)} {...props}>
      {children}
    </div>
  )
}

function TableMessage({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn('px-4 py-10 text-center text-sm text-muted-foreground', className)}>{children}</div>
}

export { DataTableShell, SearchField, TableMessage }

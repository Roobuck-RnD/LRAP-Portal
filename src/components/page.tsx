import * as React from 'react'

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { cn } from '@/lib/utils'

type PageShellProps = React.ComponentProps<'div'> & {
  size?: 'narrow' | 'default' | 'wide' | 'full'
}

const pageWidths = {
  narrow: 'max-w-2xl',
  default: 'max-w-5xl',
  wide: 'max-w-7xl',
  full: 'max-w-none'
}

function PageShell({ className, size = 'wide', ...props }: PageShellProps) {
  return (
    <div
      data-slot="page-shell"
      className={cn('mx-auto flex w-full flex-col gap-6 pb-6', pageWidths[size], className)}
      {...props}
    />
  )
}

function PageHeader({
  title,
  description,
  actions,
  className
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <header className={cn('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between', className)}>
      <div className="min-w-0">
        <h1 className="font-display text-2xl font-semibold tracking-tight text-foreground">{title}</h1>
        {description ? <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  )
}

function SectionCard({
  title,
  description,
  action,
  icon,
  className,
  contentClassName,
  children,
  ...props
}: React.ComponentProps<typeof Card> & {
  title?: React.ReactNode
  description?: React.ReactNode
  action?: React.ReactNode
  icon?: React.ReactNode
  contentClassName?: string
}) {
  return (
    <Card className={cn('gap-0 py-0', className)} {...props}>
      {title || description || action ? (
        <CardHeader className="border-b px-5 py-4">
          <div className="flex min-w-0 items-center gap-3">
            {icon ? <div className="text-primary shrink-0">{icon}</div> : null}
            <div className="min-w-0">
              {title ? <CardTitle>{title}</CardTitle> : null}
              {description ? <CardDescription className="mt-1">{description}</CardDescription> : null}
            </div>
          </div>
          {action ? <div className="col-start-2 row-span-2 row-start-1 self-center justify-self-end">{action}</div> : null}
        </CardHeader>
      ) : null}
      <CardContent className={cn('p-5', contentClassName)}>{children}</CardContent>
    </Card>
  )
}

function StatCard({ label, value, detail, className }: { label: React.ReactNode; value: React.ReactNode; detail?: React.ReactNode; className?: string }) {
  return (
    <Card className={cn('gap-2 p-5 py-5', className)}>
      <div className="text-xs font-medium uppercase tracking-[0.1em] text-muted-foreground">{label}</div>
      <div className="font-display text-2xl font-semibold text-foreground">{value}</div>
      {detail ? <div className="data text-xs text-muted-foreground">{detail}</div> : null}
    </Card>
  )
}

function PlaceholderPage({ title }: { title: string }) {
  return (
    <PageShell>
      <PageHeader title={title} description="This section is not configured yet." />
      <SectionCard>
        <div className="py-12 text-center text-sm text-muted-foreground">Current tab: {title}</div>
      </SectionCard>
    </PageShell>
  )
}

export { PageHeader, PageShell, PlaceholderPage, SectionCard, StatCard }

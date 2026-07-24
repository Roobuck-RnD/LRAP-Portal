import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Network, Route } from 'lucide-react'
import { DataTableShell, SearchField } from '@/components/data-table'
import { PageHeader, PageShell, SectionCard, StatCard } from '@/components/page'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'

// ---------- Types ----------

type ArpEntry = {
  ip: string
  mac: string
  dev: string
}

type RouteEntry = {
  network: string
  target: string
  gateway: string
  metric: number | string
  table: string
}

type Module = {
  name: string
  ipaddress: string
  type: string
  port?: string
}

type RoutesStatusCache = {
  arp: ArpEntry[]
  routes: RouteEntry[]
}

const ROUTES_STATUS_CACHE_KEY = 'status.routes'

const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message
  return String(error)
}

function RoutesStatus(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const [cachedAtMount] = useState<RoutesStatusCache | undefined>(() =>
    getPageDataCache<RoutesStatusCache>(ROUTES_STATUS_CACHE_KEY)
  )

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [arp, setArp] = useState<ArpEntry[]>(() => cachedAtMount?.arp ?? [])
  const [routes, setRoutes] = useState<RouteEntry[]>(() => cachedAtMount?.routes ?? [])
  const [loading, setLoading] = useState(() => cachedAtMount === undefined)
  const [error, setError] = useState<string | null>(null)

  const [arpSearch, setArpSearch] = useState('')
  const [routeSearch, setRouteSearch] = useState('')

  const fetchJSON = useCallback(async <T,>(path: string): Promise<T> => {
    const res = await apiFetch(path, {
      method: 'GET',
      headers: {
        'Content-Type': 'application/json'
      }
    })

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(`HTTP ${res.status} ${txt.slice(0, 120)}`)
    }

    return (await res.json()) as T
  }, [])

  const loadStatus = useCallback(
    async (isBackground = false) => {
      if (!isBackground) {
        setLoading(true)
      }

      setError(null)

      try {
        const [arpData, routesData] = await Promise.all([
          fetchJSON<ArpEntry[]>('/api/net/arp'),
          fetchJSON<RouteEntry[]>('/api/net/routes')
        ])

        const nextArp = Array.isArray(arpData) ? arpData : []
        const nextRoutes = Array.isArray(routesData) ? routesData : []
        setPageDataCache(ROUTES_STATUS_CACHE_KEY, {
          arp: nextArp,
          routes: nextRoutes
        })
        setArp(nextArp)
        setRoutes(nextRoutes)
      } catch (err: unknown) {
        setError(getErrorMessage(err))

        if (!isBackground) {
          setArp([])
          setRoutes([])
        }
      } finally {
        setLoading(false)
      }
    },
    [fetchJSON]
  )

  useEffect(() => {
    let inFlight = true
    void loadStatus(cachedAtMount !== undefined).finally(() => {
      inFlight = false
    })

    const refreshWhenVisible = () => {
      if (document.visibilityState !== 'visible' || inFlight) return

      inFlight = true

      void loadStatus(true).finally(() => {
        inFlight = false
      })
    }

    const timer = window.setInterval(refreshWhenVisible, 5000)
    document.addEventListener('visibilitychange', refreshWhenVisible)

    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', refreshWhenVisible)
    }
  }, [cachedAtMount, loadStatus])

  const filteredArp = useMemo(() => {
    const q = arpSearch.trim().toLowerCase()
    if (!q) return arp

    return arp.filter((row) =>
      [row.ip, row.mac, row.dev].some((field) =>
        String(field || '').toLowerCase().includes(q)
      )
    )
  }, [arp, arpSearch])

  const filteredRoutes = useMemo(() => {
    const q = routeSearch.trim().toLowerCase()
    if (!q) return routes

    return routes.filter((row) =>
      [row.network, row.target, row.gateway, row.metric, row.table].some((field) =>
        String(field || '').toLowerCase().includes(q)
      )
    )
  }, [routes, routeSearch])

  return (
    <PageShell>
      <PageHeader
        title="Network Routes & ARP"
        description="Live gateway view of ARP entries and active IPv4 routes."
      />

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard label="Gateway Device" value={acModule?.name || 'Router'} detail={acModule?.ipaddress || ''} />
        <StatCard label="ARP Entries" value={arp.length} />
        <StatCard label="Main IPv4 Routes" value={routes.length} />
      </div>

        {error && (
          <div className="mb-6 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
            Failed to retrieve network status: {error}
          </div>
        )}

        {loading && arp.length === 0 && routes.length === 0 ? (
          <div className="flex h-40 items-center justify-center rounded-lg border bg-card text-muted-foreground shadow-sm">
            Loading network status...
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-6">
            <SectionCard
              title="ARP Table"
              description="IP-to-MAC neighbor entries learned by the gateway."
              icon={<Network className="size-5" />}
              action={<SearchField value={arpSearch} onChange={(e) => setArpSearch(e.target.value)} placeholder="Search ARP..." />}
            >
              <DataTableShell>
                  <Table className="text-xs">
                    <TableHeader>
                      <TableRow>
                        <TableHead>IP Address</TableHead>
                        <TableHead>MAC Address</TableHead>
                        <TableHead>Interface</TableHead>
                      </TableRow>
                    </TableHeader>

                    <TableBody>
                      {filteredArp.length === 0 ? (
                          <TableRow>
                            <TableCell colSpan={3} className="h-24 text-center text-muted-foreground">
                            {arpSearch.trim()
                              ? 'No ARP entries matched your search.'
                              : 'No ARP entries.'}
                            </TableCell>
                          </TableRow>
                      ) : (
                        filteredArp.map((row, idx) => (
                          <TableRow key={`${row.ip}-${row.mac}-${idx}`}>
                            <TableCell className="font-medium">{row.ip}</TableCell>
                            <TableCell className="data text-muted-foreground">{row.mac}</TableCell>
                            <TableCell className="text-muted-foreground">{row.dev}</TableCell>
                          </TableRow>
                        ))
                      )}
                    </TableBody>
                  </Table>
              </DataTableShell>
            </SectionCard>

            {/* Routes Table */}
            <SectionCard
              title="Main IPv4 Routes"
              description="Active main routing table. Local kernel routes are hidden."
              icon={<Route className="size-5 text-success" />}
              action={<SearchField value={routeSearch} onChange={(e) => setRouteSearch(e.target.value)} placeholder="Search routes..." />}
            >
              <DataTableShell>
                  <Table className="text-xs">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Network</TableHead>
                        <TableHead>Interface</TableHead>
                        <TableHead>Gateway</TableHead>
                        <TableHead className="text-right">Metric</TableHead>
                        <TableHead>Table</TableHead>
                      </TableRow>
                    </TableHeader>

                    <TableBody>
                      {filteredRoutes.length === 0 ? (
                          <TableRow>
                            <TableCell colSpan={5} className="h-24 text-center text-muted-foreground">
                            {routeSearch.trim() ? 'No routes matched your search.' : 'No routes found.'}
                            </TableCell>
                          </TableRow>
                      ) : (
                        filteredRoutes.map((row, idx) => (
                          <TableRow
                            key={`${row.network}-${row.target}-${row.gateway}-${idx}`}
                          >
                            <TableCell className="font-medium">{row.network}</TableCell>
                            <TableCell className="text-muted-foreground">{row.target}</TableCell>
                            <TableCell className="text-muted-foreground">{row.gateway || '-'}</TableCell>
                            <TableCell className="text-right text-muted-foreground">{row.metric}</TableCell>
                            <TableCell className="text-muted-foreground">{row.table || 'main'}</TableCell>
                          </TableRow>
                        ))
                      )}
                    </TableBody>
                  </Table>
              </DataTableShell>
            </SectionCard>
          </div>
        )}
    </PageShell>
  )
}

export default RoutesStatus

import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Network, Route, Search } from 'lucide-react'

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

const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message
  return String(error)
}

function RoutesStatus(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [arp, setArp] = useState<ArpEntry[]>([])
  const [routes, setRoutes] = useState<RouteEntry[]>([])
  const [loading, setLoading] = useState(true)
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

        setArp(Array.isArray(arpData) ? arpData : [])
        setRoutes(Array.isArray(routesData) ? routesData : [])
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
    void loadStatus()

    let inFlight = false

    const timer = window.setInterval(() => {
      if (inFlight) return

      inFlight = true

      void loadStatus(true).finally(() => {
        inFlight = false
      })
    }, 5000)

    return () => window.clearInterval(timer)
  }, [loadStatus])

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
    <div className="p-6">
      <div className="mx-auto max-w-6xl">
        <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-3xl font-bold text-foreground">Network Routes & ARP</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              Live gateway view of ARP entries and active IPv4 routes.
            </p>
          </div>
        </div>

        <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="rounded-xl border bg-card p-4 shadow-sm">
            <div className="text-sm text-muted-foreground">Gateway Device</div>
            <div className="mt-2 text-xl font-bold text-foreground">
              {acModule?.name || 'Router'}
            </div>
            <div className="mt-1 font-mono text-xs text-muted-foreground">
              {acModule?.ipaddress || ''}
            </div>
          </div>

          <div className="rounded-xl border bg-card p-4 shadow-sm">
            <div className="text-sm text-muted-foreground">ARP Entries</div>
            <div className="mt-2 text-3xl font-bold text-foreground">{arp.length}</div>
          </div>

          <div className="rounded-xl border bg-card p-4 shadow-sm">
            <div className="text-sm text-muted-foreground">Main IPv4 Routes</div>
            <div className="mt-2 text-3xl font-bold text-foreground">{routes.length}</div>
          </div>
        </div>

        {error && (
          <div className="mb-6 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
            Failed to retrieve network status: {error}
          </div>
        )}

        {loading && arp.length === 0 && routes.length === 0 ? (
          <div className="flex h-40 items-center justify-center rounded-xl border bg-card text-muted-foreground shadow-sm">
            Loading network status...
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-6">
            {/* ARP Table */}
            <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
              <div className="flex flex-col gap-3 border-b border-border bg-muted px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex items-center gap-2">
                  <Network className="h-5 w-5 text-primary" />
                  <div>
                    <h3 className="text-lg font-semibold text-foreground">ARP Table</h3>
                    <div className="text-xs text-muted-foreground">
                      IP-to-MAC neighbor entries learned by the gateway.
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2 rounded border bg-card px-2 py-1.5">
                  <Search className="h-4 w-4 text-muted-foreground" />
                  <input
                    value={arpSearch}
                    onChange={(e) => setArpSearch(e.target.value)}
                    placeholder="Search ARP..."
                    className="w-56 border-0 bg-transparent text-xs outline-none"
                  />
                </div>
              </div>

              <div className="p-4">
                <div className="overflow-x-auto rounded border border-border">
                  <table className="min-w-full text-left text-xs">
                    <thead className="border-b border-border text-muted-foreground">
                      <tr>
                        <th className="px-3 py-2 font-medium">IP Address</th>
                        <th className="px-3 py-2 font-medium">MAC Address</th>
                        <th className="px-3 py-2 font-medium">Interface</th>
                      </tr>
                    </thead>

                    <tbody className="divide-y divide-border">
                      {filteredArp.length === 0 ? (
                        <tr>
                          <td colSpan={3} className="px-3 py-8 text-center italic text-muted-foreground">
                            {arpSearch.trim()
                              ? 'No ARP entries matched your search.'
                              : 'No ARP entries.'}
                          </td>
                        </tr>
                      ) : (
                        filteredArp.map((row, idx) => (
                          <tr key={`${row.ip}-${row.mac}-${idx}`} className="hover:bg-muted">
                            <td className="px-3 py-2 font-medium text-foreground">{row.ip}</td>
                            <td className="px-3 py-2 font-mono text-muted-foreground">{row.mac}</td>
                            <td className="px-3 py-2 text-muted-foreground">{row.dev}</td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>

            {/* Routes Table */}
            <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
              <div className="flex flex-col gap-3 border-b border-border bg-muted px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex items-center gap-2">
                  <Route className="h-5 w-5 text-success" />
                  <div>
                    <h3 className="text-lg font-semibold text-foreground">Main IPv4 Routes</h3>
                    <div className="text-xs text-muted-foreground">
                      Active main routing table. Local kernel routes are hidden.
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2 rounded border bg-card px-2 py-1.5">
                  <Search className="h-4 w-4 text-muted-foreground" />
                  <input
                    value={routeSearch}
                    onChange={(e) => setRouteSearch(e.target.value)}
                    placeholder="Search routes..."
                    className="w-56 border-0 bg-transparent text-xs outline-none"
                  />
                </div>
              </div>

              <div className="p-4">
                <div className="overflow-x-auto rounded border border-border">
                  <table className="min-w-full text-left text-xs">
                    <thead className="border-b border-border text-muted-foreground">
                      <tr>
                        <th className="px-3 py-2 font-medium">Network</th>
                        <th className="px-3 py-2 font-medium">Interface</th>
                        <th className="px-3 py-2 font-medium">Gateway</th>
                        <th className="px-3 py-2 text-right font-medium">Metric</th>
                        <th className="px-3 py-2 font-medium">Table</th>
                      </tr>
                    </thead>

                    <tbody className="divide-y divide-border">
                      {filteredRoutes.length === 0 ? (
                        <tr>
                          <td colSpan={5} className="px-3 py-8 text-center italic text-muted-foreground">
                            {routeSearch.trim() ? 'No routes matched your search.' : 'No routes found.'}
                          </td>
                        </tr>
                      ) : (
                        filteredRoutes.map((row, idx) => (
                          <tr
                            key={`${row.network}-${row.target}-${row.gateway}-${idx}`}
                            className="hover:bg-muted"
                          >
                            <td className="px-3 py-2 font-medium text-foreground">{row.network}</td>
                            <td className="px-3 py-2 text-muted-foreground">{row.target}</td>
                            <td className="px-3 py-2 text-muted-foreground">{row.gateway || '-'}</td>
                            <td className="px-3 py-2 text-right text-muted-foreground">{row.metric}</td>
                            <td className="px-3 py-2 text-muted-foreground">{row.table || 'main'}</td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default RoutesStatus
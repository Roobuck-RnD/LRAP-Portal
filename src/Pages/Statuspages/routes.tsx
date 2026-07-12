import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { RefreshCw, Network, Route, Search, Info } from 'lucide-react'

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
            <h2 className="text-3xl font-bold text-gray-900">Network Routes & ARP</h2>
            <p className="mt-1 text-sm text-gray-500">
              Live AC gateway view of ARP entries and active IPv4 routes.
            </p>
          </div>

          <button
            onClick={() => void loadStatus()}
            disabled={loading}
            className="flex items-center gap-2 rounded bg-gray-100 px-3 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-200 disabled:opacity-50"
          >
            <RefreshCw size={16} className={loading ? 'animate-spin' : ''} />
            Refresh
          </button>
        </div>

        <div className="mb-6 rounded-xl border border-blue-100 bg-blue-50 p-4 text-sm text-blue-800">
          <div className="flex gap-2">
            <Info className="mt-0.5 h-4 w-4 shrink-0" />
            <div>
              <div className="font-semibold">AC-only network status</div>
              <p className="mt-1">
                AP modules are pure bridge APs. ARP and routing status are shown from the AC main
                controller because client traffic uses the AC as the default gateway.
              </p>
            </div>
          </div>
        </div>

        <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="rounded-xl border bg-white p-4 shadow-sm">
            <div className="text-sm text-gray-500">Gateway Device</div>
            <div className="mt-2 text-xl font-bold text-gray-900">
              {acModule?.name || 'Main Module'}
            </div>
            <div className="mt-1 font-mono text-xs text-gray-500">
              {acModule?.ipaddress || 'Local AC'}
            </div>
          </div>

          <div className="rounded-xl border bg-white p-4 shadow-sm">
            <div className="text-sm text-gray-500">ARP Entries</div>
            <div className="mt-2 text-3xl font-bold text-gray-900">{arp.length}</div>
          </div>

          <div className="rounded-xl border bg-white p-4 shadow-sm">
            <div className="text-sm text-gray-500">Main IPv4 Routes</div>
            <div className="mt-2 text-3xl font-bold text-gray-900">{routes.length}</div>
          </div>
        </div>

        {error && (
          <div className="mb-6 rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
            Failed to retrieve AC network status: {error}
          </div>
        )}

        {loading && arp.length === 0 && routes.length === 0 ? (
          <div className="flex h-40 items-center justify-center rounded-xl border bg-white text-gray-500 shadow-sm">
            Loading AC network status...
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-6">
            {/* ARP Table */}
            <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
              <div className="flex flex-col gap-3 border-b border-gray-100 bg-gray-50 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex items-center gap-2">
                  <Network className="h-5 w-5 text-blue-600" />
                  <div>
                    <h3 className="text-lg font-semibold text-gray-800">ARP Table</h3>
                    <div className="text-xs text-gray-500">
                      IP-to-MAC neighbor entries learned by the AC gateway.
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2 rounded border bg-white px-2 py-1.5">
                  <Search className="h-4 w-4 text-gray-400" />
                  <input
                    value={arpSearch}
                    onChange={(e) => setArpSearch(e.target.value)}
                    placeholder="Search ARP..."
                    className="w-56 border-0 bg-transparent text-xs outline-none"
                  />
                </div>
              </div>

              <div className="p-4">
                <div className="overflow-x-auto rounded border border-gray-200">
                  <table className="min-w-full text-left text-xs">
                    <thead className="bg-gray-50 text-gray-500">
                      <tr>
                        <th className="px-3 py-2 font-medium">IP Address</th>
                        <th className="px-3 py-2 font-medium">MAC Address</th>
                        <th className="px-3 py-2 font-medium">Interface</th>
                      </tr>
                    </thead>

                    <tbody className="divide-y divide-gray-100">
                      {filteredArp.length === 0 ? (
                        <tr>
                          <td colSpan={3} className="px-3 py-8 text-center italic text-gray-400">
                            {arpSearch.trim()
                              ? 'No ARP entries matched your search.'
                              : 'No ARP entries.'}
                          </td>
                        </tr>
                      ) : (
                        filteredArp.map((row, idx) => (
                          <tr key={`${row.ip}-${row.mac}-${idx}`} className="hover:bg-gray-50">
                            <td className="px-3 py-2 font-medium text-gray-700">{row.ip}</td>
                            <td className="px-3 py-2 font-mono text-gray-500">{row.mac}</td>
                            <td className="px-3 py-2 text-gray-500">{row.dev}</td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>

            {/* Routes Table */}
            <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
              <div className="flex flex-col gap-3 border-b border-gray-100 bg-gray-50 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
                <div className="flex items-center gap-2">
                  <Route className="h-5 w-5 text-green-600" />
                  <div>
                    <h3 className="text-lg font-semibold text-gray-800">Main IPv4 Routes</h3>
                    <div className="text-xs text-gray-500">
                      Active main routing table on the AC. Local kernel routes are hidden.
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2 rounded border bg-white px-2 py-1.5">
                  <Search className="h-4 w-4 text-gray-400" />
                  <input
                    value={routeSearch}
                    onChange={(e) => setRouteSearch(e.target.value)}
                    placeholder="Search routes..."
                    className="w-56 border-0 bg-transparent text-xs outline-none"
                  />
                </div>
              </div>

              <div className="p-4">
                <div className="overflow-x-auto rounded border border-gray-200">
                  <table className="min-w-full text-left text-xs">
                    <thead className="bg-gray-50 text-gray-500">
                      <tr>
                        <th className="px-3 py-2 font-medium">Network</th>
                        <th className="px-3 py-2 font-medium">Interface</th>
                        <th className="px-3 py-2 font-medium">Gateway</th>
                        <th className="px-3 py-2 text-right font-medium">Metric</th>
                        <th className="px-3 py-2 font-medium">Table</th>
                      </tr>
                    </thead>

                    <tbody className="divide-y divide-gray-100">
                      {filteredRoutes.length === 0 ? (
                        <tr>
                          <td colSpan={5} className="px-3 py-8 text-center italic text-gray-400">
                            {routeSearch.trim() ? 'No routes matched your search.' : 'No routes found.'}
                          </td>
                        </tr>
                      ) : (
                        filteredRoutes.map((row, idx) => (
                          <tr
                            key={`${row.network}-${row.target}-${row.gateway}-${idx}`}
                            className="hover:bg-gray-50"
                          >
                            <td className="px-3 py-2 font-medium text-gray-700">{row.network}</td>
                            <td className="px-3 py-2 text-gray-500">{row.target}</td>
                            <td className="px-3 py-2 text-gray-500">{row.gateway || '-'}</td>
                            <td className="px-3 py-2 text-right text-gray-500">{row.metric}</td>
                            <td className="px-3 py-2 text-gray-500">{row.table || 'main'}</td>
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

        <div className="mt-4 text-xs text-gray-400">Auto-refreshes every 5 seconds.</div>
      </div>
    </div>
  )
}

export default RoutesStatus
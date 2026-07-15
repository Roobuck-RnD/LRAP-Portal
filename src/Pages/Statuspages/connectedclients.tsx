import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { apiFetch } from '@/utils/http'
import useDevModeStore from '@/states/devModeState'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  RefreshCw,
  Wifi,
  Router,
  Smartphone,
  Monitor,
  AlertCircle,
  Search
} from 'lucide-react'

// ---------- Types ----------
type ModuleType = 'main' | 'ap' | string

type ConnectedClient = {
  hostname?: string
  ip?: string
  mac: string
  ssid?: string
  band?: string
  interface?: string
  rssi?: number
  signal?: string
  connected_time?: string
}

type ClientModule = {
  name: string
  type: ModuleType
  ip?: string
  mac?: string
  br_lan_mac?: string
  ra0_mac?: string
  rax0_mac?: string
  online?: boolean
  clients: ConnectedClient[]
}

type ConnectedClientsResponse = {
  modules: ClientModule[]
}

type FlatClient = ConnectedClient & {
  moduleName: string
  moduleType: ModuleType
  moduleIP?: string
}

const CLIENTS_ENDPOINT = '/api/status/connected-clients'

function normalizeMac(mac?: string): string {
  return (mac || '').toUpperCase()
}

function formatHostname(client: ConnectedClient): string {
  if (client.hostname && client.hostname !== '*' && client.hostname !== '?') {
    return client.hostname
  }

  return '(unknown)'
}

function signalClass(rssi?: number): string {
  if (typeof rssi !== 'number' || rssi === 0) return 'text-gray-600 bg-gray-100'
  if (rssi >= -55) return 'text-green-700 bg-green-100'
  if (rssi >= -67) return 'text-blue-700 bg-blue-100'
  if (rssi >= -75) return 'text-amber-700 bg-amber-100'
  return 'text-red-700 bg-red-100'
}

function signalText(client: ConnectedClient): string {
  if (client.signal) return client.signal

  const rssi = client.rssi
  if (typeof rssi !== 'number' || rssi === 0) return 'Unknown'
  if (rssi >= -55) return 'Excellent'
  if (rssi >= -67) return 'Good'
  if (rssi >= -75) return 'Fair'
  return 'Weak'
}

function bandBadgeClass(band?: string): string {
  const b = (band || '').toLowerCase()

  if (b.includes('5')) return 'bg-purple-100 text-purple-700'
  if (b.includes('2.4') || b.includes('2g')) return 'bg-blue-100 text-blue-700'

  return 'bg-gray-100 text-gray-700'
}

function stableStringifyModules(modules: ClientModule[]): string {
  return JSON.stringify(
    modules.map((mod) => ({
      name: mod.name,
      type: mod.type,
      ip: mod.ip || '',
      mac: mod.mac || '',
      br_lan_mac: mod.br_lan_mac || '',
      ra0_mac: mod.ra0_mac || '',
      rax0_mac: mod.rax0_mac || '',
      online: mod.online !== false,
      clients: (mod.clients || []).map((client) => ({
        hostname: client.hostname || '',
        ip: client.ip || '',
        mac: normalizeMac(client.mac),
        ssid: client.ssid || '',
        band: client.band || '',
        interface: client.interface || '',
        rssi: client.rssi || 0,
        signal: client.signal || '',
        connected_time: client.connected_time || ''
      }))
    }))
  )
}

function ConnectedClients(): JSX.Element {
  const { devMode } = useDevModeStore()

  const [modules, setModules] = useState<ClientModule[]>([])
  const [initialLoading, setInitialLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  const modulesSnapshotRef = useRef<string>('')
  const hasLoadedOnceRef = useRef(false)
  const inFlightRef = useRef(false)

  const fetchClients = useCallback(async (mode: 'initial' | 'background' | 'manual' = 'background') => {
    if (inFlightRef.current) return

    inFlightRef.current = true

    if (mode === 'initial') {
      setInitialLoading(true)
    }

    if (mode === 'manual') {
      setRefreshing(true)
    }

    try {
      const res = await apiFetch(CLIENTS_ENDPOINT, {
        method: 'GET'
      })

      if (!res.ok) {
        const text = await res.text().catch(() => '')
        throw new Error(text || `HTTP ${res.status}`)
      }

      const data = (await res.json()) as ConnectedClientsResponse
      const nextModules = Array.isArray(data.modules) ? data.modules : []
      const nextSnapshot = stableStringifyModules(nextModules)

      if (nextSnapshot !== modulesSnapshotRef.current) {
        modulesSnapshotRef.current = nextSnapshot
        setModules(nextModules)
      }

      setError(null)
      hasLoadedOnceRef.current = true
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(`Failed to load connected clients: ${msg}`)
    } finally {
      inFlightRef.current = false
      setInitialLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    void fetchClients('initial')

    const timer = window.setInterval(() => {
      void fetchClients('background')
    }, 5000)

    return () => {
      window.clearInterval(timer)
    }
  }, [fetchClients])

  const flatClients = useMemo<FlatClient[]>(() => {
    return modules.flatMap((mod) =>
      (mod.clients || []).map((client) => ({
        ...client,
        moduleName: mod.name,
        moduleType: mod.type,
        moduleIP: mod.ip
      }))
    )
  }, [modules])

  const filteredClients = useMemo(() => {
    const q = search.trim().toLowerCase()

    if (!q) return flatClients

    return flatClients.filter((client) => {
      const fields = [
        client.hostname,
        client.ip,
        client.mac,
        client.ssid,
        client.band,
        client.moduleName,
        client.moduleIP
      ]

      return fields.some((field) => String(field || '').toLowerCase().includes(q))
    })
  }, [flatClients, search])

  const totalClients = flatClients.length
  const acClients = flatClients.filter((client) => client.moduleType === 'main').length
  const apClients = flatClients.filter((client) => client.moduleType === 'ap').length
  const apCount = modules.filter((m) => m.type === 'ap').length

  if (initialLoading && !hasLoadedOnceRef.current) {
    return (
      <div className="w-full p-6">
        <div className="mb-8">
          <div className="mb-3 h-9 w-72 animate-pulse rounded bg-gray-200" />
          <div className="h-5 w-96 animate-pulse rounded bg-gray-100" />
        </div>

        <div className="grid grid-cols-1 gap-4 md:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <Card key={i}>
              <CardHeader>
                <div className="h-5 w-28 animate-pulse rounded bg-gray-200" />
              </CardHeader>
              <CardContent>
                <div className="h-8 w-16 animate-pulse rounded bg-gray-100" />
              </CardContent>
            </Card>
          ))}
        </div>

        <div className="mt-6 h-72 animate-pulse rounded-xl bg-gray-100" />
      </div>
    )
  }

  return (
    <div className="w-full p-6">
      <div className="mb-8 flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
        <div>
          <h2 className="text-3xl font-bold text-gray-900">Connected Clients</h2>
        </div>

        <Button
          onClick={() => {
            void fetchClients('manual')
          }}
          disabled={refreshing}
          className="bg-blue-600 text-white hover:bg-blue-700"
        >
          <RefreshCw className={`mr-2 h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      {error && (
        <div className="mb-6 flex gap-2 rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-4">
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Total Clients</CardDescription>
            <CardTitle className="flex items-center gap-2 text-3xl">
              <Smartphone className="h-6 w-6 text-blue-600" />
              {totalClients}
            </CardTitle>
          </CardHeader>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Router Clients</CardDescription>
            <CardTitle className="flex items-center gap-2 text-3xl">
              <Router className="h-6 w-6 text-gray-700" />
              {acClients}
            </CardTitle>
          </CardHeader>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Antenna Clients</CardDescription>
            <CardTitle className="flex items-center gap-2 text-3xl">
              <Wifi className="h-6 w-6 text-purple-600" />
              {apClients}
            </CardTitle>
          </CardHeader>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Antenna Number</CardDescription>
            <CardTitle className="flex items-center gap-2 text-3xl">
              <Wifi className="h-6 w-6 text-blue-600" />
              {apCount}
            </CardTitle>
          </CardHeader>
        </Card>
      </div>

      <div className="mb-6 flex items-center gap-2 rounded-xl border bg-white px-3 py-2 shadow-sm">
        <Search className="h-4 w-4 text-gray-400" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by hostname, IP, MAC, SSID, or device name..."
          className="border-0 shadow-none focus-visible:ring-0"
        />
      </div>

      <div className="mb-8">
        <h3 className="mb-3 text-lg font-semibold text-gray-900">Devices</h3>

        {modules.length === 0 ? (
          <Card>
            <CardContent className="p-6 text-sm text-gray-500">
              No devices found.
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
            {modules.map((mod, idx) => {
              const clientCount = mod.clients?.length || 0
              const isOnline = mod.online !== false
              const brLanMac = normalizeMac(mod.br_lan_mac || mod.mac)
              const ra0Mac = normalizeMac(mod.ra0_mac)
              const rax0Mac = normalizeMac(mod.rax0_mac)

              return (
                <Card key={`${mod.type}-${mod.ip || mod.mac || idx}`}>
                  <CardHeader className="pb-3">
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex items-start gap-2">
                        {mod.type === 'main' ? (
                          <Router className="mt-1 h-5 w-5 shrink-0 text-gray-700" />
                        ) : (
                          <Wifi className="mt-1 h-5 w-5 shrink-0 text-blue-600" />
                        )}

                        <div>
                          <CardTitle className="text-base">
                            {mod.name || 'Unknown Device'}
                          </CardTitle>

                          {devMode && (
                            <>
                              <CardDescription>{mod.ip || '—'}</CardDescription>

                              <div className="mt-2 space-y-1 text-[11px] leading-tight text-gray-500">
                                <div>
                                  <span className="font-medium text-gray-600">br-lan:</span>{' '}
                                  <span className="font-mono">{brLanMac || '—'}</span>
                                </div>

                                <div>
                                  <span className="font-medium text-gray-600">ra0 / 2.4G:</span>{' '}
                                  <span className="font-mono">{ra0Mac || '—'}</span>
                                </div>

                                <div>
                                  <span className="font-medium text-gray-600">rax0 / 5G:</span>{' '}
                                  <span className="font-mono">{rax0Mac || '—'}</span>
                                </div>
                              </div>
                            </>
                          )}
                        </div>
                      </div>

                      <span
                        className={
                          'rounded px-2 py-1 text-xs font-medium ' +
                          (isOnline
                            ? 'bg-green-100 text-green-700'
                            : 'bg-gray-100 text-gray-600')
                        }
                      >
                        {isOnline ? 'Online' : 'Offline'}
                      </span>
                    </div>
                  </CardHeader>

                  <CardContent>
                    <div className="flex items-end justify-between">
                      <div>
                        <div className="text-xs text-gray-500">Connected Clients</div>
                        <div className="text-3xl font-bold text-gray-900">{clientCount}</div>
                      </div>
                    </div>
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Client List</CardTitle>
          <CardDescription>
            {filteredClients.length} client{filteredClients.length === 1 ? '' : 's'} shown
          </CardDescription>
        </CardHeader>

        <CardContent>
          {filteredClients.length === 0 ? (
            <div className="rounded-lg border border-dashed p-8 text-center">
              <Monitor className="mx-auto mb-3 h-8 w-8 text-gray-400" />
              <div className="text-sm font-medium text-gray-700">No connected clients found</div>
              <div className="mt-1 text-xs text-gray-500">
                Try refreshing or clearing the search filter.
              </div>
            </div>
          ) : (
            <div className="overflow-auto">
              <table className="min-w-full text-sm">
                <thead className="border-b bg-gray-50 text-xs text-gray-500">
                  <tr>
                    <th className="px-3 py-2 text-left">Client</th>
                    <th className="px-3 py-2 text-left">IP</th>
                    <th className="px-3 py-2 text-left">MAC</th>
                    <th className="px-3 py-2 text-left">Connected To</th>
                    <th className="px-3 py-2 text-left">SSID</th>
                    <th className="px-3 py-2 text-left">Band</th>
                    <th className="px-3 py-2 text-left">Signal</th>
                    <th className="px-3 py-2 text-left">Connected Time</th>
                  </tr>
                </thead>

                <tbody>
                  {filteredClients.map((client) => (
                    <tr
                      key={`${normalizeMac(client.mac)}-${client.moduleIP || ''}`}
                      className="border-b"
                    >
                      <td className="px-3 py-2">
                        <div className="font-medium text-gray-900">{formatHostname(client)}</div>
                      </td>

                      <td className="px-3 py-2 text-gray-700">{client.ip || '—'}</td>

                      <td className="px-3 py-2">
                        <span className="font-mono text-xs text-gray-700">
                          {normalizeMac(client.mac)}
                        </span>
                      </td>

                      <td className="px-3 py-2">
                        <div className="font-medium text-gray-900">{client.moduleName}</div>
                        {devMode && (
                          <div className="text-xs text-gray-500">{client.moduleIP || '—'}</div>
                        )}
                      </td>

                      <td className="px-3 py-2 text-gray-700">{client.ssid || '—'}</td>

                      <td className="px-3 py-2">
                        <span
                          className={`rounded px-2 py-1 text-xs font-medium ${bandBadgeClass(client.band)}`}
                        >
                          {client.band || '—'}
                        </span>
                      </td>

                      <td className="px-3 py-2">
                        <span
                          className={`rounded px-2 py-1 text-xs font-medium ${signalClass(client.rssi)}`}
                        >
                          {signalText(client)}
                          {typeof client.rssi === 'number' && client.rssi !== 0
                            ? ` ${client.rssi} dBm`
                            : ''}
                        </span>
                      </td>

                      <td className="px-3 py-2 text-gray-700">
                        {client.connected_time || '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

export default ConnectedClients
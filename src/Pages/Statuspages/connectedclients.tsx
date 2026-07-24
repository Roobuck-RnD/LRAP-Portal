import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { apiFetch } from '@/utils/http'
import useDevModeStore from '@/states/devModeState'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { SearchField } from '@/components/data-table'
import { PageHeader, PageShell, StatCard } from '@/components/page'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'
import { Table } from '@/components/ui/table'
import { compareManagedModules } from '@/lib/module-order'
import {
  Wifi,
  Router,
  Monitor,
  AlertCircle,
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
  module_id?: string
  name: string
  type: ModuleType
  port?: string
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
  collection_complete?: boolean
  expected_antenna_count?: number
  online_antenna_count?: number
}

type ConnectedClientsCache = {
  modules: ClientModule[]
  collectionComplete: boolean
  expectedAntennaCount: number
  onlineAntennaCount: number
}

type FlatClient = ConnectedClient & {
  moduleName: string
  moduleType: ModuleType
  moduleIP?: string
}

const CLIENTS_ENDPOINT = '/api/status/connected-clients'
const CONNECTED_CLIENTS_CACHE_KEY = 'status.connected-clients.v2'

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
  if (typeof rssi !== 'number' || rssi === 0) return 'text-muted-foreground bg-muted'
  if (rssi >= -55) return 'text-success bg-success/15'
  if (rssi >= -67) return 'text-primary bg-primary/15'
  if (rssi >= -75) return 'text-warning bg-warning/15'
  return 'text-destructive bg-destructive/15'
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

  if (b.includes('5')) return 'bg-signal/15 text-signal'
  if (b.includes('2.4') || b.includes('2g')) return 'bg-primary/15 text-primary'

  return 'bg-muted text-foreground'
}

function stableStringifyModules(modules: ClientModule[]): string {
  return JSON.stringify(
    modules.map((mod) => ({
      module_id: mod.module_id || '',
      name: mod.name,
      type: mod.type,
      port: mod.port || '',
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
  const [cachedAtMount] = useState<ConnectedClientsCache | undefined>(() =>
    getPageDataCache<ConnectedClientsCache>(CONNECTED_CLIENTS_CACHE_KEY)
  )

  const [modules, setModules] = useState<ClientModule[]>(() => cachedAtMount?.modules ?? [])
  const [collectionComplete, setCollectionComplete] = useState(
    () => cachedAtMount?.collectionComplete ?? false
  )
  const [expectedAntennaCount, setExpectedAntennaCount] = useState(
    () => cachedAtMount?.expectedAntennaCount ?? 0
  )
  const [onlineAntennaCount, setOnlineAntennaCount] = useState(
    () => cachedAtMount?.onlineAntennaCount ?? 0
  )
  const [initialLoading, setInitialLoading] = useState(() => cachedAtMount === undefined)
  const [, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  const modulesSnapshotRef = useRef<string>(
    cachedAtMount ? stableStringifyModules(cachedAtMount.modules) : ''
  )
  const hasLoadedOnceRef = useRef(cachedAtMount !== undefined)
  const hasCompleteSnapshotRef = useRef(cachedAtMount?.collectionComplete ?? false)
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
      const nextModules = Array.isArray(data.modules)
        ? [...data.modules].sort(compareManagedModules)
        : []
      const nextCollectionComplete = data.collection_complete !== false
      const nextExpectedAntennaCount =
        typeof data.expected_antenna_count === 'number'
          ? data.expected_antenna_count
          : nextModules.filter((mod) => mod.type === 'ap').length
      const nextOnlineAntennaCount =
        typeof data.online_antenna_count === 'number'
          ? data.online_antenna_count
          : nextModules.filter((mod) => mod.type === 'ap' && mod.online !== false).length
      const nextSnapshot = stableStringifyModules(nextModules)

      setCollectionComplete(nextCollectionComplete)
      setExpectedAntennaCount(nextExpectedAntennaCount)
      setOnlineAntennaCount(nextOnlineAntennaCount)

      // A partial AP collection is not a genuine zero-client snapshot. Keep the
      // last complete data visible while polling recovers instead of replacing
      // it with Router-only/zero data.
      if (!nextCollectionComplete && hasCompleteSnapshotRef.current) {
        setError(null)
        hasLoadedOnceRef.current = true
        return
      }

      if (nextCollectionComplete) {
        hasCompleteSnapshotRef.current = true
        setPageDataCache(CONNECTED_CLIENTS_CACHE_KEY, {
          modules: nextModules,
          collectionComplete: true,
          expectedAntennaCount: nextExpectedAntennaCount,
          onlineAntennaCount: nextOnlineAntennaCount
        })
      }

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
    void fetchClients(cachedAtMount === undefined ? 'initial' : 'background')

    const refreshWhenVisible = () => {
      if (document.visibilityState !== 'visible') return
      void fetchClients('background')
    }

    const timer = window.setInterval(refreshWhenVisible, 5000)
    document.addEventListener('visibilitychange', refreshWhenVisible)

    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', refreshWhenVisible)
    }
  }, [cachedAtMount, fetchClients])

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
          <div className="mb-3 h-9 w-72 animate-pulse rounded bg-muted" />
          <div className="h-5 w-96 animate-pulse rounded bg-muted" />
        </div>

        <div className="grid grid-cols-1 gap-4 md:grid-cols-4">
          {[1, 2, 3, 4].map((i) => (
            <Card key={i}>
              <CardHeader>
                <div className="h-5 w-28 animate-pulse rounded bg-muted" />
              </CardHeader>
              <CardContent>
                <div className="h-8 w-16 animate-pulse rounded bg-muted" />
              </CardContent>
            </Card>
          ))}
        </div>

        <div className="mt-6 h-72 animate-pulse rounded-xl bg-muted" />
      </div>
    )
  }

  return (
    <PageShell size="full">
      <PageHeader title="Connected Clients" description="Devices currently connected to the router and managed antennas." />

      {error && (
        <div className="mb-6 flex gap-2 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {!collectionComplete && (
        <div className="mb-6 flex gap-2 rounded border border-warning/25 bg-warning/10 p-3 text-sm text-warning">
          <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
          <span>
            Client data is recovering ({onlineAntennaCount}/{expectedAntennaCount} antennas
            available). The page will update automatically.
          </span>
        </div>
      )}

      <div className="mb-6 grid grid-cols-1 gap-4 md:grid-cols-4">
        <StatCard label="Total Clients" value={collectionComplete ? totalClients : '—'} />
        <StatCard label="Router Clients" value={collectionComplete ? acClients : '—'} />
        <StatCard label="Antenna Clients" value={collectionComplete ? apClients : '—'} />
        <StatCard
          label="Antenna Number"
          value={collectionComplete ? apCount : `${onlineAntennaCount}/${expectedAntennaCount}`}
        />
      </div>

      <SearchField
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by hostname, IP, MAC, SSID, or device name..."
          className="sm:w-full"
      />

      <div className="mb-8">
        <h3 className="mb-3 text-lg font-semibold text-foreground">Devices</h3>

        {modules.length === 0 ? (
          <Card>
            <CardContent className="p-6 text-sm text-muted-foreground">
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
                          <Router className="mt-1 h-5 w-5 shrink-0 text-foreground" />
                        ) : (
                          <Wifi className="mt-1 h-5 w-5 shrink-0 text-primary" />
                        )}

                        <div>
                          <CardTitle className="text-base">
                            {mod.name || 'Unknown Device'}
                          </CardTitle>

                          {devMode && (
                            <>
                              <CardDescription>{mod.ip || '—'}</CardDescription>

                              <div className="mt-2 space-y-1 text-[11px] leading-tight text-muted-foreground">
                                <div>
                                  <span className="font-medium text-muted-foreground">br-lan:</span>{' '}
                                  <span className="font-mono">{brLanMac || '—'}</span>
                                </div>

                                <div>
                                  <span className="font-medium text-muted-foreground">ra0 / 2.4G:</span>{' '}
                                  <span className="font-mono">{ra0Mac || '—'}</span>
                                </div>

                                <div>
                                  <span className="font-medium text-muted-foreground">rax0 / 5G:</span>{' '}
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
                            ? 'bg-success/15 text-success'
                            : 'bg-muted text-muted-foreground')
                        }
                      >
                        {isOnline ? 'Online' : 'Offline'}
                      </span>
                    </div>
                  </CardHeader>

                  <CardContent>
                    <div className="flex items-end justify-between">
                      <div>
                        <div className="text-xs text-muted-foreground">Connected Clients</div>
                        <div className="text-3xl font-bold text-foreground">{clientCount}</div>
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
          {!collectionComplete && filteredClients.length === 0 ? (
            <div className="rounded-lg border border-dashed p-8 text-center">
              <Monitor className="mx-auto mb-3 h-8 w-8 text-muted-foreground" />
              <div className="text-sm font-medium text-foreground">Client data is recovering</div>
              <div className="mt-1 text-xs text-muted-foreground">
                Waiting for all managed antennas to answer.
              </div>
            </div>
          ) : filteredClients.length === 0 ? (
            <div className="rounded-lg border border-dashed p-8 text-center">
              <Monitor className="mx-auto mb-3 h-8 w-8 text-muted-foreground" />
              <div className="text-sm font-medium text-foreground">No connected clients found</div>
              <div className="mt-1 text-xs text-muted-foreground">
                Try refreshing or clearing the search filter.
              </div>
            </div>
          ) : (
            <div className="overflow-auto">
              <Table className="min-w-full text-sm">
                <thead className="border-b border-border text-xs text-muted-foreground">
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
                        <div className="font-medium text-foreground">{formatHostname(client)}</div>
                      </td>

                      <td className="px-3 py-2 text-foreground">{client.ip || '—'}</td>

                      <td className="px-3 py-2">
                        <span className="font-mono text-xs text-foreground">
                          {normalizeMac(client.mac)}
                        </span>
                      </td>

                      <td className="px-3 py-2">
                        <div className="font-medium text-foreground">{client.moduleName}</div>
                        {devMode && (
                          <div className="text-xs text-muted-foreground">{client.moduleIP || '—'}</div>
                        )}
                      </td>

                      <td className="px-3 py-2 text-foreground">{client.ssid || '—'}</td>

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

                      <td className="px-3 py-2 text-foreground">
                        {client.connected_time || '—'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </PageShell>
  )
}

export default ConnectedClients

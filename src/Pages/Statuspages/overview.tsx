import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'

// ---------- helpers (memory UI) ----------
function formatKiB(kib?: number | null): string {
  if (!kib || kib <= 0) return '0 MiB'
  return (kib / 1024).toFixed(2) + ' MiB'
}

function percentage(part: number, total: number): string {
  if (!total || total <= 0 || !part || part <= 0) return '0%'
  const p = Math.min(100, (part / total) * 100)
  return p.toFixed(0) + '%'
}

function MemoryBar({ label, value, total }: { label: string; value: number; total: number }) {
  const width = percentage(value, total)

  return (
    <div>
      <div className="mb-1 flex justify-between text-xs">
        <span>{label}</span>
        <span>{`${formatKiB(value)} / ${formatKiB(total)} (${width})`}</span>
      </div>
      <div className="h-2 rounded bg-gray-200">
        <div className="h-2 rounded bg-blue-600" style={{ width }} />
      </div>
    </div>
  )
}

// ---------- types ----------
type SysInfo = {
  hostname: string
  model: string
  architecture: string
  target: string
  firmware_version: string
  kernel_version: string
  local_time: string
  uptime: string
  load_average: string
  temperature?: string
}

type MemoryInfo = {
  total: number
  available: number
  used: number
  buffered: number
  cached: number
}

type NetworkInfo = {
  protocol: string
  address: string
  device: string
  gateway: string
  dns: string
  connected: string
  mac: string
}

type SysView = {
  name: string
  ip?: string
  sys?: SysInfo
  error?: string
}

type MemView = {
  name: string
  ip?: string
  mem?: MemoryInfo
  error?: string
}

type NetView = {
  name: string
  ip?: string
  net?: NetworkInfo
  error?: string
}

type LeaseInfo = {
  hostname: string
  ip: string
  mac: string
  expires: string
}

type LeaseView = {
  name: string
  ip?: string
  leases?: LeaseInfo[]
  error?: string
}

type StaticEntry = {
  section: string
  mac: string
  ip: string
  name?: string
}

type StaticView = {
  name: string
  ip?: string
  statics?: Record<string, StaticEntry>
  error?: string
}

const RESERVED_AP_DHCP_IPS = new Set(['10.10.18.2', '10.10.18.3', '10.10.18.4', '10.10.18.5'])

function isReservedAPLease(lease: LeaseInfo): boolean {
  return RESERVED_AP_DHCP_IPS.has(String(lease.ip || '').trim())
}

function Overview(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const [sysViews, setSysViews] = useState<SysView[]>([])
  const [memViews, setMemViews] = useState<MemView[]>([])
  const [netViews, setNetViews] = useState<NetView[]>([])

  // AC-only DHCP state
  const [leaseView, setLeaseView] = useState<LeaseView>({
    name: 'Main Module',
    leases: []
  })
  const [staticView, setStaticView] = useState<StaticView>({
    name: 'Main Module',
    statics: {}
  })
  const [leaseSearch, setLeaseSearch] = useState('')
  const [leasesLoading, setLeasesLoading] = useState(false)
  const [resettingDhcp, setResettingDhcp] = useState(false)

  const [pendingKey, setPendingKey] = useState<string | null>(null)
  const [isConfiguring, setIsConfiguring] = useState(false)

  const moduleKey = useMemo(() => {
    return currentAllModule
      .map((m) => `${m.type || ''}:${m.ipaddress || ''}:${m.name || ''}`)
      .join('|')
  }, [currentAllModule])

  const mainModule = useMemo(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  // ---------- common helpers ----------
  const buildQueryForModule = useCallback((m: { type?: string; ipaddress?: string }) => {
    const isSub = m.type !== 'Main Module'
    return isSub && m.ipaddress ? `?ip=${encodeURIComponent(m.ipaddress)}` : ''
  }, [])

  const authHeaders = useCallback((token: string) => {
    return {
      ...(token ? { Authorization: `Bearer ${token}` } : {})
    }
  }, [])

  const jsonAuthHeaders = useCallback((token: string) => {
    return {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {})
    }
  }, [])

  const fetchJSON = useCallback(
    async <T,>(path: string, who: string) => {
      const token = sessionStorage.getItem('token')?.trim() || ''

      const res = await apiFetch(path, {
        method: 'GET',
        headers: authHeaders(token)
      })

      if (res.status === 401) {
        sessionStorage.removeItem('isLoggedIn')
        sessionStorage.removeItem('token')
        throw new Error('Unauthorized')
      }

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`HTTP ${res.status} for ${who} :: ${preview.slice(0, 180)}`)
      }

      const ct = res.headers.get('content-type') || ''
      if (!ct.includes('application/json')) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Non-JSON for ${who} :: ${preview.slice(0, 180)}`)
      }

      return (await res.json()) as T
    },
    [authHeaders]
  )

  // ---------- AC-only DHCP ----------
  const loadACLeases = useCallback(
    async (cancelledRef?: { cancelled: boolean }) => {
      const token = sessionStorage.getItem('token')?.trim() || ''
      if (!token) return

      setLeasesLoading(true)

      try {
        const leases = await fetchJSON<LeaseInfo[]>('/api/lan/leases', 'AC /leases')

        if (cancelledRef?.cancelled) return

        const visibleLeases = Array.isArray(leases)
          ? leases.filter((lease) => !isReservedAPLease(lease))
          : []

        setLeaseView({
          name: mainModule?.name || 'Main Module',
          ip: mainModule?.ipaddress,
          leases: visibleLeases
        })
      } catch (e) {
        if (cancelledRef?.cancelled) return

        console.error('AC leases fetch failed:', e)
        setLeaseView({
          name: mainModule?.name || 'Main Module',
          ip: mainModule?.ipaddress,
          error: String(e)
        })
      } finally {
        if (!cancelledRef?.cancelled) {
          setLeasesLoading(false)
        }
      }
    },
    [fetchJSON, mainModule?.ipaddress, mainModule?.name]
  )

  const loadStaticMap = useCallback(
    async (cancelledRef?: { cancelled: boolean }) => {
      const token = sessionStorage.getItem('token')?.trim() || ''
      if (!token) return

      try {
        const res = await apiFetch('/api/lan/static-map', {
          method: 'GET',
          headers: authHeaders(token)
        })

        if (res.status === 401) {
          sessionStorage.removeItem('isLoggedIn')
          sessionStorage.removeItem('token')
          throw new Error('Unauthorized')
        }

        if (!res.ok) {
          const preview = await res.text().catch(() => '')
          throw new Error(`HTTP ${res.status} :: ${preview.slice(0, 180)}`)
        }

        const list = (await res.json()) as StaticEntry[]
        const mapObj: Record<string, StaticEntry> = {}

        list.forEach((e) => {
          const mac = (e.mac || '').toUpperCase()
          if (mac) {
            mapObj[mac] = e
          }
        })

        if (cancelledRef?.cancelled) return

        setStaticView({
          name: mainModule?.name || 'Main Module',
          ip: mainModule?.ipaddress,
          statics: mapObj
        })
      } catch (e) {
        if (cancelledRef?.cancelled) return

        console.error('Static map fetch failed:', e)
        setStaticView({
          name: mainModule?.name || 'Main Module',
          ip: mainModule?.ipaddress,
          error: String(e)
        })
      }
    },
    [authHeaders, mainModule?.ipaddress, mainModule?.name]
  )

  // ---------- overview polling ----------
  useEffect(() => {
    if (!currentAllModule || currentAllModule.length === 0) {
      setSysViews([])
      setMemViews([])
      setNetViews([])
      setLeaseView({ name: 'Main Module', leases: [] })
      return
    }

    let cancelled = false
    let inFlight = false

    const fetchAll = async () => {
      const modulesSnapshot = [...currentAllModule]

      // ---- System ----
      const sysResults = await Promise.allSettled(
        modulesSnapshot.map(async (m) => {
          const qs = buildQueryForModule(m)
          const who = `${m.name}${qs ? ` ${qs}` : ''}`
          const sys = await fetchJSON<SysInfo>(`/api/status/overview/system${qs}`, `${who} /system`)

          return {
            name: m.name || '(unknown)',
            ip: m.ipaddress,
            sys
          } as SysView
        })
      )

      if (cancelled) return

      const sysOk: SysView[] = []
      const sysErr: SysView[] = []

      sysResults.forEach((r, i) => {
        const m = modulesSnapshot[i]
        if (r.status === 'fulfilled') {
          sysOk.push(r.value)
        } else {
          console.error('System fetch failed:', m?.name, r.reason)
          sysErr.push({
            name: m?.name ?? `#${i}`,
            ip: m?.ipaddress,
            error: String(r.reason)
          })
        }
      })

      setSysViews([...sysOk, ...sysErr])

      // ---- Memory ----
      const memResults = await Promise.allSettled(
        modulesSnapshot.map(async (m) => {
          const qs = buildQueryForModule(m)
          const who = `${m.name}${qs ? ` ${qs}` : ''}`
          const mem = await fetchJSON<MemoryInfo>(
            `/api/status/overview/memory${qs}`,
            `${who} /memory`
          )

          return {
            name: m.name || '(unknown)',
            ip: m.ipaddress,
            mem
          } as MemView
        })
      )

      if (cancelled) return

      const memOk: MemView[] = []
      const memErr: MemView[] = []

      memResults.forEach((r, i) => {
        const m = modulesSnapshot[i]
        if (r.status === 'fulfilled') {
          memOk.push(r.value)
        } else {
          console.error('Memory fetch failed:', m?.name, r.reason)
          memErr.push({
            name: m?.name ?? `#${i}`,
            ip: m?.ipaddress,
            error: String(r.reason)
          })
        }
      })

      setMemViews([...memOk, ...memErr])

      // ---- Network ----
      const netResults = await Promise.allSettled(
        modulesSnapshot.map(async (m) => {
          const qs = buildQueryForModule(m)
          const who = `${m.name}${qs ? ` ${qs}` : ''}`
          const net = await fetchJSON<NetworkInfo>(
            `/api/status/overview/network${qs}`,
            `${who} /network`
          )

          return {
            name: m.name || '(unknown)',
            ip: m.ipaddress,
            net
          } as NetView
        })
      )

      if (cancelled) return

      const netOk: NetView[] = []
      const netErr: NetView[] = []

      netResults.forEach((r, i) => {
        const m = modulesSnapshot[i]
        if (r.status === 'fulfilled') {
          netOk.push(r.value)
        } else {
          console.error('Network fetch failed:', m?.name, r.reason)
          netErr.push({
            name: m?.name ?? `#${i}`,
            ip: m?.ipaddress,
            error: String(r.reason)
          })
        }
      })

      setNetViews([...netOk, ...netErr])

      await loadACLeases({ cancelled })
    }

    const tick = async () => {
      if (inFlight || cancelled) return

      inFlight = true
      try {
        await fetchAll()
      } finally {
        inFlight = false
      }
    }

    void tick()

    const timer: ReturnType<typeof setInterval> = setInterval(tick, 5000)

    return () => {
      cancelled = true
      clearInterval(timer)
    }
  }, [buildQueryForModule, currentAllModule, fetchJSON, loadACLeases])

  // ---------- Static Map ----------
  useEffect(() => {
    const cancelledRef = { cancelled: false }

    void loadStaticMap(cancelledRef)

    return () => {
      cancelledRef.cancelled = true
    }
  }, [moduleKey, loadStaticMap])

  // ---------- actions: reset / set / unset ----------
  const handleResetDhcp = async () => {
    const ok = window.confirm(
      'This will clear current DHCP lease records on the AC and restart dnsmasq. Clients may need to renew DHCP. Continue?'
    )

    if (!ok) return

    const token = sessionStorage.getItem('token')?.trim() || ''

    setResettingDhcp(true)
    setIsConfiguring(true)

    try {
      const res = await apiFetch('/api/lan/leases/reset', {
        method: 'POST',
        headers: jsonAuthHeaders(token),
        body: JSON.stringify({})
      })

      if (res.status === 401) {
        sessionStorage.removeItem('isLoggedIn')
        sessionStorage.removeItem('token')
        throw new Error('Unauthorized')
      }

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Reset DHCP failed: HTTP ${res.status} :: ${preview.slice(0, 180)}`)
      }

      await loadACLeases()
    } catch (e) {
      console.error('Reset DHCP failed:', e)
      window.alert(`Reset DHCP failed: ${String(e)}`)
    } finally {
      setResettingDhcp(false)
      setIsConfiguring(false)
    }
  }

  const handleSetStatic = async (lease: LeaseInfo) => {
    const token = sessionStorage.getItem('token')?.trim() || ''

    const body = {
      hostname: lease.hostname === '(unknown)' ? '' : lease.hostname,
      mac: lease.mac.toUpperCase(),
      ipaddr: lease.ip
    }

    const key = `ac-${lease.mac}`
    setPendingKey(key)
    setIsConfiguring(true)

    try {
      const res = await apiFetch('/api/lan/static-lease', {
        method: 'POST',
        headers: jsonAuthHeaders(token),
        body: JSON.stringify(body)
      })

      if (res.status === 401) {
        sessionStorage.removeItem('isLoggedIn')
        sessionStorage.removeItem('token')
        throw new Error('Unauthorized')
      }

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Set static lease failed: HTTP ${res.status} :: ${preview.slice(0, 180)}`)
      }

      await loadStaticMap()
    } catch (e) {
      console.error('Set static lease failed:', e)
      window.alert(`Set static lease failed: ${String(e)}`)
    } finally {
      setPendingKey(null)
      setIsConfiguring(false)
    }
  }

  const handleUnsetStatic = async (lease: LeaseInfo) => {
    const token = sessionStorage.getItem('token')?.trim() || ''

    const key = `ac-${lease.mac}`
    setPendingKey(key)
    setIsConfiguring(true)

    try {
      const res = await apiFetch('/api/lan/static-lease', {
        method: 'DELETE',
        headers: jsonAuthHeaders(token),
        body: JSON.stringify({ mac: lease.mac.toUpperCase() })
      })

      if (res.status === 401) {
        sessionStorage.removeItem('isLoggedIn')
        sessionStorage.removeItem('token')
        throw new Error('Unauthorized')
      }

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Unset static lease failed: HTTP ${res.status} :: ${preview.slice(0, 180)}`)
      }

      await loadStaticMap()
    } catch (e) {
      console.error('Unset static lease failed:', e)
      window.alert(`Unset static lease failed: ${String(e)}`)
    } finally {
      setPendingKey(null)
      setIsConfiguring(false)
    }
  }

  const filteredLeases = useMemo(() => {
    const leases = leaseView.leases || []
    const q = leaseSearch.trim().toLowerCase()

    if (!q) return leases

    return leases.filter((item) => {
      return [item.hostname, item.ip, item.mac, item.expires].some((v) =>
        String(v || '').toLowerCase().includes(q)
      )
    })
  }, [leaseSearch, leaseView.leases])

  const leaseTotal = leaseView.leases?.length || 0
  const staticTotal = (leaseView.leases || []).filter((item) => {
    const macUpper = item.mac.toUpperCase()
    return !!staticView.statics?.[macUpper]
  }).length

  return (
    <div className="relative p-4">
      {/* ---------- Fullscreen Overlay ---------- */}
      {isConfiguring && (
        <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-black/60 backdrop-blur-sm transition-opacity">
          <div className="flex flex-col items-center rounded-xl bg-white p-8 shadow-2xl animate-bounce-in">
            <svg
              className="mb-4 h-10 w-10 animate-spin text-blue-600"
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
            >
              <circle
                className="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                strokeWidth="4"
              />
              <path
                className="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              />
            </svg>
            <h3 className="mb-2 text-xl font-bold text-gray-800">
              {resettingDhcp ? 'Resetting DHCP...' : 'Applying Changes...'}
            </h3>
            <p className="max-w-xs text-center text-gray-500">
              {resettingDhcp ? 'Clearing DHCP lease records.' : 'Updating static lease settings.'}
              <br />
              Please wait.
            </p>
          </div>
        </div>
      )}

      {/* ---------- System Info ---------- */}
      <div className="mb-3">
        <h2 className="text-lg font-semibold">System Info</h2>
      </div>

      {sysViews.length === 0 ? (
        <div className="text-sm text-gray-500">Loading system info…</div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3 xl:grid-cols-5">
          {sysViews.map((v, idx) => {
            const info = v.sys

            return (
              <div
                key={(info?.hostname || v.name || 'sys') + '-' + idx}
                className="rounded-xl border border-gray-200 bg-white p-3 shadow-sm"
              >
                <div className="mb-2 flex items-start justify-between gap-2">
                  <div className="truncate text-sm font-semibold">
                    {info?.hostname || v.name || 'Unknown Host'}
                  </div>
                  <div className="max-w-[120px] truncate text-[10px] leading-tight text-gray-500" title={info?.model || '—'}>{info?.model || '—'}</div>
                </div>

                {info ? (
                  <dl className="grid grid-cols-2 gap-x-2 gap-y-2 text-[13px] leading-tight">
                    <div>
                      <dt className="text-[10px] text-gray-500">Architecture</dt>
                      <dd className="whitespace-nowrap font-medium">{info.architecture || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-[10px] text-gray-500">Target</dt>
                      <dd className="whitespace-nowrap font-medium">{info.target || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-[10px] text-gray-500">Firmware</dt>
                      <dd className="whitespace-pre-wrap break-words font-medium">{info.firmware_version || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-[10px] text-gray-500">Kernel</dt>
                      <dd className="whitespace-nowrap font-medium">{info.kernel_version || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-[10px] text-gray-500">Local Time</dt>
                      <dd className="whitespace-nowrap font-medium">{info.local_time || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-[10px] text-gray-500">Uptime</dt>
                      <dd className="whitespace-nowrap font-medium">{info.uptime || '—'}</dd>
                    </div>
                    <div className="col-span-2">
                      <dt className="text-[10px] text-gray-500">Load Average</dt>
                      <dd className="whitespace-nowrap font-medium">{info.load_average || '—'}</dd>
                    </div>
                    {info.temperature !== undefined && (
                      <div className="col-span-2">
                        <dt className="text-[10px] text-gray-500">Temperature</dt>
                        <dd className="whitespace-nowrap font-medium">{info.temperature ?? 'N/A'}</dd>
                      </div>
                    )}
                  </dl>
                ) : (
                  <div className="rounded-md bg-amber-50 p-2 text-xs text-amber-700">
                    {v.error ? 'System error' : 'N/A'}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      <div className="my-6 h-px w-full bg-gray-200" />

      {/* ---------- System Memory ---------- */}
      <div className="mb-3">
        <h2 className="text-lg font-semibold">System Memory</h2>
      </div>

      {memViews.length === 0 ? (
        <div className="text-sm text-gray-500">Loading memory…</div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3 xl:grid-cols-5">
          {memViews.map((v, idx) => {
            const m = v.mem

            return (
              <div
                key={(v.name || 'mem') + '-' + idx}
                className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm"
              >
                <div className="mb-3 flex items-center justify-between">
                  <div className="text-base font-medium">{v.name || 'Unknown Host'}</div>
                  <div className="text-xs text-gray-500">{v.ip || '—'}</div>
                </div>

                {m ? (
                  <div className="space-y-2">
                    <MemoryBar label="Available" value={m.available} total={m.total} />
                    <MemoryBar label="Used" value={m.used} total={m.total} />
                    <MemoryBar label="Buffered" value={m.buffered} total={m.total} />
                    <MemoryBar label="Cached" value={m.cached} total={m.total} />
                  </div>
                ) : (
                  <div className="rounded-md bg-amber-50 p-2 text-xs text-amber-700">
                    {v.error ? 'Memory error' : 'N/A'}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      <div className="my-6 h-px w-full bg-gray-200" />

      {/* ---------- Network ---------- */}
      <div className="mb-3">
        <h2 className="text-lg font-semibold">Network</h2>
      </div>

      {netViews.length === 0 ? (
        <div className="text-sm text-gray-500">Loading network…</div>
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-3 xl:grid-cols-5">
          {netViews.map((v, idx) => {
            const n = v.net

            return (
              <div
                key={(v.name || 'net') + '-' + idx}
                className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm"
              >
                <div className="mb-3 flex items-center justify-between">
                  <div className="text-base font-medium">{v.name || 'Unknown Host'}</div>
                  <div className="text-xs text-gray-500">{v.ip || '—'}</div>
                </div>

                {n ? (
                  <dl className="grid grid-cols-2 gap-x-3 gap-y-2 text-sm">
                    <div>
                      <dt className="text-xs text-gray-500">Protocol</dt>
                      <dd className="font-medium">{n.protocol || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-xs text-gray-500">Device</dt>
                      <dd className="font-medium">{n.device || '—'}</dd>
                    </div>
                    <div className="col-span-2">
                      <dt className="text-xs text-gray-500">Address</dt>
                      <dd className="break-all font-medium">{n.address || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-xs text-gray-500">Gateway</dt>
                      <dd className="font-medium">{n.gateway || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-xs text-gray-500">DNS</dt>
                      <dd className="font-medium">{n.dns || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-xs text-gray-500">Connected</dt>
                      <dd className="font-medium">{n.connected || '—'}</dd>
                    </div>
                    <div>
                      <dt className="text-xs text-gray-500">MAC</dt>
                      <dd className="break-all font-medium">{n.mac || '—'}</dd>
                    </div>
                  </dl>
                ) : (
                  <div className="rounded-md bg-amber-50 p-2 text-xs text-amber-700">
                    {v.error ? 'Network error' : 'N/A'}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}

      <div className="my-6 h-px w-full bg-gray-200" />

      {/* ---------- AC DHCP Leases ---------- */}
      <div className="mb-3 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h2 className="text-lg font-semibold">DHCP Leases</h2>
          <p className="text-xs text-gray-500">
            AC DHCP server only. Reserved AP management IPs 10.10.18.2–10.10.18.5 are hidden.
          </p>
        </div>

        <button
          onClick={() => {
            void handleResetDhcp()
          }}
          disabled={resettingDhcp}
          className={
            'rounded px-3 py-2 text-sm text-white ' +
            (resettingDhcp
              ? 'cursor-not-allowed bg-gray-400'
              : 'bg-red-600 hover:bg-red-700')
          }
        >
          {resettingDhcp ? 'Resetting…' : 'Reset DHCP'}
        </button>
      </div>

      <div className="mb-4 grid grid-cols-1 gap-4 md:grid-cols-3">
        <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
          <div className="text-xs text-gray-500">Total Active Leases</div>
          <div className="mt-1 text-3xl font-bold">{leaseTotal}</div>
        </div>

        <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
          <div className="text-xs text-gray-500">Static Leases</div>
          <div className="mt-1 text-3xl font-bold">{staticTotal}</div>
        </div>

        <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
          <div className="text-xs text-gray-500">Filtered Results</div>
          <div className="mt-1 text-3xl font-bold">{filteredLeases.length}</div>
        </div>
      </div>

      <div className="mb-4">
        <input
          value={leaseSearch}
          onChange={(e) => setLeaseSearch(e.target.value)}
          placeholder="Search hostname, IP, MAC, or expiry..."
          className="w-full rounded-xl border border-gray-200 bg-white px-3 py-2 text-sm shadow-sm outline-none focus:border-blue-500 focus:ring-2 focus:ring-blue-100"
        />
      </div>

      <div className="rounded-xl border border-gray-200 bg-white p-4 shadow-sm">
        <div className="mb-3 flex items-center justify-between">
          <div>
            <div className="text-base font-medium">{leaseView.name || 'Main Module'}</div>
            <div className="text-xs text-gray-500">{leaseView.ip || 'AC DHCP Server'}</div>
          </div>

          {leasesLoading && <div className="text-xs text-gray-400">Refreshing…</div>}
        </div>

        {leaseView.error ? (
          <div className="rounded-md bg-amber-50 p-2 text-xs text-amber-700">
            Leases error: {leaseView.error}
          </div>
        ) : filteredLeases.length === 0 ? (
          <div className="text-sm text-gray-500">
            {leaseSearch.trim() ? 'No leases matched your search.' : 'No active leases.'}
          </div>
        ) : (
          <div className="max-h-96 overflow-auto">
            <table className="min-w-full text-xs">
              <thead className="sticky top-0 bg-white text-gray-500">
                <tr>
                  <th className="py-1 pr-1 text-left">Hostname</th>
                  <th className="px-1 py-1 text-left">IP</th>
                  <th className="px-1 py-1 text-left">MAC</th>
                  <th className="py-1 pl-1 text-left">Expires</th>
                  <th className="py-1 pl-1 text-left">Static Lease</th>
                </tr>
              </thead>
              <tbody>
                {filteredLeases.map((item, i2) => {
                  const macUpper = item.mac.toUpperCase()
                  const isStatic = !!staticView.statics?.[macUpper]
                  const k = `ac-${item.mac}`
                  const disabled = !item.ip || !item.mac || pendingKey === k

                  return (
                    <tr key={`${item.mac}-${item.ip}-${i2}`} className="border-t">
                      <td className="py-1 pr-1">{item.hostname || '(unknown)'}</td>
                      <td className="px-1 py-1">{item.ip}</td>
                      <td className="break-all px-1 py-1">{item.mac}</td>
                      <td className="py-1 pl-1">{item.expires}</td>
                      <td className="py-1 pl-1">
                        {isStatic ? (
                          <button
                            disabled={disabled}
                            onClick={() => {
                              void handleUnsetStatic(item)
                            }}
                            className={
                              'rounded px-2 py-1 text-white ' +
                              (disabled
                                ? 'cursor-not-allowed bg-gray-400'
                                : 'bg-red-600 hover:bg-red-700')
                            }
                          >
                            {pendingKey === k ? 'Unsetting…' : 'Unset Static'}
                          </button>
                        ) : (
                          <button
                            disabled={disabled}
                            onClick={() => {
                              void handleSetStatic(item)
                            }}
                            className={
                              'rounded px-2 py-1 text-white ' +
                              (disabled
                                ? 'cursor-not-allowed bg-gray-400'
                                : 'bg-blue-600 hover:bg-blue-700')
                            }
                          >
                            {pendingKey === k ? 'Setting…' : 'Set Static'}
                          </button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

export default Overview
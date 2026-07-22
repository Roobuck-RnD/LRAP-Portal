import type { JSX, ReactNode } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import useDevModeStore from '@/states/devModeState'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

// ---------- helpers (memory UI) ----------
// ubus system.info reports memory in BYTES, so convert bytes -> MiB (GiB once large).
function formatMem(bytes?: number | null): string {
  if (!bytes || bytes <= 0) return '0 MiB'
  const mib = bytes / (1024 * 1024)
  return mib >= 1024 ? (mib / 1024).toFixed(2) + ' GiB' : mib.toFixed(1) + ' MiB'
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
      <div className="mb-1 flex items-baseline justify-between gap-2">
        <span className="text-muted-foreground text-xs">{label}</span>
        <span className="data text-foreground/80 text-[11px]">{width}</span>
      </div>
      <div className="bg-muted h-1.5 overflow-hidden rounded-full">
        <div className="bg-primary h-full rounded-full transition-[width] duration-500" style={{ width }} />
      </div>
      <div className="data text-muted-foreground mt-1 text-[10px]">
        {formatMem(value)} / {formatMem(total)}
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

// 小工具:label 在上、value 在下的一格键值对。value 支持 mono(数据)。
function Field({ label, value, mono }: { label: string; value?: string; mono?: boolean }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground text-[10px] tracking-wide uppercase">{label}</dt>
      <dd className={`text-foreground text-[13px] font-medium break-words ${mono ? 'data' : ''}`}>
        {value || '—'}
      </dd>
    </div>
  )
}

function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="border-warning/30 bg-warning/10 text-warning rounded-md border p-2 text-xs">
      {children}
    </div>
  )
}

// 卡壳:统一深色卡外观。
function Panel({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={`border-border bg-card rounded-lg border p-4 shadow-sm ${className ?? ''}`}>
      {children}
    </div>
  )
}

function CardHead({ name, ip }: { name?: string; ip?: string }) {
  return (
    <div className="mb-3 flex items-center justify-between gap-2">
      <div className="font-display truncate text-sm font-semibold tracking-tight">
        {name || 'Unknown Host'}
      </div>
      {ip !== undefined && <div className="data text-muted-foreground text-xs">{ip || '—'}</div>}
    </div>
  )
}

// 单张卡抽成组件,供开发者模式的"每模块堆叠"与默认模式的"主设备三合一行"复用。
function SysCard({ v, className }: { v: SysView; className?: string }) {
  const info = v.sys

  return (
    <Panel className={className}>
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="font-display truncate text-sm font-semibold tracking-tight">
          {info?.hostname || v.name || 'Unknown Host'}
        </div>
        <div
          className="text-muted-foreground max-w-[140px] truncate text-[10px] leading-tight"
          title={info?.model || '—'}
        >
          {info?.model || '—'}
        </div>
      </div>

      {info ? (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3">
          <Field label="Architecture" value={info.architecture} />
          <Field label="Target" value={info.target} mono />
          <Field label="Firmware" value={info.firmware_version} />
          <Field label="Kernel" value={info.kernel_version} mono />
          <Field label="Local Time" value={info.local_time} mono />
          <Field label="Uptime" value={info.uptime} mono />
          <div className="col-span-2">
            <Field label="Load Average" value={info.load_average} mono />
          </div>
          {info.temperature !== undefined && (
            <div className="col-span-2">
              <Field label="Temperature" value={info.temperature ?? 'N/A'} mono />
            </div>
          )}
        </dl>
      ) : (
        <ErrorNote>{v.error ? 'System error' : 'N/A'}</ErrorNote>
      )}
    </Panel>
  )
}

function MemCard({ v, className }: { v: MemView; className?: string }) {
  const m = v.mem

  return (
    <Panel className={className}>
      <CardHead name={v.name} ip={v.ip} />

      {m ? (
        <div className="space-y-3">
          <MemoryBar label="Available" value={m.available} total={m.total} />
          <MemoryBar label="Used" value={m.used} total={m.total} />
          <MemoryBar label="Buffered" value={m.buffered} total={m.total} />
          <MemoryBar label="Cached" value={m.cached} total={m.total} />
        </div>
      ) : (
        <ErrorNote>{v.error ? 'Memory error' : 'N/A'}</ErrorNote>
      )}
    </Panel>
  )
}

function NetCard({ v, className }: { v: NetView; className?: string }) {
  const n = v.net

  return (
    <Panel className={className}>
      <CardHead name={v.name} ip={v.ip} />

      {n ? (
        <dl className="grid grid-cols-2 gap-x-4 gap-y-3">
          <Field label="Protocol" value={n.protocol} />
          <Field label="Device" value={n.device} mono />
          <div className="col-span-2">
            <Field label="Address" value={n.address} mono />
          </div>
          <Field label="Gateway" value={n.gateway} mono />
          <Field label="DNS" value={n.dns} mono />
          <Field label="Connected" value={n.connected} mono />
          <Field label="MAC" value={n.mac} mono />
        </dl>
      ) : (
        <ErrorNote>{v.error ? 'Network error' : 'N/A'}</ErrorNote>
      )}
    </Panel>
  )
}

function Overview(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const { devMode } = useDevModeStore()

  const [sysViews, setSysViews] = useState<SysView[]>([])
  const [memViews, setMemViews] = useState<MemView[]>([])
  const [netViews, setNetViews] = useState<NetView[]>([])

  // AC-only DHCP state
  const [leaseView, setLeaseView] = useState<LeaseView>({
    name: 'Router',
    leases: []
  })
  const [staticView, setStaticView] = useState<StaticView>({
    name: 'Router',
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

  const fetchJSON = useCallback(
    async <T,>(path: string, who: string) => {
      const res = await apiFetch(path, {
        method: 'GET'
      })

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
    []
  )

  // ---------- AC-only DHCP ----------
  const loadACLeases = useCallback(
    async (cancelledRef?: { cancelled: boolean }) => {
      setLeasesLoading(true)

      try {
        const leases = await fetchJSON<LeaseInfo[]>('/api/lan/leases', 'leases')

        if (cancelledRef?.cancelled) return

        const visibleLeases = Array.isArray(leases)
          ? leases.filter((lease) => !isReservedAPLease(lease))
          : []

        setLeaseView({
          name: mainModule?.name || 'Router',
          ip: mainModule?.ipaddress,
          leases: visibleLeases
        })
      } catch (e) {
        if (cancelledRef?.cancelled) return

        console.error('AC leases fetch failed:', e)
        setLeaseView({
          name: mainModule?.name || 'Router',
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
      try {
        const res = await apiFetch('/api/lan/static-map', {
          method: 'GET'
        })

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
          name: mainModule?.name || 'Router',
          ip: mainModule?.ipaddress,
          statics: mapObj
        })
      } catch (e) {
        if (cancelledRef?.cancelled) return

        console.error('Static map fetch failed:', e)
        setStaticView({
          name: mainModule?.name || 'Router',
          ip: mainModule?.ipaddress,
          error: String(e)
        })
      }
    },
    [mainModule?.ipaddress, mainModule?.name]
  )

  // ---------- overview polling ----------
  useEffect(() => {
    if (!currentAllModule || currentAllModule.length === 0) {
      setSysViews([])
      setMemViews([])
      setNetViews([])
      setLeaseView({ name: 'Router', leases: [] })
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
    const ok = await confirmDialog({
      title: 'Reset DHCP leases?',
      description:
        'This will clear current DHCP lease records and restart dnsmasq. Clients may need to renew DHCP. Continue?',
      destructive: true,
      confirmText: 'Reset'
    })

    if (!ok) return

    setResettingDhcp(true)
    setIsConfiguring(true)

    try {
      const res = await apiFetch('/api/lan/leases/reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({})
      })

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Reset DHCP failed: HTTP ${res.status} :: ${preview.slice(0, 180)}`)
      }

      await loadACLeases()
    } catch (e) {
      console.error('Reset DHCP failed:', e)
      toast.error('Reset DHCP failed', { description: String(e) })
    } finally {
      setResettingDhcp(false)
      setIsConfiguring(false)
    }
  }

  const handleSetStatic = async (lease: LeaseInfo) => {
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
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      })

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Set static lease failed: HTTP ${res.status} :: ${preview.slice(0, 180)}`)
      }

      await loadStaticMap()
    } catch (e) {
      console.error('Set static lease failed:', e)
      toast.error('Set static lease failed', { description: String(e) })
    } finally {
      setPendingKey(null)
      setIsConfiguring(false)
    }
  }

  const handleUnsetStatic = async (lease: LeaseInfo) => {
    const key = `ac-${lease.mac}`
    setPendingKey(key)
    setIsConfiguring(true)

    try {
      const res = await apiFetch('/api/lan/static-lease', {
        method: 'DELETE',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mac: lease.mac.toUpperCase() })
      })

      if (!res.ok) {
        const preview = await res.text().catch(() => '')
        throw new Error(`Unset static lease failed: HTTP ${res.status} :: ${preview.slice(0, 180)}`)
      }

      await loadStaticMap()
    } catch (e) {
      console.error('Unset static lease failed:', e)
      toast.error('Unset static lease failed', { description: String(e) })
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

  // 非开发者模式:System / Memory / Network 三段只显示主设备(Router),隐藏各
  // Antenna 卡。主设备按 IP(其次 name)与 mainModule 匹配。
  const isMainView = (v: { name: string; ip?: string }): boolean =>
    (!!mainModule?.ipaddress && v.ip === mainModule.ipaddress) ||
    (!!mainModule?.name && v.name === mainModule.name)

  const visibleSysViews = devMode ? sysViews : sysViews.filter(isMainView)
  const visibleMemViews = devMode ? memViews : memViews.filter(isMainView)
  const visibleNetViews = devMode ? netViews : netViews.filter(isMainView)

  // 开发者模式:每模块一卡,走多列网格。
  const cardsGridClass = 'grid grid-cols-1 gap-4 md:grid-cols-3 xl:grid-cols-5'

  // 默认模式:只有主设备,把 System / Memory / Network 三张卡并成一行(见下方)。
  const mainSysView = visibleSysViews[0]
  const mainMemView = visibleMemViews[0]
  const mainNetView = visibleNetViews[0]

  return (
    <div className="relative p-4">
      {/* ---------- Fullscreen Overlay ---------- */}
      {isConfiguring && (
        <div className="bg-background/70 fixed inset-0 z-50 flex flex-col items-center justify-center backdrop-blur-sm transition-opacity">
          <div className="border-border bg-card animate-bounce-in flex flex-col items-center rounded-lg border p-8 shadow-2xl">
            <svg
              className="text-primary mb-4 h-10 w-10 animate-spin"
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
            <h3 className="font-display text-foreground mb-2 text-xl font-semibold">
              {resettingDhcp ? 'Resetting DHCP…' : 'Applying changes…'}
            </h3>
            <p className="text-muted-foreground max-w-xs text-center text-sm">
              {resettingDhcp ? 'Clearing DHCP lease records.' : 'Updating static lease settings.'}
              <br />
              Please wait.
            </p>
          </div>
        </div>
      )}

      {devMode ? (
        <>
          {/* ---------- System Info (per-module) ---------- */}
          <div className="mb-3">
            <h2 className="text-lg font-semibold tracking-tight">System Info</h2>
          </div>

          {sysViews.length === 0 ? (
            <div className="text-muted-foreground text-sm">Loading system info…</div>
          ) : (
            <div className={cardsGridClass}>
              {visibleSysViews.map((v, idx) => (
                <SysCard key={(v.sys?.hostname || v.name || 'sys') + '-' + idx} v={v} />
              ))}
            </div>
          )}

          <div className="bg-border my-6 h-px w-full" />

          {/* ---------- System Memory (per-module) ---------- */}
          <div className="mb-3">
            <h2 className="text-lg font-semibold tracking-tight">System Memory</h2>
          </div>

          {memViews.length === 0 ? (
            <div className="text-muted-foreground text-sm">Loading memory…</div>
          ) : (
            <div className={cardsGridClass}>
              {visibleMemViews.map((v, idx) => (
                <MemCard key={(v.name || 'mem') + '-' + idx} v={v} />
              ))}
            </div>
          )}

          <div className="bg-border my-6 h-px w-full" />

          {/* ---------- Network (per-module) ---------- */}
          <div className="mb-3">
            <h2 className="text-lg font-semibold tracking-tight">Network</h2>
          </div>

          {netViews.length === 0 ? (
            <div className="text-muted-foreground text-sm">Loading network…</div>
          ) : (
            <div className={cardsGridClass}>
              {visibleNetViews.map((v, idx) => (
                <NetCard key={(v.name || 'net') + '-' + idx} v={v} />
              ))}
            </div>
          )}
        </>
      ) : (
        <>
          {/* ---------- 默认模式:主设备的 System / Memory / Network 三合一行 ---------- */}
          <div className="mb-3">
            <h2 className="text-lg font-semibold tracking-tight">Device Overview</h2>
          </div>

          {sysViews.length === 0 && memViews.length === 0 && netViews.length === 0 ? (
            <div className="text-muted-foreground text-sm">Loading…</div>
          ) : (
            <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
              <div className="flex flex-col gap-2">
                <h3 className="text-muted-foreground font-sans text-xs font-medium tracking-[0.12em] uppercase">
                  System
                </h3>
                {mainSysView ? (
                  <SysCard v={mainSysView} className="flex-1" />
                ) : (
                  <div className="text-muted-foreground text-sm">Loading…</div>
                )}
              </div>

              <div className="flex flex-col gap-2">
                <h3 className="text-muted-foreground font-sans text-xs font-medium tracking-[0.12em] uppercase">
                  Memory
                </h3>
                {mainMemView ? (
                  <MemCard v={mainMemView} className="flex-1" />
                ) : (
                  <div className="text-muted-foreground text-sm">Loading…</div>
                )}
              </div>

              <div className="flex flex-col gap-2">
                <h3 className="text-muted-foreground font-sans text-xs font-medium tracking-[0.12em] uppercase">
                  Network
                </h3>
                {mainNetView ? (
                  <NetCard v={mainNetView} className="flex-1" />
                ) : (
                  <div className="text-muted-foreground text-sm">Loading…</div>
                )}
              </div>
            </div>
          )}
        </>
      )}

      <div className="bg-border my-6 h-px w-full" />

      {/* ---------- AC DHCP Leases ---------- */}
      <div className="mb-3 flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h2 className="text-lg font-semibold tracking-tight">DHCP Leases</h2>
        </div>

        <Button
          variant="destructive"
          size="sm"
          onClick={() => {
            void handleResetDhcp()
          }}
          disabled={resettingDhcp}
        >
          {resettingDhcp ? 'Resetting…' : 'Reset DHCP'}
        </Button>
      </div>

      <div className="mb-4 grid grid-cols-1 gap-4 md:grid-cols-3">
        <Panel>
          <div className="text-muted-foreground text-xs tracking-wide uppercase">Total Active Leases</div>
          <div className="font-display data mt-1 text-3xl font-semibold">{leaseTotal}</div>
        </Panel>

        <Panel>
          <div className="text-muted-foreground text-xs tracking-wide uppercase">Static Leases</div>
          <div className="font-display data mt-1 text-3xl font-semibold">{staticTotal}</div>
        </Panel>

        <Panel>
          <div className="text-muted-foreground text-xs tracking-wide uppercase">Filtered Results</div>
          <div className="font-display data mt-1 text-3xl font-semibold">{filteredLeases.length}</div>
        </Panel>
      </div>

      <div className="mb-4">
        <Input
          value={leaseSearch}
          onChange={(e) => setLeaseSearch(e.target.value)}
          placeholder="Search hostname, IP, MAC, or expiry…"
        />
      </div>

      <Panel>
        <div className="mb-3 flex items-center justify-between">
          <div>
            <div className="font-display text-sm font-semibold tracking-tight">{leaseView.name || 'Router'}</div>
            <div className="data text-muted-foreground text-xs">{leaseView.ip || 'DHCP Server'}</div>
          </div>

          {leasesLoading && (
            <div className="text-muted-foreground flex items-center gap-1.5 text-xs">
              <span className="status-dot status-dot--live text-signal" />
              Refreshing…
            </div>
          )}
        </div>

        {leaseView.error ? (
          <ErrorNote>Leases error: {leaseView.error}</ErrorNote>
        ) : filteredLeases.length === 0 ? (
          <div className="text-muted-foreground text-sm">
            {leaseSearch.trim() ? 'No leases matched your search.' : 'No active leases.'}
          </div>
        ) : (
          <div className="max-h-96 overflow-auto">
            <table className="min-w-full text-xs">
              <thead className="bg-card text-muted-foreground sticky top-0">
                <tr className="border-border border-b">
                  <th className="py-2 pr-2 text-left font-medium tracking-wide uppercase">Hostname</th>
                  <th className="px-2 py-2 text-left font-medium tracking-wide uppercase">IP</th>
                  <th className="px-2 py-2 text-left font-medium tracking-wide uppercase">MAC</th>
                  <th className="py-2 pl-2 text-left font-medium tracking-wide uppercase">Expires</th>
                  <th className="py-2 pl-2 text-left font-medium tracking-wide uppercase">Static Lease</th>
                </tr>
              </thead>
              <tbody>
                {filteredLeases.map((item, i2) => {
                  const macUpper = item.mac.toUpperCase()
                  const isStatic = !!staticView.statics?.[macUpper]
                  const k = `ac-${item.mac}`
                  const disabled = !item.ip || !item.mac || pendingKey === k

                  return (
                    <tr key={`${item.mac}-${item.ip}-${i2}`} className="border-border/60 hover:bg-muted/40 border-t transition-colors">
                      <td className="py-1.5 pr-2">{item.hostname || '(unknown)'}</td>
                      <td className="data px-2 py-1.5">{item.ip}</td>
                      <td className="data px-2 py-1.5 break-all">{item.mac}</td>
                      <td className="data py-1.5 pl-2">{item.expires}</td>
                      <td className="py-1.5 pl-2">
                        {isStatic ? (
                          <Button
                            variant="destructive"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            disabled={disabled}
                            onClick={() => {
                              void handleUnsetStatic(item)
                            }}
                          >
                            {pendingKey === k ? 'Unsetting…' : 'Unset Static'}
                          </Button>
                        ) : (
                          <Button
                            size="sm"
                            className="h-7 px-2 text-xs"
                            disabled={disabled}
                            onClick={() => {
                              void handleSetStatic(item)
                            }}
                          >
                            {pendingKey === k ? 'Setting…' : 'Set Static'}
                          </Button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  )
}

export default Overview

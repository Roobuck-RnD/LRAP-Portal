// src/pages/InterfacesConfiguration.tsx
import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import useDevModeStore from '@/states/devModeState'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Table } from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { PageHeader, PageShell } from '@/components/page'
import { FullScreenTaskOverlay } from '@/components/task-overlay'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '@/components/ui/dialog'
import {
  AlertCircle,
  Network,
  Plus,
  RefreshCw,
  ServerCog,
  Square,
  Trash2,
  Wifi
} from 'lucide-react'

// ---------- Types ----------

interface InterfaceDHCPSettings {
  enabled: boolean
  start: string
  limit: string
  leasetime: string
  dynamic_dhcp: boolean
  force: boolean
  dhcp_options: string[]
}

interface InterfaceStats {
  id: string
  name: string
  device: string
  protocol: string
  uptime: number
  macaddr: string
  rx_bytes: number
  rx_pkts: number
  tx_bytes: number
  tx_pkts: number
  ipv4: string
  ipaddr: string
  netmask: string
  gateway: string
  dns: string
  firewall_zone: string
  dhcp?: InterfaceDHCPSettings | null
  auto: boolean
  up: boolean
  editable: boolean
  can_restart: boolean
  can_stop: boolean
  can_delete: boolean
}

interface APManagementInfo {
  name: string
  antenna_index?: number
  ip: string
  online: boolean
  ipaddr: string
  netmask: string
  gateway: string
  dns: string
  error?: string
}

interface InterfacesResponse {
  ac_name: string
  ac_ip: string
  interfaces: InterfaceStats[]
  ap_management: APManagementInfo[]
}

interface Module {
  name: string
  ipaddress: string
  type: string
  port?: string
}

type SaveInterfacePayload = {
  action: 'create' | 'edit' | 'restart' | 'stop' | 'delete'
  interface?: string
  name?: string
  ipaddr?: string
  netmask?: string
  gateway?: string
  dns?: string
  auto?: boolean
  dhcp?: {
    enabled: boolean
    start: string
    limit: string
    leasetime: string
    dynamic_dhcp: boolean
    force: boolean
    dhcp_options: string[]
  }
}

interface InterfaceActionResponse {
  ok: boolean
  changed?: boolean
  lan_restarting?: boolean
  estimated_seconds?: number
}

// ---------- Helpers ----------

const FIXED_LAN_IP = '10.10.18.1'
const INTERFACES_CACHE_KEY = 'network.interfaces'
const REQUIRED_LAN_ADDRESSES = [
  FIXED_LAN_IP,
  '10.10.18.2',
  '10.10.18.3',
  '10.10.18.4',
  '10.10.18.5'
]

function formatUptime(seconds: number): string {
  if (!seconds) return '0s'

  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)

  const parts: string[] = []
  if (d > 0) parts.push(`${d}d`)
  if (h > 0) parts.push(`${h}h`)
  if (m > 0) parts.push(`${m}m`)
  parts.push(`${s}s`)

  return parts.join(' ')
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B'

  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1)

  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(2))} ${sizes[i]}`
}

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

function isValidIPv4(value: string): boolean {
  const parts = value.trim().split('.')
  if (parts.length !== 4) return false

  return parts.every((part) => {
    if (!/^\d+$/.test(part)) return false
    const n = Number(part)
    return n >= 0 && n <= 255
  })
}

function isValidNetmask(value: string): boolean {
  const v = value.trim()
  if (!isValidIPv4(v)) return false

  const binary = v
    .split('.')
    .map((part) => Number(part).toString(2).padStart(8, '0'))
    .join('')

  return /^1*0*$/.test(binary) && binary.includes('1') && binary.includes('0')
}

function ipv4ToUint32(value: string): number | null {
  if (!isValidIPv4(value)) return null

  return value
    .trim()
    .split('.')
    .reduce((result, part) => ((result << 8) | Number(part)) >>> 0, 0)
}

function isLANNetmaskCompatible(mask: string): boolean {
  if (!isValidNetmask(mask)) return false

  const maskValue = ipv4ToUint32(mask)
  const lanValue = ipv4ToUint32(FIXED_LAN_IP)
  if (maskValue === null || lanValue === null) return false

  const network = (lanValue & maskValue) >>> 0
  const broadcast = (network | ~maskValue) >>> 0

  return REQUIRED_LAN_ADDRESSES.every((address) => {
    const value = ipv4ToUint32(address)
    return (
      value !== null &&
      ((value & maskValue) >>> 0) === network &&
      value !== network &&
      value !== broadcast
    )
  })
}

async function showUnableToSaveDialog(description: string): Promise<void> {
  await confirmDialog({
    title: 'Unable to save settings',
    description,
    confirmText: 'OK',
    alertOnly: true
  })
}

function getLANDHCPPoolError(mask: string, startValue: string, limitValue: string): string | null {
  if (!/^\d+$/.test(startValue) || Number(startValue) < 100) {
    return 'Start Address Offset must be 100 or greater.'
  }

  if (!/^\d+$/.test(limitValue) || Number(limitValue) < 1) {
    return 'Limit must be at least 1.'
  }

  const maskValue = ipv4ToUint32(mask)
  const lanValue = ipv4ToUint32(FIXED_LAN_IP)
  if (maskValue === null || lanValue === null) {
    return 'The DHCP address pool is not compatible with the current network configuration.'
  }

  const start = Number(startValue)
  const limit = Number(limitValue)
  const end = start + limit - 1
  const hostMask = (~maskValue) >>> 0

  if (!Number.isSafeInteger(end) || end >= hostMask) {
    return 'The DHCP address pool does not fit inside the selected subnet mask.'
  }

  const network = (lanValue & maskValue) >>> 0
  const overlapsReservedAddress = REQUIRED_LAN_ADDRESSES.some((address) => {
    const value = ipv4ToUint32(address)
    if (value === null || value < network) return true
    const offset = value - network
    return start <= offset && offset <= end
  })

  if (overlapsReservedAddress) {
    return 'The DHCP address pool overlaps addresses reserved by the system.'
  }

  return null
}

function isValidInterfaceName(value: string): boolean {
  return /^[A-Za-z0-9_]+$/.test(value.trim())
}

function splitList(value: string): string[] {
  return value
    .split(/[\s,;\n]+/)
    .map((v) => v.trim())
    .filter(Boolean)
}

function joinList(value?: string[] | null): string {
  if (!Array.isArray(value)) return ''
  return value.join('\n')
}

function stringListsEqual(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index])
}

function interfaceTone(id: string): string {
  const lower = id.toLowerCase()

  if (lower === 'lan') return 'bg-success/15 text-success border-success/30'
  if (lower === 'wan' || lower.includes('wan')) return 'bg-destructive/15 text-destructive border-destructive/30'
  if (lower.includes('vpn')) return 'bg-signal/15 text-signal border-signal/30'
  if (lower.includes('guest')) return 'bg-warning/15 text-warning border-warning/30'
  if (lower.includes('alias')) return 'bg-primary/15 text-primary border-primary/30'

  return 'bg-muted text-muted-foreground border-border'
}

function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

// ---------- Main Page Component ----------

export default function Interfaces(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const { devMode } = useDevModeStore()
  const [cachedAtMount] = useState<InterfacesResponse | undefined>(() =>
    getPageDataCache<InterfacesResponse>(INTERFACES_CACHE_KEY)
  )

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [interfaces, setInterfaces] = useState<InterfaceStats[]>(
    () => cachedAtMount?.interfaces ?? []
  )
  const [apManagement, setApManagement] = useState<APManagementInfo[]>(
    () => cachedAtMount?.ap_management ?? []
  )
  const [acName, setAcName] = useState(() => cachedAtMount?.ac_name || 'Router')
  const [acIp, setAcIp] = useState(() => cachedAtMount?.ac_ip || '')

  const [loading, setLoading] = useState(() => cachedAtMount === undefined)
  const [error, setError] = useState<string | null>(null)

  const [isAddOpen, setIsAddOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newIp, setNewIp] = useState('')
  const [newMask, setNewMask] = useState('255.255.255.0')
  const [newAuto, setNewAuto] = useState(true)

  const [isEditOpen, setIsEditOpen] = useState(false)
  const [editTab, setEditTab] = useState<'general' | 'dhcp'>('general')
  const [editTarget, setEditTarget] = useState<InterfaceStats | null>(null)
  const [editIp, setEditIp] = useState('')
  const [editMask, setEditMask] = useState('')
  const [editGateway, setEditGateway] = useState('')
  const [editDns, setEditDns] = useState('')
  const [editAuto, setEditAuto] = useState(true)
  const [editDHCPEnabled, setEditDHCPEnabled] = useState(true)
  const [editDHCPStart, setEditDHCPStart] = useState('100')
  const [editDHCPLimit, setEditDHCPLimit] = useState('150')
  const [editDHCPLeaseTime, setEditDHCPLeaseTime] = useState('12h')
  const [editDHCPDynamic, setEditDHCPDynamic] = useState(true)
  const [editDHCPForce, setEditDHCPForce] = useState(false)
  const [editDHCPOptions, setEditDHCPOptions] = useState('')

  const [isSaving, setIsSaving] = useState(false)
  const [lanRestartDeadline, setLanRestartDeadline] = useState<number | null>(null)
  const [lanRestartCountdown, setLanRestartCountdown] = useState(0)

  const orderedAPManagement = useMemo(
    () =>
      [...apManagement].sort((left, right) => {
        const leftIndex =
          typeof left.antenna_index === 'number' && left.antenna_index > 0
            ? left.antenna_index
            : Number.MAX_SAFE_INTEGER
        const rightIndex =
          typeof right.antenna_index === 'number' && right.antenna_index > 0
            ? right.antenna_index
            : Number.MAX_SAFE_INTEGER
        return leftIndex - rightIndex
      }),
    [apManagement]
  )

  const fetchInterfaces = useCallback(
    async (isBackground = false) => {
      if (!isBackground) {
        setLoading(true)
      }

      setError(null)

      try {
        const res = await apiFetch('/api/net/interfaces', {
          method: 'GET'
        })

        if (!res.ok) {
          const text = await res.text().catch(() => '')
          throw new Error(text || `HTTP ${res.status}`)
        }

        const data = (await res.json()) as InterfacesResponse
        const normalizedData: InterfacesResponse = {
          ac_name: data.ac_name || acModule?.name || 'Router',
          ac_ip: data.ac_ip || acModule?.ipaddress || '',
          interfaces: Array.isArray(data.interfaces) ? data.interfaces : [],
          ap_management: Array.isArray(data.ap_management) ? data.ap_management : []
        }

        setPageDataCache(INTERFACES_CACHE_KEY, normalizedData)
        setInterfaces(normalizedData.interfaces)
        setApManagement(normalizedData.ap_management)
        setAcName(normalizedData.ac_name)
        setAcIp(normalizedData.ac_ip)
      } catch (err: unknown) {
        setError(getErrorMessage(err))

        if (!isBackground) {
          setInterfaces([])
          setApManagement([])
        }
      } finally {
        setLoading(false)
      }
    },
    [acModule?.ipaddress, acModule?.name]
  )

  useEffect(() => {
    void fetchInterfaces(cachedAtMount !== undefined)
  }, [cachedAtMount, fetchInterfaces])

  const postInterfaceAction = async (
    payload: SaveInterfacePayload
  ): Promise<InterfaceActionResponse> => {
    const res = await apiFetch('/api/net/interfaces', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    })

    if (!res.ok) {
      const text = await res.text().catch(() => '')
      let message = text || 'Operation failed'
      try {
        const parsed = JSON.parse(text) as { error?: string }
        if (parsed.error) message = parsed.error
      } catch {
        // Keep the plain response if it is not JSON.
      }
      throw new Error(message)
    }

    return (await res.json().catch(() => ({
      ok: true,
      changed: true
    }))) as InterfaceActionResponse
  }

  const startLANRestartOverlay = (seconds?: number) => {
    const duration =
      typeof seconds === 'number' && Number.isFinite(seconds) && seconds > 0
        ? Math.ceil(seconds)
        : 15
    setLanRestartCountdown(duration)
    setLanRestartDeadline(Date.now() + duration * 1000)
  }

  useEffect(() => {
    if (lanRestartDeadline === null) return

    const updateRemaining = () => {
      const remaining = Math.max(0, Math.ceil((lanRestartDeadline - Date.now()) / 1000))
      setLanRestartCountdown(remaining)
      if (remaining === 0) {
        setLanRestartDeadline(null)
        void fetchInterfaces(true)
      }
    }

    updateRemaining()
    const timer = window.setInterval(updateRemaining, 250)
    return () => window.clearInterval(timer)
  }, [fetchInterfaces, lanRestartDeadline])

  const resetAddForm = () => {
    setNewName('')
    setNewIp('')
    setNewMask('255.255.255.0')
    setNewAuto(true)
  }

  const handleCreateInterface = async () => {
    const name = newName.trim()
    const ip = newIp.trim()
    const mask = newMask.trim()

    if (!name) {
      toast.error('Interface name is required.')
      return
    }

    if (!isValidInterfaceName(name)) {
      toast.error('Interface name can only contain letters, numbers, and underscore.')
      return
    }

    if (!isValidIPv4(ip)) {
      toast.error('Please enter a valid IPv4 address.')
      return
    }

    if (!isValidNetmask(mask)) {
      toast.error('Please enter a valid subnet mask.')
      return
    }

    const exists = interfaces.some((iface) => iface.id === name)
    if (exists) {
      toast.error(`Interface "${name}" already exists.`)
      return
    }

    const ok = await confirmDialog({
      title: 'Create interface?',
      description:
        `Create new LAN alias interface "${name}"?\n\n` +
        `Protocol: Static address\n` +
        `Device: Alias Interface "@lan"\n` +
        `IPv4: ${ip}\n` +
        `Netmask: ${mask}`,
      confirmText: 'Create'
    })

    if (!ok) return

    setIsSaving(true)

    try {
      await postInterfaceAction({
        action: 'create',
        name,
        ipaddr: ip,
        netmask: mask,
        auto: newAuto
      })

      setIsAddOpen(false)
      resetAddForm()

      await delay(1500)
      await fetchInterfaces()
    } catch (err: unknown) {
      toast.error('Error', { description: getErrorMessage(err) })
    } finally {
      setIsSaving(false)
    }
  }

  const handleOpenEdit = (iface: InterfaceStats) => {
    const dhcp = iface.dhcp

    setEditTarget(iface)
    setEditTab('general')
    setEditIp(iface.ipaddr || '')
    setEditMask(iface.netmask || '255.255.255.0')
    setEditGateway(iface.gateway || '')
    setEditDns(iface.dns || '')
    setEditAuto(iface.auto !== false)

    setEditDHCPEnabled(dhcp?.enabled ?? true)
    setEditDHCPStart(dhcp?.start || '100')
    setEditDHCPLimit(dhcp?.limit || '150')
    setEditDHCPLeaseTime(dhcp?.leasetime || '12h')
    setEditDHCPDynamic(dhcp?.dynamic_dhcp ?? true)
    setEditDHCPForce(dhcp?.force ?? false)
    setEditDHCPOptions(joinList(dhcp?.dhcp_options))

    setIsEditOpen(true)
  }

  const handleSaveEdit = async () => {
    if (!editTarget) return

    const ip = editIp.trim()
    const mask = editMask.trim()
    const gateway = editGateway.trim()
    const dnsValues = splitList(editDns)

    if (!isValidIPv4(ip)) {
      toast.error('Please enter a valid IPv4 address.')
      return
    }

    if (!isValidNetmask(mask)) {
      toast.error('Please enter a valid subnet mask.')
      return
    }

    if (editTarget.id === 'lan' && !isLANNetmaskCompatible(mask)) {
      await showUnableToSaveDialog(
        'This subnet mask is not compatible with the current network configuration. Enter a different subnet mask and try again.'
      )
      return
    }

    if (gateway && !isValidIPv4(gateway)) {
      toast.error('Please enter a valid IPv4 gateway or leave it blank.')
      return
    }

    const invalidDns = dnsValues.find((v) => !isValidIPv4(v))
    if (invalidDns) {
      toast.error(`Invalid DNS server: ${invalidDns}`)
      return
    }

    if (editTarget.id === 'lan') {
      const poolError = getLANDHCPPoolError(
        mask,
        editDHCPStart.trim(),
        editDHCPLimit.trim()
      )
      if (poolError) {
        await showUnableToSaveDialog(poolError)
        return
      }

      if (!editDHCPLeaseTime.trim()) {
        toast.error('DHCP lease time is required.')
        return
      }
    }

    const networkChanged =
      ip !== (editTarget.ipaddr || '').trim() ||
      mask !== (editTarget.netmask || '').trim() ||
      gateway !== (editTarget.gateway || '').trim() ||
      !stringListsEqual(dnsValues, splitList(editTarget.dns || '')) ||
      editAuto !== (editTarget.auto !== false)

    const currentDHCP = editTarget.dhcp
    const dhcpChanged =
      editTarget.id === 'lan' &&
      (!currentDHCP ||
        editDHCPEnabled !== currentDHCP.enabled ||
        editDHCPStart.trim() !== currentDHCP.start.trim() ||
        editDHCPLimit.trim() !== currentDHCP.limit.trim() ||
        editDHCPLeaseTime.trim() !== currentDHCP.leasetime.trim() ||
        editDHCPDynamic !== currentDHCP.dynamic_dhcp ||
        editDHCPForce !== currentDHCP.force ||
        !stringListsEqual(
          splitList(editDHCPOptions),
          splitList(joinList(currentDHCP.dhcp_options))
        ))

    if (!networkChanged && !dhcpChanged) {
      setIsEditOpen(false)
      return
    }

    const confirmMsg =
      editTarget.id === 'lan'
        ? `Save changes to LAN?

WARNING: Changing the subnet mask, DHCP, gateway, or DNS may temporarily disconnect clients.`
        : `Save changes to ${editTarget.id}?`

    if (!(await confirmDialog({ title: 'Apply interface settings?', description: confirmMsg, confirmText: 'Save' }))) return

    setIsSaving(true)

    try {
      const payload: SaveInterfacePayload = {
        action: 'edit',
        interface: editTarget.id,
        ipaddr: ip,
        netmask: mask,
        gateway,
        dns: dnsValues.join(' '),
        auto: editAuto
      }

      if (editTarget.id === 'lan') {
        payload.dhcp = {
          enabled: editDHCPEnabled,
          start: editDHCPStart.trim(),
          limit: editDHCPLimit.trim(),
          leasetime: editDHCPLeaseTime.trim(),
          dynamic_dhcp: editDHCPDynamic,
          force: editDHCPForce,
          dhcp_options: splitList(editDHCPOptions)
        }
      }

      const result = await postInterfaceAction(payload)

      setIsEditOpen(false)

      if (result.lan_restarting) {
        startLANRestartOverlay(result.estimated_seconds)
        return
      }

      if (result.changed === false) return

      await delay(1500)
      await fetchInterfaces()
    } catch (err: unknown) {
      const msg = getErrorMessage(err)

      if (msg.includes('Failed to fetch') || msg.includes('NetworkError')) {
        toast.error('Network is restarting. Please reconnect if the IP changed.')
        setIsEditOpen(false)
      } else if (
        msg.includes('subnet mask is incompatible') ||
        msg.includes('LAN IPv4 address is fixed') ||
        msg.includes('DHCP start address offset') ||
        msg.includes('DHCP address pool is incompatible')
      ) {
        const description = msg.includes('DHCP start address offset')
          ? 'Start Address Offset must be 100 or greater.'
          : msg.includes('DHCP address pool')
            ? 'The DHCP address pool is not compatible with the current network configuration.'
            : 'This subnet mask is not compatible with the current network configuration. Enter a different subnet mask and try again.'
        await showUnableToSaveDialog(description)
      } else {
        toast.error('Error', { description: msg })
      }
    } finally {
      setIsSaving(false)
    }
  }

  const handleAction = async (
    action: 'restart' | 'stop' | 'delete',
    iface: InterfaceStats
  ) => {
    const labels = {
      restart: 'restart',
      stop: 'stop',
      delete: 'delete'
    }

    const warning =
      action === 'delete'
        ? `Delete interface "${iface.id}"?\n\nThis cannot be undone.`
        : action === 'stop'
          ? `Stop interface "${iface.id}"?`
          : `Restart interface "${iface.id}"?`

    if (!(await confirmDialog({
      title:
        action === 'delete'
          ? 'Delete interface?'
          : action === 'stop'
            ? 'Stop interface?'
            : 'Restart interface?',
      description: warning,
      destructive: action === 'delete',
      confirmText: action === 'delete' ? 'Delete' : action === 'stop' ? 'Stop' : 'Restart'
    }))) return

    setIsSaving(true)

    try {
      const result = await postInterfaceAction({
        action,
        interface: iface.id
      })

      if (result.lan_restarting) {
        startLANRestartOverlay(result.estimated_seconds)
        return
      }

      await delay(1500)
      await fetchInterfaces()
    } catch (err: unknown) {
      toast.error(`Failed to ${labels[action]} interface`, { description: getErrorMessage(err) })
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <PageShell>
      {lanRestartDeadline !== null && (
        <FullScreenTaskOverlay panelClassName="flex flex-col items-center">
          <div className="mb-5 flex size-20 items-center justify-center rounded-full border border-warning/30 bg-warning/10">
            <RefreshCw className="size-9 animate-spin text-warning" />
          </div>
          <h3 className="font-display text-2xl font-bold text-foreground">Restarting LAN</h3>
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            The network interface is restarting. Connectivity may be briefly interrupted.
          </p>
          <div className="mt-6 font-mono text-3xl font-semibold text-foreground">
            {lanRestartCountdown}s
          </div>
          <p className="mt-2 text-xs text-muted-foreground">
            This page will remain open and resume automatically.
          </p>
        </FullScreenTaskOverlay>
      )}

      <PageHeader
        title="Interfaces"
        description="Manage network interfaces and view antenna network settings."
        actions={
          <Button onClick={() => setIsAddOpen(true)} disabled={isSaving}>
            <Plus className="mr-2 h-4 w-4" />
            Add Interface
          </Button>
        }
      />

        {error && (
          <div className="mb-6 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {loading && interfaces.length === 0 ? (
          <Card className="animate-pulse">
            <CardHeader className="h-20 border-b bg-muted" />
            <CardContent className="space-y-4 p-6">
              <div className="h-24 rounded-md bg-muted" />
              <div className="h-24 rounded-md bg-muted" />
              <div className="h-24 rounded-md bg-muted" />
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-6">
            <Card className="border-border shadow-sm">
              <CardHeader className="border-b border-border pb-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <ServerCog className="h-5 w-5 text-muted-foreground" />
                    <div>
                      <CardTitle className="text-lg">
                        {acName || acModule?.name || 'Router'}
                      </CardTitle>
                      <CardDescription>
                        {acIp || acModule?.ipaddress || ''}
                      </CardDescription>
                    </div>
                  </div>
                </div>
              </CardHeader>

              <CardContent className="p-0">
                {interfaces.length === 0 ? (
                  <div className="p-6 text-center text-muted-foreground">No interfaces found.</div>
                ) : (
                  <div className="flex flex-col divide-y">
                    {interfaces.map((iface) => (
                      <div
                        key={iface.id}
                        className="flex flex-col gap-6 p-4 transition-colors hover:bg-muted md:flex-row md:items-center"
                      >
                        <div className="w-36 shrink-0 overflow-hidden rounded-md border bg-card shadow-sm">
                          <div
                            className={`border-b px-2 py-1 text-center text-sm font-bold ${interfaceTone(
                              iface.id
                            )}`}
                          >
                            {iface.name || iface.id}
                          </div>

                          <div className="flex flex-col items-center justify-center p-3">
                            <Network className="mb-1 h-6 w-6 text-muted-foreground" />
                            <span className="max-w-[120px] truncate text-xs text-muted-foreground">
                              {iface.device || '-'}
                            </span>
                          </div>
                        </div>

                        <div className="grid flex-grow grid-cols-1 gap-x-4 gap-y-1 text-sm text-foreground sm:grid-cols-2 lg:grid-cols-3">
                          <div>
                            <span className="font-semibold text-foreground">ID:</span>{' '}
                            {iface.id || '-'}
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Protocol:</span>{' '}
                            {iface.protocol || '-'}
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Status:</span>{' '}
                            {iface.up ? (
                              <span className="text-success">Up</span>
                            ) : (
                              <span className="text-muted-foreground">Down</span>
                            )}
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Uptime:</span>{' '}
                            {formatUptime(iface.uptime)}
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">MAC:</span>{' '}
                            <span className="font-mono">{iface.macaddr || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">IPv4:</span>{' '}
                            <span className="font-mono">{iface.ipv4 || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Netmask:</span>{' '}
                            <span className="font-mono">{iface.netmask || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Gateway:</span>{' '}
                            <span className="font-mono">{iface.gateway || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">DNS:</span>{' '}
                            <span className="font-mono">{iface.dns || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Firewall Zone:</span>{' '}
                            {iface.firewall_zone || '-'}
                          </div>

                          {iface.id === 'lan' && iface.dhcp && (
                            <div>
                              <span className="font-semibold text-foreground">Client DHCP Server:</span>{' '}
                              {iface.dhcp.enabled ? 'Enabled' : 'Disabled'}
                            </div>
                          )}

                          <div>
                            <span className="font-semibold text-foreground">Device:</span>{' '}
                            {iface.device || '-'}
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">Auto start:</span>{' '}
                            {iface.auto ? 'Yes' : 'No'}
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">RX:</span>{' '}
                            {formatBytes(iface.rx_bytes)} ({iface.rx_pkts} Pkts.)
                          </div>

                          <div>
                            <span className="font-semibold text-foreground">TX:</span>{' '}
                            {formatBytes(iface.tx_bytes)} ({iface.tx_pkts} Pkts.)
                          </div>
                        </div>

                        <div className="flex shrink-0 flex-wrap justify-end gap-2">
                          {iface.editable ? (
                            <>
                              <Button
                                variant="default"
                                size="sm"
                                onClick={() => handleOpenEdit(iface)}
                                disabled={isSaving}
                              >
                                Edit
                              </Button>

                              {iface.can_restart && (
                                <Button
                                  variant="warning"
                                  size="sm"
                                  onClick={() => void handleAction('restart', iface)}
                                  disabled={isSaving}
                                >
                                  Restart
                                </Button>
                              )}

                              {iface.can_stop && (
                                <Button
                                  variant="destructive"
                                  size="sm"
                                  onClick={() => void handleAction('stop', iface)}
                                  disabled={isSaving}
                                >
                                  <Square className="mr-1 h-3 w-3" />
                                  Stop
                                </Button>
                              )}

                              {iface.can_delete && (
                                <Button
                                  variant="destructiveOutline"
                                  size="sm"
                                  onClick={() => void handleAction('delete', iface)}
                                  disabled={isSaving}
                                >
                                  <Trash2 className="mr-1 h-3 w-3" />
                                  Delete
                                </Button>
                              )}
                            </>
                          ) : (
                            <span className="rounded bg-muted px-2 py-1 text-xs text-muted-foreground">
                              Read-only
                            </span>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>

            <Card className="border-border shadow-sm">
              <CardHeader className="border-b border-border pb-3">
                <div className="flex items-center gap-2">
                  <Wifi className="h-5 w-5 text-primary" />
                  <div>
                    <CardTitle className="text-lg">Antenna Network</CardTitle>
                  </div>
                </div>
              </CardHeader>

              <CardContent className="p-4">
                {orderedAPManagement.length === 0 ? (
                  <div className="rounded border border-dashed p-6 text-center text-sm text-muted-foreground">
                    No antennas found.
                  </div>
                ) : (
                  <div className="overflow-x-auto rounded border border-border">
                    <Table className="min-w-full text-left text-xs">
                      <thead className="border-b border-border text-muted-foreground">
                        <tr>
                          <th className="px-3 py-2 font-medium">Antenna</th>
                          <th className="px-3 py-2 font-medium">Status</th>
                        </tr>
                      </thead>

                      <tbody className="divide-y divide-border">
                        {orderedAPManagement.map((ap) => (
                          <tr key={ap.ip || ap.name} className="hover:bg-muted">
                            <td className="px-3 py-2">
                              <div className="font-medium text-foreground">
                                {ap.name || 'Antenna'}
                              </div>
                              {devMode && (
                                <div className="font-mono text-[11px] text-muted-foreground">
                                  {ap.ip || '-'}
                                </div>
                              )}
                            </td>

                            <td className="px-3 py-2">
                              {ap.online ? (
                                <span className="rounded bg-success/15 px-2 py-1 text-xs text-success">
                                  Online
                                </span>
                              ) : ap.error === 'identification pending' ? (
                                <span className="rounded bg-warning/15 px-2 py-1 text-xs text-warning">
                                  Detecting
                                </span>
                              ) : (
                                <span className="rounded bg-destructive/15 px-2 py-1 text-xs text-destructive">
                                  Offline
                                </span>
                              )}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </Table>
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        )}

      <Dialog
        open={isAddOpen}
        onOpenChange={(open) => {
          setIsAddOpen(open)
          if (!open) resetAddForm()
        }}
      >
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>Add New Interface</DialogTitle>
            <DialogDescription>Create a safe LAN alias interface.</DialogDescription>
          </DialogHeader>

            <div className="space-y-4 p-6">
              <div className="space-y-1.5">
                <Label>Name</Label>
                <Input
                  type="text"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                />
                <div className="text-xs text-muted-foreground">
                  Letters, numbers, and underscore only.
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>Protocol</Label>
                  <NativeSelect
                    value="static"
                    disabled
                    className="bg-muted text-muted-foreground"
                  >
                    <option value="static">Static address</option>
                  </NativeSelect>
                </div>

                <div className="space-y-1.5">
                  <Label>Device</Label>
                  <NativeSelect
                    value="@lan"
                    disabled
                    className="bg-muted text-muted-foreground"
                  >
                    <option value="@lan">Alias Interface: "@lan"</option>
                  </NativeSelect>
                </div>
              </div>

              <div className="space-y-1.5">
                <Label>IPv4 Address</Label>
                <Input
                  type="text"
                  value={newIp}
                  onChange={(e) => setNewIp(e.target.value)}
                />
              </div>

              <div className="space-y-1.5">
                <Label>IPv4 Netmask</Label>
                <Input
                  type="text"
                  value={newMask}
                  onChange={(e) => setNewMask(e.target.value)}
                />
              </div>

              <Label className="flex items-center gap-2">
                <Switch checked={newAuto} onCheckedChange={setNewAuto} />
                Bring up on boot
              </Label>
            </div>

            <DialogFooter>
              <Button
                variant="outline"
                onClick={() => {
                  setIsAddOpen(false)
                  resetAddForm()
                }}
                disabled={isSaving}
              >
                Cancel
              </Button>

              <Button onClick={handleCreateInterface} disabled={isSaving}>
                {isSaving ? 'Creating...' : 'Create Interface'}
              </Button>
            </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={isEditOpen} onOpenChange={setIsEditOpen}>
        {editTarget && (
          <DialogContent className="sm:max-w-[640px]">
            <DialogHeader>
              <DialogTitle>Edit {editTarget.name || editTarget.id}</DialogTitle>
              <DialogDescription>Modify safe interface settings.</DialogDescription>
            </DialogHeader>

            <div className="border-b px-6 pt-4">
              <div className="flex gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant={editTab === 'general' ? 'secondary' : 'ghost'}
                  onClick={() => setEditTab('general')}
                >
                  General Settings
                </Button>

                {editTarget.id === 'lan' && (
                  <Button
                    type="button"
                    size="sm"
                    variant={editTab === 'dhcp' ? 'secondary' : 'ghost'}
                    onClick={() => setEditTab('dhcp')}
                  >
                    Client DHCP
                  </Button>
                )}
              </div>
            </div>

            <div className="space-y-4 p-6">
              {editTarget.id === 'lan' && (
                <div className="flex items-start gap-2 rounded-md bg-warning/10 p-3 text-sm text-warning">
                  <AlertCircle className="mt-0.5 h-5 w-5 shrink-0" />
                  <p>
                    Changing the subnet mask, DHCP, gateway, or DNS may temporarily disconnect
                    clients.
                  </p>
                </div>
              )}

              {editTab === 'general' ? (
                <>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <Label>Protocol</Label>
                      <NativeSelect
                        value="static"
                        disabled
                        className="bg-muted text-muted-foreground"
                      >
                        <option value="static">{editTarget.protocol || 'Static address'}</option>
                      </NativeSelect>
                    </div>

                    <div className="space-y-1.5">
                      <Label>Device</Label>
                      <NativeSelect
                        value={editTarget.device || '@lan'}
                        disabled
                        className="bg-muted text-muted-foreground"
                      >
                        <option value={editTarget.device || '@lan'}>
                          {editTarget.device || '@lan'}
                        </option>
                      </NativeSelect>
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <Label>
                        IPv4 Address{editTarget.id === 'lan' ? ' (Fixed)' : ''}
                      </Label>
                      <Input
                        type="text"
                        value={editIp}
                        onChange={(e) => setEditIp(e.target.value)}
                        disabled={editTarget.id === 'lan'}
                        className={
                          editTarget.id === 'lan' ? 'bg-muted text-muted-foreground' : undefined
                        }
                      />
                      {editTarget.id === 'lan' ? (
                        <div className="text-xs text-muted-foreground">
                          This management address is reserved by the system.
                        </div>
                      ) : null}
                    </div>

                    <div className="space-y-1.5">
                      <Label>IPv4 Netmask</Label>
                      <Input
                        type="text"
                        value={editMask}
                        onChange={(e) => setEditMask(e.target.value)}
                      />
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <Label>IPv4 Gateway</Label>
                      <Input
                        type="text"
                        value={editGateway}
                        onChange={(e) => setEditGateway(e.target.value)}
                      />
                    </div>

                    <div className="space-y-1.5">
                      <Label>DNS Servers</Label>
                      <Input
                        type="text"
                        value={editDns}
                        onChange={(e) => setEditDns(e.target.value)}
                      />
                      <div className="text-xs text-muted-foreground">
                        Separate multiple DNS servers with spaces or commas.
                      </div>
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <Label>Firewall Zone</Label>
                      <Input
                        type="text"
                        value={editTarget.firewall_zone || 'lan'}
                        readOnly
                        className="bg-muted text-muted-foreground"
                      />
                    </div>

                    <Label className="flex items-center gap-2 pt-7">
                      <Switch checked={editAuto} onCheckedChange={setEditAuto} />
                      Bring up on boot
                    </Label>
                  </div>
                </>
              ) : (
                <>
                  <Label className="flex items-center justify-between gap-3 rounded border p-3">
                    <div>
                      <div className="font-medium text-foreground">Enable Client DHCP Server</div>
                      <div className="text-xs text-muted-foreground">
                        Controls address assignment for connected client devices. Internal device
                        management remains available.
                      </div>
                    </div>
                    <Switch checked={editDHCPEnabled} onCheckedChange={setEditDHCPEnabled} />
                  </Label>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <div className="space-y-1.5">
                      <Label>Start Address Offset</Label>
                      <Input
                        type="number"
                        min={100}
                        step={1}
                        value={editDHCPStart}
                        onChange={(e) => setEditDHCPStart(e.target.value)}
                        disabled={!editDHCPEnabled}
                      />
                      <div className="text-xs text-muted-foreground">
                        Minimum 100. Lower addresses are reserved by the system.
                      </div>
                    </div>

                    <div className="space-y-1.5">
                      <Label>Limit</Label>
                      <Input
                        type="number"
                        min={1}
                        step={1}
                        value={editDHCPLimit}
                        onChange={(e) => setEditDHCPLimit(e.target.value)}
                        disabled={!editDHCPEnabled}
                      />
                    </div>

                    <div className="space-y-1.5">
                      <Label>Lease Time</Label>
                      <Input
                        type="text"
                        value={editDHCPLeaseTime}
                        onChange={(e) => setEditDHCPLeaseTime(e.target.value)}
                        disabled={!editDHCPEnabled}
                      />
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <Label className="flex items-center gap-2 rounded border p-3">
                      <Switch checked={editDHCPDynamic} onCheckedChange={setEditDHCPDynamic} disabled={!editDHCPEnabled} />
                      Dynamic Client DHCP
                    </Label>

                    <Label className="flex items-center gap-2 rounded border p-3">
                      <Switch checked={editDHCPForce} onCheckedChange={setEditDHCPForce} disabled={!editDHCPEnabled} />
                      Force DHCP
                    </Label>
                  </div>

                  <div className="space-y-1.5">
                    <Label>DHCP Options</Label>
                    <Textarea
                      value={editDHCPOptions}
                      onChange={(e) => setEditDHCPOptions(e.target.value)}
                      className="min-h-[90px]"
                      disabled={!editDHCPEnabled}
                    />
                    <div className="text-xs text-muted-foreground">
                      One option per line, or separate options with spaces or commas.
                    </div>
                  </div>
                </>
              )}
            </div>

            <DialogFooter>
              <Button variant="outline" onClick={() => setIsEditOpen(false)} disabled={isSaving}>
                Cancel
              </Button>

              <Button onClick={handleSaveEdit} disabled={isSaving}>
                {isSaving ? 'Saving...' : 'Save & Apply'}
              </Button>
            </DialogFooter>
          </DialogContent>
        )}
      </Dialog>
    </PageShell>
  )
}

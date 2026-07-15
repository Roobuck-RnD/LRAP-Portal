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

// ---------- Helpers ----------

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

function isValidInterfaceName(value: string): boolean {
  return /^[A-Za-z0-9_]+$/.test(value.trim())
}

function isPositiveNumber(value: string): boolean {
  const v = value.trim()
  if (!/^\d+$/.test(v)) return false
  return Number(v) >= 0
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

function interfaceTone(id: string): string {
  const lower = id.toLowerCase()

  if (lower === 'lan') return 'bg-green-100 text-green-800 border-green-200'
  if (lower === 'wan' || lower.includes('wan')) return 'bg-red-100 text-red-800 border-red-200'
  if (lower.includes('vpn')) return 'bg-purple-100 text-purple-800 border-purple-200'
  if (lower.includes('guest')) return 'bg-amber-100 text-amber-800 border-amber-200'
  if (lower.includes('alias')) return 'bg-blue-100 text-blue-800 border-blue-200'

  return 'bg-slate-100 text-slate-800 border-slate-200'
}

function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

// ---------- Main Page Component ----------

export default function Interfaces(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const { devMode } = useDevModeStore()

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [interfaces, setInterfaces] = useState<InterfaceStats[]>([])
  const [apManagement, setApManagement] = useState<APManagementInfo[]>([])
  const [acName, setAcName] = useState('Router')
  const [acIp, setAcIp] = useState('')

  const [loading, setLoading] = useState(true)
  const [backgroundLoading, setBackgroundLoading] = useState(false)
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

  const fetchInterfaces = useCallback(
    async (isBackground = false) => {
      if (isBackground) {
        setBackgroundLoading(true)
      } else {
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

        setInterfaces(Array.isArray(data.interfaces) ? data.interfaces : [])
        setApManagement(Array.isArray(data.ap_management) ? data.ap_management : [])
        setAcName(data.ac_name || acModule?.name || 'Router')
        setAcIp(data.ac_ip || acModule?.ipaddress || '')
      } catch (err: unknown) {
        setError(getErrorMessage(err))

        if (!isBackground) {
          setInterfaces([])
          setApManagement([])
        }
      } finally {
        setLoading(false)
        setBackgroundLoading(false)
      }
    },
    [acModule?.ipaddress, acModule?.name]
  )

  useEffect(() => {
    void fetchInterfaces()

    let inFlight = false

    const interval = window.setInterval(() => {
      if (inFlight) return
      inFlight = true

      void fetchInterfaces(true).finally(() => {
        inFlight = false
      })
    }, 5000)

    return () => window.clearInterval(interval)
  }, [fetchInterfaces])

  const postInterfaceAction = async (payload: SaveInterfacePayload) => {
    const res = await apiFetch('/api/net/interfaces', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    })

    if (!res.ok) {
      const text = await res.text().catch(() => '')
      throw new Error(text || 'Operation failed')
    }
  }

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
      if (!isPositiveNumber(editDHCPStart)) {
        toast.error('DHCP start must be a non-negative number.')
        return
      }

      if (!isPositiveNumber(editDHCPLimit)) {
        toast.error('DHCP limit must be a non-negative number.')
        return
      }

      if (!editDHCPLeaseTime.trim()) {
        toast.error('DHCP lease time is required.')
        return
      }
    }

    const confirmMsg =
      editTarget.id === 'lan'
        ? `Save changes to LAN?

WARNING: Changing LAN IP, DHCP, gateway, or DNS may disconnect clients.`
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

      await postInterfaceAction(payload)

      setIsEditOpen(false)

      await delay(1500)
      await fetchInterfaces()
    } catch (err: unknown) {
      const msg = getErrorMessage(err)

      if (msg.includes('Failed to fetch') || msg.includes('NetworkError')) {
        toast.error('Network is restarting. Please reconnect if the IP changed.')
        setIsEditOpen(false)
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
      await postInterfaceAction({
        action,
        interface: iface.id
      })

      await delay(1500)
      await fetchInterfaces()
    } catch (err: unknown) {
      toast.error(`Failed to ${labels[action]} interface`, { description: getErrorMessage(err) })
    } finally {
      setIsSaving(false)
    }
  }

  return (
    <div className="w-full p-6">
      <div className="mx-auto max-w-6xl">
        <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-3xl font-bold text-gray-900">Interfaces</h2>
            <p className="mt-1 text-gray-500">
              Manage network interfaces and view antenna network settings.
            </p>
          </div>

          <div className="flex gap-2">
            <Button
              variant="outline"
              onClick={() => void fetchInterfaces()}
              disabled={loading || backgroundLoading || isSaving}
            >
              <RefreshCw
                className={`mr-2 h-4 w-4 ${
                  loading || backgroundLoading || isSaving ? 'animate-spin' : ''
                }`}
              />
              Refresh
            </Button>

            <Button onClick={() => setIsAddOpen(true)} disabled={isSaving}>
              <Plus className="mr-2 h-4 w-4" />
              Add Interface
            </Button>
          </div>
        </div>

        {error && (
          <div className="mb-6 rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
            {error}
          </div>
        )}

        {loading && interfaces.length === 0 ? (
          <Card className="animate-pulse">
            <CardHeader className="h-20 border-b bg-gray-50" />
            <CardContent className="space-y-4 p-6">
              <div className="h-24 rounded-md bg-gray-100" />
              <div className="h-24 rounded-md bg-gray-100" />
              <div className="h-24 rounded-md bg-gray-100" />
            </CardContent>
          </Card>
        ) : (
          <div className="grid grid-cols-1 gap-6">
            <Card className="border-gray-200 shadow-sm">
              <CardHeader className="border-b bg-gray-50/50 pb-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <ServerCog className="h-5 w-5 text-gray-600" />
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
                  <div className="p-6 text-center text-gray-500">No interfaces found.</div>
                ) : (
                  <div className="flex flex-col divide-y">
                    {interfaces.map((iface) => (
                      <div
                        key={iface.id}
                        className="flex flex-col gap-6 p-4 transition-colors hover:bg-gray-50 md:flex-row md:items-center"
                      >
                        <div className="w-36 shrink-0 overflow-hidden rounded-md border bg-white shadow-sm">
                          <div
                            className={`border-b px-2 py-1 text-center text-sm font-bold ${interfaceTone(
                              iface.id
                            )}`}
                          >
                            {iface.name || iface.id}
                          </div>

                          <div className="flex flex-col items-center justify-center p-3">
                            <Network className="mb-1 h-6 w-6 text-gray-500" />
                            <span className="max-w-[120px] truncate text-xs text-gray-500">
                              {iface.device || '-'}
                            </span>
                          </div>
                        </div>

                        <div className="grid flex-grow grid-cols-1 gap-x-4 gap-y-1 text-sm text-gray-700 sm:grid-cols-2 lg:grid-cols-3">
                          <div>
                            <span className="font-semibold text-gray-900">ID:</span>{' '}
                            {iface.id || '-'}
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Protocol:</span>{' '}
                            {iface.protocol || '-'}
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Status:</span>{' '}
                            {iface.up ? (
                              <span className="text-green-700">Up</span>
                            ) : (
                              <span className="text-gray-500">Down</span>
                            )}
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Uptime:</span>{' '}
                            {formatUptime(iface.uptime)}
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">MAC:</span>{' '}
                            <span className="font-mono">{iface.macaddr || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">IPv4:</span>{' '}
                            <span className="font-mono">{iface.ipv4 || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Netmask:</span>{' '}
                            <span className="font-mono">{iface.netmask || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Gateway:</span>{' '}
                            <span className="font-mono">{iface.gateway || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">DNS:</span>{' '}
                            <span className="font-mono">{iface.dns || '-'}</span>
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Firewall Zone:</span>{' '}
                            {iface.firewall_zone || '-'}
                          </div>

                          {iface.id === 'lan' && iface.dhcp && (
                            <div>
                              <span className="font-semibold text-gray-900">DHCP Server:</span>{' '}
                              {iface.dhcp.enabled ? 'Enabled' : 'Disabled'}
                            </div>
                          )}

                          <div>
                            <span className="font-semibold text-gray-900">Device:</span>{' '}
                            {iface.device || '-'}
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">Auto start:</span>{' '}
                            {iface.auto ? 'Yes' : 'No'}
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">RX:</span>{' '}
                            {formatBytes(iface.rx_bytes)} ({iface.rx_pkts} Pkts.)
                          </div>

                          <div>
                            <span className="font-semibold text-gray-900">TX:</span>{' '}
                            {formatBytes(iface.tx_bytes)} ({iface.tx_pkts} Pkts.)
                          </div>
                        </div>

                        <div className="flex shrink-0 flex-wrap justify-end gap-2">
                          {iface.editable ? (
                            <>
                              <Button
                                variant="default"
                                size="sm"
                                className="bg-blue-600 hover:bg-blue-700"
                                onClick={() => handleOpenEdit(iface)}
                                disabled={isSaving}
                              >
                                Edit
                              </Button>

                              {iface.can_restart && (
                                <Button
                                  variant="outline"
                                  size="sm"
                                  onClick={() => void handleAction('restart', iface)}
                                  disabled={isSaving}
                                >
                                  Restart
                                </Button>
                              )}

                              {iface.can_stop && (
                                <Button
                                  variant="outline"
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
                                  variant="outline"
                                  size="sm"
                                  className="border-red-200 text-red-600 hover:bg-red-50"
                                  onClick={() => void handleAction('delete', iface)}
                                  disabled={isSaving}
                                >
                                  <Trash2 className="mr-1 h-3 w-3" />
                                  Delete
                                </Button>
                              )}
                            </>
                          ) : (
                            <span className="rounded bg-gray-100 px-2 py-1 text-xs text-gray-500">
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

            <Card className="border-gray-200 shadow-sm">
              <CardHeader className="border-b bg-gray-50/50 pb-3">
                <div className="flex items-center gap-2">
                  <Wifi className="h-5 w-5 text-blue-600" />
                  <div>
                    <CardTitle className="text-lg">Antenna Network</CardTitle>
                  </div>
                </div>
              </CardHeader>

              <CardContent className="p-4">
                {apManagement.length === 0 ? (
                  <div className="rounded border border-dashed p-6 text-center text-sm text-gray-500">
                    No antennas found.
                  </div>
                ) : (
                  <div className="overflow-x-auto rounded border border-gray-200">
                    <table className="min-w-full text-left text-xs">
                      <thead className="bg-gray-50 text-gray-500">
                        <tr>
                          <th className="px-3 py-2 font-medium">Antenna</th>
                          <th className="px-3 py-2 font-medium">Status</th>
                        </tr>
                      </thead>

                      <tbody className="divide-y divide-gray-100">
                        {apManagement.map((ap) => (
                          <tr key={ap.ip || ap.name} className="hover:bg-gray-50">
                            <td className="px-3 py-2">
                              <div className="font-medium text-gray-800">
                                {ap.name || 'Antenna'}
                              </div>
                              {devMode && (
                                <div className="font-mono text-[11px] text-gray-400">
                                  {ap.ip || '-'}
                                </div>
                              )}
                            </td>

                            <td className="px-3 py-2">
                              {ap.online ? (
                                <span className="rounded bg-green-100 px-2 py-1 text-xs text-green-700">
                                  Online
                                </span>
                              ) : (
                                <span className="rounded bg-red-100 px-2 py-1 text-xs text-red-700">
                                  Offline
                                </span>
                              )}
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
        )}

      </div>

      {isAddOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm">
          <div className="max-w-[92vw] overflow-hidden rounded-lg bg-white shadow-xl sm:w-[520px]">
            <div className="border-b bg-gray-50 px-6 py-4">
              <h3 className="text-lg font-semibold text-gray-900">Add New Interface</h3>
              <p className="text-sm text-gray-500">
                Create a safe LAN alias interface.
              </p>
            </div>

            <div className="space-y-4 p-6">
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-gray-700">Name</label>
                <input
                  type="text"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                />
                <div className="text-xs text-gray-400">
                  Letters, numbers, and underscore only.
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-gray-700">Protocol</label>
                  <select
                    value="static"
                    disabled
                    className="w-full rounded-md border bg-gray-100 px-3 py-2 text-gray-600"
                  >
                    <option value="static">Static address</option>
                  </select>
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium text-gray-700">Device</label>
                  <select
                    value="@lan"
                    disabled
                    className="w-full rounded-md border bg-gray-100 px-3 py-2 text-gray-600"
                  >
                    <option value="@lan">Alias Interface: "@lan"</option>
                  </select>
                </div>
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-gray-700">IPv4 Address</label>
                <input
                  type="text"
                  value={newIp}
                  onChange={(e) => setNewIp(e.target.value)}
                  className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>

              <div className="space-y-1.5">
                <label className="text-sm font-medium text-gray-700">IPv4 Netmask</label>
                <input
                  type="text"
                  value={newMask}
                  onChange={(e) => setNewMask(e.target.value)}
                  className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>

              <label className="flex items-center gap-2 text-sm text-gray-700">
                <input
                  type="checkbox"
                  checked={newAuto}
                  onChange={(e) => setNewAuto(e.target.checked)}
                />
                Bring up on boot
              </label>
            </div>

            <div className="flex justify-end gap-3 border-t bg-gray-50 px-6 py-4">
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
            </div>
          </div>
        </div>
      )}

      {isEditOpen && editTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm">
          <div className="max-w-[92vw] overflow-hidden rounded-lg bg-white shadow-xl sm:w-[640px]">
            <div className="border-b bg-gray-50 px-6 py-4">
              <h3 className="text-lg font-semibold text-gray-900">
                Edit {editTarget.name || editTarget.id}
              </h3>
              <p className="text-sm text-gray-500">Modify safe interface settings.</p>
            </div>

            <div className="border-b px-6 pt-4">
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setEditTab('general')}
                  className={`rounded-t-md border px-3 py-2 text-sm ${
                    editTab === 'general'
                      ? 'border-b-white bg-white text-blue-700'
                      : 'bg-gray-50 text-gray-600'
                  }`}
                >
                  General Settings
                </button>

                {editTarget.id === 'lan' && (
                  <button
                    type="button"
                    onClick={() => setEditTab('dhcp')}
                    className={`rounded-t-md border px-3 py-2 text-sm ${
                      editTab === 'dhcp'
                        ? 'border-b-white bg-white text-blue-700'
                        : 'bg-gray-50 text-gray-600'
                    }`}
                  >
                    DHCP Server
                  </button>
                )}
              </div>
            </div>

            <div className="space-y-4 p-6">
              {editTarget.id === 'lan' && (
                <div className="flex items-start gap-2 rounded-md bg-amber-50 p-3 text-sm text-amber-800">
                  <AlertCircle className="mt-0.5 h-5 w-5 shrink-0" />
                  <p>
                    Changing LAN IP, DHCP, gateway, or DNS may disconnect clients. Reconnect to the
                    new address if the management IP changes.
                  </p>
                </div>
              )}

              {editTab === 'general' ? (
                <>
                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">Protocol</label>
                      <select
                        value="static"
                        disabled
                        className="w-full rounded-md border bg-gray-100 px-3 py-2 text-gray-600"
                      >
                        <option value="static">{editTarget.protocol || 'Static address'}</option>
                      </select>
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">Device</label>
                      <select
                        value={editTarget.device || '@lan'}
                        disabled
                        className="w-full rounded-md border bg-gray-100 px-3 py-2 text-gray-600"
                      >
                        <option value={editTarget.device || '@lan'}>
                          {editTarget.device || '@lan'}
                        </option>
                      </select>
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">IPv4 Address</label>
                      <input
                        type="text"
                        value={editIp}
                        onChange={(e) => setEditIp(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">IPv4 Netmask</label>
                      <input
                        type="text"
                        value={editMask}
                        onChange={(e) => setEditMask(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">IPv4 Gateway</label>
                      <input
                        type="text"
                        value={editGateway}
                        onChange={(e) => setEditGateway(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">DNS Servers</label>
                      <input
                        type="text"
                        value={editDns}
                        onChange={(e) => setEditDns(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                      />
                      <div className="text-xs text-gray-400">
                        Separate multiple DNS servers with spaces or commas.
                      </div>
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">Firewall Zone</label>
                      <input
                        type="text"
                        value={editTarget.firewall_zone || 'lan'}
                        readOnly
                        className="w-full rounded-md border bg-gray-100 px-3 py-2 text-gray-600"
                      />
                    </div>

                    <label className="flex items-center gap-2 pt-7 text-sm text-gray-700">
                      <input
                        type="checkbox"
                        checked={editAuto}
                        onChange={(e) => setEditAuto(e.target.checked)}
                      />
                      Bring up on boot
                    </label>
                  </div>
                </>
              ) : (
                <>
                  <label className="flex items-center justify-between gap-3 rounded border p-3 text-sm">
                    <div>
                      <div className="font-medium text-gray-800">Enable DHCP Server</div>
                      <div className="text-xs text-gray-500">
                        When disabled, this interface will not serve DHCP leases.
                      </div>
                    </div>
                    <input
                      type="checkbox"
                      checked={editDHCPEnabled}
                      onChange={(e) => setEditDHCPEnabled(e.target.checked)}
                    />
                  </label>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">Start Address Offset</label>
                      <input
                        type="text"
                        value={editDHCPStart}
                        onChange={(e) => setEditDHCPStart(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                        disabled={!editDHCPEnabled}
                      />
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">Limit</label>
                      <input
                        type="text"
                        value={editDHCPLimit}
                        onChange={(e) => setEditDHCPLimit(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                        disabled={!editDHCPEnabled}
                      />
                    </div>

                    <div className="space-y-1.5">
                      <label className="text-sm font-medium text-gray-700">Lease Time</label>
                      <input
                        type="text"
                        value={editDHCPLeaseTime}
                        onChange={(e) => setEditDHCPLeaseTime(e.target.value)}
                        className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                        disabled={!editDHCPEnabled}
                      />
                    </div>
                  </div>

                  <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                    <label className="flex items-center gap-2 rounded border p-3 text-sm text-gray-700">
                      <input
                        type="checkbox"
                        checked={editDHCPDynamic}
                        onChange={(e) => setEditDHCPDynamic(e.target.checked)}
                        disabled={!editDHCPEnabled}
                      />
                      Dynamic DHCP
                    </label>

                    <label className="flex items-center gap-2 rounded border p-3 text-sm text-gray-700">
                      <input
                        type="checkbox"
                        checked={editDHCPForce}
                        onChange={(e) => setEditDHCPForce(e.target.checked)}
                        disabled={!editDHCPEnabled}
                      />
                      Force DHCP
                    </label>
                  </div>

                  <div className="space-y-1.5">
                    <label className="text-sm font-medium text-gray-700">DHCP Options</label>
                    <textarea
                      value={editDHCPOptions}
                      onChange={(e) => setEditDHCPOptions(e.target.value)}
                      className="min-h-[90px] w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-blue-500"
                      disabled={!editDHCPEnabled}
                    />
                    <div className="text-xs text-gray-400">
                      One option per line, or separate options with spaces or commas.
                    </div>
                  </div>
                </>
              )}
            </div>

            <div className="flex justify-end gap-3 border-t bg-gray-50 px-6 py-4">
              <Button variant="outline" onClick={() => setIsEditOpen(false)} disabled={isSaving}>
                Cancel
              </Button>

              <Button onClick={handleSaveEdit} disabled={isSaving}>
                {isSaving ? 'Saving...' : 'Save & Apply'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
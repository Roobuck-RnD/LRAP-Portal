// src/pages/DHCPandDNS.tsx
import type { JSX } from 'react'
import { useEffect, useState, useCallback, useMemo } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Trash2, Plus, RefreshCw, Save, Server } from 'lucide-react'

// Shadcn UI Components
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from '@/components/ui/alert-dialog'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Badge } from '@/components/ui/badge'
import { SearchField } from '@/components/data-table'
import { PageHeader, PageShell, StatCard } from '@/components/page'
import { FullScreenTaskOverlay } from '@/components/task-overlay'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'

// Sonner Toast
import { toast } from 'sonner'

// ---------- Types ----------

type StaticLease = {
  section: string
  hostname: string
  mac: string
  ipaddr: string
}

type ModuleView = {
  name: string
  ip?: string
  leases: StaticLease[]
  loading: boolean
  error?: string
}

type NewLeaseForm = {
  hostname: string
  mac: string
  ipaddr: string
}

const INITIAL_FORM: NewLeaseForm = {
  hostname: '',
  mac: '',
  ipaddr: ''
}

const STATIC_LEASE_PREFIX = '10.10.18'
const STATIC_LEASE_MIN_HOST = 6
const STATIC_LEASE_MAX_HOST = 99
const STATIC_LEASES_CACHE_KEY = 'network.static-leases'

// ---------- Helpers ----------

const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message
  return String(error)
}

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

function normalizeMac(mac: string): string {
  return mac.trim().replace(/-/g, ':').toUpperCase()
}

function getStaticLeaseIPError(
  ip: string,
  mac: string,
  existing: StaticLease[]
): string | null {
  const parts = ip.trim().split('.')
  if (
    parts.length !== 4 ||
    parts.some((part) => !/^\d+$/.test(part) || Number(part) < 0 || Number(part) > 255)
  ) {
    return 'Please enter a valid IPv4 address.'
  }

  const prefix = parts.slice(0, 3).join('.')
  const host = Number(parts[3])
  if (
    prefix !== STATIC_LEASE_PREFIX ||
    host < STATIC_LEASE_MIN_HOST ||
    host > STATIC_LEASE_MAX_HOST
  ) {
    return `Static IPv4 addresses must be between ${STATIC_LEASE_PREFIX}.${STATIC_LEASE_MIN_HOST} and ${STATIC_LEASE_PREFIX}.${STATIC_LEASE_MAX_HOST}.`
  }

  const normalizedMAC = normalizeMac(mac)
  const duplicate = existing.find(
    (lease) =>
      lease.ipaddr.trim() === ip.trim() && normalizeMac(lease.mac) !== normalizedMAC
  )
  if (duplicate) {
    return 'This IPv4 address is already assigned to another device.'
  }

  return null
}

export default function DHCPandDNS(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const [cachedAtMount] = useState<ModuleView | undefined>(() =>
    getPageDataCache<ModuleView>(STATIC_LEASES_CACHE_KEY)
  )

  const mainModule = useMemo(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [view, setView] = useState<ModuleView>(
    () =>
      cachedAtMount ?? {
        name: 'Router',
        ip: undefined,
        leases: [],
        loading: false
      }
  )

  const [form, setForm] = useState<NewLeaseForm>(INITIAL_FORM)
  const [search, setSearch] = useState('')

  const [actionConfirm, setActionConfirm] = useState<{
    isOpen: boolean
    type: 'add' | 'delete'
    section?: string
  }>({ isOpen: false, type: 'add' })

  const [isProcessing, setIsProcessing] = useState(false)

  // ---------- API Helpers ----------

  const fetchLeases = useCallback(async (): Promise<StaticLease[]> => {
    const res = await apiFetch('/api/lan/static-leases-config', {
      method: 'GET'
    })

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || `HTTP ${res.status}`)
    }

    const data = await res.json()
    return Array.isArray(data) ? data : []
  }, [])

  const addLease = useCallback(async (data: NewLeaseForm) => {
    const formattedData = {
      ...data,
      hostname: data.hostname.trim(),
      mac: normalizeMac(data.mac),
      ipaddr: data.ipaddr.trim()
    }

    const res = await apiFetch('/api/lan/static-leases-config', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(formattedData)
    })

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      let message = txt || 'Add failed'
      try {
        const parsed = JSON.parse(txt) as { error?: string }
        if (parsed.error) message = parsed.error
      } catch {
        // Keep the plain response if it is not JSON.
      }
      throw new Error(message)
    }
  }, [])

  const deleteLease = useCallback(async (section: string) => {
    const res = await apiFetch(
      `/api/lan/static-leases-config?section=${encodeURIComponent(section)}`,
      {
        method: 'DELETE'
      }
    )

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || 'Delete failed')
    }
  }, [])

  // ---------- Data Loading ----------

  const loadAll = useCallback(async (isBackground = false) => {
    setView((prev) => ({
      ...prev,
      name: mainModule?.name || 'Router',
      ip: mainModule?.ipaddress,
      loading: true,
      error: undefined
    }))

    try {
      const leases = await fetchLeases()

      const nextView: ModuleView = {
        name: mainModule?.name || 'Router',
        ip: mainModule?.ipaddress,
        leases,
        loading: false,
        error: undefined
      }
      setPageDataCache(STATIC_LEASES_CACHE_KEY, nextView)
      setView(nextView)
    } catch (error: unknown) {
      setView((prev) => ({
        name: mainModule?.name || prev.name || 'Router',
        ip: mainModule?.ipaddress || prev.ip,
        leases: isBackground ? prev.leases : [],
        loading: false,
        error: getErrorMessage(error)
      }))
    }
  }, [fetchLeases, mainModule?.ipaddress, mainModule?.name])

  useEffect(() => {
    void loadAll(cachedAtMount !== undefined)
  }, [cachedAtMount, loadAll])

  // ---------- Derived Data ----------

  const filteredLeases = useMemo(() => {
    const q = search.trim().toLowerCase()

    if (!q) return view.leases

    return view.leases.filter((lease) => {
      return [
        lease.hostname,
        lease.mac,
        lease.ipaddr,
        lease.section
      ].some((field) => String(field || '').toLowerCase().includes(q))
    })
  }, [search, view.leases])

  // ---------- Event Handlers ----------

  const handleFormChange = (field: keyof NewLeaseForm, val: string) => {
    setForm((prev) => ({
      ...prev,
      [field]: val
    }))
  }

  const triggerAdd = () => {
    if (!form.mac || !form.ipaddr) {
      toast.error('Validation Error', { description: 'MAC Address and IPv4 Address are required.' })
      return
    }

    const macRegex = /^([0-9A-Fa-f]{2}[:-]){5}([0-9A-Fa-f]{2})$/
    if (!macRegex.test(form.mac.trim())) {
      toast.error('Invalid MAC Format', { description: 'Please use format XX:XX:XX:XX:XX:XX' })
      return
    }

    const ipError = getStaticLeaseIPError(form.ipaddr, form.mac, view.leases)
    if (ipError) {
      toast.error('Invalid Static Address', { description: ipError })
      return
    }

    setActionConfirm({ isOpen: true, type: 'add' })
  }

  const triggerDelete = (section: string) => {
    setActionConfirm({ isOpen: true, type: 'delete', section })
  }

  const executeAction = async () => {
    const { type, section } = actionConfirm

    setActionConfirm({ isOpen: false, type })
    setIsProcessing(true)

    try {
      setView((prev) => ({ ...prev, loading: true }))

      if (type === 'add') {
        await addLease(form)
        setForm(INITIAL_FORM)
      } else {
        if (!section) throw new Error('Missing section')
        await deleteLease(section)
      }

      await delay(1200)

      const newLeases = await fetchLeases()
      const nextView: ModuleView = {
        name: mainModule?.name || view.name || 'Router',
        ip: mainModule?.ipaddress || view.ip,
        leases: newLeases || [],
        loading: false,
        error: undefined
      }

      setPageDataCache(STATIC_LEASES_CACHE_KEY, nextView)
      setView(nextView)

      toast.success('Success', {
        description: `Static lease successfully ${type === 'add' ? 'added' : 'deleted'}.`
      })
    } catch (error: unknown) {
      toast.error('Operation Failed', { description: getErrorMessage(error) })
      setView((prev) => ({
        ...prev,
        loading: false,
        error: getErrorMessage(error)
      }))
    } finally {
      setIsProcessing(false)
    }
  }

  const safeLeases = filteredLeases
  const totalLeases = view.leases.length
  const filteredCount = filteredLeases.length

  return (
    <PageShell size="full" className="relative">
      <PageHeader title="Static DHCP Leases" description="Manage fixed IPv4 assignments on the DHCP server." />

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard label="Total Static Leases" value={totalLeases} />
        <StatCard label="Filtered Results" value={filteredCount} />
        <StatCard label="DHCP Server" value={view.name || 'Router'} detail={view.ip || ''} />
      </div>

      <SearchField
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search hostname, MAC address, IPv4 address, or section..."
          className="sm:w-full"
      />

      <Card className="flex flex-col overflow-hidden shadow-sm">
        <CardHeader className="border-b border-border pb-4">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3">
              <div className="p-2 bg-primary/10 rounded-md">
                <Server className="h-5 w-5 text-primary" />
              </div>
              <div>
                <CardTitle className="text-lg">{view.name || 'Router'}</CardTitle>
                <CardDescription className="font-mono mt-0.5">
                  {view.ip || 'DHCP Server'}
                </CardDescription>
              </div>
            </div>

            <div className="flex items-center gap-2">
              {view.loading && (
                <Badge variant="secondary" className="animate-pulse">
                  Syncing...
                </Badge>
              )}
              {view.error && <Badge variant="destructive">Error</Badge>}
            </div>
          </div>
        </CardHeader>

        <CardContent className="p-0 flex-1 flex flex-col">
          {view.error ? (
            <div className="p-6 text-destructive text-sm">
              <p className="font-medium">Failed to load configuration:</p>
              <p className="mt-1">{view.error}</p>
            </div>
          ) : (
            <div className="flex flex-col h-full">
              <div className="flex-1 overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Hostname</TableHead>
                      <TableHead>MAC Address</TableHead>
                      <TableHead>IPv4 Address</TableHead>
                      <TableHead className="w-[80px] text-right">Actions</TableHead>
                    </TableRow>
                  </TableHeader>

                  <TableBody>
                    {safeLeases.length === 0 ? (
                      <TableRow>
                        <TableCell
                          colSpan={4}
                          className="h-24 text-center text-muted-foreground italic"
                        >
                          {search.trim()
                            ? 'No static DHCP leases matched your search.'
                            : 'No static DHCP leases configured.'}
                        </TableCell>
                      </TableRow>
                    ) : (
                      safeLeases.map((lease) => (
                        <TableRow key={lease.section}>
                          <TableCell className="font-medium">
                            {lease.hostname || '-'}
                          </TableCell>

                          <TableCell className="font-mono text-muted-foreground">
                            {lease.mac}
                          </TableCell>

                          <TableCell className="font-mono text-primary">
                            {lease.ipaddr}
                          </TableCell>

                          <TableCell className="text-right">
                            <Button
                              variant="destructiveOutline"
                              size="icon"
                              onClick={() => triggerDelete(lease.section)}
                              disabled={view.loading}
                              title="Delete static DHCP lease"
                              aria-label="Delete static DHCP lease"
                            >
                              <Trash2 className="h-4 w-4" />
                            </Button>
                          </TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              </div>

              <div className="p-4 border-t border-border mt-auto">
                <div className="flex items-center gap-2 mb-4">
                  <Plus className="h-4 w-4 text-primary" />
                  <h4 className="text-sm font-semibold text-foreground">Add Static Lease</h4>
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-3 gap-4 mb-4">
                  <div className="space-y-1.5">
                    <Label htmlFor="host-ac" className="text-xs text-muted-foreground">
                      Hostname (Optional)
                    </Label>
                    <Input
                      id="host-ac"
                      placeholder="e.g. NAS-Server"
                      className="h-8 text-xs"
                      value={form.hostname}
                      onChange={(e) => handleFormChange('hostname', e.target.value)}
                    />
                  </div>

                  <div className="space-y-1.5">
                    <Label htmlFor="mac-ac" className="text-xs text-muted-foreground">
                      MAC Address <span className="text-destructive">*</span>
                    </Label>
                    <Input
                      id="mac-ac"
                      placeholder="AA:BB:CC:DD:EE:FF"
                      className="h-8 text-xs font-mono uppercase"
                      value={form.mac}
                      onChange={(e) => handleFormChange('mac', e.target.value)}
                    />
                  </div>

                  <div className="space-y-1.5">
                    <Label htmlFor="ip-ac" className="text-xs text-muted-foreground">
                      IPv4 Address <span className="text-destructive">*</span>
                    </Label>
                    <Input
                      id="ip-ac"
                      placeholder={`${STATIC_LEASE_PREFIX}.${STATIC_LEASE_MIN_HOST}`}
                      className="h-8 text-xs font-mono"
                      value={form.ipaddr}
                      onChange={(e) => handleFormChange('ipaddr', e.target.value)}
                    />
                    <div className="text-xs text-muted-foreground">
                      Allowed range: {STATIC_LEASE_PREFIX}.{STATIC_LEASE_MIN_HOST}–
                      {STATIC_LEASE_PREFIX}.{STATIC_LEASE_MAX_HOST}
                    </div>
                  </div>
                </div>

                <div className="flex justify-end">
                  <Button
                    size="sm"
                    onClick={triggerAdd}
                    disabled={view.loading || !form.mac || !form.ipaddr}
                    className="w-full sm:w-auto"
                  >
                    <Save className="h-4 w-4 mr-2" />
                    Save Lease
                  </Button>
                </div>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <AlertDialog
        open={actionConfirm.isOpen}
        onOpenChange={(open) => {
          if (!open) setActionConfirm({ ...actionConfirm, isOpen: false })
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {actionConfirm.type === 'add' ? 'Apply New Lease?' : 'Delete Static Lease?'}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {actionConfirm.type === 'add'
                ? 'This will bind the IP to the MAC address on the DHCP server. The DHCP service will reload.'
                : 'Are you sure you want to remove this static DHCP lease from the DHCP server? The DHCP service will reload.'}
            </AlertDialogDescription>
          </AlertDialogHeader>

          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant={actionConfirm.type === 'delete' ? 'destructive' : 'default'}
              onClick={() => {
                void executeAction()
              }}
            >
              Continue
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {isProcessing && (
        <FullScreenTaskOverlay>
              <RefreshCw className="mb-4 h-10 w-10 animate-spin text-primary" />
              <h3 className="text-lg font-semibold text-foreground">Applying Configuration</h3>
              <p className="mt-2 text-center text-sm text-muted-foreground">
                Reloading DHCP service...
              </p>
        </FullScreenTaskOverlay>
      )}
    </PageShell>
  )
}

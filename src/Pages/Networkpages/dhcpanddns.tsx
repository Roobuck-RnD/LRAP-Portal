// src/pages/DHCPandDNS.tsx
import type { JSX } from 'react'
import { useEffect, useState, useCallback, useMemo } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Trash2, Plus, RefreshCw, Save, Server, Search } from 'lucide-react'

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

// ---------- Helpers ----------

const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message
  return String(error)
}

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

function normalizeMac(mac: string): string {
  return mac.trim().toUpperCase()
}

export default function DHCPandDNS(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const mainModule = useMemo(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [view, setView] = useState<ModuleView>({
    name: 'Main Controller',
    ip: undefined,
    leases: [],
    loading: false
  })

  const [form, setForm] = useState<NewLeaseForm>(INITIAL_FORM)
  const [search, setSearch] = useState('')

  const [actionConfirm, setActionConfirm] = useState<{
    isOpen: boolean
    type: 'add' | 'delete'
    section?: string
  }>({ isOpen: false, type: 'add' })

  const [isProcessing, setIsProcessing] = useState(false)

  const getToken = () => sessionStorage.getItem('token')?.trim() || ''

  // ---------- API Helpers ----------

  const fetchLeases = useCallback(async (): Promise<StaticLease[]> => {
    const token = getToken()

    const res = await apiFetch('/api/lan/static-leases-config', {
      method: 'GET',
      headers: token ? { Authorization: `Bearer ${token}` } : {}
    })

    if (res.status === 401) {
      sessionStorage.removeItem('isLoggedIn')
      sessionStorage.removeItem('token')
      throw new Error('Unauthorized')
    }

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || `HTTP ${res.status}`)
    }

    const data = await res.json()
    return Array.isArray(data) ? data : []
  }, [])

  const addLease = useCallback(async (data: NewLeaseForm) => {
    const token = getToken()

    const formattedData = {
      ...data,
      hostname: data.hostname.trim(),
      mac: normalizeMac(data.mac),
      ipaddr: data.ipaddr.trim()
    }

    const res = await apiFetch('/api/lan/static-leases-config', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(token ? { Authorization: `Bearer ${token}` } : {})
      },
      body: JSON.stringify(formattedData)
    })

    if (res.status === 401) {
      sessionStorage.removeItem('isLoggedIn')
      sessionStorage.removeItem('token')
      throw new Error('Unauthorized')
    }

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || 'Add failed')
    }
  }, [])

  const deleteLease = useCallback(async (section: string) => {
    const token = getToken()

    const res = await apiFetch(
      `/api/lan/static-leases-config?section=${encodeURIComponent(section)}`,
      {
        method: 'DELETE',
        headers: token ? { Authorization: `Bearer ${token}` } : {}
      }
    )

    if (res.status === 401) {
      sessionStorage.removeItem('isLoggedIn')
      sessionStorage.removeItem('token')
      throw new Error('Unauthorized')
    }

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || 'Delete failed')
    }
  }, [])

  // ---------- Data Loading ----------

  const loadAll = useCallback(async () => {
    setView((prev) => ({
      ...prev,
      name: mainModule?.name || 'Main Controller',
      ip: mainModule?.ipaddress,
      loading: true,
      error: undefined
    }))

    try {
      const leases = await fetchLeases()

      setView({
        name: mainModule?.name || 'Main Controller',
        ip: mainModule?.ipaddress,
        leases,
        loading: false,
        error: undefined
      })
    } catch (error: unknown) {
      setView({
        name: mainModule?.name || 'Main Controller',
        ip: mainModule?.ipaddress,
        leases: [],
        loading: false,
        error: getErrorMessage(error)
      })
    }
  }, [fetchLeases, mainModule?.ipaddress, mainModule?.name])

  useEffect(() => {
    void loadAll()
  }, [loadAll])

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

      setView((prev) => ({
        ...prev,
        leases: newLeases || [],
        loading: false,
        error: undefined
      }))

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
    <div className="p-6 max-w-[1600px] mx-auto relative">
      <div className="mb-8 flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h2 className="text-3xl font-bold tracking-tight text-gray-900">Static DHCP Leases</h2>
          <p className="text-sm text-muted-foreground mt-1">
            Manage fixed IPv4 assignments on the AC DHCP server. AP modules are pure APs and do not run DHCP.
          </p>
        </div>

        <Button variant="outline" onClick={() => void loadAll()} className="gap-2">
          <RefreshCw className={`h-4 w-4 ${view.loading ? 'animate-spin' : ''}`} />
          Refresh
        </Button>
      </div>

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Total Static Leases</CardDescription>
            <CardTitle className="text-3xl">{totalLeases}</CardTitle>
          </CardHeader>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>Filtered Results</CardDescription>
            <CardTitle className="text-3xl">{filteredCount}</CardTitle>
          </CardHeader>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardDescription>DHCP Server</CardDescription>
            <CardTitle className="text-lg">{view.name || 'Main Controller'}</CardTitle>
            <CardDescription className="font-mono">{view.ip || 'Local AC'}</CardDescription>
          </CardHeader>
        </Card>
      </div>

      <div className="mb-6 flex items-center gap-2 rounded-xl border bg-white px-3 py-2 shadow-sm">
        <Search className="h-4 w-4 text-gray-400" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search hostname, MAC address, IPv4 address, or section..."
          className="border-0 shadow-none focus-visible:ring-0"
        />
      </div>

      <Card className="flex flex-col overflow-hidden shadow-sm">
        <CardHeader className="border-b bg-muted/40 pb-4">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3">
              <div className="p-2 bg-primary/10 rounded-md">
                <Server className="h-5 w-5 text-primary" />
              </div>
              <div>
                <CardTitle className="text-lg">{view.name || 'Main Controller'}</CardTitle>
                <CardDescription className="font-mono mt-0.5">
                  {view.ip || 'AC DHCP Server'}
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
                    <TableRow className="bg-muted/20">
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
                              variant="ghost"
                              size="icon"
                              onClick={() => triggerDelete(lease.section)}
                              disabled={view.loading}
                              className="text-muted-foreground hover:text-destructive hover:bg-destructive/10"
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

              <div className="p-4 bg-muted/20 border-t mt-auto">
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
                      placeholder="10.10.18.x"
                      className="h-8 text-xs font-mono"
                      value={form.ipaddr}
                      onChange={(e) => handleFormChange('ipaddr', e.target.value)}
                    />
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
                ? 'This will bind the IP to the MAC address on the AC DHCP server. The DHCP service will reload.'
                : 'Are you sure you want to remove this static DHCP lease from the AC DHCP server? The DHCP service will reload.'}
            </AlertDialogDescription>
          </AlertDialogHeader>

          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                void executeAction()
              }}
              className={
                actionConfirm.type === 'delete'
                  ? 'bg-destructive text-destructive-foreground hover:bg-destructive/90'
                  : ''
              }
            >
              Continue
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {isProcessing && (
        <div className="fixed inset-0 z-[9999] flex flex-col items-center justify-center bg-background/80 backdrop-blur-sm transition-opacity">
          <Card className="w-[300px] shadow-2xl animate-in zoom-in-95">
            <CardContent className="pt-6 pb-6 flex flex-col items-center">
              <RefreshCw className="mb-4 h-10 w-10 animate-spin text-primary" />
              <h3 className="text-lg font-semibold text-foreground">Applying Configuration</h3>
              <p className="mt-2 text-center text-sm text-muted-foreground">
                Reloading DHCP service on AC...
              </p>
            </CardContent>
          </Card>
        </div>
      )}
    </div>
  )
}
import type { JSX } from 'react'
import { useEffect, useState, useCallback, useMemo } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Trash2, Plus, Save, Route } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Table } from '@/components/ui/table'
import { SearchField } from '@/components/data-table'
import { PageHeader, PageShell, StatCard } from '@/components/page'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'

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

import { toast } from 'sonner'

// ---------- Types ----------

type StaticRoute = {
  section: string
  interface: string
  target: string
  netmask: string
  gateway: string
  metric: string
}

type NewRouteForm = {
  interface: string
  target: string
  netmask: string
  gateway: string
  metric: string
}

type Module = {
  name: string
  ipaddress: string
  type: string
  port?: string
}

const INITIAL_FORM: NewRouteForm = {
  interface: 'lan',
  target: '',
  netmask: '255.255.255.0',
  gateway: '',
  metric: '0'
}

// ---------- Helpers ----------

const getErrorMessage = (error: unknown): string => {
  if (error instanceof Error) return error.message
  return String(error)
}

function normalizeRouteField(value: string): string {
  return value.trim()
}

function isValidIPv4(value: string): boolean {
  const parts = value.trim().split('.')
  if (parts.length !== 4) return false

  return parts.every((part) => {
    if (!/^\d+$/.test(part)) return false
    const num = Number(part)
    return num >= 0 && num <= 255
  })
}

const STATIC_ROUTES_CACHE_KEY = 'network.static-routes'

function ipv4ToUint32(value: string): number {
  return value
    .trim()
    .split('.')
    .reduce((result, part) => ((result * 256) + Number(part)) >>> 0, 0)
}

function netmaskPrefixLength(value: string): number | null {
  if (!isValidIPv4(value)) return null

  const binary = value
    .trim()
    .split('.')
    .map((part) => Number(part).toString(2).padStart(8, '0'))
    .join('')

  if (!/^1*0*$/.test(binary)) return null
  return binary.indexOf('0') === -1 ? 32 : binary.indexOf('0')
}

export default function StaticRoutes(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const [cachedAtMount] = useState<StaticRoute[] | undefined>(() =>
    getPageDataCache<StaticRoute[]>(STATIC_ROUTES_CACHE_KEY)
  )

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [routes, setRoutes] = useState<StaticRoute[]>(() => cachedAtMount ?? [])
  const [form, setForm] = useState<NewRouteForm>(INITIAL_FORM)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | undefined>(undefined)
  const [search, setSearch] = useState('')

  const [actionConfirm, setActionConfirm] = useState<{
    isOpen: boolean
    type: 'add' | 'delete'
    section?: string
  }>({ isOpen: false, type: 'add' })

  // ---------- API Helpers ----------

  const fetchRoutes = useCallback(async (): Promise<StaticRoute[]> => {
    const res = await apiFetch('/api/net/static-routes', {
      method: 'GET'
    })

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || `HTTP ${res.status}`)
    }

    const data = await res.json()
    const list = Array.isArray(data) ? data : []
    setPageDataCache(STATIC_ROUTES_CACHE_KEY, list)
    return list
  }, [])

  const addRoute = useCallback(async (data: NewRouteForm) => {
    const payload: NewRouteForm = {
      interface: normalizeRouteField(data.interface),
      target: normalizeRouteField(data.target),
      netmask: normalizeRouteField(data.netmask),
      gateway: normalizeRouteField(data.gateway),
      metric: normalizeRouteField(data.metric)
    }

    const res = await apiFetch('/api/net/static-routes', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    })

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || 'Add failed')
    }
  }, [])

  const deleteRoute = useCallback(async (section: string) => {
    const res = await apiFetch(
      `/api/net/static-routes?section=${encodeURIComponent(section)}`,
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

  const loadRoutes = useCallback(async (isBackground = false) => {
    setLoading(true)
    setError(undefined)

    try {
      const list = await fetchRoutes()
      setRoutes(list)
    } catch (err: unknown) {
      const msg = getErrorMessage(err)
      setError(msg)
      if (!isBackground) {
        setRoutes([])
      }
    } finally {
      setLoading(false)
    }
  }, [fetchRoutes])

  useEffect(() => {
    void loadRoutes(cachedAtMount !== undefined)
  }, [cachedAtMount, loadRoutes])

  // ---------- Derived ----------

  const filteredRoutes = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return routes

    return routes.filter((route) =>
      [
        route.section,
        route.interface,
        route.target,
        route.netmask,
        route.gateway,
        route.metric
      ].some((field) => String(field || '').toLowerCase().includes(q))
    )
  }, [routes, search])

  // ---------- Event Handlers ----------

  const handleFormChange = (field: keyof NewRouteForm, val: string) => {
    setForm((prev) => ({
      ...prev,
      [field]: val
    }))
  }

  const validateForm = (): string | null => {
    if (!form.interface.trim()) return 'Interface is required.'
    if (!form.target.trim()) return 'Target IP is required.'
    if (!form.netmask.trim()) return 'Netmask is required.'

    if (!isValidIPv4(form.target)) return 'Target must be a valid IPv4 address.'
    if (!isValidIPv4(form.netmask)) return 'Netmask must be a valid IPv4 address.'

    if (netmaskPrefixLength(form.netmask) === null) {
      return 'Netmask must contain contiguous bits.'
    }

    const target = ipv4ToUint32(form.target)
    const netmask = ipv4ToUint32(form.netmask)
    if (((target & netmask) >>> 0) !== target) {
      return 'Target must be the network address for the selected netmask.'
    }

    if (form.gateway.trim() && !isValidIPv4(form.gateway)) {
      return 'Gateway must be a valid IPv4 address.'
    }

    if (form.metric.trim() && !/^\d+$/.test(form.metric.trim())) {
      return 'Metric must be a non-negative number.'
    }

    return null
  }

  const triggerAdd = () => {
    const validationError = validateForm()

    if (validationError) {
      toast.error('Validation Error', {
        description: validationError
      })
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
    setLoading(true)
    setError(undefined)

    try {
      if (type === 'add') {
        await addRoute(form)
        setForm(INITIAL_FORM)

        toast.info('Applying Route', {
          description: 'Saving and activating the static route...'
        })
      } else {
        if (!section) throw new Error('Missing route section.')

        await deleteRoute(section)

        toast.info('Applying Route', {
          description: 'Removing the static route...'
        })
      }

      const newRoutes = await fetchRoutes()
      setRoutes(newRoutes)
      setError(undefined)

      toast.success('Success', {
        description: `Static route successfully ${type === 'add' ? 'added' : 'deleted'}.`
      })
    } catch (err: unknown) {
      const msg = getErrorMessage(err)
      setError(msg)

      toast.error('Operation Failed', {
        description: msg
      })
    } finally {
      setLoading(false)
    }
  }

  return (
    <PageShell className="relative">
      <PageHeader title="Static IPv4 Routes" description="Configure persistent routing rules." />

        <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <StatCard label="DHCP / Gateway Device" value={acModule?.name || 'Router'} detail={acModule?.ipaddress || ''} />
          <StatCard label="Total Routes" value={routes.length} />
          <StatCard label="Filtered Results" value={filteredRoutes.length} />
        </div>

        <SearchField
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search interface, target, netmask, gateway, metric..."
            className="sm:w-full"
        />

        <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
          <div className="flex items-center justify-between border-b border-border px-4 py-3">
            <div className="flex items-center gap-2">
              <Route className="h-5 w-5 text-primary" />
              <div>
                <h3 className="text-lg font-semibold text-foreground">
                  {acModule?.name || 'Router'}
                </h3>
                <div className="font-mono text-xs text-muted-foreground">
                  {acModule?.ipaddress || 'Static Routes'}
                </div>
              </div>
            </div>

            {loading && <span className="text-xs text-primary animate-pulse">Syncing...</span>}
            {error && <span className="text-xs font-medium text-destructive">Error</span>}
          </div>

          <div className="p-4">
            {error && (
              <div className="mb-4 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}

            <div className="mb-6 overflow-x-auto rounded border border-border">
              <Table className="min-w-full text-left text-xs">
                <thead className="border-b border-border text-muted-foreground">
                  <tr>
                    <th className="px-3 py-2 font-medium">Interface</th>
                    <th className="px-3 py-2 font-medium">Target</th>
                    <th className="px-3 py-2 font-medium">Netmask</th>
                    <th className="px-3 py-2 font-medium">Gateway</th>
                    <th className="px-3 py-2 text-right font-medium">Metric</th>
                    <th className="w-10 px-3 py-2 font-medium"></th>
                  </tr>
                </thead>

                <tbody className="divide-y divide-border">
                  {filteredRoutes.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="px-3 py-8 text-center italic text-muted-foreground">
                        {search.trim()
                          ? 'No static routes matched your search.'
                          : 'No static routes configured.'}
                      </td>
                    </tr>
                  ) : (
                    filteredRoutes.map((route) => (
                      <tr key={route.section} className="group hover:bg-muted">
                        <td className="px-3 py-2 font-medium text-foreground">
                          {route.interface || '-'}
                        </td>
                        <td className="px-3 py-2 font-mono">{route.target}</td>
                        <td className="px-3 py-2 font-mono text-muted-foreground">{route.netmask}</td>
                        <td className="px-3 py-2 font-mono text-muted-foreground">
                          {route.gateway || '-'}
                        </td>
                        <td className="px-3 py-2 text-right text-muted-foreground">
                          {route.metric || '0'}
                        </td>
                        <td className="px-3 py-2 text-right">
                          <Button
                            variant="destructiveOutline"
                            size="icon"
                            onClick={() => triggerDelete(route.section)}
                            title="Delete Route"
                            aria-label="Delete route"
                            disabled={loading}
                          >
                            <Trash2 size={14} />
                          </Button>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </Table>
            </div>

            <div className="rounded-lg border border-border bg-muted/40 p-4">
              <div className="mb-3 flex items-center gap-1 text-xs font-semibold uppercase text-muted-foreground">
                <Plus size={12} className="text-primary" />
                Add Static Route
              </div>

              <div className="mb-3 grid grid-cols-12 gap-2">
                <div className="col-span-4 sm:col-span-2">
                  <Label className="mb-1 block text-xs">Interface</Label>
                  <NativeSelect
                    className="h-8 text-xs"
                    value={form.interface}
                    onChange={(e) => handleFormChange('interface', e.target.value)}
                  >
                    <option value="lan">lan</option>
                    <option value="wan">wan</option>
                    <option value="vpn">vpn</option>
                  </NativeSelect>
                </div>

                <div className="col-span-8 sm:col-span-3">
                  <Label className="mb-1 block text-xs">Target IP</Label>
                  <Input
                    type="text"
                    placeholder="192.168.50.0"
                    className="h-8 text-xs"
                    value={form.target}
                    onChange={(e) => handleFormChange('target', e.target.value)}
                  />
                </div>

                <div className="col-span-4 sm:col-span-3">
                  <Label className="mb-1 block text-xs">Netmask</Label>
                  <Input
                    type="text"
                    placeholder="255.255.255.0"
                    className="h-8 text-xs"
                    value={form.netmask}
                    onChange={(e) => handleFormChange('netmask', e.target.value)}
                  />
                </div>

                <div className="col-span-5 sm:col-span-3">
                  <Label className="mb-1 block text-xs">Gateway</Label>
                  <Input
                    type="text"
                    className="h-8 text-xs"
                    value={form.gateway}
                    onChange={(e) => handleFormChange('gateway', e.target.value)}
                  />
                </div>

                <div className="col-span-3 sm:col-span-1">
                  <Label className="mb-1 block text-xs">Metric</Label>
                  <Input
                    type="number"
                    min={0}
                    className="h-8 text-xs"
                    value={form.metric}
                    onChange={(e) => handleFormChange('metric', e.target.value)}
                  />
                </div>
              </div>

              <div className="flex justify-end">
                <Button size="sm" onClick={triggerAdd} disabled={loading || !form.target}>
                  <Save size={14} />
                  Save & Apply
                </Button>
              </div>
            </div>
          </div>
        </div>
      <AlertDialog
        open={actionConfirm.isOpen}
        onOpenChange={(open) => {
          if (!open) setActionConfirm({ ...actionConfirm, isOpen: false })
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {actionConfirm.type === 'add' ? 'Apply New Route?' : 'Delete Static Route?'}
            </AlertDialogTitle>

            <AlertDialogDescription>
              {actionConfirm.type === 'add'
                ? form.target.trim() === '0.0.0.0' && form.netmask.trim() === '0.0.0.0'
                  ? 'This changes the default route immediately and may affect internet access. Network interfaces will not restart.'
                  : 'This route will be saved and activated immediately without restarting network interfaces.'
                : 'The selected route will be removed immediately without restarting network interfaces.'}
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
    </PageShell>
  )
}

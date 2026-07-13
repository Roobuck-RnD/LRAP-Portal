import type { JSX } from 'react'
import { useEffect, useState, useCallback, useMemo } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Trash2, Plus, RefreshCw, Save, Route, Search } from 'lucide-react'

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

const delay = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

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

export default function StaticRoutes(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const [routes, setRoutes] = useState<StaticRoute[]>([])
  const [form, setForm] = useState<NewRouteForm>(INITIAL_FORM)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | undefined>(undefined)
  const [search, setSearch] = useState('')
  const [isProcessing, setIsProcessing] = useState(false)

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
    return Array.isArray(data) ? data : []
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

  const loadRoutes = useCallback(async () => {
    setLoading(true)
    setError(undefined)

    try {
      const list = await fetchRoutes()
      setRoutes(list)
    } catch (err: unknown) {
      const msg = getErrorMessage(err)
      setError(msg)
      setRoutes([])
    } finally {
      setLoading(false)
    }
  }, [fetchRoutes])

  useEffect(() => {
    void loadRoutes()
  }, [loadRoutes])

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
    setIsProcessing(true)
    setLoading(true)
    setError(undefined)

    try {
      if (type === 'add') {
        await addRoute(form)
        setForm(INITIAL_FORM)

        toast.info('Applying Configuration', {
          description: 'Static route saved. Reloading network...'
        })
      } else {
        if (!section) throw new Error('Missing route section.')

        await deleteRoute(section)

        toast.info('Applying Configuration', {
          description: 'Static route deleted. Reloading network...'
        })
      }

      await delay(2500)

      let newRoutes: StaticRoute[] = []
      let reconnected = false

      for (let i = 0; i < 8; i++) {
        try {
          newRoutes = await fetchRoutes()
          reconnected = true
          break
        } catch {
          await delay(1500)
        }
      }

      if (!reconnected) {
        throw new Error('Network reload timeout. Please refresh later.')
      }

      setRoutes(newRoutes)
      setError(undefined)

      toast.success('Success', {
        description: `Static route successfully ${type === 'add' ? 'added' : 'deleted'}.`
      })
    } catch (err: unknown) {
      const msg = getErrorMessage(err)
      setError(msg)

      if (msg.includes('timeout') || msg.includes('Failed to fetch')) {
        toast.warning('Connection Lost', {
          description: 'Configuration may have applied, but the connection was temporarily lost.'
        })
      } else {
        toast.error('Operation Failed', {
          description: msg
        })
      }
    } finally {
      setLoading(false)
      setIsProcessing(false)
    }
  }

  return (
    <div className="relative p-6">
      <div className="mx-auto max-w-6xl">
        <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-3xl font-bold text-gray-900">Static IPv4 Routes</h2>
            <p className="mt-1 text-sm text-gray-500">
              Configure persistent routing rules.
            </p>
          </div>

          <button
            onClick={() => void loadRoutes()}
            disabled={loading}
            className="flex items-center gap-2 rounded bg-gray-100 px-3 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-200 disabled:opacity-50"
          >
            <RefreshCw size={16} className={loading ? 'animate-spin' : ''} />
            Refresh
          </button>
        </div>

        <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div className="rounded-xl border bg-white p-4 shadow-sm">
            <div className="text-sm text-gray-500">DHCP / Gateway Device</div>
            <div className="mt-2 text-xl font-bold text-gray-900">
              {acModule?.name || 'Router'}
            </div>
            <div className="mt-1 font-mono text-xs text-gray-500">
              {acModule?.ipaddress || ''}
            </div>
          </div>

          <div className="rounded-xl border bg-white p-4 shadow-sm">
            <div className="text-sm text-gray-500">Total Routes</div>
            <div className="mt-2 text-3xl font-bold text-gray-900">{routes.length}</div>
          </div>

          <div className="rounded-xl border bg-white p-4 shadow-sm">
            <div className="text-sm text-gray-500">Filtered Results</div>
            <div className="mt-2 text-3xl font-bold text-gray-900">{filteredRoutes.length}</div>
          </div>
        </div>

        <div className="mb-6 flex items-center gap-2 rounded-xl border bg-white px-3 py-2 shadow-sm">
          <Search className="h-4 w-4 text-gray-400" />
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search interface, target, netmask, gateway, metric..."
            className="w-full border-0 bg-transparent text-sm outline-none"
          />
        </div>

        <div className="overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
          <div className="flex items-center justify-between border-b border-gray-100 bg-gray-50 px-4 py-3">
            <div className="flex items-center gap-2">
              <Route className="h-5 w-5 text-blue-600" />
              <div>
                <h3 className="text-lg font-semibold text-gray-800">
                  {acModule?.name || 'Router'}
                </h3>
                <div className="font-mono text-xs text-gray-500">
                  {acModule?.ipaddress || 'Static Routes'}
                </div>
              </div>
            </div>

            {loading && <span className="text-xs text-blue-500 animate-pulse">Syncing...</span>}
            {error && <span className="text-xs font-medium text-red-500">Error</span>}
          </div>

          <div className="p-4">
            {error && (
              <div className="mb-4 rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
                {error}
              </div>
            )}

            <div className="mb-6 overflow-x-auto rounded border border-gray-200">
              <table className="min-w-full text-left text-xs">
                <thead className="bg-gray-50 text-gray-500">
                  <tr>
                    <th className="px-3 py-2 font-medium">Interface</th>
                    <th className="px-3 py-2 font-medium">Target</th>
                    <th className="px-3 py-2 font-medium">Netmask</th>
                    <th className="px-3 py-2 font-medium">Gateway</th>
                    <th className="px-3 py-2 text-right font-medium">Metric</th>
                    <th className="w-10 px-3 py-2 font-medium"></th>
                  </tr>
                </thead>

                <tbody className="divide-y divide-gray-100">
                  {filteredRoutes.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="px-3 py-8 text-center italic text-gray-400">
                        {search.trim()
                          ? 'No static routes matched your search.'
                          : 'No static routes configured.'}
                      </td>
                    </tr>
                  ) : (
                    filteredRoutes.map((route) => (
                      <tr key={route.section} className="group hover:bg-gray-50">
                        <td className="px-3 py-2 font-medium text-gray-700">
                          {route.interface || '-'}
                        </td>
                        <td className="px-3 py-2 font-mono">{route.target}</td>
                        <td className="px-3 py-2 font-mono text-gray-500">{route.netmask}</td>
                        <td className="px-3 py-2 font-mono text-gray-500">
                          {route.gateway || '-'}
                        </td>
                        <td className="px-3 py-2 text-right text-gray-500">
                          {route.metric || '0'}
                        </td>
                        <td className="px-3 py-2 text-right">
                          <button
                            onClick={() => triggerDelete(route.section)}
                            className="text-gray-400 transition hover:text-red-600"
                            title="Delete Route"
                            disabled={loading}
                          >
                            <Trash2 size={14} />
                          </button>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>

            <div className="rounded-lg border border-blue-100 bg-blue-50/50 p-4">
              <div className="mb-3 flex items-center gap-1 text-xs font-semibold uppercase text-blue-700">
                <Plus size={12} />
                Add Static Route
              </div>

              <div className="mb-3 grid grid-cols-12 gap-2">
                <div className="col-span-4 sm:col-span-2">
                  <label className="mb-0.5 block text-[10px] text-gray-500">Interface</label>
                  <select
                    className="w-full rounded border-gray-300 px-1 py-1 text-xs focus:border-blue-500 focus:ring-blue-500"
                    value={form.interface}
                    onChange={(e) => handleFormChange('interface', e.target.value)}
                  >
                    <option value="lan">lan</option>
                    <option value="wan">wan</option>
                    <option value="vpn">vpn</option>
                  </select>
                </div>

                <div className="col-span-8 sm:col-span-3">
                  <label className="mb-0.5 block text-[10px] text-gray-500">Target IP</label>
                  <input
                    type="text"
                    placeholder="192.168.50.0"
                    className="w-full rounded border-gray-300 px-2 py-1 text-xs focus:border-blue-500 focus:ring-blue-500"
                    value={form.target}
                    onChange={(e) => handleFormChange('target', e.target.value)}
                  />
                </div>

                <div className="col-span-4 sm:col-span-3">
                  <label className="mb-0.5 block text-[10px] text-gray-500">Netmask</label>
                  <input
                    type="text"
                    placeholder="255.255.255.0"
                    className="w-full rounded border-gray-300 px-2 py-1 text-xs focus:border-blue-500 focus:ring-blue-500"
                    value={form.netmask}
                    onChange={(e) => handleFormChange('netmask', e.target.value)}
                  />
                </div>

                <div className="col-span-5 sm:col-span-3">
                  <label className="mb-0.5 block text-[10px] text-gray-500">Gateway</label>
                  <input
                    type="text"
                    placeholder="10.10.18.254"
                    className="w-full rounded border-gray-300 px-2 py-1 text-xs focus:border-blue-500 focus:ring-blue-500"
                    value={form.gateway}
                    onChange={(e) => handleFormChange('gateway', e.target.value)}
                  />
                </div>

                <div className="col-span-3 sm:col-span-1">
                  <label className="mb-0.5 block text-[10px] text-gray-500">Metric</label>
                  <input
                    type="number"
                    min={0}
                    className="w-full rounded border-gray-300 px-1 py-1 text-xs focus:border-blue-500 focus:ring-blue-500"
                    value={form.metric}
                    onChange={(e) => handleFormChange('metric', e.target.value)}
                  />
                </div>
              </div>

              <div className="flex justify-end">
                <button
                  onClick={triggerAdd}
                  disabled={loading || !form.target}
                  className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1.5 text-xs text-white transition hover:bg-blue-700 disabled:opacity-50"
                >
                  <Save size={14} />
                  Save & Apply
                </button>
              </div>
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
                ? 'This will apply the static route. The network service will reload briefly.'
                : 'Are you sure you want to remove this static route? The network service will reload briefly.'}
            </AlertDialogDescription>
          </AlertDialogHeader>

          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                void executeAction()
              }}
              className={actionConfirm.type === 'delete' ? 'bg-red-600 hover:bg-red-700' : ''}
            >
              Continue
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {isProcessing && (
        <div className="fixed inset-0 z-[9999] flex flex-col items-center justify-center bg-black/40 backdrop-blur-sm transition-opacity">
          <div className="flex flex-col items-center rounded-2xl bg-white px-8 py-10 shadow-2xl">
            <RefreshCw className="mb-6 h-12 w-12 animate-spin text-blue-600" />
            <h3 className="text-xl font-bold text-gray-800">Applying Configuration</h3>
            <p className="mt-3 text-center text-sm leading-relaxed text-gray-500">
              Reloading network service.
              <br />
              Please wait while we reconnect...
            </p>
          </div>
        </div>
      )}
    </div>
  )
}
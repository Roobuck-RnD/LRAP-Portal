import type { JSX } from 'react'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'

// ---------- Interfaces ----------

interface Module {
  name: string
  ipaddress: string
  type: string
  port?: string
}

interface SystemConfig {
  hostname: string
  timezone: string
  localtime: string
}

interface TimezoneOption {
  label: string
  value: string
}

interface SaveResponse {
  status: string
  message?: string
  synced_modules?: number
  warnings?: string[]
}

// ---------- Main Container ----------
function SystemSettings(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const [timezones, setTimezones] = useState<TimezoneOption[]>([])
  const [config, setConfig] = useState<SystemConfig>({
    hostname: '',
    timezone: 'UTC',
    localtime: 'Loading...'
  })

  const [firstLoad, setFirstLoad] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [warnings, setWarnings] = useState<string[]>([])
  const [syncedModules, setSyncedModules] = useState<number | null>(null)

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const apCount = useMemo(() => {
    return currentAllModule.filter((m) => m.type !== 'Main Module').length
  }, [currentAllModule])

  const fetchData = async (isBackground = false) => {
    if (!isBackground) {
      setError(null)
    }

    try {
      const res = await apiFetch('/api/system/general', {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json'
        }
      })

      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }

      const data = (await res.json()) as SystemConfig
      setConfig(data)
      setError(null)
    } catch (err: unknown) {
      if (!isBackground) {
        const msg = err instanceof Error ? err.message : String(err)
        setError(msg)
      }
    } finally {
      setFirstLoad(false)
    }
  }

  const fetchTimezones = async () => {
    try {
      const res = await apiFetch('/api/system/timezones')

      if (res.ok) {
        const list = (await res.json()) as TimezoneOption[]
        setTimezones(Array.isArray(list) ? list : [])
      }
    } catch (e) {
      console.error('Failed to load timezones', e)
    }
  }

  useEffect(() => {
    void fetchTimezones()
  }, [])

  useEffect(() => {
    void fetchData()

    const timer = window.setInterval(() => {
      void fetchData(true)
    }, 5000)

    return () => window.clearInterval(timer)
  }, [])

  const handleSave = async () => {
    setSaving(true)
    setError(null)
    setWarnings([])
    setSyncedModules(null)

    try {
      const res = await apiFetch('/api/system/general', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          hostname: config.hostname,
          timezone: config.timezone
        })
      })

      if (!res.ok) {
        const text = await res.text().catch(() => '')
        throw new Error(text || 'Save failed')
      }

      const result = (await res.json().catch(() => null)) as SaveResponse | null

      setSyncedModules(result?.synced_modules ?? null)
      setWarnings(result?.warnings ?? [])

      await fetchData()

      if (result?.warnings && result.warnings.length > 0) {
        toast.success('Saved, but some AP modules could not be synced. See warnings on the page.')
      } else {
        toast.success('Saved successfully. Timezone has been synced to online AP modules.')
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(msg)
      toast.error('Error', { description: msg })
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="p-6">
      <div className="mx-auto max-w-3xl">
        <div className="mb-6">
          <h2 className="text-2xl font-bold text-gray-800">System Properties</h2>
          <p className="text-sm text-gray-500">
            Configure AC hostname and synchronize timezone across AC and AP modules.
          </p>
        </div>

        <div className="flex flex-col overflow-hidden rounded-xl border border-gray-200 bg-white shadow-sm">
          <div className="flex items-center justify-between border-b border-gray-100 bg-gray-50 px-4 py-3">
            <div>
              <h3 className="text-lg font-semibold text-gray-800">
                {acModule?.name || config.hostname || 'Main Controller'}
              </h3>
              <div className="font-mono text-xs text-gray-500">
                {acModule?.ipaddress || 'Local AC'}
              </div>
            </div>

            <div className="text-right">
              <div className="rounded bg-blue-100 px-2 py-1 text-xs font-medium text-blue-700">
                AC
              </div>
              <div className="mt-1 text-xs text-gray-500">
                {apCount} AP module{apCount === 1 ? '' : 's'}
              </div>
            </div>
          </div>

          <div className="space-y-4 p-5">
            {firstLoad ? (
              <div className="text-sm text-gray-400">Loading settings...</div>
            ) : (
              <>
                {error && (
                  <div className="rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
                    {error}
                  </div>
                )}

                {warnings.length > 0 && (
                  <div className="rounded border border-amber-100 bg-amber-50 p-3 text-sm text-amber-700">
                    <div className="mb-1 font-medium">Sync warnings</div>
                    <ul className="list-disc space-y-1 pl-5">
                      {warnings.map((w, idx) => (
                        <li key={`${w}-${idx}`}>{w}</li>
                      ))}
                    </ul>
                  </div>
                )}

                {syncedModules !== null && warnings.length === 0 && (
                  <div className="rounded border border-green-100 bg-green-50 p-3 text-sm text-green-700">
                    Timezone synced to {syncedModules} AP module
                    {syncedModules === 1 ? '' : 's'}.
                  </div>
                )}

                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500">
                    Local Time
                  </label>
                  <div className="flex w-full items-center justify-between rounded border border-gray-200 bg-gray-50 px-3 py-2 font-mono text-sm text-gray-700">
                    <span>{config.localtime}</span>
                    <span className="h-2 w-2 animate-pulse rounded-full bg-green-400" />
                  </div>
                  <p className="mt-1 text-xs text-gray-500">
                    AP local time follows the same timezone. Exact clock time still depends on NTP/system clock sync.
                  </p>
                </div>

                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500">
                    AC Hostname
                  </label>
                  <input
                    type="text"
                    className="w-full rounded border border-gray-300 px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
                    value={config.hostname}
                    onChange={(e) => setConfig({ ...config, hostname: e.target.value })}
                  />
                  <p className="mt-1 text-xs text-gray-500">
                    Hostname is applied to AC only. AP hostnames remain unchanged.
                  </p>
                </div>

                <div>
                  <label className="mb-1 block text-xs font-medium text-gray-500">
                    Timezone
                  </label>
                  <select
                    className="w-full rounded border border-gray-300 bg-white px-3 py-2 text-sm focus:border-blue-500 focus:outline-none"
                    value={config.timezone}
                    onChange={(e) => setConfig({ ...config, timezone: e.target.value })}
                  >
                    <option value={config.timezone} disabled hidden>
                      {config.timezone}
                    </option>

                    {timezones.map((tz) => (
                      <option key={tz.label} value={tz.label}>
                        {tz.label}
                      </option>
                    ))}
                  </select>
                  <p className="mt-1 text-xs text-gray-500">
                    Timezone will be synced to AC and all reachable AP modules.
                  </p>
                </div>
              </>
            )}
          </div>

          <div className="border-t border-gray-100 bg-gray-50 px-4 py-3 text-right">
            <button
              onClick={handleSave}
              disabled={saving || firstLoad}
              className={`rounded px-4 py-2 text-sm font-medium text-white shadow-sm transition-colors ${
                saving ? 'cursor-not-allowed bg-blue-400' : 'bg-blue-600 hover:bg-blue-700'
              }`}
            >
              {saving ? 'Saving & Syncing...' : 'Save & Sync Timezone'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

export default SystemSettings
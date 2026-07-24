import type { JSX } from 'react'
import { useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { PageHeader, PageShell, SectionCard } from '@/components/page'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'

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

const SYSTEM_CONFIG_CACHE_KEY = 'system.general'
const TIMEZONES_CACHE_KEY = 'system.timezones'
const DEFAULT_SYSTEM_CONFIG: SystemConfig = {
  hostname: '',
  timezone: 'UTC',
  localtime: 'Loading...'
}

// ---------- Main Container ----------
function SystemSettings(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()
  const [cachedConfigAtMount] = useState<SystemConfig | undefined>(() =>
    getPageDataCache<SystemConfig>(SYSTEM_CONFIG_CACHE_KEY)
  )

  const [timezones, setTimezones] = useState<TimezoneOption[]>(
    () => getPageDataCache<TimezoneOption[]>(TIMEZONES_CACHE_KEY) ?? []
  )
  const [config, setConfig] = useState<SystemConfig>(
    () => cachedConfigAtMount ?? DEFAULT_SYSTEM_CONFIG
  )

  const [firstLoad, setFirstLoad] = useState(() => cachedConfigAtMount === undefined)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [warnings, setWarnings] = useState<string[]>([])

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
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
      setPageDataCache(SYSTEM_CONFIG_CACHE_KEY, data)
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
        const normalizedList = Array.isArray(list) ? list : []
        setPageDataCache(TIMEZONES_CACHE_KEY, normalizedList)
        setTimezones(normalizedList)
      }
    } catch (e) {
      console.error('Failed to load timezones', e)
    }
  }

  useEffect(() => {
    void fetchTimezones()
  }, [])

  useEffect(() => {
    void fetchData(cachedConfigAtMount !== undefined)
  }, [cachedConfigAtMount])

  const handleSave = async () => {
    setSaving(true)
    setError(null)
    setWarnings([])

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

      setWarnings(result?.warnings ?? [])

      await fetchData()

      if (result?.warnings && result.warnings.length > 0) {
        toast.success('Saved, but some settings could not be fully applied. See warnings on the page.')
      } else {
        toast.success('Saved successfully.')
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
    <PageShell size="narrow">
      <PageHeader
        title="System Properties"
        description="Configure the gateway identity, clock and timezone."
      />
      <SectionCard title={acModule?.name || config.hostname || 'Router'}>
          <div className="space-y-5">
            {firstLoad ? (
              <div className="text-sm text-muted-foreground">Loading settings...</div>
            ) : (
              <>
                {error && (
                  <div className="rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
                    {error}
                  </div>
                )}

                {warnings.length > 0 && (
                  <div className="rounded border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
                    <div className="mb-1 font-medium">Warnings</div>
                    <ul className="list-disc space-y-1 pl-5">
                      {warnings.map((w, idx) => (
                        <li key={`${w}-${idx}`}>{w}</li>
                      ))}
                    </ul>
                  </div>
                )}

                <div>
                  <Label className="mb-2 block">
                    Local Time
                  </Label>
                  <div className="flex w-full items-center justify-between rounded border border-border bg-muted px-3 py-2 font-mono text-sm text-foreground">
                    <span>{config.localtime}</span>
                    <span className="h-2 w-2 animate-pulse rounded-full bg-success" />
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Exact clock time still depends on NTP/system clock sync.
                  </p>
                </div>

                <div>
                  <Label htmlFor="system-hostname" className="mb-2 block">
                    Hostname
                  </Label>
                  <Input
                    id="system-hostname"
                    type="text"
                    value={config.hostname}
                    onChange={(e) => setConfig({ ...config, hostname: e.target.value })}
                  />
                  <p className="mt-1 text-xs text-muted-foreground">
                    This name identifies the device across the interface.
                  </p>
                </div>

                <div>
                  <Label htmlFor="system-timezone" className="mb-2 block">
                    Timezone
                  </Label>
                  <NativeSelect
                    id="system-timezone"
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
                  </NativeSelect>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Sets the system timezone.
                  </p>
                </div>
              </>
            )}
          <div className="mt-6 flex justify-end border-t border-border pt-4">
            <Button onClick={handleSave} disabled={saving || firstLoad}>
              {saving ? 'Saving…' : 'Save'}
            </Button>
          </div>
        </div>
      </SectionCard>
    </PageShell>
  )
}

export default SystemSettings

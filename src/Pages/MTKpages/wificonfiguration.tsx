import type { JSX } from 'react'
import { useEffect, useMemo, useState } from 'react'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Wifi, Save, AlertCircle, CheckCircle2, RadioTower, Settings2, Power, Eye, EyeOff } from 'lucide-react'

// ---------- Types ----------

interface WifiModuleRadio {
  name: string
  type: 'main' | 'ap' | string
  ip: string
  online: boolean

  channel_2g: string
  channel_width_2g: string
  tx_power_2g: number

  channel_5g: string
  channel_width_5g: string
  tx_power_5g: number

  // Desired/persistent runtime state saved in the main module central state file.
  radio_enabled_2g: boolean
  radio_enabled_5g: boolean

  // Current interface state. It may briefly differ from desired state after wifi restart/reboot until the main module enforcer runs.
  radio_running_2g?: boolean
  radio_running_5g?: boolean

  // Backward compatibility with the old backend response.
  channel?: string
  channel_width?: string
  tx_power?: number
}

interface WifiConfig {
  ssid_2g: string
  pass_2g: string
  ssid_5g: string
  pass_5g: string
  modules?: WifiModuleRadio[]
}

interface WifiSyncTargetResult {
  target: string
  ip: string
  ok: boolean
  error?: string
}

interface WifiSyncResponse {
  status: string
  results?: WifiSyncTargetResult[]
}

interface WifiRadioToggleResponse {
  status: string
  result?: WifiSyncTargetResult
  state?: {
    radio_2g_enabled: boolean
    radio_5g_enabled: boolean
    updated_at?: number
  }
  runtime?: {
    radio_2g_running: boolean
    radio_5g_running: boolean
  }
}

type RadioBand = '2g' | '5g'

type RadioPatch = Partial<
  Pick<
    WifiModuleRadio,
    | 'channel_2g'
    | 'channel_width_2g'
    | 'tx_power_2g'
    | 'channel_5g'
    | 'channel_width_5g'
    | 'tx_power_5g'
  >
>

const emptyConfig: WifiConfig = {
  ssid_2g: '',
  pass_2g: '',
  ssid_5g: '',
  pass_5g: '',
  modules: []
}

const channelOptions2G = [
  { value: '0', label: 'Channel 0 (Auto)' },
  { value: '1', label: 'Channel 1 (2.412 GHz)' },
  { value: '2', label: 'Channel 2 (2.417 GHz)' },
  { value: '3', label: 'Channel 3 (2.422 GHz)' },
  { value: '4', label: 'Channel 4 (2.427 GHz)' },
  { value: '5', label: 'Channel 5 (2.432 GHz)' },
  { value: '6', label: 'Channel 6 (2.437 GHz)' },
  { value: '7', label: 'Channel 7 (2.442 GHz)' },
  { value: '8', label: 'Channel 8 (2.447 GHz)' },
  { value: '9', label: 'Channel 9 (2.452 GHz)' },
  { value: '10', label: 'Channel 10 (2.457 GHz)' },
  { value: '11', label: 'Channel 11 (2.462 GHz)' }
]

const channelOptions5G = [
  { value: '0', label: 'Channel 0 (Auto)' },
  { value: '36', label: 'Channel 36 (5.180 GHz)' },
  { value: '40', label: 'Channel 40 (5.200 GHz)' },
  { value: '44', label: 'Channel 44 (5.220 GHz)' },
  { value: '48', label: 'Channel 48 (5.240 GHz)' },
  { value: '52', label: 'Channel 52 (5.260 GHz)' },
  { value: '56', label: 'Channel 56 (5.280 GHz)' },
  { value: '60', label: 'Channel 60 (5.300 GHz)' },
  { value: '64', label: 'Channel 64 (5.320 GHz)' },
  { value: '100', label: 'Channel 100 (5.500 GHz)' },
  { value: '104', label: 'Channel 104 (5.520 GHz)' },
  { value: '108', label: 'Channel 108 (5.540 GHz)' },
  { value: '112', label: 'Channel 112 (5.560 GHz)' },
  { value: '116', label: 'Channel 116 (5.580 GHz)' },
  { value: '132', label: 'Channel 132 (5.660 GHz)' },
  { value: '136', label: 'Channel 136 (5.680 GHz)' },
  { value: '140', label: 'Channel 140 (5.700 GHz)' },
  { value: '149', label: 'Channel 149 (5.745 GHz)' },
  { value: '153', label: 'Channel 153 (5.765 GHz)' },
  { value: '157', label: 'Channel 157 (5.785 GHz)' },
  { value: '161', label: 'Channel 161 (5.805 GHz)' },
  { value: '165', label: 'Channel 165 (5.825 GHz)' }
]

const channelWidthOptions2G = [
  { value: '20', label: '20 MHz' },
  { value: '40', label: '40 MHz' },
  { value: '20/40', label: '20/40 MHz' }
]

const channelWidthOptions5G = [
  { value: '20', label: '20 MHz' },
  { value: '40', label: '40 MHz' },
  { value: '80', label: '80 MHz' },
  { value: '160', label: '160 MHz' }
]

function validateSSIDPassword(label: string, ssidRaw: string, password: string): string | null {
  const ssid = ssidRaw.trim()

  if (!ssid) return `${label} SSID cannot be empty.`
  if (ssid.length > 32) return `${label} SSID should be 32 characters or less.`

  if (password && password.length < 8) {
    return `${label} password must be at least 8 characters.`
  }

  if (password.length > 63) {
    return `${label} password should be 63 characters or less.`
  }

  if (ssid.includes('\n') || ssid.includes('\r') || password.includes('\n') || password.includes('\r')) {
    return `${label} SSID/password cannot contain newline characters.`
  }

  return null
}

function validateWifiConfig(config: WifiConfig): string | null {
  const err2g = validateSSIDPassword('2.4GHz', config.ssid_2g, config.pass_2g)
  if (err2g) return err2g

  const err5g = validateSSIDPassword('5GHz', config.ssid_5g, config.pass_5g)
  if (err5g) return err5g

  for (const mod of config.modules || []) {
    const tx2g = Number(mod.tx_power_2g)
    const tx5g = Number(mod.tx_power_5g)

    if (!channelOptions2G.some((opt) => opt.value === String(mod.channel_2g))) {
      return `${mod.name}: invalid 2.4GHz channel.`
    }

    if (!channelWidthOptions2G.some((opt) => opt.value === mod.channel_width_2g)) {
      return `${mod.name}: invalid 2.4GHz channel width.`
    }

    if (!Number.isFinite(tx2g) || tx2g < 1 || tx2g > 100) {
      return `${mod.name}: 2.4GHz TX Power must be between 1 and 100.`
    }

    if (!channelOptions5G.some((opt) => opt.value === String(mod.channel_5g))) {
      return `${mod.name}: invalid 5GHz channel.`
    }

    if (!channelWidthOptions5G.some((opt) => opt.value === mod.channel_width_5g)) {
      return `${mod.name}: invalid 5GHz channel width.`
    }

    if (!Number.isFinite(tx5g) || tx5g < 1 || tx5g > 100) {
      return `${mod.name}: 5GHz TX Power must be between 1 and 100.`
    }
  }

  return null
}

function normalizeRadioModule(mod: Partial<WifiModuleRadio>): WifiModuleRadio {
  return {
    name: mod.name || 'Unknown Device',
    type: mod.type || 'ap',
    ip: mod.ip || '',
    online: mod.online !== false,

    channel_2g: String(mod.channel_2g ?? mod.channel ?? '0'),
    channel_width_2g: mod.channel_width_2g || mod.channel_width || '40',
    tx_power_2g: Number(mod.tx_power_2g ?? mod.tx_power ?? 100),

    channel_5g: String(mod.channel_5g ?? '0'),
    channel_width_5g: mod.channel_width_5g || '80',
    tx_power_5g: Number(mod.tx_power_5g ?? 100),

    radio_enabled_2g: mod.radio_enabled_2g !== false,
    radio_enabled_5g: mod.radio_enabled_5g !== false,
    radio_running_2g:
      typeof mod.radio_running_2g === 'boolean' ? mod.radio_running_2g : mod.radio_enabled_2g !== false,
    radio_running_5g:
      typeof mod.radio_running_5g === 'boolean' ? mod.radio_running_5g : mod.radio_enabled_5g !== false
  }
}

export default function WiFiConfiguration(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const [config, setConfig] = useState<WifiConfig>(emptyConfig)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [togglingRadioKey, setTogglingRadioKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [syncResults, setSyncResults] = useState<WifiSyncTargetResult[]>([])
  const [show2g, setShow2g] = useState(false)
  const [show5g, setShow5g] = useState(false)

  const moduleNameByIP = useMemo(() => {
    const map = new Map<string, string>()

    currentAllModule.forEach((m) => {
      if (m.ipaddress) {
        map.set(m.ipaddress, m.name)
      }
    })

    return map
  }, [currentAllModule])

  const fetchWifi = async (opts?: { silent?: boolean; preserveMessages?: boolean }) => {
    if (!opts?.silent) {
      setLoading(true)
    }

    setError(null)

    if (!opts?.preserveMessages) {
      setSuccess(null)
      setSyncResults([])
    }

    try {
      const res = await apiFetch('/api/mtk/wifi', {
        method: 'GET'
      })

      if (!res.ok) {
        const text = await res.text().catch(() => '')
        throw new Error(text || `HTTP ${res.status}`)
      }

      const data = (await res.json()) as WifiConfig

      const modules = (data.modules || []).map((m) => {
        const normalized = normalizeRadioModule(m)
        return {
          ...normalized,
          name:
            normalized.ip && moduleNameByIP.get(normalized.ip)
              ? moduleNameByIP.get(normalized.ip)!
              : normalized.name
        }
      })

      setConfig({
        ssid_2g: data.ssid_2g || '',
        pass_2g: data.pass_2g || '',
        ssid_5g: data.ssid_5g || '',
        pass_5g: data.pass_5g || '',
        modules
      })
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(`Failed to load WiFi configuration: ${msg}`)
    } finally {
      if (!opts?.silent) {
        setLoading(false)
      }
    }
  }

  useEffect(() => {
    void fetchWifi()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const updateModuleRadio = (index: number, patch: RadioPatch) => {
    setConfig((prev) => {
      const modules = [...(prev.modules || [])]

      modules[index] = {
        ...modules[index],
        ...patch
      }

      return {
        ...prev,
        modules
      }
    })
  }

  const radioKey = (mod: WifiModuleRadio, band: RadioBand) =>
    `${mod.type || 'module'}-${mod.ip || 'local'}-${band}`

  const isRadioRunning = (mod: WifiModuleRadio, band: RadioBand) =>
    band === '2g' ? Boolean(mod.radio_running_2g) : Boolean(mod.radio_running_5g)

  const updateModuleRadioState = (
    mod: WifiModuleRadio,
    band: RadioBand,
    enabled: boolean,
    runtime?: WifiRadioToggleResponse['runtime']
  ) => {
    setConfig((prev) => ({
      ...prev,
      modules: (prev.modules || []).map((m) => {
        const sameModule =
          (m.type === 'main' && mod.type === 'main') ||
          (m.ip && mod.ip && m.ip === mod.ip)

        if (!sameModule) {
          return m
        }

        return {
          ...m,
          ...(band === '2g'
            ? {
                radio_enabled_2g: enabled,
                radio_running_2g: runtime ? runtime.radio_2g_running : enabled
              }
            : {
                radio_enabled_5g: enabled,
                radio_running_5g: runtime ? runtime.radio_5g_running : enabled
              })
        }
      })
    }))
  }

  const toggleRadioState = async (mod: WifiModuleRadio, band: RadioBand) => {
    const key = radioKey(mod, band)
    // Button action follows the current runtime state:
    // running -> Disable, stopped -> Enable. The chosen action is then saved
    // into the central state file so the enforcer keeps applying it.
    const nextEnabled = !isRadioRunning(mod, band)

    setTogglingRadioKey(key)
    setError(null)
    setSuccess(null)
    setSyncResults([])

    try {
      const res = await apiFetch('/api/mtk/wifi', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          action: 'radio_state',
          name: mod.name,
          type: mod.type,
          ip: mod.ip,
          band,
          enabled: nextEnabled
        })
      })

      const text = await res.text()

      if (!res.ok) {
        throw new Error(text || `HTTP ${res.status}`)
      }

      let payloadResp: WifiRadioToggleResponse = { status: 'ok' }

      try {
        payloadResp = JSON.parse(text) as WifiRadioToggleResponse
      } catch {
        payloadResp = { status: 'ok' }
      }

      const result = payloadResp.result
      if (result) {
        setSyncResults([result])
      }

      updateModuleRadioState(mod, band, nextEnabled, payloadResp.runtime)

      if (result && !result.ok) {
        setSuccess(`${mod.name}: ${band === '2g' ? '2.4GHz' : '5GHz'} state saved, but apply returned warning.`)
      } else {
        setSuccess(
          `${mod.name}: ${band === '2g' ? '2.4GHz' : '5GHz'} ${
            nextEnabled ? 'enabled' : 'disabled'
          }. Central state will be enforced every 5 seconds.`
        )
      }

      await fetchWifi({ silent: true, preserveMessages: true })
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(`Radio state update failed: ${msg}`)
    } finally {
      setTogglingRadioKey(null)
    }
  }

  const renderRadioStateButton = (mod: WifiModuleRadio, band: RadioBand) => {
    const key = radioKey(mod, band)
    const running = isRadioRunning(mod, band)
    const busy = togglingRadioKey === key
    const label = band === '2g' ? '2.4GHz' : '5GHz'
    const actionLabel = running ? 'Disable' : 'Enable'

    return (
      <div className="w-full">
        <Button
          type="button"
          disabled={saving || busy || !mod.online}
          onClick={() => {
            void toggleRadioState(mod, band)
          }}
          className={
            running
              ? 'h-8 w-full justify-center bg-red-600 px-2 text-xs text-white hover:bg-red-700'
              : 'h-8 w-full justify-center bg-green-600 px-2 text-xs text-white hover:bg-green-700'
          }
          title={running ? `Disable ${label}` : `Enable ${label}`}
        >
          <Power className="mr-1 h-3.5 w-3.5" />
          {busy ? 'Updating...' : actionLabel}
        </Button>
      </div>
    )
  }

  const handleSave = async () => {
    const validationError = validateWifiConfig(config)
    if (validationError) {
      setError(validationError)
      return
    }

    const ok = await confirmDialog({
      title: 'Apply WiFi settings?',
      description:
        'This will apply the 2.4GHz/5GHz SSID/password and per-radio settings. WiFi interfaces will restart. Continue?',
      confirmText: 'Apply'
    })

    if (!ok) return

    setSaving(true)
    setError(null)
    setSuccess(null)
    setSyncResults([])

    try {
      const payload: WifiConfig = {
        ssid_2g: config.ssid_2g.trim(),
        pass_2g: config.pass_2g,
        ssid_5g: config.ssid_5g.trim(),
        pass_5g: config.pass_5g,
        modules: (config.modules || []).map((m) => ({
          name: m.name,
          type: m.type,
          ip: m.ip,
          online: m.online,

          channel_2g: String(m.channel_2g),
          channel_width_2g: m.channel_width_2g,
          tx_power_2g: Number(m.tx_power_2g),

          channel_5g: String(m.channel_5g),
          channel_width_5g: m.channel_width_5g,
          tx_power_5g: Number(m.tx_power_5g),

          radio_enabled_2g: m.radio_enabled_2g,
          radio_enabled_5g: m.radio_enabled_5g,
          radio_running_2g: m.radio_running_2g,
          radio_running_5g: m.radio_running_5g
        }))
      }

      const res = await apiFetch('/api/mtk/wifi', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify(payload)
      })

      const text = await res.text()

      if (!res.ok) {
        throw new Error(text || `HTTP ${res.status}`)
      }

      let payloadResp: WifiSyncResponse = { status: 'ok' }

      try {
        payloadResp = JSON.parse(text) as WifiSyncResponse
      } catch {
        payloadResp = { status: 'ok' }
      }

      const results = payloadResp.results || []
      setSyncResults(results)

      const failed = results.filter((r) => !r.ok)

      if (failed.length > 0) {
        setSuccess(`WiFi settings applied with ${failed.length} warning(s).`)
      } else {
        setSuccess('WiFi settings applied successfully.')
      }

      await fetchWifi({ silent: true, preserveMessages: true })
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(`Save failed: ${msg}`)
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="w-full overflow-x-hidden px-2 py-4 sm:px-4 lg:px-5">
        <div className="mx-auto w-full max-w-none">
          <div className="mb-4">
            <div className="mb-3 h-9 w-72 animate-pulse rounded bg-gray-200" />
            <div className="h-5 w-96 animate-pulse rounded bg-gray-100" />
          </div>

          <Card>
            <CardHeader>
              <div className="h-7 w-48 animate-pulse rounded bg-gray-200" />
              <div className="h-4 w-72 animate-pulse rounded bg-gray-100" />
            </CardHeader>
            <CardContent className="px-3 pb-3 sm:px-4 sm:pb-4">
              <div className="space-y-4">
                <div className="h-10 w-full animate-pulse rounded bg-gray-100" />
                <div className="h-10 w-full animate-pulse rounded bg-gray-100" />
                <div className="h-10 w-40 animate-pulse rounded bg-gray-200" />
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    )
  }

  return (
    <div className="w-full overflow-x-hidden px-2 py-4 sm:px-4 lg:px-5">
      <div className="mx-auto w-full max-w-none">
        <div className="mb-4">
          <h2 className="text-2xl font-bold text-gray-900">WiFi Configuration</h2>
        </div>

        <div className="grid gap-3">
          <Card>
            <CardHeader className="px-3 py-3 sm:px-4">
              <div className="flex items-center gap-1.5">
                <Wifi className="h-5 w-5 text-blue-600" />
                <div>
                  <CardTitle className="text-lg">WiFi Settings</CardTitle>
                </div>
              </div>
            </CardHeader>

            <CardContent className="px-3 pb-3 sm:px-4 sm:pb-4">
              <div className="grid gap-3 md:grid-cols-2">
                <div className="rounded border border-gray-100 p-3">
                  <div className="mb-3 text-sm font-semibold text-gray-800">2.4GHz</div>

                  <div className="grid gap-3">
                    <div className="grid gap-2">
                      <Label>SSID</Label>
                      <Input
                        value={config.ssid_2g}
                        disabled={saving}
                        onChange={(e) =>
                          setConfig((prev) => ({
                            ...prev,
                            ssid_2g: e.target.value
                          }))
                        }
                      />
                    </div>

                    <div className="grid gap-2">
                      <Label>Password</Label>
                      <div className="relative">
                        <Input
                          type={show2g ? 'text' : 'password'}
                          className="pr-10"
                          value={config.pass_2g}
                          disabled={saving}
                          onChange={(e) =>
                            setConfig((prev) => ({
                              ...prev,
                              pass_2g: e.target.value
                            }))
                          }
                        />
                        <button
                          type="button"
                          onClick={() => setShow2g((v) => !v)}
                          className="absolute inset-y-0 right-0 flex items-center px-3 text-gray-500 hover:text-gray-700"
                          aria-label={show2g ? 'Hide password' : 'Show password'}
                          tabIndex={-1}
                        >
                          {show2g ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                        </button>
                      </div>
                    </div>
                  </div>
                </div>

                <div className="rounded border border-gray-100 p-3">
                  <div className="mb-3 text-sm font-semibold text-gray-800">5GHz</div>

                  <div className="grid gap-3">
                    <div className="grid gap-2">
                      <Label>SSID</Label>
                      <Input
                        value={config.ssid_5g}
                        disabled={saving}
                        onChange={(e) =>
                          setConfig((prev) => ({
                            ...prev,
                            ssid_5g: e.target.value
                          }))
                        }
                      />
                    </div>

                    <div className="grid gap-2">
                      <Label>Password</Label>
                      <div className="relative">
                        <Input
                          type={show5g ? 'text' : 'password'}
                          className="pr-10"
                          value={config.pass_5g}
                          disabled={saving}
                          onChange={(e) =>
                            setConfig((prev) => ({
                              ...prev,
                              pass_5g: e.target.value
                            }))
                          }
                        />
                        <button
                          type="button"
                          onClick={() => setShow5g((v) => !v)}
                          className="absolute inset-y-0 right-0 flex items-center px-3 text-gray-500 hover:text-gray-700"
                          aria-label={show5g ? 'Hide password' : 'Show password'}
                          tabIndex={-1}
                        >
                          {show5g ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                        </button>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="px-3 py-3 sm:px-4">
              <div className="flex items-center gap-1.5">
                <RadioTower className="h-5 w-5 text-purple-600" />
                <div>
                  <CardTitle className="text-lg">Radio Settings</CardTitle>
                </div>
              </div>
            </CardHeader>

            <CardContent className="px-3 pb-3 sm:px-4 sm:pb-4">
              {(config.modules || []).length === 0 ? (
                <div className="rounded border border-dashed p-4 text-center text-sm text-gray-500">
                  No devices found.
                </div>
              ) : (
                <div className="w-full overflow-x-auto">
                  <table className="w-full min-w-[1180px] table-fixed text-xs">
                    <colgroup>
                      <col style={{ width: '19%' }} />
                      <col style={{ width: '15.5%' }} />
                      <col style={{ width: '7%' }} />
                      <col style={{ width: '7%' }} />
                      <col style={{ width: '11%' }} />
                      <col style={{ width: '15.5%' }} />
                      <col style={{ width: '7%' }} />
                      <col style={{ width: '7%' }} />
                      <col style={{ width: '11%' }} />
                    </colgroup>
                    <thead className="border-b bg-gray-50 text-xs text-gray-500">
                      <tr>
                        <th rowSpan={2} className="px-1.5 py-2 text-left align-bottom">
                          Device
                        </th>
                        <th colSpan={4} className="border-l px-1.5 py-2 text-center">
                          2.4GHz
                        </th>
                        <th colSpan={4} className="border-l px-1.5 py-2 text-center">
                          5GHz
                        </th>
                      </tr>
                      <tr>
                        <th className="border-l px-1.5 py-2 text-left">Channel</th>
                        <th className="px-1.5 py-2 text-left">Width</th>
                        <th className="px-1.5 py-2 text-left">TX</th>
                        <th className="px-1.5 py-2 text-left">Status</th>
                        <th className="border-l px-1.5 py-2 text-left">Channel</th>
                        <th className="px-1.5 py-2 text-left">Width</th>
                        <th className="px-1.5 py-2 text-left">TX</th>
                        <th className="px-1.5 py-2 text-left">Status</th>
                      </tr>
                    </thead>

                    <tbody>
                      {(config.modules || []).map((mod, idx) => (
                        <tr key={`${mod.type}-${mod.ip || idx}`} className="border-b">
                          <td className="px-1.5 py-2">
                            <div className="flex items-center gap-1.5">
                              <Settings2 className="h-4 w-4 text-gray-500" />
                              <div>
                                <div className="font-medium text-gray-900">
                                  {mod.name || (mod.type === 'main' ? 'Router' : 'Antenna')}
                                </div>
                              </div>
                            </div>
                          </td>

                          <td className="border-l px-1.5 py-2">
                            <select
                              value={String(mod.channel_2g)}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_2g: e.target.value })}
                              className="w-full rounded border border-gray-300 bg-white px-2 py-1.5 text-xs focus:border-blue-500 focus:outline-none"
                            >
                              {channelOptions2G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </select>
                          </td>

                          <td className="px-1.5 py-2">
                            <select
                              value={mod.channel_width_2g}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_width_2g: e.target.value })}
                              className="w-full rounded border border-gray-300 bg-white px-2 py-1.5 text-xs focus:border-blue-500 focus:outline-none"
                            >
                              {channelWidthOptions2G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </select>
                          </td>

                          <td className="px-1.5 py-2">
                            <div className="flex items-center gap-1.5">
                              <Input
                                type="number"
                                min={1}
                                max={100}
                                value={mod.tx_power_2g}
                                disabled={saving}
                                onChange={(e) =>
                                  updateModuleRadio(idx, {
                                    tx_power_2g: Number(e.target.value)
                                  })
                                }
                                className="w-[76px] pr-2 text-right [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                              />
                              <span className="text-[11px] text-gray-500">%</span>
                            </div>
                          </td>

                          <td className="px-1.5 py-2 align-middle">{renderRadioStateButton(mod, '2g')}</td>

                          <td className="border-l px-1.5 py-2">
                            <select
                              value={String(mod.channel_5g)}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_5g: e.target.value })}
                              className="w-full rounded border border-gray-300 bg-white px-2 py-1.5 text-xs focus:border-blue-500 focus:outline-none"
                            >
                              {channelOptions5G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </select>
                          </td>

                          <td className="px-1.5 py-2">
                            <select
                              value={mod.channel_width_5g}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_width_5g: e.target.value })}
                              className="w-full rounded border border-gray-300 bg-white px-2 py-1.5 text-xs focus:border-blue-500 focus:outline-none"
                            >
                              {channelWidthOptions5G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </select>
                          </td>

                          <td className="px-1.5 py-2">
                            <div className="flex items-center gap-1.5">
                              <Input
                                type="number"
                                min={1}
                                max={100}
                                value={mod.tx_power_5g}
                                disabled={saving}
                                onChange={(e) =>
                                  updateModuleRadio(idx, {
                                    tx_power_5g: Number(e.target.value)
                                  })
                                }
                                className="w-[76px] pr-2 text-right [appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
                              />
                              <span className="text-[11px] text-gray-500">%</span>
                            </div>
                          </td>

                          <td className="px-1.5 py-2 align-middle">{renderRadioStateButton(mod, '5g')}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}

              {error && (
                <div className="mt-4 flex gap-2 rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}

              {success && (
                <div className="mt-4 flex gap-2 rounded border border-green-100 bg-green-50 p-3 text-sm text-green-700">
                  <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{success}</span>
                </div>
              )}

              {syncResults.length > 0 && (
                <div className="mt-4 rounded border border-gray-200 bg-gray-50 p-3">
                  <div className="mb-2 text-sm font-medium text-gray-700">Sync result</div>
                  <div className="space-y-1 text-xs">
                    {syncResults.map((r) => (
                      <div
                        key={`${r.target}-${r.ip}`}
                        className={r.ok ? 'text-green-700' : 'text-amber-700'}
                      >
                        {r.ok ? '✓' : '!'} {r.target} {r.ip ? `(${r.ip})` : ''}{' '}
                        {r.ok ? 'updated' : r.error || 'failed'}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              <div className="mt-4 flex justify-end border-t pt-3">
                <Button
                  onClick={() => {
                    void handleSave()
                  }}
                  disabled={saving}
                  className="bg-blue-600 text-white hover:bg-blue-700"
                >
                  {saving ? (
                    'Applying...'
                  ) : (
                    <>
                      <Save className="mr-2 h-4 w-4" />
                      Save WiFi Settings
                    </>
                  )}
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </div>
  )
}
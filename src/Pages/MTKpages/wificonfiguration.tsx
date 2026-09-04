import type { JSX } from 'react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { NativeSelect } from '@/components/ui/native-select'
import { Table } from '@/components/ui/table'
import { PageHeader, PageShell } from '@/components/page'
import { FullScreenTaskOverlay } from '@/components/task-overlay'
import { clearPageDataCache, getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'
import { Wifi, Save, AlertCircle, CheckCircle2, RadioTower, Settings2, Power, Eye, EyeOff, AlertTriangle } from 'lucide-react'

// ---------- Types ----------

interface WifiModuleRadio {
  module_id: string
  name: string
  type: 'main' | 'ap' | string
  ip: string
  port?: string
  identity_ready: boolean
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

  // Current interface state. It may briefly differ from desired state after wifi reload/reboot until the main module enforcer runs.
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
  apply_after_seconds?: number
  estimated_seconds?: number
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
    | 'radio_enabled_2g'
    | 'radio_enabled_5g'
  >
>

type WifiApplyPhase = 'idle' | 'submitting' | 'countdown'

const emptyConfig: WifiConfig = {
  ssid_2g: '',
  pass_2g: '',
  ssid_5g: '',
  pass_5g: '',
  modules: []
}

const WIFI_CONFIG_CACHE_KEY = 'mtk.wifi'
const DEFAULT_WIFI_APPLY_SECONDS = 45

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
  const type = mod.type || 'ap'
  const moduleID = mod.module_id || (type === 'main' ? 'main' : '')

  return {
    module_id: moduleID,
    name: mod.name || 'Unknown Device',
    type,
    ip: mod.ip || '',
    port: mod.port,
    identity_ready:
      typeof mod.identity_ready === 'boolean'
        ? mod.identity_ready
        : type === 'main' || Boolean(moduleID),
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

function buildWifiPayload(config: WifiConfig): WifiConfig {
  return {
    ssid_2g: config.ssid_2g.trim(),
    pass_2g: config.pass_2g,
    ssid_5g: config.ssid_5g.trim(),
    pass_5g: config.pass_5g,
    modules: (config.modules || []).map((m) => ({
      ...m,
      channel_2g: String(m.channel_2g),
      channel_width_2g: m.channel_width_2g,
      tx_power_2g: Number(m.tx_power_2g),
      channel_5g: String(m.channel_5g),
      channel_width_5g: m.channel_width_5g,
      tx_power_5g: Number(m.tx_power_5g),
      radio_enabled_2g: Boolean(m.radio_enabled_2g),
      radio_enabled_5g: Boolean(m.radio_enabled_5g)
    }))
  }
}

function wifiConfigSignature(config: WifiConfig): string {
  const normalized = buildWifiPayload(config)

  return JSON.stringify({
    ssid_2g: normalized.ssid_2g,
    pass_2g: normalized.pass_2g,
    ssid_5g: normalized.ssid_5g,
    pass_5g: normalized.pass_5g,
    modules: (normalized.modules || []).map((m) => ({
      module_id: m.module_id,
      type: m.type,
      channel_2g: m.channel_2g,
      channel_width_2g: m.channel_width_2g,
      tx_power_2g: m.tx_power_2g,
      channel_5g: m.channel_5g,
      channel_width_5g: m.channel_width_5g,
      tx_power_5g: m.tx_power_5g,
      radio_enabled_2g: m.radio_enabled_2g,
      radio_enabled_5g: m.radio_enabled_5g
    }))
  })
}

export default function WiFiConfiguration(): JSX.Element {
  const navigate = useNavigate()
  const { currentAllModule, updateCurrentAllModule } = useCurrentAllModuleStore()
  const [cachedAtMount] = useState<WifiConfig | undefined>(() =>
    getPageDataCache<WifiConfig>(WIFI_CONFIG_CACHE_KEY)
  )

  const [config, setConfig] = useState<WifiConfig>(() => cachedAtMount ?? emptyConfig)
  const [savedConfig, setSavedConfig] = useState<WifiConfig>(() => cachedAtMount ?? emptyConfig)
  const [loading, setLoading] = useState(() => cachedAtMount === undefined)
  const [saving, setSaving] = useState(false)
  const [applyPhase, setApplyPhase] = useState<WifiApplyPhase>('idle')
  const [applyDeadline, setApplyDeadline] = useState<number | null>(null)
  const [applyCountdown, setApplyCountdown] = useState(DEFAULT_WIFI_APPLY_SECONDS)
  const [applyTotal, setApplyTotal] = useState(DEFAULT_WIFI_APPLY_SECONDS)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [syncResults, setSyncResults] = useState<WifiSyncTargetResult[]>([])
  const [show2g, setShow2g] = useState(false)
  const [show5g, setShow5g] = useState(false)
  const [meshManaged, setMeshManaged] = useState(false)
  const hasRedirectedRef = useRef(false)

  const hasChanges = useMemo(
    () => wifiConfigSignature(config) !== wifiConfigSignature(savedConfig),
    [config, savedConfig]
  )

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

      const nextConfig: WifiConfig = {
        ssid_2g: data.ssid_2g || '',
        pass_2g: data.pass_2g || '',
        ssid_5g: data.ssid_5g || '',
        pass_5g: data.pass_5g || '',
        modules
      }
      setPageDataCache(WIFI_CONFIG_CACHE_KEY, nextConfig)
      setConfig(nextConfig)
      setSavedConfig(nextConfig)
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
    if (sessionStorage.getItem('wifiApplyPending') === 'true') return
    void fetchWifi({ silent: cachedAtMount !== undefined })
    void apiFetch('/api/mtk/easymesh')
      .then(async (response) => {
        if (!response.ok) return
        const mesh = (await response.json()) as { config?: { enabled?: boolean } }
        setMeshManaged(Boolean(mesh.config?.enabled))
      })
      .catch(() => undefined)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    const warnAboutUnsavedChanges = (event: BeforeUnloadEvent) => {
      if (!hasChanges || applyPhase !== 'idle') return
      event.preventDefault()
    }

    window.addEventListener('beforeunload', warnAboutUnsavedChanges)
    return () => window.removeEventListener('beforeunload', warnAboutUnsavedChanges)
  }, [applyPhase, hasChanges])

  useEffect(() => {
    if (sessionStorage.getItem('wifiApplyPending') !== 'true') return

    const storedDeadline = Number(sessionStorage.getItem('wifiApplyDeadline'))
    const storedDuration = Number(sessionStorage.getItem('wifiApplyDuration'))
    if (!Number.isFinite(storedDeadline) || storedDeadline <= Date.now()) {
      sessionStorage.removeItem('wifiApplyPending')
      sessionStorage.removeItem('wifiApplyDeadline')
      sessionStorage.removeItem('wifiApplyDuration')
      return
    }

    const duration =
      Number.isFinite(storedDuration) && storedDuration >= 10
        ? Math.ceil(storedDuration)
        : DEFAULT_WIFI_APPLY_SECONDS
    setLoading(false)
    setSaving(true)
    setApplyTotal(duration)
    setApplyDeadline(storedDeadline)
    setApplyCountdown(Math.max(0, Math.ceil((storedDeadline - Date.now()) / 1000)))
    setApplyPhase('countdown')
  }, [])

  useEffect(() => {
    if (applyPhase !== 'countdown' || applyDeadline === null) return

    const updateCountdown = () => {
      setApplyCountdown(Math.max(0, Math.ceil((applyDeadline - Date.now()) / 1000)))
    }

    updateCountdown()
    const timer = window.setInterval(updateCountdown, 250)
    return () => window.clearInterval(timer)
  }, [applyDeadline, applyPhase])

  useEffect(() => {
    if (applyPhase !== 'countdown' || applyCountdown > 0 || hasRedirectedRef.current) return

    hasRedirectedRef.current = true
    sessionStorage.removeItem('isLoggedIn')
    sessionStorage.removeItem('token')
    sessionStorage.removeItem('username')
    sessionStorage.removeItem('wifiApplyPending')
    sessionStorage.removeItem('wifiApplyDeadline')
    sessionStorage.removeItem('wifiApplyDuration')
    updateCurrentAllModule([])
    clearPageDataCache()
    navigate('/login', { replace: true })
  }, [applyCountdown, applyPhase, navigate, updateCurrentAllModule])

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

  const renderRadioStateButton = (mod: WifiModuleRadio, index: number, band: RadioBand) => {
    const enabled = band === '2g' ? mod.radio_enabled_2g : mod.radio_enabled_5g
    const label = band === '2g' ? '2.4GHz' : '5GHz'
    const actionLabel = enabled ? 'Disable' : 'Enable'

    return (
      <div className="w-full">
        <Button
          type="button"
          disabled={saving || !mod.online || !mod.identity_ready}
          onClick={() => {
            updateModuleRadio(
              index,
              band === '2g'
                ? { radio_enabled_2g: !enabled }
                : { radio_enabled_5g: !enabled }
            )
            setError(null)
            setSuccess(null)
            setSyncResults([])
          }}
          className="h-8 w-full justify-center px-2 text-xs"
          variant={enabled ? 'destructive' : 'success'}
          title={`${actionLabel} ${label} when settings are saved`}
        >
          <Power className="mr-1 h-3.5 w-3.5" />
          {actionLabel}
        </Button>
      </div>
    )
  }

  const handleSave = async () => {
    if (meshManaged) {
      setError('WiFi settings are controlled by the EasyMesh Controller while Mesh is enabled.')
      return
    }
    if (!hasChanges) return

    const validationError = validateWifiConfig(config)
    if (validationError) {
      setError(validationError)
      return
    }

    const ok = await confirmDialog({
      title: 'Apply WiFi settings?',
      description:
        'This will apply the 2.4GHz/5GHz SSID/password and per-radio settings. WiFi connections will briefly reset. Continue?',
      confirmText: 'Apply'
    })

    if (!ok) return

    setSaving(true)
    setApplyPhase('submitting')
    setError(null)
    setSuccess(null)
    setSyncResults([])
    sessionStorage.setItem('wifiApplyPending', 'true')

    try {
      const payload = buildWifiPayload(config)

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
      const failed = results.filter((r) => !r.ok)
      if (failed.length > 0) {
        throw new Error(`${failed.length} device configuration(s) could not be verified`)
      }

      const estimatedSeconds =
        typeof payloadResp.estimated_seconds === 'number' &&
        Number.isFinite(payloadResp.estimated_seconds) &&
        payloadResp.estimated_seconds >= 10 &&
        payloadResp.estimated_seconds <= 60
          ? Math.ceil(payloadResp.estimated_seconds)
          : DEFAULT_WIFI_APPLY_SECONDS
      const nextDeadline = Date.now() + estimatedSeconds * 1000
      const expectedModuleCount = Math.max(1, currentAllModule.length)

      setSyncResults(results)
      setSavedConfig(payload)
      setPageDataCache(WIFI_CONFIG_CACHE_KEY, payload)
      setApplyTotal(estimatedSeconds)
      setApplyCountdown(estimatedSeconds)
      setApplyDeadline(nextDeadline)
      sessionStorage.setItem('wifiApplyDeadline', String(nextDeadline))
      sessionStorage.setItem('wifiApplyDuration', String(estimatedSeconds))
      sessionStorage.setItem('wifiExpectedModuleCount', String(expectedModuleCount))
      sessionStorage.setItem(
        'wifiModuleRecoveryDeadline',
        String(nextDeadline + 2 * 60 * 1000)
      )
      setApplyPhase('countdown')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      sessionStorage.removeItem('wifiApplyPending')
      sessionStorage.removeItem('wifiApplyDeadline')
      sessionStorage.removeItem('wifiApplyDuration')
      setApplyPhase('idle')
      setApplyDeadline(null)
      setSaving(false)
      setError(`Save failed: ${msg}`)
    }
  }

  const applyProgress =
    applyPhase === 'countdown'
      ? Math.min(100, Math.max(0, ((applyTotal - applyCountdown) / applyTotal) * 100))
      : 0
  const progressRadius = 40
  const progressCircumference = 2 * Math.PI * progressRadius

  if (loading) {
    return (
      <div className="w-full overflow-x-hidden px-2 py-4 sm:px-4 lg:px-5">
        <div className="mx-auto w-full max-w-none">
          <div className="mb-4">
            <div className="mb-3 h-9 w-72 animate-pulse rounded bg-muted" />
            <div className="h-5 w-96 animate-pulse rounded bg-muted" />
          </div>

          <Card>
            <CardHeader>
              <div className="h-7 w-48 animate-pulse rounded bg-muted" />
              <div className="h-4 w-72 animate-pulse rounded bg-muted" />
            </CardHeader>
            <CardContent className="px-3 pb-3 sm:px-4 sm:pb-4">
              <div className="space-y-4">
                <div className="h-10 w-full animate-pulse rounded bg-muted" />
                <div className="h-10 w-full animate-pulse rounded bg-muted" />
                <div className="h-10 w-40 animate-pulse rounded bg-muted" />
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    )
  }

  return (
    <PageShell size="full" className="overflow-x-hidden">
      {applyPhase !== 'idle' && (
        <FullScreenTaskOverlay panelClassName="flex flex-col items-center">
          <div className="relative mb-6 flex items-center justify-center">
            <svg className="h-32 w-32 -rotate-90 transform">
              <circle
                cx="64"
                cy="64"
                r={progressRadius}
                stroke="currentColor"
                strokeWidth="8"
                fill="transparent"
                className="text-muted-foreground/40"
              />
              <circle
                cx="64"
                cy="64"
                r={progressRadius}
                stroke="currentColor"
                strokeWidth="8"
                fill="transparent"
                strokeDasharray={progressCircumference}
                strokeDashoffset={
                  progressCircumference - (applyProgress / 100) * progressCircumference
                }
                className="text-primary transition-all duration-1000 ease-linear"
              />
            </svg>

            <div className="absolute text-3xl font-bold text-foreground">
              {applyPhase === 'countdown' ? (
                applyCountdown
              ) : (
                <Wifi className="h-9 w-9 animate-pulse text-primary" />
              )}
            </div>
          </div>

          <h3 className="mb-2 font-display text-2xl font-bold text-foreground">
            Applying WiFi Settings
          </h3>

          <div className="mb-6 rounded-lg border border-warning/30 bg-warning/10 p-4 text-left">
            <div className="flex items-start gap-2 text-sm text-warning">
              <AlertTriangle className="h-5 w-5 shrink-0" />
              <p>
                WiFi will disconnect while the new settings are applied. Reconnect to the
                configured WiFi network when the countdown finishes.
              </p>
            </div>
          </div>

          <p className="text-muted-foreground">
            {applyPhase === 'submitting'
              ? 'Saving and verifying configuration…'
              : `Returning to login in ${applyCountdown}s…`}
          </p>
        </FullScreenTaskOverlay>
      )}

      <PageHeader title="WiFi Configuration" description="Manage the shared SSIDs and per-device radio settings for the router and antennas." />

      {meshManaged ? (
        <div className="flex items-start gap-3 rounded-lg border border-primary/30 bg-primary/10 p-4 text-sm">
          <RadioTower className="mt-0.5 size-4 shrink-0 text-primary" />
          <p className="text-muted-foreground">
            EasyMesh is active. SSIDs, channels and Radio state are owned by the Root Controller and cannot be saved from this page.
          </p>
        </div>
      ) : null}

        <div className="grid gap-3">
          <Card>
            <CardHeader className="px-3 py-3 sm:px-4">
              <div className="flex items-center gap-1.5">
                <Wifi className="h-5 w-5 text-primary" />
                <div>
                  <CardTitle className="text-lg">WiFi Settings</CardTitle>
                </div>
              </div>
            </CardHeader>

            <CardContent className="px-3 pb-3 sm:px-4 sm:pb-4">
              <div className="grid gap-3 md:grid-cols-2">
                <div className="rounded border border-border p-3">
                  <div className="mb-3 text-sm font-semibold text-foreground">2.4GHz</div>

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
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => setShow2g((v) => !v)}
                          className="absolute inset-y-0 right-0 text-muted-foreground hover:text-foreground"
                          aria-label={show2g ? 'Hide password' : 'Show password'}
                          tabIndex={-1}
                        >
                          {show2g ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                        </Button>
                      </div>
                    </div>
                  </div>
                </div>

                <div className="rounded border border-border p-3">
                  <div className="mb-3 text-sm font-semibold text-foreground">5GHz</div>

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
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon"
                          onClick={() => setShow5g((v) => !v)}
                          className="absolute inset-y-0 right-0 flex items-center px-3 text-muted-foreground hover:text-foreground"
                          aria-label={show5g ? 'Hide password' : 'Show password'}
                          tabIndex={-1}
                        >
                          {show5g ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                        </Button>
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
                <RadioTower className="h-5 w-5 text-signal" />
                <div>
                  <CardTitle className="text-lg">Radio Settings</CardTitle>
                </div>
              </div>
            </CardHeader>

            <CardContent className="px-3 pb-3 sm:px-4 sm:pb-4">
              {(config.modules || []).length === 0 ? (
                <div className="rounded border border-dashed p-4 text-center text-sm text-muted-foreground">
                  No devices found.
                </div>
              ) : (
                <div className="w-full overflow-x-auto">
                  <Table className="w-full min-w-[1180px] table-fixed text-xs">
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
                    <thead className="border-b border-border text-xs text-muted-foreground">
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
                        <tr key={mod.module_id || `${mod.type}-${mod.ip || idx}`} className="border-b">
                          <td className="px-1.5 py-2">
                            <div className="flex items-center gap-1.5">
                              <Settings2 className="h-4 w-4 text-muted-foreground" />
                              <div>
                                <div className="font-medium text-foreground">
                                  {mod.name || (mod.type === 'main' ? 'Router' : 'Antenna')}
                                </div>
                                {!mod.identity_ready && mod.type !== 'main' && (
                                  <div className="text-[10px] text-warning">
                                    Identifying physical port…
                                  </div>
                                )}
                              </div>
                            </div>
                          </td>

                          <td className="border-l px-1.5 py-2">
                            <NativeSelect
                              value={String(mod.channel_2g)}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_2g: e.target.value })}
                              className="w-full rounded border border-border bg-card px-2 py-1.5 text-xs focus:border-ring focus:outline-none"
                            >
                              {channelOptions2G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </NativeSelect>
                          </td>

                          <td className="px-1.5 py-2">
                            <NativeSelect
                              value={mod.channel_width_2g}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_width_2g: e.target.value })}
                              className="w-full rounded border border-border bg-card px-2 py-1.5 text-xs focus:border-ring focus:outline-none"
                            >
                              {channelWidthOptions2G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </NativeSelect>
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
                              <span className="text-[11px] text-muted-foreground">%</span>
                            </div>
                          </td>

                          <td className="px-1.5 py-2 align-middle">{renderRadioStateButton(mod, idx, '2g')}</td>

                          <td className="border-l px-1.5 py-2">
                            <NativeSelect
                              value={String(mod.channel_5g)}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_5g: e.target.value })}
                              className="w-full rounded border border-border bg-card px-2 py-1.5 text-xs focus:border-ring focus:outline-none"
                            >
                              {channelOptions5G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </NativeSelect>
                          </td>

                          <td className="px-1.5 py-2">
                            <NativeSelect
                              value={mod.channel_width_5g}
                              disabled={saving}
                              onChange={(e) => updateModuleRadio(idx, { channel_width_5g: e.target.value })}
                              className="w-full rounded border border-border bg-card px-2 py-1.5 text-xs focus:border-ring focus:outline-none"
                            >
                              {channelWidthOptions5G.map((opt) => (
                                <option key={opt.value} value={opt.value}>
                                  {opt.label}
                                </option>
                              ))}
                            </NativeSelect>
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
                              <span className="text-[11px] text-muted-foreground">%</span>
                            </div>
                          </td>

                          <td className="px-1.5 py-2 align-middle">{renderRadioStateButton(mod, idx, '5g')}</td>
                        </tr>
                      ))}
                    </tbody>
                  </Table>
                </div>
              )}

              {error && (
                <div className="mt-4 flex gap-2 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}

              {success && (
                <div className="mt-4 flex gap-2 rounded border border-success/20 bg-success/10 p-3 text-sm text-success">
                  <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{success}</span>
                </div>
              )}

              {syncResults.length > 0 && (
                <div className="mt-4 rounded border border-border bg-muted p-3">
                  <div className="mb-2 text-sm font-medium text-foreground">Sync result</div>
                  <div className="space-y-1 text-xs">
                    {syncResults.map((r) => (
                      <div
                        key={`${r.target}-${r.ip}`}
                        className={r.ok ? 'text-success' : 'text-warning'}
                      >
                        {r.ok ? '✓' : '!'} {r.target} {r.ip ? `(${r.ip})` : ''}{' '}
                        {r.ok ? 'updated' : r.error || 'failed'}
                      </div>
                    ))}
                  </div>
                </div>
              )}

              <div className="mt-4 flex items-center justify-between gap-3 border-t pt-3">
                <span className="text-xs text-muted-foreground">
                  {hasChanges ? 'Unsaved changes' : 'All changes saved'}
                </span>
                <Button
                  onClick={() => {
                    void handleSave()
                  }}
                  disabled={saving || !hasChanges || meshManaged}
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
    </PageShell>
  )
}

import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import {
  Activity,
  Antenna,
  CheckCircle2,
  CircleAlert,
  LoaderCircle,
  Network,
  RadioTower,
  Router,
  Save,
  ShieldCheck,
  Unplug,
  Wifi
} from 'lucide-react'
import { toast } from 'sonner'

import { MeshTopology, type MeshTopologyGraph } from '@/components/mesh-topology'
import { PageHeader, PageShell, SectionCard, StatCard } from '@/components/page'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { confirmDialog } from '@/components/ui/confirm'
import { apiFetch } from '@/utils/http'

type MeshRole = 'standalone' | 'controller' | 'agent'

type MeshConfig = {
  version: number
  role: MeshRole
  backhaul_band: '2g' | '5g'
  network_prepared: boolean
  enabled: boolean
  agent_handoff: boolean
  onboarding_at?: number
  updated_at: number
}

type MeshModule = {
  module_id: string
  name: string
  port: string
  ip: string
  mac: string
  online: boolean
  capable: boolean
  capability_error?: string
}

type MeshRemoteNode = {
  name: string
  al_id: string
  main_mac?: string
  management_ip?: string
  backhaul_medium: string
  backhaul_rssi?: string
  distance?: string
  upstream_al_id?: string
  connected: boolean
  management_online: boolean
  fronthaul_ready: boolean
  fronthaul_ssids: string[]
}

type MeshJob = {
  id?: number
  running: boolean
  action?: string
  stage?: string
  error?: string
  started_at?: number
  ended_at?: number
}

type MeshStatus = {
  config: MeshConfig
  modules: MeshModule[]
  remote_nodes: MeshRemoteNode[]
  node_id: string
  engine_available: boolean
  engine_running: boolean
  runtime_role: string
  backhaul_status: string
  backhaul_connected: boolean
  backhaul_active_band?: string
  client_handoff_ready: boolean
  management_ip?: string
  radio_prepared: boolean
  topology: string
  topology_graph: MeshTopologyGraph
  client_vlan: number
  management_vlan: number
  management_subnet: string
  requires_separation: boolean
  job: MeshJob
}

type MeshForm = {
  role: MeshRole
  backhaulBand: '2g' | '5g'
}

type PendingMeshAction = {
  action: string
  jobID: number
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function roleLabel(role: MeshRole): string {
  if (role === 'controller') return 'Root Controller'
  if (role === 'agent') return 'Mesh Agent'
  return 'Standalone'
}

function backhaulMediumMatchesBand(medium: string, band: '2g' | '5g'): boolean {
  const normalized = medium.toLowerCase().replace(/\s+/g, '')
  return band === '2g'
    ? normalized.includes('2.4') || normalized === '2g' || normalized === '24g'
    : normalized.includes('5g')
}

function agentCount(status: MeshStatus): number {
  return (status.remote_nodes || []).length
}

function runtimeSummary(raw: string, fallback: string): string {
  const value = raw.trim()
  if (!value) return fallback
  const line = value
    .split('\n')
    .map((item) => item.trim())
    .find(Boolean)
  return line || fallback
}

function normalizeMeshStatus(raw: MeshStatus): MeshStatus {
  return {
    ...raw,
    modules: Array.isArray(raw.modules) ? raw.modules : [],
    remote_nodes: Array.isArray(raw.remote_nodes)
      ? raw.remote_nodes.map((node) => ({
          ...node,
          fronthaul_ssids: Array.isArray(node.fronthaul_ssids) ? node.fronthaul_ssids : []
        }))
      : []
  }
}

async function fetchMeshStatus(): Promise<MeshStatus> {
  const response = await apiFetch('/api/mtk/easymesh')
  if (!response.ok) {
    throw new Error((await response.text().catch(() => '')) || `HTTP ${response.status}`)
  }
  return normalizeMeshStatus((await response.json()) as MeshStatus)
}

function isAmbiguousFetchFailure(error: unknown): boolean {
  const message = errorMessage(error).toLowerCase()
  return (
    error instanceof TypeError ||
    message.includes('failed to fetch') ||
    message.includes('networkerror') ||
    message.includes('network request failed')
  )
}

function meshActionObserved(next: MeshStatus, action: string, form: MeshForm): boolean {
  const jobStartedAt = next.job.started_at || 0
  const recentMatchingJob =
    next.job.action === action && jobStartedAt > 0 && Math.abs(Date.now() / 1000 - jobStartedAt) < 180
  if (recentMatchingJob) return true

  switch (action) {
    case 'save':
      return (
        next.config.role === form.role && next.config.backhaul_band === form.backhaulBand
      )
    case 'prepare_network':
      return next.config.network_prepared
    case 'prepare_radios':
      return next.radio_prepared
    case 'activate':
      return next.config.enabled
    case 'onboard':
      return Boolean(
        next.config.onboarding_at && Date.now() / 1000 - next.config.onboarding_at < 360
      )
    case 'leave':
    case 'disable':
      return next.config.role === 'standalone' && !next.config.enabled && !next.config.network_prepared
    default:
      return false
  }
}

export default function EasyMesh() {
  const navigate = useNavigate()
  const [status, setStatus] = useState<MeshStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [pendingAction, setPendingAction] = useState<PendingMeshAction | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [transition, setTransition] = useState<'agent_handoff' | 'radio_prepare' | 'leave' | null>(null)
  const [transitionCountdown, setTransitionCountdown] = useState(25)
  const transitionRedirected = useRef(false)
  const agentHandoffTransitionStarted = useRef(false)
  const [form, setForm] = useState<MeshForm>({
    role: 'standalone',
    backhaulBand: '5g'
  })

  const loadStatus = useCallback(async (background = false) => {
    if (!background) setLoading(true)
    try {
      const next = await fetchMeshStatus()
      setStatus(next)
      setForm((current) => {
        if (background && current.role !== 'standalone') return current
        return {
          role: next.config.role,
          backhaulBand: next.config.backhaul_band || '5g'
        }
      })
      setError(null)
    } catch (nextError) {
      setError(errorMessage(nextError))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadStatus()
  }, [loadStatus])

  // Poll continuously while this page is open. EasyMesh transitions (Enable
  // Turnkey, Prepare) briefly drop WiFi/DHCP, so status requests fail for a few
  // seconds around the exact moment a step completes. A conditional interval
  // could then miss the recovery poll and leave the next step's button (e.g.
  // "Join Root") stale until a manual refresh; a steady interval always
  // reconverges once connectivity returns.
  useEffect(() => {
    const timer = window.setInterval(() => void loadStatus(true), 3000)
    return () => window.clearInterval(timer)
  }, [loadStatus])

  useEffect(() => {
    if (!pendingAction || !status?.job || status.job.id !== pendingAction.jobID) return
    if (status.job.running) return

    setPendingAction(null)
    if (status.job.error) {
      toast.error('EasyMesh operation failed', { description: status.job.error })
    } else {
      toast.success('EasyMesh operation completed.')
    }
  }, [pendingAction, status?.job])

  useEffect(() => {
    if (
      sessionStorage.getItem('meshAgentJoinPending') === 'true' &&
      status?.config.role === 'agent' &&
      status.config.agent_handoff &&
      status.backhaul_connected &&
      !agentHandoffTransitionStarted.current
    ) {
      agentHandoffTransitionStarted.current = true
      sessionStorage.setItem('meshNetworkTransitionPending', 'true')
      setTransition('agent_handoff')
      setTransitionCountdown(25)
    } else if (
      sessionStorage.getItem('meshAgentJoinPending') === 'true' &&
      !status?.job.running &&
      status?.job.action === 'onboard' &&
      status.job.error
    ) {
      sessionStorage.removeItem('meshAgentJoinPending')
    }
  }, [status])

  useEffect(() => {
    if (!transition) return
    const timer = window.setInterval(() => {
      setTransitionCountdown((current) => Math.max(0, current - 1))
    }, 1000)
    return () => window.clearInterval(timer)
  }, [transition])

  useEffect(() => {
    if (!transition || transitionCountdown > 0 || transitionRedirected.current) return
    transitionRedirected.current = true
    sessionStorage.removeItem('isLoggedIn')
    sessionStorage.removeItem('token')
    sessionStorage.removeItem('username')
    sessionStorage.removeItem('meshAgentJoinPending')
    sessionStorage.removeItem('meshNetworkTransitionPending')
    navigate('/login', { replace: true })
  }, [navigate, transition, transitionCountdown])

  // The Backhaul runs on this main module's own Radio, so a role and a band are
  // all that is required. Antenna submodules are plain client APs.
  const formValid = true
  // The backend job is authoritative. A completed job must release the UI
  // even if React still holds the local request marker for one render (or a
  // background status request completed out of order).
  const pendingAwaitingStatus = Boolean(
    pendingAction && (!status?.job.id || status.job.id < pendingAction.jobID)
  )
  const busy = submitting || pendingAwaitingStatus || Boolean(status?.job.running)
  const controllerPairingComplete = Boolean(
    status?.config.role === 'controller' &&
      (status.remote_nodes || []).some(
        (node) =>
          node.connected &&
          node.management_online &&
          backhaulMediumMatchesBand(node.backhaul_medium, status.config.backhaul_band)
      )
  )

  const postAction = async (action: string, includeForm = false) => {
    setSubmitting(true)
    try {
      const response = await apiFetch('/api/mtk/easymesh', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action,
          ...(includeForm
            ? {
                role: form.role,
                backhaul_band: form.backhaulBand
              }
            : {})
        })
      })
      if (!response.ok) {
        const raw = await response.text().catch(() => '')
        try {
          const parsed = JSON.parse(raw) as { error?: string }
          throw new Error(parsed.error || raw || `HTTP ${response.status}`)
        } catch (parseError) {
          if (parseError instanceof SyntaxError) throw new Error(raw || `HTTP ${response.status}`)
          throw parseError
        }
      }
      const accepted = (await response.json()) as { job_id?: number }
      if (action !== 'save') {
        if (typeof accepted.job_id !== 'number') {
          throw new Error('EasyMesh operation was accepted without a task identifier.')
        }
        setPendingAction({ action, jobID: accepted.job_id })
      }
      await loadStatus(true)
      return true
    } catch (nextError) {
      if (isAmbiguousFetchFailure(nextError)) {
        toast.info('Connection interrupted', {
          description: 'The request may already be running. Confirming the device state now.'
        })
        for (let attempt = 0; attempt < 8; attempt += 1) {
          if (attempt > 0) await new Promise((resolve) => window.setTimeout(resolve, 2000))
          try {
            const recovered = await fetchMeshStatus()
            setStatus(recovered)
            if (!meshActionObserved(recovered, action, form)) continue
            if (recovered.job.action === action && recovered.job.error) {
              toast.error('EasyMesh operation failed', { description: recovered.job.error })
              return false
            }
            if (recovered.job.running && typeof recovered.job.id === 'number') {
              setPendingAction({ action, jobID: recovered.job.id })
            }
            toast.info('EasyMesh operation confirmed', {
              description: recovered.job.running
                ? 'The device accepted the request and is still applying it.'
                : 'The device completed the request despite the interrupted response.'
            })
            return true
          } catch {
            // The Radio or management path can be unavailable for a few
            // seconds. Keep reconciling before presenting a final result.
          }
        }
        toast.error('Unable to confirm EasyMesh operation', {
          description: 'Reconnect to this device, then reopen EasyMesh to view the authoritative status.'
        })
        return false
      }
      toast.error('EasyMesh operation failed', { description: errorMessage(nextError) })
      return false
    } finally {
      setSubmitting(false)
    }
  }

  const saveConfiguration = async () => {
    if (!formValid) {
      toast.error('Select an online EasyMesh-capable Antenna.')
      return
    }
    if (await postAction('save', true)) {
      toast.success('EasyMesh node configuration saved.')
    }
  }

  const enableMesh = async () => {
    const isAgent = status?.config.role === 'agent'
    const confirmed = await confirmDialog({
      title: `Enable ${isAgent ? 'Mesh Agent' : 'Root Controller'}?`,
      description:
        'This migrates client traffic to VLAN 100 and Antenna management to VLAN 200, reserves the shared Backhaul Radio, then reboots the hardware once to apply it. The reboot also restores the client WiFi, and activation resumes automatically afterwards — just reconnect to this WiFi once when it returns (no power cycle needed).',
      confirmText: 'Enable Mesh'
    })
    if (!confirmed) return
    if (await postAction('enable_mesh')) {
      sessionStorage.setItem('meshNetworkTransitionPending', 'true')
      setTransition('radio_prepare')
      setTransitionCountdown(90)
      toast.info('Enabling Mesh. The device migrates networks and reboots once — reconnect when the WiFi returns; activation continues on its own.')
    }
  }

  const syncFronthaul = async () => {
    const confirmed = await confirmDialog({
      title: 'Synchronize Mesh client WiFi?',
      description:
        'This keeps the EasyMesh Backhaul active and applies this device\'s client WiFi to the selected directional Antenna. WiFi may pause briefly while the Radio reloads.',
      confirmText: 'Synchronize WiFi'
    })
    if (!confirmed) return
    if (await postAction('sync_fronthaul')) {
      toast.info('Shared Backhaul and client WiFi synchronization started.')
    }
  }

  const startOnboarding = async () => {
    const isAgent = status?.config.role === 'agent'
    const confirmed = await confirmDialog({
      title: isAgent ? 'Join the Root Controller?' : 'Open wireless onboarding window?',
      description: isAgent
        ? 'Open onboarding on the Root first. This Agent will wait up to five minutes for its selected directional Antenna to connect. DHCP and the current management address remain unchanged if pairing fails; after success, this session will move to the Root network.'
        : 'Then run onboarding on one joining Agent within five minutes. Only one new Mesh device should be in onboarding mode nearby.',
      confirmText: isAgent ? 'Join Root' : 'Start onboarding'
    })
    if (!confirmed) return
    if (await postAction('onboard')) {
      if (isAgent) sessionStorage.setItem('meshAgentJoinPending', 'true')
      toast.success(
        isAgent
          ? 'Agent onboarding started. Keep this page open while the backhaul is verified.'
          : 'Wireless EasyMesh onboarding window opened.'
      )
    }
  }

  const leaveMesh = async () => {
    const isController = status?.config.role === 'controller'
    const confirmed = await confirmDialog({
      title: 'Leave EasyMesh and restore standalone mode?',
      description: isController
        ? 'Each Agent is told to return to standalone first, then this device stops the Root Mesh role and restores its own backed-up standalone network and Antennas. An Agent that is already out of contact will not receive the instruction and has to be restored from its own portal. WiFi and this management session will disconnect.'
        : 'This stops the Mesh Agent and restores the backed-up standalone network on this complete device and its Antennas. WiFi and this management session will disconnect; reconnect to its original WiFi afterward.',
      confirmText: 'Leave Mesh',
      destructive: true
    })
    if (!confirmed) return
    if (await postAction('leave')) {
      sessionStorage.setItem('meshNetworkTransitionPending', 'true')
      setTransition('leave')
      setTransitionCountdown(25)
      toast.info('Leaving EasyMesh and restoring the standalone network.')
    }
  }

  const rollbackNetwork = async () => {
    const confirmed = await confirmDialog({
      title: 'Restore the pre-Mesh network?',
      description:
        'This restores the backed-up network and DHCP configuration on every reachable Antenna first, then on the main module. Use it to recover from VLAN preparation before EasyMesh is enabled.',
      confirmText: 'Restore network',
      destructive: true
    })
    if (!confirmed) return
    if (await postAction('rollback_network')) {
      toast.info('Pre-Mesh network restoration started.')
    }
  }

  if (loading && !status) {
    return (
      <PageShell>
        <PageHeader title="EasyMesh" description="Loading Mesh capabilities and local topology…" />
        <Card>
          <CardContent className="flex min-h-52 items-center justify-center">
            <LoaderCircle className="size-8 animate-spin text-primary" />
          </CardContent>
        </Card>
      </PageShell>
    )
  }

  return (
    <PageShell size="full">
      {transition ? (
        <div className="fixed inset-0 z-[100] flex items-center justify-center bg-background/95 px-6 backdrop-blur-md">
          <div className="w-full max-w-lg rounded-2xl border border-primary/25 bg-card p-8 text-center shadow-2xl">
            <LoaderCircle className="mx-auto size-12 animate-spin text-primary" />
            <h2 className="mt-6 text-2xl font-semibold tracking-tight text-foreground">
              {transition === 'agent_handoff'
                ? 'Mesh Backhaul Connected'
                : transition === 'radio_prepare'
                  ? 'Preparing Shared Radio'
                  : 'Restoring Standalone Network'}
            </h2>
            <p className="mt-3 text-sm leading-6 text-muted-foreground">
              {transition === 'agent_handoff'
                ? 'This Agent is transferring DHCP and client networking to the Root Controller. The current connection may briefly disconnect.'
                : transition === 'radio_prepare'
                  ? "The second BSSID is being reserved and the Agent hardware is rebooting once. Reconnect to this device's WiFi afterward, then continue with Enable Turnkey."
                  : 'EasyMesh is stopping and the saved pre-Mesh network is being restored. Reconnect to this device’s original WiFi afterward.'}
            </p>
            <div className="mt-7 font-mono text-5xl font-semibold tabular-nums text-primary">
              {transitionCountdown}
            </div>
            <p className="mt-2 text-xs uppercase tracking-[0.2em] text-muted-foreground">
              Returning to login
            </p>
          </div>
        </div>
      ) : null}
      <PageHeader
        title="EasyMesh"
        description="Build a long-range Mesh while keeping each Router and its directional Antennas grouped as one managed device."
      />

      {error ? (
        <div className="flex items-start gap-3 rounded-lg border border-destructive/35 bg-destructive/10 p-4 text-sm text-destructive">
          <CircleAlert className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      ) : null}

      {status?.job.running || pendingAwaitingStatus ? (
        <div className="flex items-center gap-3 rounded-lg border border-primary/30 bg-primary/10 p-4">
          <LoaderCircle className="size-5 animate-spin text-primary" />
          <div>
            <div className="font-medium text-foreground">
              {status?.job.running && status.job.action === (pendingAction?.action || status.job.action)
                ? status.job.stage || 'Working…'
                : 'Starting EasyMesh operation…'}
            </div>
            <div className="text-xs text-muted-foreground">Do not power off this device.</div>
          </div>
        </div>
      ) : status?.job.error ? (
        <div className="flex items-start gap-3 rounded-lg border border-destructive/35 bg-destructive/10 p-4 text-sm">
          <CircleAlert className="mt-0.5 size-4 shrink-0 text-destructive" />
          <div>
            <div className="font-medium text-destructive">Last operation failed</div>
            <div className="mt-1 text-muted-foreground">{status.job.error}</div>
          </div>
        </div>
      ) : null}

      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label="Node role"
          value={roleLabel(status?.config.role || 'standalone')}
          detail={`Node ${status?.node_id || 'Unknown'}`}
        />
        <StatCard
          label="Turnkey engine"
          value={status?.engine_running ? 'Running' : 'Stopped'}
          detail={status?.engine_available ? 'MediaTek engine available' : 'Engine files unavailable'}
        />
        <StatCard
          label="Backhaul"
          value={
            status?.config.role === 'agent' && status?.config.enabled
              ? status.backhaul_connected
                ? `${(status.backhaul_active_band || status.config.backhaul_band).toUpperCase()} connected`
                : `Waiting for ${status.config.backhaul_band.toUpperCase()}`
              : status?.config.role === 'controller' && status.config.enabled
                ? // A Root has no upstream of its own, so its own bh_conn_status
                  // is always 0 and reads as a fault. What matters here is how
                  // many Agents have joined it.
                  agentCount(status) === 1
                  ? '1 Agent linked'
                  : `${agentCount(status)} Agents linked`
                : status?.config.enabled
                  ? runtimeSummary(status.backhaul_status, 'Ready')
                  : 'Not active'
          }
          detail={
            status?.config.role === 'agent' &&
            status.config.enabled &&
            status.backhaul_active_band &&
            status.backhaul_active_band !== status.config.backhaul_band
              ? // The Mesh engine may move the Backhaul off the selected band on
                // its own. Saying so beats letting an operator believe a 5 GHz
                // selection is what the Mesh is riding.
                `Mesh engine moved it off the selected ${status.config.backhaul_band.toUpperCase()}`
              : 'Main module Radio · selected band'
          }
        />
        <StatCard
          label="Network planes"
          value={status?.config.network_prepared ? 'Separated' : 'Legacy'}
          detail={
            status?.config.network_prepared
              ? status.config.role === 'agent' && status.config.enabled
                ? status.client_handoff_ready
                  ? `Root managed · ${status.management_ip || 'DHCP address assigned'}`
                  : 'Local recovery network until pairing is verified'
                : `Client VLAN ${status.client_vlan} · Management VLAN ${status.management_vlan}`
              : 'Preparation required'
          }
        />
      </div>

      <div className="grid gap-4 xl:grid-cols-[minmax(0,1.2fr)_minmax(360px,0.8fr)]">
        <SectionCard
          title="Node configuration"
          description="The selected directional Antenna uses one Radio for both wireless backhaul and client coverage."
          icon={<RadioTower className="size-5" />}
        >
          <div className="grid gap-5">
            <div className="grid gap-2">
              <Label htmlFor="mesh-role">Role of this complete device</Label>
              <NativeSelect
                id="mesh-role"
                value={form.role}
                disabled={busy || Boolean(status?.config.enabled)}
                onChange={(event) =>
                  setForm((current) => ({ ...current, role: event.target.value as MeshRole }))
                }
              >
                <option value="standalone">Standalone router</option>
                <option value="controller">Root Controller</option>
                <option value="agent">Mesh Agent</option>
              </NativeSelect>
              <p className="text-xs text-muted-foreground">
                The Root owns WAN, NAT and client DHCP. Agent nodes bridge clients to the Root.
              </p>
            </div>

            <div className="grid gap-2">
              <Label htmlFor="mesh-band">Shared Backhaul + Fronthaul Radio</Label>
              <NativeSelect
                id="mesh-band"
                value={form.backhaulBand}
                disabled={busy || form.role === 'standalone' || Boolean(status?.config.enabled)}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    backhaulBand: event.target.value as '2g' | '5g'
                  }))
                }
              >
                <option value="5g">5GHz · Recommended</option>
                <option value="2g">2.4GHz · Longer range, lower capacity</option>
              </NativeSelect>
              <p className="text-xs text-muted-foreground">
                Backhaul is restricted to the selected Radio. Client Fronthaul remains available on both bands.
              </p>
            </div>

            <div className="flex justify-end">
              <Button onClick={() => void saveConfiguration()} disabled={busy || !formValid || Boolean(status?.config.enabled)}>
                <Save /> Save configuration
              </Button>
            </div>
          </div>
        </SectionCard>

        <SectionCard
          title="Phase 1 activation"
          description="Complete each guarded step in order."
          icon={<ShieldCheck className="size-5" />}
        >
          <div className="grid gap-3">
            <ActivationStep
              number="1"
              title="Enable Mesh"
              description={
                status?.config.enabled
                  ? `VLANs separated (clients on VLAN ${status?.client_vlan || 100}, Antennas on ${status?.management_subnet || '172.31.255.0/29'}), the Mesh Radio is prepared, and the EasyMesh engine is running.`
                  : 'One click: separate client/management VLANs, prepare the main module Radio for the Backhaul, and start the engine. The device reboots once to apply it and restore WiFi; activation resumes automatically, so reconnect to this WiFi only once (no power cycle).'
              }
              complete={Boolean(status?.config.enabled)}
              icon={<Activity className="size-4" />}
              action={
                <Button
                  size="sm"
                  disabled={
                    busy ||
                    status?.config.role === 'standalone' ||
                    Boolean(status?.config.enabled)
                  }
                  onClick={() => void enableMesh()}
                >
                  Enable Mesh
                </Button>
              }
            />
            <ActivationStep
              number="2"
              title="Pair the Root and Agent"
              description={
                status?.config.role === 'agent'
                  ? 'Join within five minutes; only a verified wireless backhaul triggers DHCP handoff.'
                  : 'Open the PBC window, then start onboarding on one Agent within five minutes.'
              }
              complete={Boolean(
                status?.config.enabled &&
                  (status.config.role === 'agent'
                    ? status.config.agent_handoff && status.backhaul_connected && status.client_handoff_ready
                    : controllerPairingComplete)
              )}
              icon={<Wifi className="size-4" />}
              action={
                <Button
                  size="sm"
                  disabled={
                    busy ||
                    !status?.config.enabled ||
                    (status.config.role === 'controller' && controllerPairingComplete)
                  }
                  onClick={() => void startOnboarding()}
                >
                  {status?.config.role === 'agent' ? 'Join Root' : 'Start onboarding'}
                </Button>
              }
            />
          </div>
        </SectionCard>
      </div>

      <SectionCard
        title="Local modules"
        description="The Backhaul is carried by the main module's own Radio. Antennas stay on directional client coverage and never join the Mesh."
        icon={<Network className="size-5" />}
      >
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <ModuleCard
            name="Main Module"
            detail={status?.config.role === 'controller' ? 'Controller + local Radio' : status?.config.role === 'agent' ? 'Agent + local Radio' : 'Standalone'}
            selected={false}
            capable={Boolean(status?.engine_available)}
            icon={<Router className="size-5" />}
          />
          {(status?.modules || []).map((module) => (
            <ModuleCard
              key={module.module_id}
              name={module.name}
              detail="Directional client coverage"
              selected={false}
              capable={module.capable}
              icon={<Antenna className="size-5" />}
            />
          ))}
        </div>
      </SectionCard>

      {status?.engine_running && (status.topology_graph?.nodes?.length ?? 0) > 0 ? (
        <SectionCard
          title="Mesh topology"
          description="Live view of the Mesh as the engine reports it: every device, what carries each Backhaul hop, and the clients each one serves."
          icon={<Network className="size-5" />}
          contentClassName="p-3 sm:p-4"
        >
          <MeshTopology graph={status.topology_graph} />
        </SectionCard>
      ) : null}

      {status?.config.role === 'controller' && status.config.enabled ? (
        <SectionCard
          title="Mesh devices"
          description="Complete remote devices connected through the directional EasyMesh Backhaul."
          icon={<RadioTower className="size-5" />}
          action={
            <Button size="sm" variant="outline" disabled={busy} onClick={() => void syncFronthaul()}>
              Re-apply client WiFi policy
            </Button>
          }
        >
          {(status.remote_nodes || []).length ? (
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
              {(status.remote_nodes || []).map((node) => (
                <div key={node.al_id} className="rounded-lg border border-border bg-muted/15 p-4">
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <div className="font-medium text-foreground">{node.name}</div>
                      <div className="mt-1 font-mono text-xs text-muted-foreground">{node.al_id}</div>
                    </div>
                    <span className="rounded-full bg-success/15 px-2 py-0.5 text-[11px] font-medium text-success">
                      Connected
                    </span>
                  </div>
                  <div className="mt-4 space-y-1.5 text-xs text-muted-foreground">
                    <div>Backhaul: {node.backhaul_medium}{node.backhaul_rssi && node.backhaul_rssi !== 'NA' ? ` · ${node.backhaul_rssi} dBm` : ''}</div>
                    <div>
                      Client coverage:{' '}
                      {node.fronthaul_ready
                        ? (node.fronthaul_ssids || []).join(' / ')
                        : 'Backhaul only — Fronthaul is not active'}
                    </div>
                    <div>
                      Management:{' '}
                      {node.management_online
                        ? `${node.management_ip} · reachable from Root`
                        : node.management_ip
                          ? `${node.management_ip} · currently unreachable`
                          : 'Waiting for an address'}
                    </div>
                  </div>
                  <div className="mt-4 flex items-center justify-between gap-3">
                    <p className="text-xs text-muted-foreground">
                      The Root keeps this complete Agent visible as one managed device.
                    </p>
                    {node.management_online && node.management_ip ? (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => window.open(`http://${node.management_ip}/`, '_blank', 'noopener,noreferrer')}
                      >
                        Open Agent
                      </Button>
                    ) : null}
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <div className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
              No remote Mesh device is currently visible in the Controller topology.
            </div>
          )}
        </SectionCard>
      ) : null}

      {status?.engine_running ? (
        <SectionCard
          title="Runtime diagnostics"
          description="Raw MediaTek Turnkey output is shown for first-stage hardware validation."
          icon={<Activity className="size-5" />}
        >
          <div className="grid gap-4 lg:grid-cols-2">
            <DiagnosticBlock title="Runtime role" value={status.runtime_role} />
            <DiagnosticBlock title="Backhaul status" value={status.backhaul_status} />
            <DiagnosticBlock title="Topology" value={status.topology} className="lg:col-span-2" />
          </div>
        </SectionCard>
      ) : null}

      <div className="flex items-start gap-3 rounded-lg border border-warning/30 bg-warning/10 p-4 text-sm">
        <Unplug className="mt-0.5 size-4 shrink-0 text-warning" />
        <div className="flex min-w-0 flex-1 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-muted-foreground">
            An Agent keeps its original management address and DHCP server until the selected wireless backhaul is verified. Leaving Mesh restores the complete pre-Mesh network backup.
          </p>
          {status?.config.enabled ? (
            <Button
              variant="destructiveOutline"
              size="sm"
              disabled={busy}
              onClick={() => void leaveMesh()}
            >
              Leave Mesh & Restore Standalone
            </Button>
          ) : (status?.config.network_prepared || status?.job.error) ? (
            <Button
              variant="destructiveOutline"
              size="sm"
              disabled={busy}
              onClick={() => void rollbackNetwork()}
            >
              Restore pre-Mesh network
            </Button>
          ) : null}
        </div>
      </div>
    </PageShell>
  )
}

function ActivationStep({
  number,
  title,
  description,
  complete,
  icon,
  action
}: {
  number: string
  title: string
  description: string
  complete: boolean
  icon: ReactNode
  action: ReactNode
}) {
  return (
    <div className="flex flex-col gap-3 rounded-lg border border-border bg-muted/20 p-4 sm:flex-row sm:items-center">
      <div
        className={`flex size-9 shrink-0 items-center justify-center rounded-full border ${
          complete
            ? 'border-success/40 bg-success/15 text-success'
            : 'border-border bg-background text-muted-foreground'
        }`}
      >
        {complete ? <CheckCircle2 className="size-4" /> : number}
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 font-medium text-foreground">
          {icon}
          {title}
        </div>
        <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{description}</p>
      </div>
      <div className="shrink-0">{action}</div>
    </div>
  )
}

function ModuleCard({
  name,
  detail,
  selected,
  capable,
  icon
}: {
  name: string
  detail: string
  selected: boolean
  capable: boolean
  icon: ReactNode
}) {
  return (
    <div
      className={`rounded-lg border p-4 ${
        selected ? 'border-primary/45 bg-primary/10' : 'border-border bg-muted/15'
      }`}
    >
      <div className="flex items-start justify-between gap-3">
        <div className={selected ? 'text-primary' : 'text-muted-foreground'}>{icon}</div>
        <span
          className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
            capable ? 'bg-success/15 text-success' : 'bg-destructive/15 text-destructive'
          }`}
        >
          {capable ? 'Ready' : 'Unavailable'}
        </span>
      </div>
      <div className="mt-3 font-medium text-foreground">{name}</div>
      <div className="mt-1 text-xs leading-relaxed text-muted-foreground">{detail}</div>
    </div>
  )
}

function DiagnosticBlock({ title, value, className = '' }: { title: string; value: string; className?: string }) {
  return (
    <div className={`min-w-0 rounded-lg border border-border bg-background p-4 ${className}`}>
      <div className="mb-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">{title}</div>
      <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-xs text-foreground">
        {value.trim() || 'No data reported'}
      </pre>
    </div>
  )
}

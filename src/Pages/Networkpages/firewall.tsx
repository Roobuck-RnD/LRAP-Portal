// src/Pages/Networkpages/firewall.tsx
import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Table } from '@/components/ui/table'
import { PageHeader, PageShell } from '@/components/page'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '@/components/ui/dialog'
import {
  Shield,
  Plus,
  Trash2,
  Edit,
  Save,
  Power,
  PowerOff,
  ShieldCheck
} from 'lucide-react'

// ---------- Types ----------

type FirewallDefaults = {
  section: string
  syn_flood: boolean
  drop_invalid: boolean
  input: string
  output: string
  forward: string
  flow_offloading: boolean
}

type FirewallZone = {
  section: string
  name: string
  networks?: string[] | null
  input: string
  output: string
  forward: string
  masq: boolean
  forwardings?: string[] | null
}

type PortForward = {
  section: string
  name: string
  enabled: boolean
  proto: string
  src: string
  src_dip: string
  src_dport: string
  dest: string
  dest_ip: string
  dest_port: string
  target: string
}

type FirewallResponse = {
  defaults: FirewallDefaults
  zones: FirewallZone[]
  port_forwards: PortForward[]
}

const FIREWALL_CACHE_KEY = 'network.firewall'

type PortForwardForm = {
  section?: string
  name: string
  enabled: boolean
  proto: string
  src: string
  src_dip: string
  src_dport: string
  dest: string
  dest_ip: string
  dest_port: string
}

const EMPTY_FORWARD: PortForwardForm = {
  name: '',
  enabled: true,
  proto: 'tcp',
  src: 'lan',
  src_dip: '',
  src_dport: '',
  dest: 'wan',
  dest_ip: '',
  dest_port: ''
}

// ---------- Helpers ----------

function getErrorMessage(err: unknown): string {
  if (err instanceof Error) return err.message
  return String(err)
}

function isValidIPv4(value: string): boolean {
  const v = value.trim()
  if (!v) return false

  const parts = v.split('.')
  if (parts.length !== 4) return false

  return parts.every((p) => {
    if (!/^\d+$/.test(p)) return false
    const n = Number(p)
    return n >= 0 && n <= 255
  })
}

function isValidPort(value: string): boolean {
  const v = value.trim()
  if (!/^\d+$/.test(v)) return false

  const n = Number(v)
  return n >= 1 && n <= 65535
}

function protoLabel(proto: string): string {
  if (proto === 'tcp udp') return 'TCP+UDP'
  return proto.toUpperCase()
}

function boolText(v: boolean): string {
  return v ? 'Enabled' : 'Disabled'
}

function policyTone(policy: string): string {
  const p = String(policy || '').toLowerCase()

  if (p === 'accept') return 'bg-success/15 text-success'
  if (p === 'reject') return 'bg-warning/15 text-warning'
  if (p === 'drop') return 'bg-destructive/15 text-destructive'

  return 'bg-muted text-muted-foreground'
}

function safeListText(value?: string[] | null): string {
  if (!Array.isArray(value) || value.length === 0) return '-'
  return value.join(', ')
}

// ---------- Component ----------

export default function Firewall(): JSX.Element {
  const [cachedAtMount] = useState<FirewallResponse | undefined>(() =>
    getPageDataCache<FirewallResponse>(FIREWALL_CACHE_KEY)
  )
  const [data, setData] = useState<FirewallResponse | null>(() => cachedAtMount ?? null)
  const [loading, setLoading] = useState(() => cachedAtMount === undefined)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [synFlood, setSynFlood] = useState(() => cachedAtMount?.defaults.syn_flood ?? true)
  const [flowOffloading, setFlowOffloading] = useState(
    () => cachedAtMount?.defaults.flow_offloading ?? false
  )

  const [isFormOpen, setIsFormOpen] = useState(false)
  const [formMode, setFormMode] = useState<'create' | 'edit'>('create')
  const [form, setForm] = useState<PortForwardForm>(EMPTY_FORWARD)

  const fetchFirewall = useCallback(async (isBackground = false) => {
    if (!isBackground) {
      setLoading(true)
    }

    setError(null)

    try {
      const res = await apiFetch('/api/net/firewall', {
        method: 'GET'
      })

      if (!res.ok) {
        const txt = await res.text().catch(() => '')
        throw new Error(txt || `HTTP ${res.status}`)
      }

      const json = (await res.json()) as FirewallResponse

      const normalizedData: FirewallResponse = {
        defaults: json.defaults,
        zones: Array.isArray(json.zones) ? json.zones : [],
        port_forwards: Array.isArray(json.port_forwards) ? json.port_forwards : []
      }

      setPageDataCache(FIREWALL_CACHE_KEY, normalizedData)
      setData(normalizedData)
      setSynFlood(json.defaults?.syn_flood ?? true)
      setFlowOffloading(json.defaults?.flow_offloading ?? false)
    } catch (err: unknown) {
      setError(getErrorMessage(err))
      if (!isBackground) setData(null)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchFirewall(cachedAtMount !== undefined)
  }, [cachedAtMount, fetchFirewall])

  const postAction = async (payload: unknown) => {
    const res = await apiFetch('/api/net/firewall', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    })

    if (!res.ok) {
      const txt = await res.text().catch(() => '')
      throw new Error(txt || 'Operation failed')
    }
  }

  const validateForwardForm = (): string | null => {
    if (!form.name.trim()) return 'Name is required.'
    if (!form.src_dport.trim()) return 'External port is required.'
    if (!form.dest_ip.trim()) return 'Internal IP is required.'
    if (!form.dest_port.trim()) return 'Internal port is required.'

    if (form.src_dip.trim() && !isValidIPv4(form.src_dip)) {
      return 'External IP must be a valid IPv4 address.'
    }

    if (!isValidPort(form.src_dport)) return 'External port must be 1-65535.'
    if (!isValidIPv4(form.dest_ip)) return 'Internal IP must be a valid IPv4 address.'
    if (!isValidPort(form.dest_port)) return 'Internal port must be 1-65535.'

    return null
  }

  const openCreateForm = () => {
    setFormMode('create')
    setForm(EMPTY_FORWARD)
    setIsFormOpen(true)
  }

  const openEditForm = (item: PortForward) => {
    setFormMode('edit')
    setForm({
      section: item.section,
      name: item.name,
      enabled: item.enabled,
      proto: item.proto || 'tcp',
      src: item.src || 'lan',
      src_dip: item.src_dip || '',
      src_dport: item.src_dport || '',
      dest: item.dest || 'wan',
      dest_ip: item.dest_ip || '',
      dest_port: item.dest_port || ''
    })
    setIsFormOpen(true)
  }

  const saveFirewallSettings = async () => {
    if (!(await confirmDialog({ title: 'Save firewall settings?', description: 'Save firewall global settings? Firewall will reload briefly.', confirmText: 'Save' }))) return

    setSaving(true)

    try {
      await postAction({
        action: 'update_settings',
        syn_flood: synFlood,
        flow_offloading: flowOffloading
      })

      await fetchFirewall()
    } catch (err: unknown) {
      toast.error('Error', { description: getErrorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  const saveForward = async () => {
    const validation = validateForwardForm()
    if (validation) {
      toast.error(validation)
      return
    }

    const ok = await confirmDialog({
      title: `${formMode === 'create' ? 'Create' : 'Update'} port forward?`,
      description:
        `${formMode === 'create' ? 'Create' : 'Update'} port forward?\n\n` +
        `Match: ${form.src_dip || 'any'}:${form.src_dport} (${protoLabel(form.proto)})\n` +
        `Forward to: ${form.dest_ip}:${form.dest_port}`,
      confirmText: formMode === 'create' ? 'Create' : 'Update'
    })

    if (!ok) return

    setSaving(true)

    try {
      await postAction({
        action: formMode === 'create' ? 'create_forward' : 'update_forward',
        section: form.section,
        name: form.name.trim(),
        enabled: form.enabled,
        proto: form.proto,
        src: form.src,
        src_dip: form.src_dip.trim(),
        src_dport: form.src_dport.trim(),
        dest: form.dest,
        dest_ip: form.dest_ip.trim(),
        dest_port: form.dest_port.trim()
      })

      setIsFormOpen(false)
      await fetchFirewall()
    } catch (err: unknown) {
      toast.error('Error', { description: getErrorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  const toggleForward = async (item: PortForward) => {
    setSaving(true)

    try {
      await postAction({
        action: 'toggle_forward',
        section: item.section,
        enabled: !item.enabled
      })

      await fetchFirewall()
    } catch (err: unknown) {
      toast.error('Error', { description: getErrorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  const deleteForward = async (item: PortForward) => {
    if (!(await confirmDialog({ title: 'Delete port forward?', description: `Delete port forward "${item.name || item.section}"?`, destructive: true, confirmText: 'Delete' }))) return

    setSaving(true)

    try {
      await postAction({
        action: 'delete_forward',
        section: item.section
      })

      await fetchFirewall()
    } catch (err: unknown) {
      toast.error('Error', { description: getErrorMessage(err) })
    } finally {
      setSaving(false)
    }
  }

  const settingsChanged = useMemo(() => {
    if (!data) return false

    return (
      synFlood !== data.defaults.syn_flood ||
      flowOffloading !== data.defaults.flow_offloading
    )
  }, [data, synFlood, flowOffloading])

  return (
    <PageShell>
      <PageHeader
        title="Firewall"
        description="Firewall overview, basic toggles, and port forwarding rules."
      />

        {error && (
          <div className="mb-6 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {loading && !data ? (
          <Card className="animate-pulse">
            <CardHeader className="h-20 border-b bg-muted" />
            <CardContent className="space-y-4 p-6">
              <div className="h-24 rounded-md bg-muted" />
              <div className="h-24 rounded-md bg-muted" />
            </CardContent>
          </Card>
        ) : data ? (
          <div className="space-y-6">
            <Card>
              <CardHeader className="border-b border-border">
                <div className="flex items-center gap-2">
                  <Shield className="h-5 w-5 text-foreground" />
                  <div>
                    <CardTitle>Firewall Overview</CardTitle>
                    <CardDescription>
                      Global defaults are mostly read-only. Only safe toggles are editable.
                    </CardDescription>
                  </div>
                </div>
              </CardHeader>

              <CardContent className="p-4">
                <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                  <div className="rounded border p-4">
                    <div className="mb-3 font-semibold text-foreground">Global Defaults</div>

                    <div className="space-y-3 text-sm">
                      <label className="flex items-center justify-between gap-3">
                        <span>SYN-flood protection</span>
                        <Switch checked={synFlood} onCheckedChange={setSynFlood} />
                      </label>

                      <label className="flex items-center justify-between gap-3">
                        <span>Software flow offloading</span>
                        <Switch checked={flowOffloading} onCheckedChange={setFlowOffloading} />
                      </label>

                      <div className="flex items-center justify-between gap-3">
                        <span>Drop invalid packets</span>
                        <span className="rounded bg-muted px-2 py-1 text-xs text-muted-foreground">
                          {boolText(data.defaults.drop_invalid)}
                        </span>
                      </div>

                      <div className="grid grid-cols-3 gap-2 pt-2">
                        <div>
                          <div className="text-xs text-muted-foreground">Input</div>
                          <span
                            className={`rounded px-2 py-1 text-xs ${policyTone(
                              data.defaults.input
                            )}`}
                          >
                            {data.defaults.input}
                          </span>
                        </div>

                        <div>
                          <div className="text-xs text-muted-foreground">Output</div>
                          <span
                            className={`rounded px-2 py-1 text-xs ${policyTone(
                              data.defaults.output
                            )}`}
                          >
                            {data.defaults.output}
                          </span>
                        </div>

                        <div>
                          <div className="text-xs text-muted-foreground">Forward</div>
                          <span
                            className={`rounded px-2 py-1 text-xs ${policyTone(
                              data.defaults.forward
                            )}`}
                          >
                            {data.defaults.forward}
                          </span>
                        </div>
                      </div>

                      <div className="pt-2">
                        <Button
                          size="sm"
                          onClick={() => void saveFirewallSettings()}
                          disabled={!settingsChanged || saving}
                        >
                          <Save className="mr-2 h-4 w-4" />
                          Save Settings
                        </Button>
                      </div>
                    </div>
                  </div>

                  <div className="rounded border p-4">
                    <div className="mb-3 font-semibold text-foreground">Zones</div>

                    <div className="overflow-x-auto rounded border">
                      <Table className="min-w-full text-left text-xs">
                        <thead className="border-b border-border text-muted-foreground">
                          <tr>
                            <th className="px-3 py-2">Zone</th>
                            <th className="px-3 py-2">Networks</th>
                            <th className="px-3 py-2">Input</th>
                            <th className="px-3 py-2">Output</th>
                            <th className="px-3 py-2">Forward</th>
                            <th className="px-3 py-2">Masq</th>
                            <th className="px-3 py-2">Forwards To</th>
                          </tr>
                        </thead>

                        <tbody className="divide-y">
                          {data.zones.length === 0 ? (
                            <tr>
                              <td colSpan={7} className="px-3 py-6 text-center text-muted-foreground">
                                No zones found.
                              </td>
                            </tr>
                          ) : (
                            data.zones.map((zone) => (
                              <tr key={zone.section}>
                                <td className="px-3 py-2 font-semibold">{zone.name || '-'}</td>
                                <td className="px-3 py-2">{safeListText(zone.networks)}</td>
                                <td className="px-3 py-2">{zone.input || '-'}</td>
                                <td className="px-3 py-2">{zone.output || '-'}</td>
                                <td className="px-3 py-2">{zone.forward || '-'}</td>
                                <td className="px-3 py-2">{zone.masq ? 'Yes' : 'No'}</td>
                                <td className="px-3 py-2">{safeListText(zone.forwardings)}</td>
                              </tr>
                            ))
                          )}
                        </tbody>
                      </Table>
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="border-b border-border">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex items-center gap-2">
                    <ShieldCheck className="h-5 w-5 text-primary" />
                    <div>
                      <CardTitle>Port Forwards</CardTitle>
                      <CardDescription>DNAT rules for forwarding traffic to another host.</CardDescription>
                    </div>
                  </div>

                  <Button onClick={openCreateForm} disabled={saving}>
                    <Plus className="mr-2 h-4 w-4" />
                    Add Port Forward
                  </Button>
                </div>
              </CardHeader>

              <CardContent className="p-4">
                <div className="overflow-x-auto rounded border">
                  <Table className="min-w-full text-left text-xs">
                    <thead className="border-b border-border text-muted-foreground">
                      <tr>
                        <th className="px-3 py-2">Name</th>
                        <th className="px-3 py-2">Match</th>
                        <th className="px-3 py-2">Forward To</th>
                        <th className="px-3 py-2">Zones</th>
                        <th className="px-3 py-2">Enabled</th>
                        <th className="px-3 py-2 text-right">Actions</th>
                      </tr>
                    </thead>

                    <tbody className="divide-y">
                      {data.port_forwards.length === 0 ? (
                        <tr>
                          <td colSpan={6} className="px-3 py-8 text-center italic text-muted-foreground">
                            No port forwards configured.
                          </td>
                        </tr>
                      ) : (
                        data.port_forwards.map((item) => (
                          <tr key={item.section} className="hover:bg-muted">
                            <td className="px-3 py-2 font-semibold">
                              {item.name || item.section}
                            </td>

                            <td className="px-3 py-2 font-mono">
                              {item.src_dip || 'any'}:{item.src_dport || '-'}{' '}
                              <span className="text-muted-foreground">({protoLabel(item.proto)})</span>
                            </td>

                            <td className="px-3 py-2 font-mono">
                              {item.dest_ip || '-'}:{item.dest_port || '-'}
                            </td>

                            <td className="px-3 py-2">
                              {item.src || '-'} → {item.dest || '-'}
                            </td>

                            <td className="px-3 py-2">
                              {item.enabled ? (
                                <span className="rounded bg-success/15 px-2 py-1 text-success">
                                  Enabled
                                </span>
                              ) : (
                                <span className="rounded bg-muted px-2 py-1 text-muted-foreground">
                                  Disabled
                                </span>
                              )}
                            </td>

                            <td className="px-3 py-2">
                              <div className="flex justify-end gap-2">
                                <Button
                                  variant={item.enabled ? 'warning' : 'success'}
                                  size="icon"
                                  onClick={() => void toggleForward(item)}
                                  disabled={saving}
                                  title={item.enabled ? 'Disable port forward' : 'Enable port forward'}
                                  aria-label={item.enabled ? 'Disable port forward' : 'Enable port forward'}
                                >
                                  {item.enabled ? (
                                    <PowerOff className="h-3 w-3" />
                                  ) : (
                                    <Power className="h-3 w-3" />
                                  )}
                                </Button>

                                <Button
                                  variant="outline"
                                  size="icon"
                                  onClick={() => openEditForm(item)}
                                  disabled={saving}
                                  title="Edit port forward"
                                  aria-label="Edit port forward"
                                >
                                  <Edit className="h-3 w-3" />
                                </Button>

                                <Button
                                  variant="destructiveOutline"
                                  size="icon"
                                  onClick={() => void deleteForward(item)}
                                  disabled={saving}
                                  title="Delete port forward"
                                  aria-label="Delete port forward"
                                >
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </div>
                            </td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </Table>
                </div>
              </CardContent>
            </Card>
          </div>
        ) : null}
      <Dialog open={isFormOpen} onOpenChange={setIsFormOpen}>
        <DialogContent className="sm:max-w-[620px]">
          <DialogHeader>
            <DialogTitle>{formMode === 'create' ? 'Add Port Forward' : 'Edit Port Forward'}</DialogTitle>
            <DialogDescription>Create a controlled DNAT rule.</DialogDescription>
          </DialogHeader>

            <div className="space-y-4 p-6">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>Name</Label>
                  <Input
                    value={form.name}
                    onChange={(e) => setForm((p) => ({ ...p, name: e.target.value }))}
                  />
                </div>

                <div className="space-y-1.5">
                  <Label>Protocol</Label>
                  <NativeSelect
                    value={form.proto}
                    onChange={(e) => setForm((p) => ({ ...p, proto: e.target.value }))}
                  >
                    <option value="tcp">TCP</option>
                    <option value="udp">UDP</option>
                    <option value="tcp udp">TCP+UDP</option>
                  </NativeSelect>
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>Source Zone</Label>
                  <NativeSelect
                    value={form.src}
                    onChange={(e) => setForm((p) => ({ ...p, src: e.target.value }))}
                  >
                    <option value="lan">lan</option>
                    <option value="wan">wan</option>
                  </NativeSelect>
                </div>

                <div className="space-y-1.5">
                  <Label>Destination Zone</Label>
                  <NativeSelect
                    value={form.dest}
                    onChange={(e) => setForm((p) => ({ ...p, dest: e.target.value }))}
                  >
                    <option value="wan">wan</option>
                    <option value="lan">lan</option>
                  </NativeSelect>
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>External IP</Label>
                  <Input
                    value={form.src_dip}
                    onChange={(e) => setForm((p) => ({ ...p, src_dip: e.target.value }))}
                  />
                </div>

                <div className="space-y-1.5">
                  <Label>External Port</Label>
                  <Input
                    value={form.src_dport}
                    onChange={(e) => setForm((p) => ({ ...p, src_dport: e.target.value }))}
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label>Internal IP</Label>
                  <Input
                    value={form.dest_ip}
                    onChange={(e) => setForm((p) => ({ ...p, dest_ip: e.target.value }))}
                  />
                </div>

                <div className="space-y-1.5">
                  <Label>Internal Port</Label>
                  <Input
                    value={form.dest_port}
                    onChange={(e) => setForm((p) => ({ ...p, dest_port: e.target.value }))}
                  />
                </div>
              </div>

              <Label className="flex items-center gap-2">
                <Switch checked={form.enabled} onCheckedChange={(checked) => setForm((p) => ({ ...p, enabled: checked }))} />
                Enable this rule
              </Label>
            </div>

            <DialogFooter>
              <Button variant="outline" onClick={() => setIsFormOpen(false)} disabled={saving}>
                Cancel
              </Button>

              <Button onClick={() => void saveForward()} disabled={saving}>
                {saving ? 'Saving...' : 'Save & Apply'}
              </Button>
            </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageShell>
  )
}

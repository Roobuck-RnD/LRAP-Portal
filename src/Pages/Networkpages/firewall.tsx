// src/Pages/Networkpages/firewall.tsx
import type { JSX } from 'react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { toast } from 'sonner'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Shield,
  RefreshCw,
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

  if (p === 'accept') return 'bg-green-100 text-green-700'
  if (p === 'reject') return 'bg-amber-100 text-amber-700'
  if (p === 'drop') return 'bg-red-100 text-red-700'

  return 'bg-gray-100 text-gray-600'
}

function safeListText(value?: string[] | null): string {
  if (!Array.isArray(value) || value.length === 0) return '-'
  return value.join(', ')
}

// ---------- Component ----------

export default function Firewall(): JSX.Element {
  const [data, setData] = useState<FirewallResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [backgroundLoading, setBackgroundLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [synFlood, setSynFlood] = useState(true)
  const [flowOffloading, setFlowOffloading] = useState(false)

  const [isFormOpen, setIsFormOpen] = useState(false)
  const [formMode, setFormMode] = useState<'create' | 'edit'>('create')
  const [form, setForm] = useState<PortForwardForm>(EMPTY_FORWARD)

  const fetchFirewall = useCallback(async (isBackground = false) => {
    if (isBackground) {
      setBackgroundLoading(true)
    } else {
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

      setData({
        defaults: json.defaults,
        zones: Array.isArray(json.zones) ? json.zones : [],
        port_forwards: Array.isArray(json.port_forwards) ? json.port_forwards : []
      })

      setSynFlood(json.defaults?.syn_flood ?? true)
      setFlowOffloading(json.defaults?.flow_offloading ?? false)
    } catch (err: unknown) {
      setError(getErrorMessage(err))
      if (!isBackground) setData(null)
    } finally {
      setLoading(false)
      setBackgroundLoading(false)
    }
  }, [])

  useEffect(() => {
    void fetchFirewall()

    let inFlight = false

    const timer = window.setInterval(() => {
      if (inFlight) return
      inFlight = true

      void fetchFirewall(true).finally(() => {
        inFlight = false
      })
    }, 8000)

    return () => window.clearInterval(timer)
  }, [fetchFirewall])

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
    <div className="w-full p-6">
      <div className="mx-auto max-w-6xl">
        <div className="mb-8 flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-3xl font-bold text-gray-900">Firewall</h2>
            <p className="mt-1 text-gray-500">
              Firewall overview, basic toggles, and port forwarding rules.
            </p>
          </div>

          <Button
            variant="outline"
            onClick={() => void fetchFirewall()}
            disabled={loading || backgroundLoading || saving}
          >
            <RefreshCw
              className={`mr-2 h-4 w-4 ${
                loading || backgroundLoading || saving ? 'animate-spin' : ''
              }`}
            />
            Refresh
          </Button>
        </div>

        {error && (
          <div className="mb-6 rounded border border-red-100 bg-red-50 p-3 text-sm text-red-600">
            {error}
          </div>
        )}

        {loading && !data ? (
          <Card className="animate-pulse">
            <CardHeader className="h-20 border-b bg-gray-50" />
            <CardContent className="space-y-4 p-6">
              <div className="h-24 rounded-md bg-gray-100" />
              <div className="h-24 rounded-md bg-gray-100" />
            </CardContent>
          </Card>
        ) : data ? (
          <div className="space-y-6">
            <Card>
              <CardHeader className="border-b bg-gray-50/50">
                <div className="flex items-center gap-2">
                  <Shield className="h-5 w-5 text-gray-700" />
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
                    <div className="mb-3 font-semibold text-gray-800">Global Defaults</div>

                    <div className="space-y-3 text-sm">
                      <label className="flex items-center justify-between gap-3">
                        <span>SYN-flood protection</span>
                        <input
                          type="checkbox"
                          checked={synFlood}
                          onChange={(e) => setSynFlood(e.target.checked)}
                        />
                      </label>

                      <label className="flex items-center justify-between gap-3">
                        <span>Software flow offloading</span>
                        <input
                          type="checkbox"
                          checked={flowOffloading}
                          onChange={(e) => setFlowOffloading(e.target.checked)}
                        />
                      </label>

                      <div className="flex items-center justify-between gap-3">
                        <span>Drop invalid packets</span>
                        <span className="rounded bg-gray-100 px-2 py-1 text-xs text-gray-600">
                          {boolText(data.defaults.drop_invalid)}
                        </span>
                      </div>

                      <div className="grid grid-cols-3 gap-2 pt-2">
                        <div>
                          <div className="text-xs text-gray-400">Input</div>
                          <span
                            className={`rounded px-2 py-1 text-xs ${policyTone(
                              data.defaults.input
                            )}`}
                          >
                            {data.defaults.input}
                          </span>
                        </div>

                        <div>
                          <div className="text-xs text-gray-400">Output</div>
                          <span
                            className={`rounded px-2 py-1 text-xs ${policyTone(
                              data.defaults.output
                            )}`}
                          >
                            {data.defaults.output}
                          </span>
                        </div>

                        <div>
                          <div className="text-xs text-gray-400">Forward</div>
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
                    <div className="mb-3 font-semibold text-gray-800">Zones</div>

                    <div className="overflow-x-auto rounded border">
                      <table className="min-w-full text-left text-xs">
                        <thead className="bg-gray-50 text-gray-500">
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
                              <td colSpan={7} className="px-3 py-6 text-center text-gray-400">
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
                      </table>
                    </div>
                  </div>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="border-b bg-gray-50/50">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex items-center gap-2">
                    <ShieldCheck className="h-5 w-5 text-blue-600" />
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
                  <table className="min-w-full text-left text-xs">
                    <thead className="bg-gray-50 text-gray-500">
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
                          <td colSpan={6} className="px-3 py-8 text-center italic text-gray-400">
                            No port forwards configured.
                          </td>
                        </tr>
                      ) : (
                        data.port_forwards.map((item) => (
                          <tr key={item.section} className="hover:bg-gray-50">
                            <td className="px-3 py-2 font-semibold">
                              {item.name || item.section}
                            </td>

                            <td className="px-3 py-2 font-mono">
                              {item.src_dip || 'any'}:{item.src_dport || '-'}{' '}
                              <span className="text-gray-400">({protoLabel(item.proto)})</span>
                            </td>

                            <td className="px-3 py-2 font-mono">
                              {item.dest_ip || '-'}:{item.dest_port || '-'}
                            </td>

                            <td className="px-3 py-2">
                              {item.src || '-'} → {item.dest || '-'}
                            </td>

                            <td className="px-3 py-2">
                              {item.enabled ? (
                                <span className="rounded bg-green-100 px-2 py-1 text-green-700">
                                  Enabled
                                </span>
                              ) : (
                                <span className="rounded bg-gray-100 px-2 py-1 text-gray-600">
                                  Disabled
                                </span>
                              )}
                            </td>

                            <td className="px-3 py-2">
                              <div className="flex justify-end gap-2">
                                <Button
                                  variant="outline"
                                  size="sm"
                                  onClick={() => void toggleForward(item)}
                                  disabled={saving}
                                >
                                  {item.enabled ? (
                                    <PowerOff className="h-3 w-3" />
                                  ) : (
                                    <Power className="h-3 w-3" />
                                  )}
                                </Button>

                                <Button
                                  variant="outline"
                                  size="sm"
                                  onClick={() => openEditForm(item)}
                                  disabled={saving}
                                >
                                  <Edit className="h-3 w-3" />
                                </Button>

                                <Button
                                  variant="outline"
                                  size="sm"
                                  className="border-red-200 text-red-600 hover:bg-red-50"
                                  onClick={() => void deleteForward(item)}
                                  disabled={saving}
                                >
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </div>
                            </td>
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              </CardContent>
            </Card>
          </div>
        ) : null}
      </div>

      {isFormOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm">
          <div className="max-w-[94vw] overflow-hidden rounded-lg bg-white shadow-xl sm:w-[620px]">
            <div className="border-b bg-gray-50 px-6 py-4">
              <h3 className="text-lg font-semibold text-gray-900">
                {formMode === 'create' ? 'Add Port Forward' : 'Edit Port Forward'}
              </h3>
              <p className="text-sm text-gray-500">
                Create a controlled DNAT rule.
              </p>
            </div>

            <div className="space-y-4 p-6">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium">Name</label>
                  <input
                    value={form.name}
                    onChange={(e) => setForm((p) => ({ ...p, name: e.target.value }))}
                    className="w-full rounded border px-3 py-2"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium">Protocol</label>
                  <select
                    value={form.proto}
                    onChange={(e) => setForm((p) => ({ ...p, proto: e.target.value }))}
                    className="w-full rounded border bg-white px-3 py-2"
                  >
                    <option value="tcp">TCP</option>
                    <option value="udp">UDP</option>
                    <option value="tcp udp">TCP+UDP</option>
                  </select>
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium">Source Zone</label>
                  <select
                    value={form.src}
                    onChange={(e) => setForm((p) => ({ ...p, src: e.target.value }))}
                    className="w-full rounded border bg-white px-3 py-2"
                  >
                    <option value="lan">lan</option>
                    <option value="wan">wan</option>
                  </select>
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium">Destination Zone</label>
                  <select
                    value={form.dest}
                    onChange={(e) => setForm((p) => ({ ...p, dest: e.target.value }))}
                    className="w-full rounded border bg-white px-3 py-2"
                  >
                    <option value="wan">wan</option>
                    <option value="lan">lan</option>
                  </select>
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium">External IP</label>
                  <input
                    value={form.src_dip}
                    onChange={(e) => setForm((p) => ({ ...p, src_dip: e.target.value }))}
                    className="w-full rounded border px-3 py-2"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium">External Port</label>
                  <input
                    value={form.src_dport}
                    onChange={(e) => setForm((p) => ({ ...p, src_dport: e.target.value }))}
                    className="w-full rounded border px-3 py-2"
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <label className="text-sm font-medium">Internal IP</label>
                  <input
                    value={form.dest_ip}
                    onChange={(e) => setForm((p) => ({ ...p, dest_ip: e.target.value }))}
                    className="w-full rounded border px-3 py-2"
                  />
                </div>

                <div className="space-y-1.5">
                  <label className="text-sm font-medium">Internal Port</label>
                  <input
                    value={form.dest_port}
                    onChange={(e) => setForm((p) => ({ ...p, dest_port: e.target.value }))}
                    className="w-full rounded border px-3 py-2"
                  />
                </div>
              </div>

              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.enabled}
                  onChange={(e) => setForm((p) => ({ ...p, enabled: e.target.checked }))}
                />
                Enable this rule
              </label>
            </div>

            <div className="flex justify-end gap-3 border-t bg-gray-50 px-6 py-4">
              <Button variant="outline" onClick={() => setIsFormOpen(false)} disabled={saving}>
                Cancel
              </Button>

              <Button onClick={() => void saveForward()} disabled={saving}>
                {saving ? 'Saving...' : 'Save & Apply'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
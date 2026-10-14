import type { JSX } from 'react'
import { useCallback, useEffect, useState } from 'react'
import { Network, Pencil, Plus, RefreshCw, Trash2, Lock } from 'lucide-react'
import { toast } from 'sonner'

import { apiFetch } from '@/utils/http'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Table } from '@/components/ui/table'
import { confirmDialog } from '@/components/ui/confirm'
import { FullScreenTaskOverlay } from '@/components/task-overlay'
import { PageHeader, PageShell, StatCard } from '@/components/page'
import { getPageDataCache, setPageDataCache } from '@/utils/page-data-cache'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle
} from '@/components/ui/dialog'

// Customer VLANs: tagged networks on the customer port (lan5), each with its
// own subnet, DHCP and firewall zone. The backend (backend/vlans.go) checks
// every change against the rest of the router and undoes it unless this page
// confirms it within a minute, which it does as soon as the apply returns.

type VLAN = {
  vid: number
  label: string
  ipaddr: string
  netmask: string
  dhcp: boolean
  dhcp_start: number
  dhcp_limit: number
  access: 'internet' | 'lan'
  up: boolean
}

type VLANList = {
  port: string
  port_present: boolean
  max: number
  reserved: number[]
  locked?: string
  vlans: VLAN[]
}

type Form = {
  vid: string
  label: string
  ipaddr: string
  netmask: string
  dhcp: boolean
  dhcp_start: string
  dhcp_limit: string
  access: 'internet' | 'lan'
}

const CACHE_KEY = 'network.vlans'

const EMPTY_FORM: Form = {
  vid: '',
  label: '',
  ipaddr: '',
  netmask: '255.255.255.0',
  dhcp: true,
  dhcp_start: '100',
  dhcp_limit: '100',
  access: 'internet'
}

const NETMASKS = Array.from({ length: 15 }, (_, i) => 30 - i).map((prefix) => {
  const mask = prefix === 0 ? 0 : (0xffffffff << (32 - prefix)) >>> 0
  const text = [24, 16, 8, 0].map((shift) => (mask >>> shift) & 255).join('.')
  return { prefix, text }
})

function prefixOf(netmask: string): number | null {
  const found = NETMASKS.find((m) => m.text === netmask)
  return found ? found.prefix : null
}

function isIPv4(value: string): boolean {
  const parts = value.trim().split('.')
  return parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255)
}

async function errorOf(res: Response): Promise<string> {
  try {
    const body = await res.json()
    if (body?.error) return String(body.error)
  } catch {
    /* not json */
  }
  return `HTTP ${res.status}`
}

export default function VLANs(): JSX.Element {
  const [list, setList] = useState<VLANList | null>(() => getPageDataCache<VLANList>(CACHE_KEY) ?? null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<number | null>(null) // VID being edited, 0 for a new one
  const [form, setForm] = useState<Form>(EMPTY_FORM)
  const [formError, setFormError] = useState('')
  const [applying, setApplying] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await apiFetch('/api/net/vlans')
      if (!res.ok) throw new Error(await errorOf(res))
      const data: VLANList = await res.json()
      setList(data)
      setPageDataCache(CACHE_KEY, data)
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const locked = list?.locked === 'mesh'
  const vlans = list?.vlans ?? []
  const full = list ? vlans.length >= list.max : false
  const canAdd = !!list && !locked && list.port_present && !full

  const openAdd = () => {
    setForm(EMPTY_FORM)
    setFormError('')
    setEditing(0)
  }

  const openEdit = (v: VLAN) => {
    setForm({
      vid: String(v.vid),
      label: v.label,
      ipaddr: v.ipaddr,
      netmask: v.netmask,
      dhcp: v.dhcp,
      dhcp_start: String(v.dhcp ? v.dhcp_start : 100),
      dhcp_limit: String(v.dhcp ? v.dhcp_limit : 100),
      access: v.access
    })
    setFormError('')
    setEditing(v.vid)
  }

  // apply sends one change, then confirms it. A change the browser cannot
  // confirm -- because it cut this browser off -- is undone by the router.
  const apply = async (what: string, request: () => Promise<Response>): Promise<string | null> => {
    setApplying(what)
    try {
      let res: Response
      try {
        res = await request()
      } catch {
        toast.error('Lost contact with the router', {
          description: 'If the change cut this connection, the router undoes it within a minute.'
        })
        return null
      }
      if (!res.ok) return await errorOf(res)
      const { change_id } = await res.json()
      const confirmed = await apiFetch('/api/net/vlans/confirm', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ change_id })
      }).catch(() => null)
      if (!confirmed || !confirmed.ok) {
        toast.error('The change was undone', {
          description: 'The router did not receive the confirmation in time.'
        })
        return null
      }
      return null
    } finally {
      setApplying('')
      void load()
    }
  }

  const save = async () => {
    const vid = Number(form.vid)
    if (!Number.isInteger(vid) || vid < 2 || vid > 4094) return setFormError('VLAN ID must be between 2 and 4094.')
    if (list?.reserved.includes(vid)) return setFormError(`VLAN ${vid} is reserved by the router.`)
    if (!isIPv4(form.ipaddr)) return setFormError('Router address must be an IPv4 address.')
    const body = JSON.stringify({
      vid,
      label: form.label.trim(),
      ipaddr: form.ipaddr.trim(),
      netmask: form.netmask,
      dhcp: form.dhcp,
      dhcp_start: form.dhcp ? Number(form.dhcp_start) : 0,
      dhcp_limit: form.dhcp ? Number(form.dhcp_limit) : 0,
      access: form.access
    })
    const isNew = editing === 0
    setFormError('')
    const failed = await apply(isNew ? `Adding VLAN ${vid}` : `Changing VLAN ${vid}`, () =>
      apiFetch(isNew ? '/api/net/vlans' : `/api/net/vlans?vid=${vid}`, {
        method: isNew ? 'POST' : 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body
      })
    )
    if (failed) {
      setFormError(failed)
      return
    }
    setEditing(null)
    toast.success(isNew ? `VLAN ${vid} added` : `VLAN ${vid} changed`)
  }

  const remove = async (v: VLAN) => {
    const ok = await confirmDialog({
      title: `Delete VLAN ${v.vid}?`,
      description: `Devices on VLAN ${v.vid}${v.label ? ` (${v.label})` : ''} lose their network. The router reloads its network; the antenna modules may be unreachable for a few seconds.`,
      confirmText: 'Delete',
      destructive: true
    })
    if (!ok) return
    const failed = await apply(`Deleting VLAN ${v.vid}`, () =>
      apiFetch(`/api/net/vlans?vid=${v.vid}`, { method: 'DELETE' })
    )
    if (failed) toast.error(`VLAN ${v.vid} was not deleted`, { description: failed })
    else toast.success(`VLAN ${v.vid} deleted`)
  }

  const set = <K extends keyof Form>(key: K, value: Form[K]) => setForm((f) => ({ ...f, [key]: value }))

  return (
    <PageShell>
      <PageHeader
        title="VLANs"
        description="Tagged networks on the customer port, each with its own subnet, DHCP and firewall."
        actions={
          <Button size="sm" onClick={openAdd} disabled={!canAdd || loading}>
            <Plus size={14} />
            Add VLAN
          </Button>
        }
      />

      <div className="mb-6 grid grid-cols-1 gap-4 sm:grid-cols-3">
        <StatCard
          label="Port"
          value={list?.port ?? 'lan5'}
          detail={list && !list.port_present ? 'Not present on this router' : 'Customer port'}
        />
        <StatCard label="VLANs" value={list ? `${vlans.length} / ${list.max}` : '—'} />
        <StatCard
          label="Reserved IDs"
          value={list ? list.reserved.join(', ') : '—'}
          detail="Used by the router itself"
        />
      </div>

      {locked && (
        <div className="mb-4 flex items-start gap-2 rounded-lg border border-warning/30 bg-warning/10 p-3 text-sm text-foreground">
          <Lock className="mt-0.5 size-4 shrink-0 text-warning" />
          <span>
            VLANs cannot be changed while a Mesh is on, or while one is being set up or taken down. Leaving a Mesh
            restores the network configuration from before it, and a change made in between would be lost.
          </span>
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm">
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <div className="flex items-center gap-2">
            <Network className="h-5 w-5 text-primary" />
            <div>
              <h3 className="text-lg font-semibold text-foreground">Customer VLANs</h3>
              <div className="text-xs text-muted-foreground">
                Only devices on {list?.port ?? 'lan5'} whose switch tags them are on a VLAN. Wi-Fi clients stay on the main
                LAN.
              </div>
            </div>
          </div>
          <Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label="Refresh">
            <RefreshCw size={14} className={loading ? 'animate-spin' : ''} />
          </Button>
        </div>

        <div className="p-4">
          {error && (
            <div className="mb-4 rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
              {error}
            </div>
          )}
          <div className="overflow-x-auto rounded border border-border">
            <Table className="min-w-full text-left text-xs">
              <thead className="border-b border-border text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">VLAN ID</th>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Router address</th>
                  <th className="px-3 py-2 font-medium">DHCP</th>
                  <th className="px-3 py-2 font-medium">Access</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="w-24 px-3 py-2 font-medium"></th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {vlans.length === 0 ? (
                  <tr>
                    <td colSpan={7} className="px-3 py-8 text-center italic text-muted-foreground">
                      No VLANs configured.
                    </td>
                  </tr>
                ) : (
                  vlans.map((v) => (
                    <tr key={v.vid} className="hover:bg-muted">
                      <td className="px-3 py-2 font-mono font-medium text-foreground">{v.vid}</td>
                      <td className="px-3 py-2">{v.label || '—'}</td>
                      <td className="px-3 py-2 font-mono">
                        {v.ipaddr}/{prefixOf(v.netmask) ?? v.netmask}
                      </td>
                      <td className="px-3 py-2 text-muted-foreground">
                        {v.dhcp ? `.${v.dhcp_start} + ${v.dhcp_limit}` : 'Off'}
                      </td>
                      <td className="px-3 py-2">{v.access === 'lan' ? 'Same as main LAN' : 'Internet only'}</td>
                      <td className="px-3 py-2">
                        <span className={v.up ? 'text-success' : 'text-muted-foreground'}>{v.up ? 'Up' : 'Down'}</span>
                      </td>
                      <td className="px-3 py-2 text-right whitespace-nowrap">
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => openEdit(v)}
                          disabled={locked || loading}
                          aria-label={`Edit VLAN ${v.vid}`}
                        >
                          <Pencil size={14} />
                        </Button>
                        <Button
                          variant="destructiveOutline"
                          size="icon"
                          onClick={() => void remove(v)}
                          disabled={locked || loading}
                          aria-label={`Delete VLAN ${v.vid}`}
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
        </div>
      </div>

      <Dialog open={editing !== null} onOpenChange={(open) => !open && !applying && setEditing(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{editing === 0 ? 'Add VLAN' : `Edit VLAN ${editing}`}</DialogTitle>
            <DialogDescription>
              Saving reloads the router&apos;s network; the antenna modules may be unreachable for a few seconds. If the
              change disturbs anything else, the router undoes it.
            </DialogDescription>
          </DialogHeader>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <Label className="mb-1 block text-xs">VLAN ID</Label>
              <Input
                type="number"
                min={2}
                max={4094}
                value={form.vid}
                disabled={editing !== 0}
                onChange={(e) => set('vid', e.target.value)}
                placeholder="10"
              />
            </div>
            <div>
              <Label className="mb-1 block text-xs">Name</Label>
              <Input value={form.label} maxLength={32} onChange={(e) => set('label', e.target.value)} placeholder="Cameras" />
            </div>
            <div>
              <Label className="mb-1 block text-xs">Router address</Label>
              <Input value={form.ipaddr} onChange={(e) => set('ipaddr', e.target.value)} placeholder="192.168.20.1" />
            </div>
            <div>
              <Label className="mb-1 block text-xs">Netmask</Label>
              <NativeSelect value={form.netmask} onChange={(e) => set('netmask', e.target.value)}>
                {NETMASKS.map((m) => (
                  <option key={m.prefix} value={m.text}>
                    {m.text} (/{m.prefix})
                  </option>
                ))}
              </NativeSelect>
            </div>
            <div className="col-span-2">
              <Label className="mb-1 block text-xs">Access</Label>
              <NativeSelect
                value={form.access}
                onChange={(e) => set('access', e.target.value as Form['access'])}
              >
                <option value="internet">Internet only: no access to the main LAN or to this router&apos;s settings</option>
                <option value="lan">Same as main LAN: reaches and is reached by the main LAN</option>
              </NativeSelect>
            </div>
            <div className="col-span-2 flex items-center justify-between rounded-md border border-border px-3 py-2">
              <div>
                <div className="text-sm font-medium">DHCP server</div>
                <div className="text-xs text-muted-foreground">Give devices on this VLAN an address automatically</div>
              </div>
              <Switch checked={form.dhcp} onCheckedChange={(v) => set('dhcp', v)} />
            </div>
            {form.dhcp && (
              <>
                <div>
                  <Label className="mb-1 block text-xs">First address (offset)</Label>
                  <Input type="number" min={1} value={form.dhcp_start} onChange={(e) => set('dhcp_start', e.target.value)} />
                </div>
                <div>
                  <Label className="mb-1 block text-xs">Number of addresses</Label>
                  <Input type="number" min={1} value={form.dhcp_limit} onChange={(e) => set('dhcp_limit', e.target.value)} />
                </div>
              </>
            )}
          </div>

          {formError && (
            <div className="rounded border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">{formError}</div>
          )}

          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)} disabled={!!applying}>
              Cancel
            </Button>
            <Button onClick={() => void save()} disabled={!!applying}>
              Save & Apply
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {applying && (
        <FullScreenTaskOverlay panelClassName="flex flex-col items-center">
          <div className="mb-5 flex size-20 items-center justify-center rounded-full border border-warning/30 bg-warning/10">
            <RefreshCw className="size-9 animate-spin text-warning" />
          </div>
          <h3 className="font-display text-2xl font-bold text-foreground">{applying}</h3>
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            The router is reloading its network and checking that everything else is as it was. This takes up to half a
            minute.
          </p>
        </FullScreenTaskOverlay>
      )}
    </PageShell>
  )
}

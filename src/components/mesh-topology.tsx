import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Antenna, Maximize2, MonitorSmartphone, Radio, RadioTower, Router, Wifi, ZoomIn, ZoomOut } from 'lucide-react'

import { cn } from '@/lib/utils'

export type MeshTopologyRadio = {
  band: string
  channel?: string
  bandwidth?: string
  streams?: string
  bssid?: string
  ssids: string[]
}

export type MeshTopologyPeer = {
  mac: string
  band?: string
  rssi?: string
}

export type MeshTopologyNode = {
  al_id: string
  label: string
  role: string
  distance: number
  map_version?: string
  is_local: boolean
  fabric_known: boolean
  hostname?: string
  management_ip?: string
  radios: MeshTopologyRadio[]
  clients: MeshTopologyPeer[]
}

export type MeshTopologyLink = {
  from_al_id: string
  to_al_id: string
  medium?: string
  rssi?: string
  wireless: boolean
  throughput_cap?: string
  link_availability?: string
  tx_packets?: string
  rx_packets?: string
  tx_errors?: string
  rx_errors?: string
}

export type MeshTopologyAntenna = {
  id: string
  name: string
  port?: string
  ip?: string
  mac?: string
  ssid_24?: string
  ssid_5?: string
  online: boolean
  chassis_al_id: string
}

export type MeshTopologyClient = {
  mac: string
  ip?: string
  hostname?: string
  ssid?: string
  band?: string
  interface?: string
  rssi?: number
  signal?: string
  connected_time?: string
  parent_id: string
}

export type MeshTopologyGraph = {
  nodes: MeshTopologyNode[]
  links: MeshTopologyLink[]
  antennas: MeshTopologyAntenna[]
  clients: MeshTopologyClient[]
}

const CHASSIS_WIDTH = 300
const CHASSIS_HEIGHT = 214
const ANTENNA_WIDTH = 300
const ANTENNA_HEIGHT = 124
const CLIENT_ROW = 68
const CLIENT_GAP = 8
const CLIENT_OVERFLOW_ROW = 24
const MAX_CLIENT_ROWS = 6
const GAP_X = 32
const GAP_Y = 92
const PADDING = 18

type SignalGrade = 'strong' | 'fair' | 'weak' | 'unknown'

function gradeDBm(dBm: number | undefined): SignalGrade {
  if (dBm === undefined || !Number.isFinite(dBm) || dBm >= 0) return 'unknown'
  if (dBm >= -62) return 'strong'
  if (dBm >= -74) return 'fair'
  return 'weak'
}

function gradeRSSI(rssi?: string): SignalGrade {
  return gradeDBm(Number.parseInt((rssi ?? '').trim(), 10))
}

const gradeStroke: Record<SignalGrade, string> = {
  strong: 'var(--signal)',
  fair: 'var(--primary)',
  weak: 'var(--warning)',
  unknown: 'var(--muted-foreground)'
}

const gradeText: Record<SignalGrade, string> = {
  strong: 'text-signal',
  fair: 'text-primary',
  weak: 'text-warning',
  unknown: 'text-muted-foreground'
}

type TreeNode = {
  id: string
  kind: 'chassis' | 'antenna'
  chassis?: MeshTopologyNode
  antenna?: MeshTopologyAntenna
  clients: MeshTopologyClient[]
  children: TreeNode[]
  width: number
  height: number
  x: number
  y: number
}

/**
 * Height of the client list drawn under a node, zero when it serves none.
 * CLIENT_ROW is the stride of one row including the gap below it, so this has to
 * track what ClientRows actually renders or the links land off the cards.
 */
function clientPanelHeight(count: number): number {
  if (!count) return 0
  const rows = Math.min(count, MAX_CLIENT_ROWS)
  const overflow = count > MAX_CLIENT_ROWS ? CLIENT_OVERFLOW_ROW + CLIENT_GAP / 2 : 0
  return CLIENT_GAP + rows * CLIENT_ROW - CLIENT_GAP / 2 + overflow
}

/**
 * Build the fabric as a tree rather than as rows of equals: a chassis owns its
 * Antennas and its downstream Mesh hops, and an Antenna owns the end devices it
 * serves. Clients hang under their own parent as a list rather than as siblings,
 * so a busy Antenna grows downwards instead of pushing the whole drawing wide.
 */
function buildForest(graph: MeshTopologyGraph): TreeNode[] {
  const clientsByParent = new Map<string, MeshTopologyClient[]>()
  for (const client of graph.clients ?? []) {
    const bucket = clientsByParent.get(client.parent_id)
    if (bucket) bucket.push(client)
    else clientsByParent.set(client.parent_id, [client])
  }

  const makeNode = (node: Partial<TreeNode> & Pick<TreeNode, 'id' | 'kind'>): TreeNode => {
    const clients = clientsByParent.get(node.id) ?? []
    const base = node.kind === 'chassis' ? CHASSIS_HEIGHT : ANTENNA_HEIGHT
    return {
      clients,
      children: [],
      width: node.kind === 'chassis' ? CHASSIS_WIDTH : ANTENNA_WIDTH,
      height: base + clientPanelHeight(clients.length),
      x: 0,
      y: 0,
      ...node
    }
  }

  const chassisNodes = new Map<string, TreeNode>()
  for (const chassis of graph.nodes ?? []) {
    chassisNodes.set(chassis.al_id, makeNode({ id: chassis.al_id, kind: 'chassis', chassis }))
  }

  for (const antenna of graph.antennas ?? []) {
    const parent = chassisNodes.get(antenna.chassis_al_id)
    if (parent) parent.children.push(makeNode({ id: antenna.id, kind: 'antenna', antenna }))
  }

  const hasParent = new Set<string>()
  for (const link of graph.links ?? []) {
    const from = chassisNodes.get(link.from_al_id)
    const to = chassisNodes.get(link.to_al_id)
    if (!from || !to || hasParent.has(link.to_al_id)) continue
    from.children.push(to)
    hasParent.add(link.to_al_id)
  }

  return [...chassisNodes.values()].filter((node) => !hasParent.has(node.id))
}

/** Tidy-tree placement: a parent is centred over the span its children occupy. */
function layoutForest(roots: TreeNode[]): { width: number; height: number } {
  const rowHeights: number[] = []
  const measureDepth = (node: TreeNode, depth: number) => {
    rowHeights[depth] = Math.max(rowHeights[depth] ?? 0, node.height)
    node.children.forEach((child) => measureDepth(child, depth + 1))
  }
  roots.forEach((root) => measureDepth(root, 0))

  const rowTops: number[] = []
  let cursor = 0
  rowHeights.forEach((height, depth) => {
    rowTops[depth] = cursor
    cursor += height + GAP_Y
  })

  const place = (node: TreeNode, left: number, depth: number): number => {
    node.y = rowTops[depth]
    if (!node.children.length) {
      node.x = left
      return node.width
    }
    let childLeft = left
    let span = 0
    node.children.forEach((child, index) => {
      const childSpan = place(child, childLeft, depth + 1)
      childLeft += childSpan + GAP_X
      span += childSpan + (index ? GAP_X : 0)
    })
    if (span < node.width) {
      // A single narrow child would otherwise sit off to one side of its parent.
      const shift = (node.width - span) / 2
      const nudge = (child: TreeNode) => {
        child.x += shift
        child.children.forEach(nudge)
      }
      node.children.forEach(nudge)
      span = node.width
    }
    node.x = left + (span - node.width) / 2
    return span
  }

  let offset = 0
  roots.forEach((root) => {
    offset += place(root, offset, 0) + GAP_X
  })

  const width = Math.max(offset - GAP_X, CHASSIS_WIDTH)
  const height = cursor > 0 ? cursor - GAP_Y : CHASSIS_HEIGHT
  return { width, height }
}

function flatten(roots: TreeNode[]): TreeNode[] {
  const out: TreeNode[] = []
  const walk = (node: TreeNode) => {
    out.push(node)
    node.children.forEach(walk)
  }
  roots.forEach(walk)
  return out
}

/** A labelled value, dimmed label then the reading. */
function Field({ label, value, className }: { label: string; value: string; className?: string }) {
  return (
    <span className={cn('inline-flex items-baseline gap-1 whitespace-nowrap', className)}>
      <span className="text-[9px] tracking-wider text-muted-foreground/70 uppercase">{label}</span>
      <span className="data text-[10px] text-foreground/90">{value}</span>
    </span>
  )
}

function ClientRows({ node }: { node: TreeNode }) {
  const shown = node.clients.slice(0, MAX_CLIENT_ROWS)
  const hidden = node.clients.length - shown.length

  return (
    <div className="mt-2 flex flex-col gap-1">
      {shown.map((client) => {
        const grade = gradeDBm(client.rssi)
        const named = client.hostname && client.hostname !== '(unknown)'
        const radio = [client.band, client.interface].filter(Boolean).join(' · ')
        // Every row is: what to call it, then how to find it. When a device has
        // no name the MAC becomes the title, so repeating it underneath said the
        // same thing twice and made two cards look like different formats.
        const title = named ? client.hostname : client.ip || client.mac
        const identifier = title === client.mac ? '' : client.mac

        return (
          <div
            key={client.mac}
            className="flex flex-col justify-center gap-0.5 rounded-md border border-border/60 bg-background/70 px-2 py-1.5 leading-tight"
            style={{ height: CLIENT_ROW - CLIENT_GAP / 2 }}
          >
            <div className="flex h-4 items-center gap-1.5">
              <MonitorSmartphone className={cn('size-3 shrink-0', gradeText[grade])} />
              <span className="min-w-0 flex-1 truncate text-[11px] font-medium text-foreground">
                {title}
              </span>
              {client.rssi ? (
                <span className={cn('data shrink-0 text-[10px]', gradeText[grade])}>
                  {client.rssi} dBm{client.signal ? ' · ' + client.signal : ''}
                </span>
              ) : null}
            </div>
            <div className="flex h-3.5 min-w-0 items-center gap-2 overflow-hidden">
              {client.ip && title !== client.ip ? <Field label="ip" value={client.ip} /> : null}
              {identifier ? (
                <span className="data min-w-0 truncate text-[10px] text-muted-foreground">{identifier}</span>
              ) : (
                <span className="text-[10px] text-muted-foreground/70">no address yet</span>
              )}
            </div>
            <div className="flex h-3.5 min-w-0 items-center gap-2 overflow-hidden text-[10px] text-muted-foreground">
              {radio ? <span className="shrink-0 whitespace-nowrap">{radio}</span> : null}
              {client.ssid ? <span className="min-w-0 truncate">{client.ssid}</span> : null}
              {client.connected_time ? (
                <span className="data ml-auto shrink-0 whitespace-nowrap">{client.connected_time}</span>
              ) : null}
            </div>
          </div>
        )
      })}
      {hidden > 0 ? (
        <div
          className="flex items-center justify-center rounded-md border border-dashed border-border/60 text-[10px] text-muted-foreground"
          style={{ height: 24 }}
        >
          +{hidden} more
        </div>
      ) : null}
    </div>
  )
}

function ChassisCard({ node }: { node: TreeNode }) {
  const chassis = node.chassis as MeshTopologyNode
  const isController = chassis.role === 'controller'
  const ssids = [...new Set(chassis.radios.flatMap((radio) => radio.ssids))]
  const antennaCount = node.children.filter((child) => child.kind === 'antenna').length

  return (
    <>
      <div
        className={cn(
          'flex flex-col gap-2 rounded-xl border bg-card/95 p-3 shadow-lg backdrop-blur-sm',
          isController
            ? 'border-primary/45 shadow-[0_0_28px_color-mix(in_oklab,var(--primary)_16%,transparent)]'
            : 'border-border',
          chassis.is_local && 'ring-1 ring-signal/40'
        )}
        style={{ height: CHASSIS_HEIGHT }}
      >
        <div className="flex items-start gap-2.5">
          <div
            className={cn(
              'flex size-9 shrink-0 items-center justify-center rounded-lg border',
              isController
                ? 'border-primary/40 bg-primary/10 text-primary'
                : 'border-border bg-muted/40 text-muted-foreground'
            )}
          >
            {isController ? <RadioTower className="size-4" /> : <Router className="size-4" />}
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5">
              <span className="truncate text-sm font-medium text-foreground">
                {chassis.hostname || chassis.label}
              </span>
              {chassis.is_local ? (
                <span className="shrink-0 rounded-full border border-signal/30 bg-signal/10 px-1.5 py-px text-[9px] font-medium tracking-wider text-signal uppercase">
                  This device
                </span>
              ) : null}
            </div>
            <div className="flex flex-wrap items-center gap-x-1.5 text-[10px] text-muted-foreground">
              <span>{chassis.label}</span>
              {chassis.map_version ? <span>MAP {chassis.map_version}</span> : null}
              {chassis.distance > 0 ? <span>{chassis.distance} hop</span> : null}
            </div>
            <div className="data mt-0.5 truncate text-[10px] text-muted-foreground/80">{chassis.al_id}</div>
          </div>
        </div>

        <div className="flex flex-col gap-1">
          {chassis.radios.map((radio) => (
            <div
              key={chassis.al_id + '-' + radio.band}
              className="flex items-center gap-2 rounded-md border border-border/60 bg-background/50 px-1.5 py-0.5 text-[10px]"
              title={radio.bssid}
            >
              <Radio className="size-2.5 shrink-0 text-primary" />
              <span className="shrink-0 text-foreground">{radio.band}</span>
              {radio.channel ? <Field label="ch" value={radio.channel} /> : null}
              {radio.bandwidth ? <Field label="bw" value={radio.bandwidth} /> : null}
              {radio.streams ? <Field label="mimo" value={radio.streams} /> : null}
            </div>
          ))}
        </div>

        {ssids.length ? (
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <Wifi className="size-3 shrink-0 text-signal" />
            <span className="truncate" title={ssids.join(' / ')}>
              {ssids.join(' / ')}
            </span>
          </div>
        ) : null}

        <div className="mt-auto flex items-center justify-between gap-2 border-t border-border/60 pt-2 text-[11px]">
          {chassis.fabric_known ? (
            // "direct" meant clients on the chassis's own radio rather than on
            // one of its Antennas, which is not something the word says on its
            // own -- spell out the radio instead.
            <span
              className="truncate text-muted-foreground"
              title={
                node.clients.length === 1
                  ? '1 client is associated to this chassis\'s own radio rather than to one of its Antennas'
                  : `${node.clients.length} clients are associated to this chassis's own radio rather than to one of its Antennas`
              }
            >
              {antennaCount} {antennaCount === 1 ? 'antenna' : 'antennas'} ·{' '}
              {node.clients.length === 0 ? 'none' : node.clients.length} on its radio
            </span>
          ) : (
            <span
              className="text-warning/90"
              title="This chassis has not reported its Antennas yet, so what it serves is unknown rather than empty."
            >
              Antennas not reported
            </span>
          )}
          {chassis.management_ip ? (
            <a
              className="data truncate text-primary hover:underline"
              href={'http://' + chassis.management_ip + '/'}
              target="_blank"
              rel="noopener noreferrer"
            >
              {chassis.management_ip}
            </a>
          ) : (
            <span className="text-muted-foreground/70">{chassis.is_local ? 'local' : 'no address'}</span>
          )}
        </div>
      </div>
      {node.clients.length ? <ClientRows node={node} /> : null}
    </>
  )
}

function AntennaCard({ node }: { node: TreeNode }) {
  const antenna = node.antenna as MeshTopologyAntenna
  const networks: string[] = []
  if (antenna.ssid_24) networks.push(antenna.ssid_24)
  if (antenna.ssid_5 && antenna.ssid_5 !== antenna.ssid_24) networks.push(antenna.ssid_5)

  return (
    <>
      <div
        className={cn(
          'flex flex-col gap-1.5 rounded-lg border border-dashed bg-card/80 p-2.5 backdrop-blur-sm',
          antenna.online ? 'border-signal/35' : 'border-border'
        )}
        style={{ height: ANTENNA_HEIGHT }}
      >
        <div className="flex items-center gap-2">
          <div
            className={cn(
              'flex size-7 shrink-0 items-center justify-center rounded-md border',
              antenna.online
                ? 'border-signal/35 bg-signal/10 text-signal'
                : 'border-border bg-muted/40 text-muted-foreground'
            )}
          >
            <Antenna className="size-3.5" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-[13px] font-medium text-foreground">{antenna.name}</div>
            <div className="text-[9px] tracking-wider text-muted-foreground uppercase">
              Antenna module{antenna.port ? ' - ' + antenna.port : ''}
            </div>
          </div>
          <span className={cn('status-dot size-1.5', antenna.online ? 'text-online' : 'text-offline')} />
        </div>

        <div className="flex items-baseline gap-3 overflow-hidden">
          {antenna.ip ? <Field label="ip" value={antenna.ip} /> : null}
          {antenna.mac ? <Field label="mac" value={antenna.mac} className="min-w-0 truncate" /> : null}
        </div>

        <div className="mt-auto flex items-center justify-between gap-2 text-[10px] text-muted-foreground">
          <span className="flex min-w-0 items-center gap-1.5">
            <Wifi className="size-2.5 shrink-0 text-signal" />
            <span className="truncate">{networks.length ? networks.join(' / ') : 'no network reported'}</span>
          </span>
          <span className="shrink-0">
            {node.clients.length} {node.clients.length === 1 ? 'client' : 'clients'}
          </span>
        </div>
      </div>
      {node.clients.length ? <ClientRows node={node} /> : null}
    </>
  )
}

const MIN_ZOOM = 0.25
const MAX_ZOOM = 2.5
const ZOOM_STEP = 1.25

type View = { x: number; y: number; z: number }

/** Zoom about a fixed point so the thing under the cursor or fingers stays put. */
function zoomAbout(view: View, factor: number, px: number, py: number): View {
  const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, view.z * factor))
  const applied = z / view.z
  return { z, x: px - (px - view.x) * applied, y: py - (py - view.y) * applied }
}

export function MeshTopology({ graph, className }: { graph: MeshTopologyGraph; className?: string }) {
  const { nodes, width, height } = useMemo(() => {
    const roots = buildForest(graph)
    const size = layoutForest(roots)
    return { nodes: flatten(roots), width: size.width, height: size.height }
  }, [graph])

  const linkByTarget = useMemo(() => {
    const byTarget = new Map<string, MeshTopologyLink>()
    for (const link of graph.links ?? []) byTarget.set(link.to_al_id, link)
    return byTarget
  }, [graph.links])

  const canvasWidth = width + PADDING * 2
  const canvasHeight = height + PADDING * 2

  const viewportRef = useRef<HTMLDivElement>(null)
  const [view, setView] = useState<View>({ x: 0, y: 0, z: 1 })

  // Centre the fabric in whatever room the viewport has, shrinking only as far
  // as needed. A Mesh that already fits is never blown up past its own size.
  const fit = useCallback(() => {
    const viewport = viewportRef.current
    if (!viewport) return
    const { clientWidth, clientHeight } = viewport
    if (!clientWidth || !clientHeight) return
    const z = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, Math.min(1, clientWidth / canvasWidth, clientHeight / canvasHeight)))
    setView({ z, x: (clientWidth - canvasWidth * z) / 2, y: (clientHeight - canvasHeight * z) / 2 })
  }, [canvasWidth, canvasHeight])

  // Auto-fit on the first draw and whenever the viewport is resized -- but stop
  // once the operator has moved the view themselves. Status is polled every few
  // seconds and a single client joining changes the drawing's height, so
  // re-fitting on every shape change would repeatedly throw away their zoom.
  // The fit button is the way back to automatic.
  const userMovedView = useRef(false)
  const shape = canvasWidth + 'x' + canvasHeight

  const fitToView = useCallback(() => {
    userMovedView.current = false
    fit()
  }, [fit])

  useLayoutEffect(() => {
    if (!userMovedView.current) fit()
  }, [fit, shape])

  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => {
      if (!userMovedView.current) fit()
    })
    observer.observe(viewport)
    return () => observer.disconnect()
  }, [fit])

  // Pointer events cover mouse, pen and touch with one path: one pointer drags,
  // two pinch.
  const pointers = useRef(new Map<number, { x: number; y: number }>())
  const pinch = useRef<{ distance: number } | null>(null)

  const localPoint = (event: { clientX: number; clientY: number }) => {
    const box = viewportRef.current?.getBoundingClientRect()
    return { x: event.clientX - (box?.left ?? 0), y: event.clientY - (box?.top ?? 0) }
  }

  const onPointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    // The controls sit on top of the pan surface. Capturing the pointer here
    // would retarget the click to this element -- a click is delivered to the
    // common ancestor of pointerdown and pointerup -- and the buttons would
    // never fire, which is exactly what happened.
    if ((event.target as HTMLElement).closest('[data-topo-control]')) return
    event.currentTarget.setPointerCapture(event.pointerId)
    pointers.current.set(event.pointerId, localPoint(event))
    if (pointers.current.size === 2) {
      const [a, b] = [...pointers.current.values()]
      pinch.current = { distance: Math.hypot(a.x - b.x, a.y - b.y) }
    }
  }

  const onPointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    const previous = pointers.current.get(event.pointerId)
    if (!previous) return
    userMovedView.current = true
    const next = localPoint(event)
    pointers.current.set(event.pointerId, next)

    if (pointers.current.size >= 2 && pinch.current) {
      const [a, b] = [...pointers.current.values()]
      const distance = Math.hypot(a.x - b.x, a.y - b.y)
      if (pinch.current.distance > 0 && distance > 0) {
        const factor = distance / pinch.current.distance
        const midX = (a.x + b.x) / 2
        const midY = (a.y + b.y) / 2
        setView((current) => zoomAbout(current, factor, midX, midY))
      }
      pinch.current = { distance }
      return
    }

    setView((current) => ({ ...current, x: current.x + (next.x - previous.x), y: current.y + (next.y - previous.y) }))
  }

  const endPointer = (event: React.PointerEvent<HTMLDivElement>) => {
    pointers.current.delete(event.pointerId)
    if (pointers.current.size < 2) pinch.current = null
  }

  // The wheel listener is attached by hand because React's synthetic one is
  // passive, and a passive listener cannot stop the page from scrolling.
  useEffect(() => {
    const viewport = viewportRef.current
    if (!viewport) return
    const onWheel = (event: WheelEvent) => {
      event.preventDefault()
      userMovedView.current = true
      const box = viewport.getBoundingClientRect()
      const factor = event.deltaY < 0 ? ZOOM_STEP : 1 / ZOOM_STEP
      setView((current) => zoomAbout(current, factor, event.clientX - box.left, event.clientY - box.top))
    }
    viewport.addEventListener('wheel', onWheel, { passive: false })
    return () => viewport.removeEventListener('wheel', onWheel)
  }, [])

  const zoomFromCentre = (factor: number) => {
    const viewport = viewportRef.current
    if (!viewport) return
    userMovedView.current = true
    setView((current) => zoomAbout(current, factor, viewport.clientWidth / 2, viewport.clientHeight / 2))
  }

  if (!nodes.length) {
    return (
      <div className="rounded-lg border border-dashed border-border p-8 text-center text-sm text-muted-foreground">
        The Mesh engine has not reported a topology yet.
      </div>
    )
  }

  const controlButton =
    'flex size-8 items-center justify-center rounded-md border border-border/70 bg-background/85 text-muted-foreground backdrop-blur-md transition-colors hover:border-primary/40 hover:text-foreground disabled:opacity-40'

  return (
    <div className={cn('relative overflow-hidden rounded-xl border border-border/70 bg-muted/10', className)}>
      <div aria-hidden className="topo-grid pointer-events-none absolute inset-0 opacity-40" />
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-signal/50 to-transparent"
      />

      <div
        ref={viewportRef}
        // touch-none keeps a drag on the fabric from scrolling the page, the way
        // any map behaves; the page still scrolls from the header and legend.
        className="relative h-[380px] cursor-grab touch-none overflow-hidden active:cursor-grabbing sm:h-[520px] lg:h-[600px]"
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={endPointer}
        onPointerCancel={endPointer}
        onPointerLeave={endPointer}
      >
        <div
          className="absolute top-0 left-0 origin-top-left"
          style={{
            width: canvasWidth,
            height: canvasHeight,
            transform: 'translate(' + view.x + 'px, ' + view.y + 'px) scale(' + view.z + ')'
          }}
        >
          <svg
            className="absolute inset-0"
            width={canvasWidth}
            height={canvasHeight}
            viewBox={'0 0 ' + canvasWidth + ' ' + canvasHeight}
            aria-hidden="true"
          >
            <g transform={'translate(' + PADDING + ' ' + PADDING + ')'}>
              {nodes.flatMap((parent) =>
                parent.children.map((child) => {
                  const link = child.kind === 'chassis' ? linkByTarget.get(child.id) : undefined
                  const wireless = link?.wireless ?? false
                  const grade = wireless ? gradeRSSI(link?.rssi) : 'unknown'
                  // A wired Antenna uplink is drawn plainly; only a Mesh hop is
                  // coloured by signal, because only it can degrade.
                  const stroke = wireless ? gradeStroke[grade] : 'var(--border)'

                  const x1 = parent.x + parent.width / 2
                  const y1 = parent.y + parent.height
                  const x2 = child.x + child.width / 2
                  const y2 = child.y
                  const curve = Math.max(24, (y2 - y1) / 2)
                  const path =
                    'M ' + x1 + ' ' + y1 + ' C ' + x1 + ' ' + (y1 + curve) + ', ' + x2 + ' ' + (y2 - curve) + ', ' + x2 + ' ' + y2

                  return (
                    <g key={parent.id + '>' + child.id}>
                      <path
                        d={path}
                        fill="none"
                        stroke={stroke}
                        strokeOpacity={wireless ? 0.3 : 0.85}
                        strokeWidth={wireless ? 2 : 1.5}
                        strokeDasharray={wireless ? undefined : '4 4'}
                      />
                      {wireless ? (
                        <path
                          d={path}
                          pathLength="1"
                          fill="none"
                          stroke={stroke}
                          strokeWidth="3"
                          strokeLinecap="round"
                          className="topo-link-flow"
                        />
                      ) : null}
                      <circle cx={x1} cy={y1} r="3" fill={stroke} className={wireless ? 'topo-node-pulse' : undefined} />
                      <circle cx={x2} cy={y2} r="3" fill={stroke} />
                    </g>
                  )
                })
              )}
            </g>
          </svg>

          <div className="absolute" style={{ left: PADDING, top: PADDING, width, height }}>
            {nodes.map((node) => (
              <div key={node.id} className="absolute" style={{ left: node.x, top: node.y, width: node.width }}>
                {node.kind === 'chassis' ? <ChassisCard node={node} /> : <AntennaCard node={node} />}
              </div>
            ))}

            {/* Hop labels ride above the cards so the signal reading stays legible. */}
            {nodes.flatMap((parent) =>
              parent.children
                .filter((child) => child.kind === 'chassis')
                .map((child) => {
                  const link = linkByTarget.get(child.id)
                  if (!link) return null
                  const grade = link.wireless ? gradeRSSI(link.rssi) : 'strong'
                  const dBm = (link.rssi ?? '').trim()

                  return (
                    <div
                      key={'label-' + parent.id + '>' + child.id}
                      className="absolute -translate-x-1/2 -translate-y-1/2"
                      style={{
                        left: (parent.x + parent.width / 2 + child.x + child.width / 2) / 2,
                        top: (parent.y + parent.height + child.y) / 2
                      }}
                    >
                      <span className="flex items-center gap-1.5 rounded-full border border-border/70 bg-background/90 px-2.5 py-1 text-[10px] whitespace-nowrap shadow-sm backdrop-blur-md">
                        <span className={cn('status-dot size-1.5', gradeText[grade], link.wireless && 'status-dot--live')} />
                        <span className="font-medium text-foreground">{link.medium || 'Backhaul'}</span>
                        {dBm && dBm !== 'NA' ? <span className={cn('data', gradeText[grade])}>{dBm} dBm</span> : null}
                      </span>
                    </div>
                  )
                })
            )}
          </div>
        </div>

        <div data-topo-control className="absolute top-3 right-3 flex flex-col gap-1.5">
          <button
            type="button"
            aria-label="Zoom in"
            className={controlButton}
            disabled={view.z >= MAX_ZOOM}
            onClick={() => zoomFromCentre(ZOOM_STEP)}
          >
            <ZoomIn className="size-4" />
          </button>
          <button
            type="button"
            aria-label="Zoom out"
            className={controlButton}
            disabled={view.z <= MIN_ZOOM}
            onClick={() => zoomFromCentre(1 / ZOOM_STEP)}
          >
            <ZoomOut className="size-4" />
          </button>
          <button type="button" aria-label="Fit to view" className={controlButton} onClick={fitToView}>
            <Maximize2 className="size-4" />
          </button>
        </div>

        <div className="data pointer-events-none absolute bottom-3 left-3 rounded-md border border-border/60 bg-background/80 px-2 py-0.5 text-[10px] text-muted-foreground backdrop-blur-md">
          {Math.round(view.z * 100)}%
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-border/60 bg-background/40 px-4 py-2 text-[10px] tracking-wider text-muted-foreground uppercase">
        <span className="flex items-center gap-1.5">
          <RadioTower className="size-3 text-primary" /> Chassis
        </span>
        <span className="flex items-center gap-1.5">
          <Antenna className="size-3 text-signal" /> Antenna module
        </span>
        <span className="flex items-center gap-1.5">
          <MonitorSmartphone className="size-3" /> End device
        </span>
        <span className="flex items-center gap-1.5">
          <span className="status-dot size-1.5 text-signal" /> Strong
        </span>
        <span className="flex items-center gap-1.5">
          <span className="status-dot size-1.5 text-primary" /> Fair
        </span>
        <span className="flex items-center gap-1.5">
          <span className="status-dot size-1.5 text-warning" /> Weak
        </span>
        <span className="ml-auto hidden normal-case sm:inline">Drag to pan, scroll or pinch to zoom</span>
      </div>
    </div>
  )
}

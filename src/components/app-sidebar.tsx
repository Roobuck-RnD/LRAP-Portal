'use client'

import * as React from 'react'
import { useEffect } from 'react'
import { Bot, Settings2, SquareTerminal, Map, Router } from 'lucide-react'
import { SignalMark } from '@/components/signal-mark'
import { NavMain } from '@/components/nav-main'
import { NavUser } from '@/components/nav-user'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail,
  SidebarMenu,
  SidebarMenuItem,
  SidebarMenuButton
} from '@/components/ui/sidebar'
import { type Module } from '@/states/moduleState'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { compareManagedModules } from '@/lib/module-order'

type LanClient = {
  module_id?: string
  port: string
  mac: string
  ip: string
  hostname: string
  type?: 'main' | 'ap'
  online?: boolean
}
const data = {
  user: {
    name: 'Admin',
    email: '',
    // avatar: '/avatars/shadcn.jpg',
    avatar: ''
  },
  navMain: [
    {
      title: 'Status',
      icon: SquareTerminal,
      isActive: true,
      items: [
        { title: 'Overview', url: '/' },
        { title: 'RoutesStatus', url: 'routesstatus' },
        { title: 'Connected Clients', url: 'connectedClients' }
      ]
    },
    {
      title: 'System',
      icon: Bot,
      items: [
        { title: 'System Information', url: 'system' },
        { title: 'Administration', url: 'administration' },
        { title: 'Flash Firmware', url: 'flashfirmware' },
        { title: 'Reboot', url: 'reboot' }
      ]
    },
    {
      title: 'Network',
      icon: Settings2,
      items: [
        { title: 'Interfaces', url: 'interfaces' },
        { title: 'DHCP and DNS', url: 'DHCPandDNS' },
        { title: 'Static Routes', url: 'staticroutes' },
        { title: 'Firewall', url: 'firewall' }
      ]
    },
    {
      title: 'MTK',
      icon: Map,
      items: [{ title: 'WiFi configuration', url: 'wificonfiguration' }]
    }
  ]
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const { updateCurrentAllModule } = useCurrentAllModuleStore()

  useEffect(() => {
    let cancelled = false
    let retryTimer: ReturnType<typeof setTimeout> | undefined

    const fetchModules = async () => {
      try {
        const res = await apiFetch('/api/lan/clients')
        if (!res.ok) {
          console.error('Failed to load modules:', res.status, await res.text())
          if (!cancelled) {
            retryTimer = setTimeout(() => void fetchModules(), 3000)
          }
          return
        }
        const arr: LanClient[] = await res.json()
        if (cancelled) return

        // 映射到你的 Module 结构
        const parsed: Module[] = arr.map((item) => {
          const isMain = item.type === 'main' || item.port === 'br-lan'

          const fallbackName = isMain ? '(unknown)' : 'RoobuckAP'
          const hasValidHostname =
            item.hostname && item.hostname !== '?' && item.hostname !== '(unknown)'

          return {
            module_id: item.module_id,
            name: hasValidHostname ? item.hostname : fallbackName,
            ipaddress: item.ip || '(unknown)',
            mac: item.mac,
            port: item.port,
            logo: Router,
            type: isMain ? 'Main Module' : 'Sub Module'
          }
        })

        // Stable product order: Router, then Antenna1/lan1 through
        // Antenna4/lan4. Management IP is only a last-resort fallback.
        parsed.sort(compareManagedModules)

        const expectedModuleCount = Number(sessionStorage.getItem('wifiExpectedModuleCount'))
        const recoveryDeadline = Number(sessionStorage.getItem('wifiModuleRecoveryDeadline'))
        const waitingForRecoveredModules =
          Number.isFinite(expectedModuleCount) &&
          expectedModuleCount > 1 &&
          parsed.length < expectedModuleCount &&
          Number.isFinite(recoveryDeadline) &&
          Date.now() < recoveryDeadline

        if (waitingForRecoveredModules) {
          console.warn(
            `Module discovery is still recovering: ${parsed.length}/${expectedModuleCount}`
          )
          retryTimer = setTimeout(() => void fetchModules(), 3000)
          return
        }

        sessionStorage.removeItem('wifiExpectedModuleCount')
        sessionStorage.removeItem('wifiModuleRecoveryDeadline')
        updateCurrentAllModule(parsed)
      } catch (e) {
        console.error('Failed to load modules:', e)
        if (!cancelled) {
          retryTimer = setTimeout(() => void fetchModules(), 3000)
        }
      }
    }

    // Retry module discovery while the management network is still recovering
    // after a WiFi reload. Live status pages own background polling afterwards.
    void fetchModules()

    return () => {
      cancelled = true
      if (retryTimer) clearTimeout(retryTimer)
    }
  }, [updateCurrentAllModule])

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild>
              <a href="#">
                <div className="ring-sidebar-border flex aspect-square size-9 items-center justify-center rounded-md bg-sidebar-accent ring-1">
                  <SignalMark className="size-5" id="brand-signal" />
                </div>
                <div className="flex flex-col gap-0.5 leading-none">
                  <span className="font-display text-[0.95rem] font-semibold tracking-tight">Roobuck</span>
                  <span className="text-muted-foreground text-[0.65rem] font-medium tracking-[0.14em] uppercase">
                    Console
                  </span>
                </div>
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <NavMain items={data.navMain} />
      </SidebarContent>
      <SidebarFooter>
        <NavUser user={data.user} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

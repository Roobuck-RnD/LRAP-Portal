'use client'

import * as React from 'react'
import { useEffect } from 'react'
import { Bot, Settings2, SquareTerminal, Map, Router } from 'lucide-react'
import { NavMain } from '@/components/nav-main'
import { NavUser } from '@/components/nav-user'
// import { ModuleSwitcher } from '@/components/module-switcher'
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
import { useCurrentModuleStore } from '@/states/moduleState'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'

type LanClient = {
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

function filterNavItems(navItems: typeof data.navMain, moduleType?: string) {
  const hiddenItemsForSubModule = ['Administration']
  if (moduleType !== 'Main Module') {
    return navItems.map((group) => {
      if (!group.items) return group
      const filteredItems = group.items.filter(
        (item) => !hiddenItemsForSubModule.includes(item.title)
      )
      return { ...group, items: filteredItems }
    })
  }
  return navItems
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  // const [modules, setModules] = useState<Module[]>([])
  const { currentModule } = useCurrentModuleStore()
  const { updateCurrentAllModule } = useCurrentAllModuleStore()

  useEffect(() => {
    let timer: number | null = null

    const fetchModules = async () => {
      const token = sessionStorage.getItem('token') ?? ''
      try {
        const res = await apiFetch('/api/lan/clients', {
          headers: { Authorization: `Bearer ${token}` }
        })
        if (res.status === 401) {
          // 未登录或 token 失效 → 清理并让上层路由去跳转登录
          sessionStorage.removeItem('isLoggedIn')
          sessionStorage.removeItem('token')
          return
        }
        if (!res.ok) {
          console.error('Failed to load modules:', res.status, await res.text())
          return
        }
        const arr: LanClient[] = await res.json()

        // 映射到你的 Module 结构
        const parsed: Module[] = arr.map((item) => {
          const isMain = item.type === 'main' || item.port === 'br-lan'

          const fallbackName = isMain ? '(unknown)' : 'RoobuckAP'
          const hasValidHostname =
            item.hostname && item.hostname !== '?' && item.hostname !== '(unknown)'

          return {
            name: hasValidHostname ? item.hostname : fallbackName,
            ipaddress: item.ip || '(unknown)',
            mac: item.mac,
            port: item.port,
            logo: Router,
            type: isMain ? 'Main Module' : 'Sub Module'
          }
        })

        // 主模块放在第一位（Go 已经这么做了，这里再兜底一下）
        parsed.sort((a, b) =>
          a.type === 'Main Module'
            ? -1
            : b.type === 'Main Module'
              ? 1
              : a.ipaddress.localeCompare(b.ipaddress)
        )

        updateCurrentAllModule(parsed)
      } catch (e) {
        console.error('Failed to load modules:', e)
      }
    }

    // 先拉一次
    fetchModules()
    // 轮询建议别太频繁（1s 太凶了，容易把 CPU/日志打爆）；5s 比较稳妥
    timer = window.setInterval(fetchModules, 5000)

    return () => {
      if (timer) window.clearInterval(timer)
    }
  }, [updateCurrentAllModule])

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader>
        {/* <ModuleSwitcher modules={modules} /> */}
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" asChild>
              <a href="#">
                <div className="bg-sidebar-primary text-sidebar-primary-foreground flex aspect-square size-8 items-center justify-center rounded-lg">
                  <Router className="size-4" />
                </div>
                <div className="flex flex-col gap-0.5 leading-none">
                  <span className="font-medium">Roobuck</span>
                </div>
              </a>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        <NavMain items={filterNavItems(data.navMain, currentModule?.type)} />
      </SidebarContent>
      <SidebarFooter>
        <NavUser user={data.user} />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}

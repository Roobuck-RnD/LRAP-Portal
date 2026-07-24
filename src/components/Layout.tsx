import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator
} from '@/components/ui/breadcrumb'
import { Separator } from '@/components/ui/separator'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { AppSidebar } from './app-sidebar'
import { Outlet, useLocation } from 'react-router'
import { ThemeToggle } from './theme-toggle'

// 面包屑从当前路由(URL)派生,而不是从内存 store 读。之前读的 zustand
// (currentTab/currentTabGroup)没有持久化,刷新后会重置成默认值(Status /
// Overview),但 URL(HashRouter)仍指向真实页面,导致面包屑与内容不一致。
// URL 是刷新后仍在的唯一可靠来源,所以以它为准。key 对应 App.tsx 里的路由 path,
// label/group 对应 app-sidebar 的显示名。
const ROUTE_BREADCRUMB: Record<string, { group: string; label: string }> = {
  '/': { group: 'Status', label: 'Overview' },
  '/routesstatus': { group: 'Status', label: 'RoutesStatus' },
  '/connectedClients': { group: 'Status', label: 'Connected Clients' },
  '/firewallstatus': { group: 'Status', label: 'Firewall Status' },
  '/systemlog': { group: 'Status', label: 'System Log' },
  '/kernellog': { group: 'Status', label: 'Kernel Log' },
  '/processes': { group: 'Status', label: 'Processes' },
  '/realtimegraphs': { group: 'Status', label: 'Realtime Graphs' },
  '/system': { group: 'System', label: 'System Information' },
  '/administration': { group: 'System', label: 'Administration' },
  '/flashfirmware': { group: 'System', label: 'Flash Firmware' },
  '/reboot': { group: 'System', label: 'Reboot' },
  '/software': { group: 'System', label: 'Software' },
  '/startup': { group: 'System', label: 'Startup' },
  '/scheduledtasks': { group: 'System', label: 'Scheduled Tasks' },
  '/mountpoints': { group: 'System', label: 'Mount Points' },
  '/ledconfiguration': { group: 'System', label: 'LED Configuration' },
  '/interfaces': { group: 'Network', label: 'Interfaces' },
  '/DHCPandDNS': { group: 'Network', label: 'DHCP and DNS' },
  '/hostnames': { group: 'Network', label: 'Hostnames' },
  '/staticroutes': { group: 'Network', label: 'Static Routes' },
  '/firewall': { group: 'Network', label: 'Firewall' },
  '/diagnostics': { group: 'Network', label: 'Diagnostics' },
  '/ipsecurity': { group: 'Network', label: 'IP Security' },
  '/networkshares': { group: 'Services', label: 'Network Shares' },
  '/wificonfiguration': { group: 'MTK', label: 'WiFi configuration' },
  '/easymesh': { group: 'MTK', label: 'EasyMesh' }
}

const DEFAULT_BREADCRUMB = { group: 'Status', label: 'Overview' }

export default function Layout() {
  const location = useLocation()

  // 先精确匹配,匹配不到再按大小写不敏感兜底(路由 path 有 connectedClients /
  // DHCPandDNS 这类混合大小写),都没有就回默认。
  const meta =
    ROUTE_BREADCRUMB[location.pathname] ||
    Object.entries(ROUTE_BREADCRUMB).find(
      ([path]) => path.toLowerCase() === location.pathname.toLowerCase()
    )?.[1] ||
    DEFAULT_BREADCRUMB

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <header className="flex h-16 shrink-0 items-center gap-2 border-b border-border/50 bg-background/85 px-4 backdrop-blur-md transition-[width,height] ease-linear group-has-[[data-collapsible=icon]]/sidebar-wrapper:h-12">
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <SidebarTrigger className="-ml-1" />
            <Separator orientation="vertical" className="mr-2 h-4" />
            <Breadcrumb>
              <BreadcrumbList>
                <BreadcrumbItem className="hidden md:block">
                  <BreadcrumbPage>{meta.group}</BreadcrumbPage>
                </BreadcrumbItem>
                <BreadcrumbSeparator className="hidden md:block" />
                <BreadcrumbItem>
                  <BreadcrumbPage>{meta.label}</BreadcrumbPage>
                </BreadcrumbItem>
              </BreadcrumbList>
            </Breadcrumb>
          </div>
          <ThemeToggle />
        </header>
        <div className="flex flex-1 flex-col gap-4 p-4">
          <Outlet />
        </div>
      </SidebarInset>
    </SidebarProvider>
  )
}

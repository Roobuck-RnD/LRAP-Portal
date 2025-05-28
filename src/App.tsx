import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator
} from '@/components/ui/breadcrumb'
import { Separator } from '@/components/ui/separator'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { AppSidebar } from './components/app-sidebar'
import { HashRouter, Routes, Route } from 'react-router'
import Administration from './Pages/Systempages/administration'
import FlashFirmware from './Pages/Systempages/flashfirmware'
import LEDConfiguration from './Pages/Systempages/ledconfiguration'
import MountPoints from './Pages/Systempages/mountpoints'
import Reboot from './Pages/Systempages/reboot'
import ScheduledTasks from './Pages/Systempages/scheduledtasks'
import Software from './Pages/Systempages/software'
import Startup from './Pages/Systempages/startup'
import System from './Pages/Systempages/system'
import FirewallStatus from './Pages/Statuspages/firewall'
import KernelLog from './Pages/Statuspages/kernellog'
import Overview from './Pages/Statuspages/overview'
import Processes from './Pages/Statuspages/processes'
import RealtimeGraphs from './Pages/Statuspages/realtimegraphs'
import RoutesStatus from './Pages/Statuspages/routes'
import SystemLog from './Pages/Statuspages/systemlog'
import NetworkShares from './Pages/Servicespages/networkshares'
import DHCPandDNS from './Pages/Networkpages/dhcpanddns'
import Diagnostics from './Pages/Networkpages/diagnostics'
import Firewall from './Pages/Networkpages/firewall'
import Hostnames from './Pages/Networkpages/hostnames'
import Interfaces from './Pages/Networkpages/interfaces'
import IPSecurity from './Pages/Networkpages/ipsecurity'
import StaticRoutes from './Pages/Networkpages/staticroutes'
import EasyMesh from './Pages/MTKpages/easymesh'
import WiFiConfiguration from './Pages/MTKpages/wificonfiguration'
import useCurrentTabStore from './states/tabState'
import useCurrentTabGroupStore from './states/tabgroupState'

function App() {
  const { currentTab } = useCurrentTabStore()
  const { currentTabGroup } = useCurrentTabGroupStore()
  return (
    <HashRouter>
      <SidebarProvider>
        <AppSidebar />
        <SidebarInset>
          <header className="flex h-16 shrink-0 items-center gap-2 transition-[width,height] ease-linear group-has-[[data-collapsible=icon]]/sidebar-wrapper:h-12">
            <div className="flex items-center gap-2 px-4">
              <SidebarTrigger className="-ml-1" />
              <Separator orientation="vertical" className="mr-2 h-4" />
              <Breadcrumb>
                <BreadcrumbList>
                  <BreadcrumbItem className="hidden md:block">
                    <BreadcrumbPage>{currentTabGroup}</BreadcrumbPage>
                  </BreadcrumbItem>
                  <BreadcrumbSeparator className="hidden md:block" />
                  <BreadcrumbItem>
                    <BreadcrumbPage>{currentTab}</BreadcrumbPage>
                  </BreadcrumbItem>
                </BreadcrumbList>
              </Breadcrumb>
            </div>
          </header>
          <div className="flex flex-1 flex-col gap-4 p-4 pt-0">
              <Routes>
                <Route path="/" element={<Overview />} />
                <Route path="firewallstatus" element={<FirewallStatus />} />
                <Route path="routesstatus" element={<RoutesStatus />} />
                <Route path="systemlog" element={<SystemLog />} />
                <Route path="kernellog" element={<KernelLog />} />
                <Route path="processes" element={<Processes />} />
                <Route path="realtimegraphs" element={<RealtimeGraphs />} />
                <Route path="system" element={<System />} />
                <Route path="administration" element={<Administration />} />
                <Route path="software" element={<Software />} />
                <Route path="startup" element={<Startup />} />
                <Route path="scheduledtasks" element={<ScheduledTasks />} />
                <Route path="mountpoints" element={<MountPoints />} />
                <Route path="ledconfiguration" element={<LEDConfiguration />} />
                <Route path="flashfirmware" element={<FlashFirmware />} />
                <Route path="reboot" element={<Reboot />} />
                <Route path="networkshares" element={<NetworkShares />} />
                <Route path="interfaces" element={<Interfaces />} />
                <Route path="DHCPandDNS" element={<DHCPandDNS />} />
                <Route path="hostnames" element={<Hostnames />} />
                <Route path="staticroutes" element={<StaticRoutes />} />
                <Route path="firewall" element={<Firewall />} />
                <Route path="diagnostics" element={<Diagnostics />} />
                <Route path="ipsecurity" element={<IPSecurity />} />
                <Route path="wificonfiguration" element={<WiFiConfiguration />} />
                <Route path="easymesh" element={<EasyMesh />} />
              </Routes>
          </div>
        </SidebarInset>
      </SidebarProvider>
    </HashRouter>
  )
}

export default App

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
import ConnectedClients from './Pages/Statuspages/connectedclients'
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
import ProtectedRoute from './Pages/Loginpages/ProtectedRoute'
import Login from './Pages/Loginpages/Login'
import Layout from './components/Layout'
import { Toaster } from './components/ui/sonner'

function App() {
  return (
    <HashRouter>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route
          path="/"
          element={
            <ProtectedRoute>
              <Layout />
            </ProtectedRoute>
          }
        >
          <Route path="/" element={<Overview />} />
          <Route path="firewallstatus" element={<FirewallStatus />} />
          <Route path="routesstatus" element={<RoutesStatus />} />
          <Route path="connectedClients" element={<ConnectedClients />} />
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
        </Route>
      </Routes>
      <Toaster position="top-right" richColors />
    </HashRouter>
  )
}

export default App

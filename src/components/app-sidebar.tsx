'use client'

import * as React from 'react'
// import { NavLink } from 'react-router'
import {
  BookOpen,
  Bot,
  Settings2,
  SquareTerminal,
  Map,
  Router
} from 'lucide-react'

import { NavMain } from '@/components/nav-main'
import { NavUser } from '@/components/nav-user'
import { ModuleSwitcher } from '@/components/module-switcher'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail
} from '@/components/ui/sidebar'
import { type Module } from '@/states/moduleState'

// This is sample data.
const modules:Module[] =[
    {
      name: 'Roobuck AC',
      ipaddress:'10.10.18.1',
      logo: Router,
      type: 'Main Module'
    },
    {
      name: 'Roobuck AP1',
      ipaddress:'192.168.200.1',
      logo: Router,
      type: 'Sub Module'
    },
    {
      name: 'Roobuck AP2',
      ipaddress:'192.168.200.2',
      logo: Router,
      type: 'Sub Module'
    },
    {
      name: 'Roobuck AP3',
      ipaddress:'192.168.200.3',
      logo: Router,
      type: 'Sub Module'
    },
    {
      name: 'Roobuck AP4',
      ipaddress:'192.168.200.4',
      logo: Router,
      type: 'Sub Module'
    }
  ]

const data = {
  user: {
    name: 'Chase',
    email: 'chasey@roobuck.com.au',
    avatar: '/avatars/shadcn.jpg'
  },
  navMain: [
    {
      title: 'Status',
      icon: SquareTerminal,
      isActive: true,
      items: [
        {
          title: 'Overview',
          url: '/'
        },
        {
          title: 'FirewallStatus',
          url: 'firewallstatus'
        },
        {
          title: 'RoutesStatus',
          url: 'routesstatus'
        },
        {
          title: 'System Log',
          url: 'systemlog'
        },
        {
          title: 'Kernel Log',
          url: 'kernellog'
        },
        {
          title: 'Processes',
          url: 'processes'
        },
        {
          title: 'Realtime Graphs',
          url: 'realtimegraphs'
        }
      ]
    },
    {
      title: 'System',
      icon: Bot,
      items: [
        {
          title: 'System Information',
          url: 'system'
        },
        {
          title: 'Administration',
          url: 'administration'
        },
        {
          title: 'Software',
          url: 'software'
        },
        {
          title: 'Startup',
          url: 'startup'
        },
        {
          title: 'Scheduled Tasks',
          url: 'scheduledtasks'
        },
        {
          title: 'Mount Points',
          url: 'mountpoints'
        },
        {
          title: 'LED Configuration',
          url: 'ledconfiguration'
        },
        {
          title: 'Backup/Flash Firmware',
          url: 'flashfirmware'
        },
        {
          title: 'Reboot',
          url: 'reboot'
        }
      ]
    },
    {
      title: 'Services',
      icon: BookOpen,
      items: [
        {
          title: 'Network Shares',
          url: 'networkshares'
        }
      ]
    },
    {
      title: 'Network',
      icon: Settings2,
      items: [
        {
          title: 'Interfaces',
          url: 'interfaces'
        },
        {
          title: 'DHCP and DNS',
          url: 'DHCPandDNS'
        },
        {
          title: 'Hostnames',
          url: 'hostnames'
        },
        {
          title: 'Static Routes',
          url: 'staticroutes'
        },
        {
          title: 'Firewall',
          url: 'firewall'
        },
        {
          title: 'Diagnostics',
          url: 'diagnostics'
        },
        {
          title: 'IP Security',
          url: 'ipsecurity'
        }
      ]
    },
    {
      title: 'MTK',
      icon: Map,
      items: [
        {
          title: 'WiFi configuration',
          url: 'wificonfiguration'
        },
        {
          title: 'EasyMesh',
          url: 'easymesh'
        }
      ]
    }
  ],
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader>
        <ModuleSwitcher modules={modules} />
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

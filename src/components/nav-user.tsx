'use client'

import { ChevronsUpDown, LogOut, Code2, UserCog } from 'lucide-react'
import { useNavigate } from 'react-router'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger
} from '@/components/ui/dropdown-menu'
import {
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar
} from '@/components/ui/sidebar'
import useDevModeStore from '@/states/devModeState'
import { apiFetch } from '@/utils/http'
import { clearPageDataCache } from '@/utils/page-data-cache'

// Developer Mode is for our own staff: it is offered only to someone signed in
// as root, and needs no password of its own. It only shows more detail; the
// server answers every account the same.

export function NavUser({
  user
}: {
  user: {
    name: string
    email: string
    avatar: string
  }
}) {
  const { isMobile } = useSidebar()
  const navigate = useNavigate()

  const { devMode, setDevMode } = useDevModeStore()

  const isRoot = sessionStorage.getItem('role') === 'root'
  // The account actually signed in (root or admin), not a fixed label.
  const name = sessionStorage.getItem('username') || user.name

  const handleLogout = () => {
    // 登出即退出开发者模式,回到默认隐藏状态。
    setDevMode(false)
    // End the session on the device too, not only in this tab. The client does
    // not attach the token to /api/sessions (it is the sign-in path), so it is
    // sent here. Nothing waits for the answer: the page signs out either way.
    const token = sessionStorage.getItem('token')
    if (token) {
      void apiFetch('/api/sessions', { method: 'DELETE', headers: { Authorization: `Bearer ${token}` } }).catch(() => {})
    }

    sessionStorage.removeItem('isLoggedIn')
    sessionStorage.removeItem('token')
    sessionStorage.removeItem('role')
    sessionStorage.removeItem('username')

    localStorage.removeItem('isLoggedIn')
    localStorage.removeItem('token')
    clearPageDataCache()

    navigate('/login', { replace: true })
  }

  return (
    <>
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <Avatar className="h-8 w-8 rounded-lg">
                <AvatarImage src={user.avatar} alt={name} />
                <AvatarFallback className="rounded-lg bg-primary/10 text-primary">
                  <UserCog className="size-4" />
                </AvatarFallback>
              </Avatar>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-semibold">{name}</span>
                <span className="truncate text-xs">{user.email}</span>
              </div>
              <ChevronsUpDown className="ml-auto size-4" />
            </SidebarMenuButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent
            className="w-[--radix-dropdown-menu-trigger-width] min-w-56 rounded-lg"
            side={isMobile ? 'bottom' : 'right'}
            align="end"
            sideOffset={4}
          >
            {isRoot && (
              <>
                <DropdownMenuItem onClick={() => setDevMode(!devMode)} className="cursor-pointer">
                  <Code2 />
                  {devMode ? 'Exit Developer Mode' : 'Developer Mode'}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
              </>
            )}

            <DropdownMenuItem
              onClick={handleLogout}
              className="cursor-pointer text-destructive focus:text-destructive"
            >
              <LogOut />
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </SidebarMenuItem>

    </SidebarMenu>
    </>
  )
}

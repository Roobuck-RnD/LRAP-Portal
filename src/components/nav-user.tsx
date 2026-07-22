'use client'

import { useState } from 'react'
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

// 开发者模式密码只存在前端(软隐藏,非安全边界)。见 devModeState.ts 说明。
const DEV_MODE_PASSWORD = 'west20st'

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

  const [showDevDialog, setShowDevDialog] = useState(false)
  const [password, setPassword] = useState('')
  const [pwError, setPwError] = useState(false)

  const handleLogout = () => {
    // 登出即退出开发者模式,回到默认隐藏状态。
    setDevMode(false)

    sessionStorage.removeItem('isLoggedIn')
    sessionStorage.removeItem('token')

    localStorage.removeItem('isLoggedIn')
    localStorage.removeItem('token')

    navigate('/login', { replace: true })
  }

  const openDevDialog = () => {
    setPassword('')
    setPwError(false)
    setShowDevDialog(true)
  }

  const closeDevDialog = () => {
    setShowDevDialog(false)
    setPassword('')
    setPwError(false)
  }

  const submitDevPassword = () => {
    if (password === DEV_MODE_PASSWORD) {
      setDevMode(true)
      closeDevDialog()
    } else {
      setPwError(true)
    }
  }

  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <SidebarMenuButton
              size="lg"
              className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground"
            >
              <Avatar className="h-8 w-8 rounded-lg">
                <AvatarImage src={user.avatar} alt={user.name} />
                <AvatarFallback className="rounded-lg bg-primary/10 text-primary">
                  <UserCog className="size-4" />
                </AvatarFallback>
              </Avatar>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-semibold">{user.name}</span>
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
            {devMode ? (
              <DropdownMenuItem
                onClick={() => setDevMode(false)}
                className="cursor-pointer"
              >
                <Code2 />
                Exit Developer Mode
              </DropdownMenuItem>
            ) : (
              <DropdownMenuItem onClick={openDevDialog} className="cursor-pointer">
                <Code2 />
                Developer Mode
              </DropdownMenuItem>
            )}

            <DropdownMenuSeparator />

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

      {showDevDialog && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 backdrop-blur-sm">
          <div className="w-[90vw] max-w-sm overflow-hidden rounded-lg bg-card shadow-xl">
            <div className="border-b bg-muted px-6 py-4">
              <h3 className="text-lg font-semibold text-foreground">Developer Mode</h3>
              <p className="mt-1 text-sm text-muted-foreground">
                Enter the developer password to enable advanced details.
              </p>
            </div>

            <div className="space-y-2 p-6">
              <input
                type="password"
                autoFocus
                value={password}
                onChange={(e) => {
                  setPassword(e.target.value)
                  if (pwError) setPwError(false)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') submitDevPassword()
                }}
                placeholder="Password"
                className="w-full rounded-md border px-3 py-2 outline-none focus:ring-2 focus:ring-ring"
              />
              {pwError && (
                <p className="text-xs text-destructive">Incorrect password.</p>
              )}
            </div>

            <div className="flex justify-end gap-3 border-t bg-muted px-6 py-4">
              <button
                onClick={closeDevDialog}
                className="rounded border px-4 py-2 text-sm text-foreground hover:bg-muted"
              >
                Cancel
              </button>
              <button
                onClick={submitDevPassword}
                className="rounded bg-primary px-4 py-2 text-sm font-medium text-white hover:bg-primary/90"
              >
                Enable
              </button>
            </div>
          </div>
        </div>
      )}
    </SidebarMenu>
  )
}

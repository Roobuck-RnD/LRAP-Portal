import { useState } from 'react'
import { ArrowRight, Eye, EyeOff, LoaderCircle, LockKeyhole, ShieldCheck, UserRound } from 'lucide-react'
import { useNavigate } from 'react-router'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'
import { authenticateWithOpenWrt } from '@/utils/auth'
import { clearPageDataCache } from '@/utils/page-data-cache'

export function LoginForm({ className, ...props }: React.ComponentPropsWithoutRef<'div'>) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const navigate = useNavigate()

  const handleLogin = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (loading) return

    setError('')
    setLoading(true)

    try {
      const token = await authenticateWithOpenWrt(username, password)

      if (!token) {
        setError('Incorrect username or password')
        return
      }

      clearPageDataCache()
      sessionStorage.setItem('isLoggedIn', 'true')
      sessionStorage.setItem('token', token)
      sessionStorage.setItem('username', username)
      navigate('/')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className={cn('relative', className)} {...props}>
      <div aria-hidden className="absolute -inset-px rounded-2xl bg-gradient-to-b from-primary/35 via-border/20 to-signal/15 blur-[1px]" />
      <Card className="relative gap-0 overflow-hidden rounded-2xl border-white/[0.08] bg-card/80 py-0 shadow-[0_28px_90px_rgba(0,0,0,0.45)] backdrop-blur-2xl">
        <div aria-hidden className="absolute inset-x-10 top-0 h-px bg-gradient-to-r from-transparent via-signal/80 to-transparent" />

        <CardHeader className="px-6 pt-7 pb-5 sm:px-8 sm:pt-8">
          <div className="mb-5 flex items-center justify-between">
            <div className="flex size-10 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary">
              <ShieldCheck className="size-5" />
            </div>
            <div className="data flex items-center gap-1.5 text-[9px] tracking-[0.14em] text-success uppercase">
              <span className="status-dot status-dot--live text-success size-1.5" />
              Ready
            </div>
          </div>
          <CardTitle className="text-2xl">Welcome back</CardTitle>
          <CardDescription className="mt-1.5 leading-5">
            Authenticate to enter your secure network console.
          </CardDescription>
        </CardHeader>

        <CardContent className="px-6 pb-7 sm:px-8 sm:pb-8">
          <form onSubmit={handleLogin} className="space-y-5">
            <div className="space-y-2">
              <Label htmlFor="username" className="text-xs tracking-wide text-muted-foreground uppercase">
                Username
              </Label>
              <div className="group relative">
                <UserRound className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground transition-colors group-focus-within:text-primary" />
                <Input
                  id="username"
                  type="text"
                  autoComplete="username"
                  value={username}
                  onChange={(event) => {
                    setUsername(event.target.value)
                    if (error) setError('')
                  }}
                  required
                  disabled={loading}
                  placeholder="Administrator account"
                  className="h-11 border-border/90 bg-background/55 pl-10 focus-visible:bg-background/80"
                />
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="password" className="text-xs tracking-wide text-muted-foreground uppercase">
                Password
              </Label>
              <div className="group relative">
                <LockKeyhole className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground transition-colors group-focus-within:text-primary" />
                <Input
                  id="password"
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => {
                    setPassword(event.target.value)
                    if (error) setError('')
                  }}
                  required
                  disabled={loading}
                  placeholder="Device password"
                  aria-invalid={Boolean(error)}
                  className="h-11 border-border/90 bg-background/55 px-10 focus-visible:bg-background/80"
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => setShowPassword((visible) => !visible)}
                  disabled={loading}
                  className="absolute inset-y-0 right-0 h-11 text-muted-foreground"
                  aria-label={showPassword ? 'Hide password' : 'Show password'}
                  title={showPassword ? 'Hide password' : 'Show password'}
                >
                  {showPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </Button>
              </div>
            </div>

            <div aria-live="polite" className="min-h-5">
              {error ? (
                <div className="rounded-md border border-destructive/25 bg-destructive/10 px-3 py-2 text-xs font-medium text-destructive">
                  {error}
                </div>
              ) : null}
            </div>

            <Button type="submit" size="lg" className="group w-full" disabled={loading}>
              {loading ? (
                <>
                  <LoaderCircle className="size-4 animate-spin" />
                  Authenticating
                </>
              ) : (
                <>
                  Enter Network Console
                  <ArrowRight className="ml-auto size-4 transition-transform group-hover:translate-x-0.5" />
                </>
              )}
            </Button>

            <div className="flex items-center justify-center gap-2 text-[10px] tracking-[0.08em] text-muted-foreground uppercase">
              <LockKeyhole className="size-3 text-success" />
              Credentials stay on your local network
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}

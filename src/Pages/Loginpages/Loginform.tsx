import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { authenticateWithOpenWrt } from '@/utils/auth'

export function LoginForm({ className, ...props }: React.ComponentPropsWithoutRef<'div'>) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('') 
  const navigate = useNavigate()

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault()

    const token = await authenticateWithOpenWrt(username, password)

    if (token) {
      sessionStorage.setItem('isLoggedIn', 'true')
      sessionStorage.setItem('token', token)
      sessionStorage.setItem('username', username) 
      navigate('/')
    } else {
      setError('Incorrect username or password')
    }
  }
  return (
    <div className={cn('flex flex-col gap-6', className)} {...props}>
      <Card>
        <CardHeader>
          <CardTitle className="text-2xl">Login</CardTitle>
          <CardDescription>Enter your username and password below to login to your account</CardDescription>
        </CardHeader>
        <CardContent>
          <form>
            <div className="flex flex-col gap-6">
              <div className="grid gap-2">
                <Label htmlFor="username">Username</Label>
                <Input
                  id="username"
                  type="username"
                  placeholder="input username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  required
                />
              </div>
              <div className="grid gap-2">
                <div className="flex items-center">
                  <Label htmlFor="password">Password</Label>
                </div>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </div>
              {error && (
                <div className="text-sm text-red-500 font-medium -mt-2">
                  {error}
                </div>
              )}
              <Button type="submit" onClick={handleLogin} className="w-full">
                Login
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}

import type { JSX } from 'react'
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { apiFetch } from '@/utils/http'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { PageHeader, PageShell } from '@/components/page'

// Changes the password of the account that is signed in. The same password
// signs in over SSH, so both change; every session of the account ends.
export default function Administration(): JSX.Element {
  const navigate = useNavigate()
  const username = sessionStorage.getItem('username') || ''

  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)

    if (!newPassword || !confirmPassword) {
      setError('Enter the new password twice.')
      return
    }
    if (newPassword !== confirmPassword) {
      setError('New passwords do not match.')
      return
    }

    setLoading(true)
    try {
      const res = await apiFetch('/api/system/password', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ new_password: newPassword }),
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || `HTTP ${res.status}`)
      }

      toast.success('Password changed. Sign in again with the new one.')
      sessionStorage.removeItem('isLoggedIn')
      sessionStorage.removeItem('token')
      sessionStorage.removeItem('username')
      sessionStorage.removeItem('role')
      navigate('/login')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(msg.charAt(0).toUpperCase() + msg.slice(1))
    } finally {
      setLoading(false)
    }
  }

  return (
    <PageShell size="narrow">
      <PageHeader title="Change Password" description="Update the password you sign in with." />

      <Card>
        <CardHeader>
          <CardTitle>Security Settings</CardTitle>
          <CardDescription>
            Changes the password of <span className="font-medium text-foreground">{username || 'this account'}</span>.
            The same password signs in here and over SSH, and every signed-in session ends.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="grid gap-2">
              <Label htmlFor="new-password">New Password</Label>
              <Input
                id="new-password"
                type="password"
                autoComplete="new-password"
                placeholder="New password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                disabled={loading}
              />
            </div>

            <div className="grid gap-2">
              <Label htmlFor="confirm-password">Confirm New Password</Label>
              <Input
                id="confirm-password"
                type="password"
                autoComplete="new-password"
                placeholder="Retype new password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                disabled={loading}
                className={
                  confirmPassword && newPassword !== confirmPassword
                    ? 'border-destructive focus-visible:ring-destructive'
                    : ''
                }
              />
              {confirmPassword && newPassword !== confirmPassword && (
                <span className="text-xs text-destructive">Passwords do not match</span>
              )}
            </div>

            {error && (
              <div className="p-3 rounded bg-destructive/10 text-destructive text-sm">
                {error}
              </div>
            )}

            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? 'Changing Password...' : 'Update Password'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </PageShell>
  )
}

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

export default function Administration(): JSX.Element {
  const navigate = useNavigate()
  
  // Form States (🔴 已删除 oldPassword)
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  
  // UI States
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)

    // 1. 前端验证
    if (!newPassword || !confirmPassword) {
      setError('Please enter and confirm your new password.')
      return
    }

    if (newPassword !== confirmPassword) {
      setError('New passwords do not match.')
      return
    }

    if (newPassword.length < 5) {
      setError('Password is too short (min 5 chars).')
      return
    }

    setLoading(true)

    // 2. 获取当前用户名
    const username = sessionStorage.getItem('username') || 'root'

    try {
      // 3. 发送请求
      const res = await apiFetch('/api/system/password', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          username: username,
          // old_password 字段已移除
          new_password: newPassword
        })
      })

      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || `HTTP ${res.status}`)
      }

      // 4. 修改成功
      toast.success('Password changed successfully! Please log in again.')
      
      // 清除 Session 并跳转登录页
      sessionStorage.removeItem('isLoggedIn')
      sessionStorage.removeItem('token')
      sessionStorage.removeItem('username')
      navigate('/login')

    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      setError(msg)
    } finally {
      setLoading(false)
    }
  }

  return (
    <PageShell size="narrow">
      <PageHeader title="Change Password" description="Update your system access password." />

      <Card>
        <CardHeader>
          <CardTitle>Security Settings</CardTitle>
          <CardDescription>
            Enter your new password twice to confirm.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="space-y-4">
            
            {/* 🔴 已删除 Old Password 输入框 */}

            {/* New Password */}
            <div className="grid gap-2">
              <Label htmlFor="new-password">New Password</Label>
              <Input
                id="new-password"
                type="password"
                placeholder="New password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                disabled={loading}
              />
            </div>

            {/* Confirm New Password */}
            <div className="grid gap-2">
              <Label htmlFor="confirm-password">Confirm New Password</Label>
              <Input
                id="confirm-password"
                type="password"
                placeholder="Retype new password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                disabled={loading}
                className={
                    confirmPassword && newPassword !== confirmPassword 
                    ? "border-destructive focus-visible:ring-destructive" 
                    : ""
                }
              />
              {confirmPassword && newPassword !== confirmPassword && (
                  <span className="text-xs text-destructive">Passwords do not match</span>
              )}
            </div>

            {/* Error Message */}
            {error && (
              <div className="p-3 rounded bg-destructive/10 text-destructive text-sm">
                {error}
              </div>
            )}

            {/* Submit Button */}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? 'Changing Password...' : 'Update Password'}
            </Button>
          </form>
        </CardContent>
      </Card>
    </PageShell>
  )
}

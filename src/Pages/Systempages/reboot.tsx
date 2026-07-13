import type { JSX } from 'react'
import { useState, useEffect, useRef, useCallback } from 'react'
import { toast } from 'sonner'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Power, Server, AlertTriangle, X } from 'lucide-react'

// ---------- Main Page Component ----------
export default function Reboot(): JSX.Element {
  const [countdown, setCountdown] = useState<number | null>(null)
  const [isSending, setIsSending] = useState(false)

  const SAFETY_DELAY = 60

  const handleTriggerReboot = async () => {
    const confirmMsg =
      `You are about to reboot the router.\n\n` +
      `A ${SAFETY_DELAY}s safety countdown will start before it restarts.`

    if (
      !(await confirmDialog({
        title: 'Reboot device?',
        description: confirmMsg,
        destructive: true,
        confirmText: 'Reboot'
      }))
    )
      return

    setCountdown(SAFETY_DELAY)
  }

  const handleCancel = useCallback(() => {
    setCountdown(null)
    setIsSending(false)
  }, [])

  const executeRebootAll = useCallback(async () => {
    setIsSending(true)

    try {
      const res = await apiFetch('/api/system/reboot', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        }
      })

      if (!res.ok) {
        const text = await res.text().catch(() => '')
        throw new Error(text || `HTTP ${res.status}`)
      }

      toast.success('Reboot command sent. The device is restarting.')

      sessionStorage.clear()
      window.location.replace('/login')
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err)
      console.error(err)
      toast.error('Reboot Failed', { description: msg })
      handleCancel()
    }
  }, [handleCancel])

  // Countdown effect
  useEffect(() => {
    if (countdown === null || countdown <= 0) return

    const timer = window.setInterval(() => {
      setCountdown((prev) => {
        if (prev === null) return null
        if (prev <= 1) {
          window.clearInterval(timer)
          return 0
        }
        return prev - 1
      })
    }, 1000)

    return () => window.clearInterval(timer)
  }, [countdown])

  // Execute once when countdown reaches 0
  const hasExecutedRef = useRef(false)

  useEffect(() => {
    if (countdown === 0 && !isSending && !hasExecutedRef.current) {
      hasExecutedRef.current = true
      void executeRebootAll()
    }

    if (countdown !== 0) {
      hasExecutedRef.current = false
    }
  }, [countdown, isSending, executeRebootAll])

  const progress = countdown ? ((SAFETY_DELAY - countdown) / SAFETY_DELAY) * 100 : 0
  const circleRadius = 40
  const circumference = 2 * Math.PI * circleRadius

  return (
    <div className="relative w-full p-6">
      {/* Fullscreen countdown modal */}
      {countdown !== null && (
        <div className="fixed inset-0 z-50 flex flex-col items-center justify-center bg-black/80 backdrop-blur-sm transition-all animate-in fade-in duration-300">
          <div className="flex w-full max-w-md flex-col items-center rounded-xl bg-white p-8 text-center shadow-2xl">
            <div className="relative mb-6 flex items-center justify-center">
              <svg className="h-32 w-32 -rotate-90 transform">
                <circle
                  cx="64"
                  cy="64"
                  r={circleRadius}
                  stroke="currentColor"
                  strokeWidth="8"
                  fill="transparent"
                  className="text-gray-200"
                />
                <circle
                  cx="64"
                  cy="64"
                  r={circleRadius}
                  stroke="currentColor"
                  strokeWidth="8"
                  fill="transparent"
                  strokeDasharray={circumference}
                  strokeDashoffset={circumference - (progress / 100) * circumference}
                  className="text-red-600 transition-all duration-1000 ease-linear"
                />
              </svg>

              <div className="absolute text-3xl font-bold text-gray-800">
                {countdown > 0 ? countdown : <Power className="h-8 w-8 animate-pulse text-red-600" />}
              </div>
            </div>

            <h3 className="mb-2 text-2xl font-bold text-gray-900">Safety Delay Active</h3>

            <div className="mb-6 rounded-lg border border-amber-200 bg-amber-50 p-4 text-left">
              <div className="flex items-start gap-2 text-sm text-amber-800">
                <AlertTriangle className="h-5 w-5 shrink-0" />
                <p>Waiting {SAFETY_DELAY} seconds before the router restarts.</p>
              </div>
            </div>

            <p className="mb-6 text-gray-600">Rebooting in {countdown}s...</p>

            <div className="flex w-full gap-4">
              <Button
                variant="outline"
                size="lg"
                className="w-full border-gray-300"
                onClick={handleCancel}
                disabled={isSending}
              >
                <X className="mr-2 h-4 w-4" /> Cancel Reboot
              </Button>
            </div>

            {isSending && (
              <p className="mt-4 animate-pulse text-sm font-semibold text-red-600">
                Sending reboot command...
              </p>
            )}
          </div>
        </div>
      )}

      <div className="mx-auto max-w-3xl">
        <div className="mb-6">
          <h2 className="flex items-center gap-2 text-3xl font-bold text-gray-900">
            <Power className="h-8 w-8 text-red-600" />
            System Reboot
          </h2>
        </div>

        <Card className="border-l-4 border-l-red-500 shadow-sm">
          <CardHeader className="border-b bg-muted/40">
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <div className="mt-1 shrink-0">
                  <Server className="h-7 w-7 text-blue-600" />
                </div>

                <div>
                  <CardTitle className="text-xl font-bold">Reboot Full System</CardTitle>
                </div>
              </div>

              <div className="rounded bg-red-100 px-2 py-1 text-xs font-medium text-red-700">
                Critical
              </div>
            </div>
          </CardHeader>

          <CardContent className="p-5">
            <Button
              variant="destructive"
              size="lg"
              className="w-full shadow-sm"
              onClick={() => void handleTriggerReboot()}
              disabled={isSending}
            >
              <Power className="mr-2 h-4 w-4" />
              Reboot Now
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

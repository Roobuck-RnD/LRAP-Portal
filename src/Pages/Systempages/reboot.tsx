import type { JSX } from 'react'
import { useState, useEffect, useRef, useCallback } from 'react'
import { toast } from 'sonner'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Button } from '@/components/ui/button'
import { Power, AlertTriangle, X } from 'lucide-react'

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
          <div className="flex w-full max-w-md flex-col items-center rounded-xl bg-card p-8 text-center shadow-2xl">
            <div className="relative mb-6 flex items-center justify-center">
              <svg className="h-32 w-32 -rotate-90 transform">
                <circle
                  cx="64"
                  cy="64"
                  r={circleRadius}
                  stroke="currentColor"
                  strokeWidth="8"
                  fill="transparent"
                  className="text-muted-foreground/40"
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
                  className="text-destructive transition-all duration-1000 ease-linear"
                />
              </svg>

              <div className="absolute text-3xl font-bold text-foreground">
                {countdown > 0 ? countdown : <Power className="h-8 w-8 animate-pulse text-destructive" />}
              </div>
            </div>

            <h3 className="mb-2 text-2xl font-bold text-foreground">Safety Delay Active</h3>

            <div className="mb-6 rounded-lg border border-warning/30 bg-warning/10 p-4 text-left">
              <div className="flex items-start gap-2 text-sm text-warning">
                <AlertTriangle className="h-5 w-5 shrink-0" />
                <p>Waiting {SAFETY_DELAY} seconds before the router restarts.</p>
              </div>
            </div>

            <p className="mb-6 text-muted-foreground">Rebooting in {countdown}s...</p>

            <div className="flex w-full gap-4">
              <Button
                variant="outline"
                size="lg"
                className="w-full border-border"
                onClick={handleCancel}
                disabled={isSending}
              >
                <X className="mr-2 h-4 w-4" /> Cancel Reboot
              </Button>
            </div>

            {isSending && (
              <p className="mt-4 animate-pulse text-sm font-semibold text-destructive">
                Sending reboot command...
              </p>
            )}
          </div>
        </div>
      )}

      <div className="mx-auto max-w-3xl">
        <div className="mb-8">
          <h2 className="text-3xl font-bold text-foreground">System Reboot</h2>
          <p className="mt-1 text-muted-foreground">
            Restart the router without changing any settings.
          </p>
        </div>

        <div className="rounded-lg border border-border bg-card p-6">
          <div className="flex flex-col gap-6 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-start gap-4">
              <div className="flex size-11 shrink-0 items-center justify-center rounded-md border border-destructive/30 bg-destructive/10">
                <Power className="h-5 w-5 text-destructive" />
              </div>

              <div>
                <h3 className="font-display text-base font-semibold tracking-tight text-foreground">
                  Reboot the device
                </h3>
                <p className="mt-1 max-w-md text-sm text-muted-foreground">
                  The network drops for about a minute while the system restarts.
                  A {SAFETY_DELAY}-second countdown runs first — you can cancel it
                  at any point.
                </p>
              </div>
            </div>

            <Button
              variant="destructive"
              className="shrink-0"
              onClick={() => void handleTriggerReboot()}
              disabled={isSending}
            >
              <Power className="mr-2 h-4 w-4" />
              Reboot Now
            </Button>
          </div>
        </div>
      </div>
    </div>
  )
}

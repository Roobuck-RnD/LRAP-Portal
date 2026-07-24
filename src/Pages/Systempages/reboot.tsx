import type { JSX } from 'react'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'
import { AlertTriangle, Power } from 'lucide-react'
import { toast } from 'sonner'

import { PageHeader, PageShell, SectionCard } from '@/components/page'
import { FullScreenTaskOverlay } from '@/components/task-overlay'
import { Button } from '@/components/ui/button'
import { confirmDialog } from '@/components/ui/confirm'
import { apiFetch } from '@/utils/http'

type RebootPhase = 'idle' | 'submitting' | 'countdown'

interface RebootAcceptedResponse {
  estimated_seconds?: number
}

const DEFAULT_REBOOT_SECONDS = 90

export default function Reboot(): JSX.Element {
  const navigate = useNavigate()
  const [phase, setPhase] = useState<RebootPhase>('idle')
  const [deadline, setDeadline] = useState<number | null>(null)
  const [countdown, setCountdown] = useState(DEFAULT_REBOOT_SECONDS)
  const [countdownTotal, setCountdownTotal] = useState(DEFAULT_REBOOT_SECONDS)
  const hasRedirectedRef = useRef(false)

  const handleTriggerReboot = async () => {
    if (phase !== 'idle') return

    const confirmed = await confirmDialog({
      title: 'Reboot device?',
      description:
        'The router and all reachable antennas will restart immediately. The network will be unavailable during reboot, and this action cannot be cancelled once accepted.',
      destructive: true,
      confirmText: 'Reboot'
    })

    if (!confirmed) return

    setPhase('submitting')

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

      const result = (await res.json().catch(() => null)) as RebootAcceptedResponse | null
      const estimatedSeconds =
        typeof result?.estimated_seconds === 'number' && result.estimated_seconds > 0
          ? Math.ceil(result.estimated_seconds)
          : DEFAULT_REBOOT_SECONDS
      const nextDeadline = Date.now() + estimatedSeconds * 1000

      sessionStorage.setItem('rebootPending', 'true')
      sessionStorage.setItem('rebootDeadline', String(nextDeadline))
      sessionStorage.setItem('rebootDuration', String(estimatedSeconds))

      setCountdownTotal(estimatedSeconds)
      setCountdown(estimatedSeconds)
      setDeadline(nextDeadline)
      setPhase('countdown')
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      console.error(err)
      toast.error('Reboot Failed', { description: message })
      setPhase('idle')
    }
  }

  // Restore the non-cancellable screen if this route remounts before the
  // countdown finishes.
  useEffect(() => {
    if (sessionStorage.getItem('rebootPending') !== 'true') return

    const storedDeadline = Number(sessionStorage.getItem('rebootDeadline'))
    const storedDuration = Number(sessionStorage.getItem('rebootDuration'))
    if (!Number.isFinite(storedDeadline) || storedDeadline <= 0) return

    const remaining = Math.max(0, Math.ceil((storedDeadline - Date.now()) / 1000))
    const duration =
      Number.isFinite(storedDuration) && storedDuration > 0
        ? Math.ceil(storedDuration)
        : DEFAULT_REBOOT_SECONDS

    setCountdownTotal(duration)
    setCountdown(remaining)
    setDeadline(storedDeadline)
    setPhase('countdown')
  }, [])

  // Use an absolute deadline so background-tab timer throttling does not make
  // the reboot screen run late.
  useEffect(() => {
    if (phase !== 'countdown' || deadline === null) return

    const updateRemaining = () => {
      setCountdown(Math.max(0, Math.ceil((deadline - Date.now()) / 1000)))
    }

    updateRemaining()
    const timer = window.setInterval(updateRemaining, 250)

    return () => window.clearInterval(timer)
  }, [deadline, phase])

  useEffect(() => {
    if (phase !== 'countdown' || countdown > 0 || hasRedirectedRef.current) return

    hasRedirectedRef.current = true
    sessionStorage.removeItem('isLoggedIn')
    sessionStorage.removeItem('token')
    sessionStorage.removeItem('username')
    sessionStorage.removeItem('rebootPending')
    sessionStorage.removeItem('rebootDeadline')
    sessionStorage.removeItem('rebootDuration')

    // HashRouter performs this navigation entirely in the already-loaded SPA.
    // It does not request /login from the router while Wi-Fi may still be down.
    navigate('/login', { replace: true })
  }, [countdown, navigate, phase])

  const progress =
    phase === 'countdown'
      ? Math.min(100, Math.max(0, ((countdownTotal - countdown) / countdownTotal) * 100))
      : 0
  const circleRadius = 40
  const circumference = 2 * Math.PI * circleRadius

  return (
    <PageShell size="narrow" className="relative">
      {phase !== 'idle' && (
        <FullScreenTaskOverlay panelClassName="flex flex-col items-center">
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
              {phase === 'countdown' ? (
                countdown
              ) : (
                <Power className="h-8 w-8 animate-pulse text-destructive" />
              )}
            </div>
          </div>

          <h3 className="mb-2 text-2xl font-bold text-foreground">
            {phase === 'submitting' ? 'Preparing reboot…' : 'System rebooting…'}
          </h3>

          <div className="mb-6 rounded-lg border border-warning/30 bg-warning/10 p-4 text-left">
            <div className="flex items-start gap-2 text-sm text-warning">
              <AlertTriangle className="h-5 w-5 shrink-0" />
              <p>
                The network and Wi-Fi may disconnect while the router restarts. Do not power off
                the device.
              </p>
            </div>
          </div>

          <p className="text-muted-foreground">
            {phase === 'submitting'
              ? 'Sending reboot command…'
              : `Returning to login in ${countdown}s…`}
          </p>
        </FullScreenTaskOverlay>
      )}

      <PageHeader title="System Reboot" description="Restart the router without changing any settings." />

      <SectionCard>
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
                The network drops while the router and antennas restart. After confirmation,
                reboot begins immediately and cannot be cancelled.
              </p>
            </div>
          </div>

          <Button
            variant="destructive"
            className="shrink-0"
            onClick={() => void handleTriggerReboot()}
            disabled={phase !== 'idle'}
          >
            <Power className="mr-2 h-4 w-4" />
            Reboot Now
          </Button>
        </div>
      </SectionCard>
    </PageShell>
  )
}

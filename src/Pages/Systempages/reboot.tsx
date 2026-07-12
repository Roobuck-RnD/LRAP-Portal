import type { JSX } from 'react'
import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
import { toast } from 'sonner'
import { useCurrentAllModuleStore } from '@/states/allModuleState'
import { apiFetch } from '@/utils/http'
import { confirmDialog } from '@/components/ui/confirm'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Power, Server, AlertTriangle, X, Router } from 'lucide-react'

// ---------- Interfaces ----------
interface Module {
  name: string
  ipaddress: string
  type: string
  port?: string
}

type RebootResponse = {
  status: string
  message?: string
  targets?: string[]
  warnings?: string[]
}

// ---------- Main Page Component ----------
export default function Reboot(): JSX.Element {
  const { currentAllModule } = useCurrentAllModuleStore()

  const [countdown, setCountdown] = useState<number | null>(null)
  const [isSending, setIsSending] = useState(false)
  const [warnings, setWarnings] = useState<string[]>([])

  const SAFETY_DELAY = 60

  const acModule = useMemo<Module | undefined>(() => {
    return (
      currentAllModule.find((m) => m.type === 'Main Module') ||
      currentAllModule.find((m) => m.port === 'br-lan') ||
      currentAllModule[0]
    )
  }, [currentAllModule])

  const apModules = useMemo(() => {
    return currentAllModule.filter((m) => m.type !== 'Main Module')
  }, [currentAllModule])

  const handleTriggerReboot = async () => {
    const confirmMsg =
      `WARNING: You are about to reboot the full AC + AP system.\n\n` +
      `This will reboot all reachable AP modules first, then reboot the AC main module.\n\n` +
      `A ${SAFETY_DELAY}s safety countdown will start before executing.`

    if (!(await confirmDialog({ title: 'Reboot device?', description: confirmMsg, destructive: true, confirmText: 'Reboot' }))) return

    setWarnings([])
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

      const data = (await res.json().catch(() => null)) as RebootResponse | null

      if (data?.warnings && data.warnings.length > 0) {
        setWarnings(data.warnings)
        toast.success('Reboot command sent, but some AP modules may not have received the command. The AC will reboot now.')
      } else {
        toast.success('Reboot command sent. AP modules and AC are restarting.')
      }

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
                <p>
                  Waiting {SAFETY_DELAY} seconds before rebooting all modules. AP modules will be
                  rebooted first, then the AC will reboot.
                </p>
              </div>
            </div>

            <p className="mb-6 text-gray-600">
              Rebooting full AC + AP system in {countdown}s...
            </p>

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
          <p className="mt-1 text-gray-500">
            Reboot the full AC + AP system. A 60s safety delay is enforced.
          </p>
        </div>

        {warnings.length > 0 && (
          <div className="mb-6 rounded border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
            <div className="mb-1 font-semibold">Warnings</div>
            <ul className="list-disc space-y-1 pl-5">
              {warnings.map((warning, idx) => (
                <li key={`${warning}-${idx}`}>{warning}</li>
              ))}
            </ul>
          </div>
        )}

        <Card className="border-l-4 border-l-red-500 shadow-sm">
          <CardHeader className="border-b bg-muted/40">
            <div className="flex items-start justify-between gap-4">
              <div className="flex items-start gap-3">
                <div className="mt-1 shrink-0">
                  <Server className="h-7 w-7 text-blue-600" />
                </div>

                <div>
                  <CardTitle className="text-xl font-bold">Reboot Full System</CardTitle>
                  <CardDescription className="mt-1">
                    AC: <span className="font-mono">{acModule?.name || 'Main Module'}</span>
                    {' · '}
                    IP: <span className="font-mono">{acModule?.ipaddress || 'Local AC'}</span>
                  </CardDescription>
                </div>
              </div>

              <div className="rounded bg-red-100 px-2 py-1 text-xs font-medium text-red-700">
                Critical
              </div>
            </div>
          </CardHeader>

          <CardContent className="space-y-5 p-5">
            <div className="rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-800">
              <div className="flex items-start gap-2">
                <AlertTriangle className="h-5 w-5 shrink-0" />
                <div>
                  <div className="font-semibold">This action will temporarily disconnect management access.</div>
                  <p className="mt-1">
                    The backend will send reboot commands to reachable AP modules first, then reboot
                    the AC main module after a short delay.
                  </p>
                </div>
              </div>
            </div>

            <div>
              <div className="mb-2 text-sm font-medium text-gray-700">Detected modules</div>

              <div className="rounded-lg border bg-white">
                <div className="flex items-center justify-between border-b px-3 py-2 text-sm">
                  <div className="flex items-center gap-2">
                    <Server className="h-4 w-4 text-blue-600" />
                    <span className="font-medium">{acModule?.name || 'Main Module'}</span>
                  </div>
                  <span className="font-mono text-xs text-gray-500">
                    {acModule?.ipaddress || 'Local AC'}
                  </span>
                </div>

                {apModules.length === 0 ? (
                  <div className="px-3 py-3 text-sm text-gray-500">No AP modules detected.</div>
                ) : (
                  apModules.map((mod, idx) => (
                    <div
                      key={mod.ipaddress || `${mod.name}-${idx}`}
                      className="flex items-center justify-between border-b px-3 py-2 text-sm last:border-b-0"
                    >
                      <div className="flex items-center gap-2">
                        <Router className="h-4 w-4 text-gray-500" />
                        <span className="font-medium">{mod.name || `AP ${idx + 1}`}</span>
                      </div>

                      <span className="font-mono text-xs text-gray-500">
                        {mod.ipaddress || '—'}
                      </span>
                    </div>
                  ))
                )}
              </div>
            </div>

            <Button
              variant="destructive"
              size="lg"
              className="w-full shadow-sm"
              onClick={() => void handleTriggerReboot()}
              disabled={isSending}
            >
              <Power className="mr-2 h-4 w-4" />
              Reboot AC + AP System
            </Button>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
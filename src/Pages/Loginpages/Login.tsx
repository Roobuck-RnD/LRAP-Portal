import { LoginForm } from './Loginform'
import { SignalMark } from '@/components/signal-mark'

export default function Login() {
  return (
    <div className="bg-background relative flex min-h-svh flex-col items-center justify-center overflow-hidden p-6 md:p-10">
      {/* Ambient signal field — concentric arcs radiating from the top, the
          Roobuck signature at atmospheric scale. Kept very low-opacity so it
          reads as texture, not decoration. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-x-0 -top-40 flex justify-center"
      >
        <svg
          width="900"
          height="900"
          viewBox="0 0 900 900"
          fill="none"
          className="text-primary/[0.07]"
        >
          {[120, 220, 320, 420].map((r) => (
            <circle
              key={r}
              cx="450"
              cy="450"
              r={r}
              stroke="currentColor"
              strokeWidth="1.5"
            />
          ))}
        </svg>
      </div>
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 bg-[radial-gradient(60%_50%_at_50%_0%,color-mix(in_oklab,var(--primary)_14%,transparent),transparent)]"
      />

      <div className="relative z-10 flex w-full max-w-sm flex-col gap-8">
        <div className="flex flex-col items-center gap-3">
          <div className="ring-border bg-card flex size-12 items-center justify-center rounded-lg ring-1">
            <SignalMark className="size-7" id="login-signal" />
          </div>
          <div className="flex flex-col items-center gap-0.5">
            <span className="font-display text-lg font-semibold tracking-tight">Roobuck</span>
            <span className="text-muted-foreground text-[0.7rem] font-medium tracking-[0.16em] uppercase">
              Network Console
            </span>
          </div>
        </div>

        <LoginForm />
      </div>
    </div>
  )
}

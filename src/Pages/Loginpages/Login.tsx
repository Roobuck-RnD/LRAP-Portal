import { SignalMark } from '@/components/signal-mark'
import { LoginForm } from './Loginform'

function NetworkConstellation() {
  const nodes = [
    { cx: 86, cy: 84, delay: '0s' },
    { cx: 116, cy: 240, delay: '-0.8s' },
    { cx: 528, cy: 72, delay: '-1.6s' },
    { cx: 560, cy: 224, delay: '-2.4s' },
    { cx: 438, cy: 274, delay: '-3.2s' }
  ]

  return (
    <div className="login-topology relative h-[310px] w-full overflow-hidden rounded-2xl border border-white/[0.07] bg-white/[0.025]">
      <div className="absolute inset-0 bg-[radial-gradient(circle_at_50%_50%,color-mix(in_oklab,var(--primary)_13%,transparent),transparent_38%)]" />
      <svg viewBox="0 0 640 310" className="absolute inset-0 size-full" aria-hidden="true">
        <defs>
          <linearGradient id="login-route" x1="80" y1="40" x2="570" y2="270" gradientUnits="userSpaceOnUse">
            <stop stopColor="#3b82f6" stopOpacity="0.08" />
            <stop offset="0.5" stopColor="#22d3ee" stopOpacity="0.58" />
            <stop offset="1" stopColor="#3b82f6" stopOpacity="0.08" />
          </linearGradient>
          <radialGradient id="login-core">
            <stop stopColor="#22d3ee" />
            <stop offset="1" stopColor="#3b82f6" />
          </radialGradient>
        </defs>

        <circle cx="320" cy="155" r="55" fill="none" stroke="#3b82f6" strokeOpacity="0.18" />
        <circle cx="320" cy="155" r="91" fill="none" stroke="#22d3ee" strokeOpacity="0.09" />
        <circle cx="320" cy="155" r="130" fill="none" stroke="#3b82f6" strokeOpacity="0.05" />

        {nodes.map((node) => (
          <g key={`${node.cx}-${node.cy}`}>
            <path
              d={`M 320 155 Q ${(320 + node.cx) / 2} ${node.cy < 155 ? node.cy - 24 : node.cy + 24} ${node.cx} ${node.cy}`}
              fill="none"
              stroke="url(#login-route)"
              strokeWidth="1.2"
            />
            <path
              d={`M 320 155 Q ${(320 + node.cx) / 2} ${node.cy < 155 ? node.cy - 24 : node.cy + 24} ${node.cx} ${node.cy}`}
              pathLength="1"
              fill="none"
              stroke="#22d3ee"
              strokeWidth="2"
              strokeLinecap="round"
              className="login-route-flow"
              style={{ animationDelay: node.delay }}
            />
            <circle cx={node.cx} cy={node.cy} r="12" fill="#0d1727" stroke="#3b82f6" strokeOpacity="0.55" />
            <circle
              cx={node.cx}
              cy={node.cy}
              r="3.5"
              fill="#22d3ee"
              className="login-node-pulse"
              style={{ animationDelay: node.delay }}
            />
          </g>
        ))}

        <circle cx="320" cy="155" r="31" fill="#0d1727" stroke="#3b82f6" strokeOpacity="0.7" />
        <circle cx="320" cy="155" r="18" fill="url(#login-core)" opacity="0.18" />
        <circle cx="320" cy="155" r="5" fill="#22d3ee" />
      </svg>

      <div className="absolute top-4 left-4 flex items-center gap-2 rounded-full border border-signal/15 bg-background/65 px-3 py-1.5 text-[10px] font-medium tracking-[0.16em] text-signal uppercase backdrop-blur-md">
        <span className="status-dot status-dot--live text-signal size-1.5" />
        Network fabric
      </div>
      <div className="data absolute right-4 bottom-4 text-[10px] tracking-[0.12em] text-muted-foreground uppercase">
        Unified control plane
      </div>
    </div>
  )
}

export default function Login() {
  return (
    <main className="login-surface relative min-h-svh overflow-hidden bg-background">
      <div aria-hidden className="login-grid pointer-events-none absolute inset-0 opacity-55" />
      <div aria-hidden className="pointer-events-none absolute -top-48 -left-40 size-[34rem] rounded-full bg-primary/10 blur-[120px]" />
      <div aria-hidden className="pointer-events-none absolute -right-40 -bottom-52 size-[38rem] rounded-full bg-signal/[0.07] blur-[140px]" />
      <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-signal/70 to-transparent" />

      <div className="relative z-10 mx-auto flex min-h-svh w-full max-w-[1240px] flex-col px-5 py-6 sm:px-8 lg:px-12 lg:py-8">
        <header className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="relative flex size-11 items-center justify-center rounded-xl border border-primary/25 bg-card/80 shadow-[0_0_32px_color-mix(in_oklab,var(--primary)_18%,transparent)] backdrop-blur-xl">
              <SignalMark className="size-7" id="login-brand-signal" />
              <span className="absolute -right-0.5 -bottom-0.5 size-2.5 rounded-full border-2 border-background bg-success" />
            </div>
            <div>
              <div className="font-display text-lg font-semibold tracking-tight">Roobuck</div>
              <div className="text-[9px] font-medium tracking-[0.22em] text-muted-foreground uppercase">
                Network Intelligence
              </div>
            </div>
          </div>

        </header>

        <div className="grid flex-1 items-center gap-10 py-10 lg:grid-cols-[1.15fr_0.85fr] lg:gap-20 lg:py-12">
          <section className="flex flex-col justify-center">
            <h1 className="max-w-2xl font-display text-4xl leading-[1.04] font-semibold tracking-[-0.04em] text-foreground sm:text-5xl xl:text-[3.7rem]">
              Command your network.
              <span className="mt-1 block bg-gradient-to-r from-primary via-signal to-primary bg-clip-text text-transparent">
                Without the complexity.
              </span>
            </h1>
            <p className="mt-5 max-w-xl text-sm leading-6 text-muted-foreground sm:text-base sm:leading-7">
              One intelligent console for performance, security, wireless coverage and every connected device.
            </p>

            <div className="mt-7 hidden lg:block">
              <NetworkConstellation />
            </div>
          </section>

          <section className="mx-auto w-full max-w-md lg:mx-0 lg:justify-self-end">
            <LoginForm />
          </section>
        </div>

        <footer className="flex items-center justify-between border-t border-white/[0.05] pt-4 text-[10px] tracking-[0.08em] text-muted-foreground uppercase">
          <span>Roobuck Management Portal</span>
        </footer>
      </div>
    </main>
  )
}

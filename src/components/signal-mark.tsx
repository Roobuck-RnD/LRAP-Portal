import * as React from 'react'
import { cn } from '@/lib/utils'

/**
 * SignalMark — the Roobuck signature.
 *
 * Concentric signal arcs radiating from a single node: the visual language of
 * an antenna transmitting. It echoes the wifi curve in the favicon and ties the
 * whole console back to what Roobuck actually makes. Used sparingly — brand
 * lockup, login, empty states — so it stays memorable rather than decorative.
 *
 * Strokes use an electric-blue -> signal-cyan gradient; the node is solid.
 * `id` must be unique per rendered instance (gradient defs are id-scoped);
 * defaults are fine when only one is on screen at a time.
 */
export function SignalMark({
  className,
  id = 'roobuck-signal',
  ...props
}: React.ComponentProps<'svg'> & { id?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      className={cn('size-6', className)}
      {...props}
    >
      <defs>
        <linearGradient id={id} x1="4" y1="20" x2="20" y2="4" gradientUnits="userSpaceOnUse">
          <stop stopColor="#3b82f6" />
          <stop offset="1" stopColor="#22d3ee" />
        </linearGradient>
      </defs>
      <g
        stroke={`url(#${id})`}
        strokeWidth="1.75"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        {/* three arcs, widening as they radiate from the node */}
        <path d="M9.4 14.6a3.7 3.7 0 0 1 5.2 0" opacity="0.95" />
        <path d="M6.6 11.8a7.7 7.7 0 0 1 10.8 0" opacity="0.7" />
        <path d="M3.8 9a11.7 11.7 0 0 1 16.4 0" opacity="0.45" />
      </g>
      {/* the transmitting node */}
      <circle cx="12" cy="18" r="1.9" fill="#22d3ee" />
    </svg>
  )
}

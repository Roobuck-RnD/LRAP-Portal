import * as React from 'react'
import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'

import { cn } from '@/lib/utils'

const buttonVariants = cva(
  "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-md text-sm font-medium transition-[color,background-color,border-color,box-shadow,transform] duration-150 hover:shadow-sm active:scale-[0.98] disabled:pointer-events-none disabled:shadow-none disabled:active:scale-100 [&_svg]:pointer-events-none [&_svg:not([class*='size-'])]:size-4 shrink-0 [&_svg]:shrink-0 outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] aria-invalid:ring-destructive/20 dark:aria-invalid:ring-destructive/40 aria-invalid:border-destructive",
  {
    variants: {
      variant: {
        // Solid buttons go flat-muted when disabled — a washed-out 50%-opacity
        // primary/destructive reads as murky on the dark surfaces.
        default:
          'bg-primary text-primary-foreground shadow-xs hover:bg-primary/88 hover:shadow-primary/25 dark:hover:bg-primary/72 disabled:bg-muted disabled:text-muted-foreground',
        destructive:
          'bg-destructive text-white shadow-xs hover:bg-destructive/86 hover:shadow-destructive/25 dark:hover:bg-destructive/68 focus-visible:ring-destructive/30 disabled:bg-muted disabled:text-muted-foreground',
        destructiveOutline:
          'border border-destructive/45 bg-destructive/5 text-destructive shadow-xs hover:border-destructive hover:bg-destructive/25 hover:text-destructive focus-visible:ring-destructive/25 disabled:opacity-50',
        success:
          'bg-success text-white shadow-xs hover:bg-success/86 hover:shadow-success/25 dark:hover:bg-success/68 focus-visible:ring-success/30 disabled:bg-muted disabled:text-muted-foreground',
        warning:
          'bg-warning text-background shadow-xs hover:bg-warning/88 hover:shadow-warning/25 dark:hover:bg-warning/68 focus-visible:ring-warning/30 disabled:bg-muted disabled:text-muted-foreground',
        outline:
          'border border-input bg-background shadow-xs hover:border-primary/70 hover:bg-primary/20 hover:text-primary disabled:opacity-50',
        secondary:
          'bg-secondary text-secondary-foreground shadow-xs hover:bg-primary/20 hover:text-primary disabled:opacity-50',
        ghost: 'hover:bg-primary/20 hover:text-primary disabled:opacity-50',
        link: 'text-primary underline-offset-4 hover:underline disabled:opacity-50'
      },
      size: {
        default: 'h-9 px-4 py-2 has-[>svg]:px-3',
        sm: 'h-8 rounded-md gap-1.5 px-3 has-[>svg]:px-2.5',
        lg: 'h-10 rounded-md px-6 has-[>svg]:px-4',
        icon: 'size-9'
      }
    },
    defaultVariants: {
      variant: 'default',
      size: 'default'
    }
  }
)

function Button({
  className,
  variant,
  size,
  asChild = false,
  ...props
}: React.ComponentProps<'button'> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }) {
  const Comp = asChild ? Slot : 'button'

  return (
    <Comp
      data-slot="button"
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Button, buttonVariants }

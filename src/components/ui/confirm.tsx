import { create } from 'zustand'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle
} from '@/components/ui/alert-dialog'

// Promise-based confirmation dialog backed by shadcn AlertDialog. Replaces the
// native, synchronous window.confirm so confirmations match the rest of the UI.
// Usage: `if (!(await confirmDialog({ title, description }))) return`

export type ConfirmOptions = {
  title?: string
  description?: string
  confirmText?: string
  cancelText?: string
  destructive?: boolean
}

type ConfirmState = {
  open: boolean
  options: ConfirmOptions
  resolve: ((value: boolean) => void) | null
  request: (options: ConfirmOptions) => Promise<boolean>
  settle: (value: boolean) => void
}

const useConfirmStore = create<ConfirmState>((set, get) => ({
  open: false,
  options: {},
  resolve: null,
  request: (options) =>
    new Promise<boolean>((resolve) => {
      // If a previous prompt is somehow still pending, decline it first.
      get().resolve?.(false)
      set({ open: true, options, resolve })
    }),
  settle: (value) => {
    get().resolve?.(value)
    set({ open: false, resolve: null })
  }
}))

// Imperative entry point — call from anywhere and await the user's choice.
export function confirmDialog(options: ConfirmOptions): Promise<boolean> {
  return useConfirmStore.getState().request(options)
}

// Mounted once (next to <Toaster/>) so confirmDialog() has somewhere to render.
export function ConfirmHost() {
  const { open, options, settle } = useConfirmStore()

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) settle(false)
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{options.title ?? 'Are you sure?'}</AlertDialogTitle>
          {options.description ? (
            <AlertDialogDescription>{options.description}</AlertDialogDescription>
          ) : null}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => settle(false)}>
            {options.cancelText ?? 'Cancel'}
          </AlertDialogCancel>
          <AlertDialogAction
            onClick={() => settle(true)}
            className={
              options.destructive
                ? 'bg-destructive text-destructive-foreground hover:bg-destructive/90'
                : undefined
            }
          >
            {options.confirmText ?? 'Confirm'}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

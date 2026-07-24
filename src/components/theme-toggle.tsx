import { Moon, Sun } from 'lucide-react'
import { useTheme } from 'next-themes'

import { Button } from '@/components/ui/button'

function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme()
  const isDark =
    resolvedTheme === 'dark' ||
    (!resolvedTheme && document.documentElement.classList.contains('dark'))
  const nextTheme = isDark ? 'light' : 'dark'

  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="h-9 rounded-full border-border/80 bg-card/75 px-3 shadow-xs backdrop-blur-sm hover:bg-primary/12"
      onClick={() => setTheme(nextTheme)}
      aria-label={`Switch to ${nextTheme} mode`}
      title={`Switch to ${nextTheme} mode`}
    >
      {isDark ? (
        <Moon className="size-4 text-signal transition-transform duration-200" />
      ) : (
        <Sun className="size-4 text-warning transition-transform duration-200" />
      )}
      <span className="hidden text-xs font-medium sm:inline">{isDark ? 'Dark' : 'Light'}</span>
    </Button>
  )
}

export { ThemeToggle }

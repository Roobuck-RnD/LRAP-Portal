import { useEffect, type ReactNode } from 'react'
import { ThemeProvider as NextThemeProvider, useTheme } from 'next-themes'
import { useLocation } from 'react-router'

function ThemeMetadata() {
  const { resolvedTheme } = useTheme()

  useEffect(() => {
    const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
    if (meta) {
      meta.content = resolvedTheme === 'light' ? '#f4f7fb' : '#0a0f1a'
    }
  }, [resolvedTheme])

  return null
}

function RoutedThemeProvider({ children }: { children: ReactNode }) {
  const location = useLocation()
  const isLogin = location.pathname.toLowerCase() === '/login'

  return (
    <NextThemeProvider
      attribute="class"
      defaultTheme="dark"
      enableSystem={false}
      forcedTheme={isLogin ? 'dark' : undefined}
      storageKey="roobuck-theme"
    >
      <ThemeMetadata />
      {children}
    </NextThemeProvider>
  )
}

export { RoutedThemeProvider }

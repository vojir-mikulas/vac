import { createContext, use, useCallback, useEffect, useState } from 'react'

export type Theme = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const STORAGE_KEY = 'vac.theme'

interface ThemeContextValue {
  /** The user's chosen preference (may be `system`). */
  theme: Theme
  /** The theme actually applied to the document (`system` resolved to light/dark). */
  resolvedTheme: ResolvedTheme
  toggleTheme: () => void
  setTheme: (theme: Theme) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

function prefersDark(): boolean {
  return typeof window !== 'undefined' && window.matchMedia('(prefers-color-scheme: dark)').matches
}

function readStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (stored === 'light' || stored === 'dark' || stored === 'system') return stored
  } catch {
    // ignore storage failures (private mode)
  }
  // New installs default to following the OS.
  return 'system'
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme)
  const [systemDark, setSystemDark] = useState<boolean>(prefersDark)

  // resolvedTheme is derived — it tracks both the chosen theme and, while on
  // `system`, the live OS preference held in systemDark.
  const resolvedTheme: ResolvedTheme = theme === 'system' ? (systemDark ? 'dark' : 'light') : theme

  // Apply to the document (external system) and persist the preference.
  useEffect(() => {
    document.documentElement.classList.toggle('dark', resolvedTheme === 'dark')
    try {
      localStorage.setItem(STORAGE_KEY, theme)
    } catch {
      // ignore storage failures (private mode, quota)
    }
  }, [theme, resolvedTheme])

  // Subscribe to OS preference changes; only affects the UI while on `system`.
  useEffect(() => {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const onChange = () => setSystemDark(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  const setTheme = useCallback((next: Theme) => setThemeState(next), [])
  // Quick toggle cycles light → dark → system.
  const toggleTheme = useCallback(
    () => setThemeState((t) => (t === 'light' ? 'dark' : t === 'dark' ? 'system' : 'light')),
    [],
  )

  return (
    <ThemeContext value={{ theme, resolvedTheme, toggleTheme, setTheme }}>{children}</ThemeContext>
  )
}

export function useTheme(): ThemeContextValue {
  const ctx = use(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within a ThemeProvider')
  return ctx
}

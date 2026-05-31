import { Monitor, Moon, Sun } from 'lucide-react'

import { cn } from '@/lib/utils'
import { useTheme, type Theme } from '@/components/theme/theme-provider'

const OPTIONS: { value: Theme; label: string; icon: typeof Sun }[] = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'dark', label: 'Dark', icon: Moon },
  { value: 'system', label: 'System', icon: Monitor },
]

export function ThemeToggle() {
  const { theme, setTheme } = useTheme()
  return (
    <div
      role="radiogroup"
      aria-label="Theme"
      className="inline-flex items-center gap-0.5 rounded-md border bg-surface-1 p-0.5"
    >
      {OPTIONS.map((opt) => {
        const active = theme === opt.value
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={opt.label}
            title={opt.label}
            onClick={() => setTheme(opt.value)}
            className={cn(
              'flex size-7 cursor-pointer items-center justify-center rounded transition-colors',
              active
                ? 'bg-surface-2 text-foreground'
                : 'text-muted-foreground hover:text-foreground',
            )}
          >
            <opt.icon className="size-3.5" />
          </button>
        )
      })}
    </div>
  )
}

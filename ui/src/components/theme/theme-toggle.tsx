import { Monitor, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'
import { useTheme, type Theme } from '@/components/theme/theme-provider'

// `key` indexes into the `theme.*` i18n catalog; resolved at render.
const OPTIONS: { value: Theme; key: 'light' | 'dark' | 'system'; icon: typeof Sun }[] = [
  { value: 'light', key: 'light', icon: Sun },
  { value: 'dark', key: 'dark', icon: Moon },
  { value: 'system', key: 'system', icon: Monitor },
]

export function ThemeToggle() {
  const { t } = useTranslation()
  const { theme, setTheme } = useTheme()
  return (
    <div
      role="radiogroup"
      aria-label={t('theme.theme')}
      className="inline-flex items-center gap-0.5 rounded-md border bg-surface-1 p-0.5"
    >
      {OPTIONS.map((opt) => {
        const active = theme === opt.value
        const label = t(`theme.${opt.key}`)
        return (
          <button
            key={opt.value}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={label}
            title={label}
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

import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { Activity, Boxes, Database, Plus, Rocket, Settings } from 'lucide-react'

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import { useApps } from '@/lib/api/apps'

// `labelKey` indexes into the i18n catalog; resolved at render.
const PAGES = [
  { to: '/apps', labelKey: 'nav.apps', icon: Boxes },
  { to: '/apps/new', labelKey: 'command.newApp', icon: Plus },
  { to: '/deployments', labelKey: 'nav.deployments', icon: Rocket },
  { to: '/activity', labelKey: 'nav.activity', icon: Activity },
  { to: '/database', labelKey: 'nav.database', icon: Database },
  { to: '/settings', labelKey: 'nav.settings', icon: Settings },
] as const

export function CommandMenu({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { data: apps } = useApps()

  const go = (to: string, params?: Record<string, string>) => {
    onOpenChange(false)
    navigate({ to, params })
  }

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder={t('command.placeholder')} />
      <CommandList>
        <CommandEmpty>{t('command.empty')}</CommandEmpty>
        <CommandGroup heading={t('command.navigate')}>
          {PAGES.map((p) => {
            const label = t(p.labelKey)
            return (
              <CommandItem key={p.to} value={label} onSelect={() => go(p.to)}>
                <p.icon />
                {label}
              </CommandItem>
            )
          })}
        </CommandGroup>
        {apps && apps.length > 0 ? (
          <CommandGroup heading={t('command.apps')}>
            {apps.map((app) => (
              <CommandItem
                key={app.id}
                value={`app ${app.name} ${app.slug}`}
                onSelect={() => go('/apps/$appId', { appId: app.id })}
              >
                <Boxes />
                {app.name}
              </CommandItem>
            ))}
          </CommandGroup>
        ) : null}
      </CommandList>
    </CommandDialog>
  )
}

// ⌘K / Ctrl-K global shortcut, returns the open state + setter.
export function useCommandMenu() {
  const [open, setOpen] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'k' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setOpen((o) => !o)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])
  return [open, setOpen] as const
}

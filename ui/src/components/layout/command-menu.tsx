import { useEffect, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Boxes, Database, Plus, Rocket, ScrollText, Settings } from 'lucide-react'

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import { useApps } from '@/lib/api/apps'

const PAGES = [
  { to: '/apps', label: 'Apps', icon: Boxes },
  { to: '/apps/new', label: 'New App', icon: Plus },
  { to: '/deployments', label: 'Deployments', icon: Rocket },
  { to: '/database', label: 'Database', icon: Database },
  { to: '/logs', label: 'Logs', icon: ScrollText },
  { to: '/settings', label: 'Settings', icon: Settings },
] as const

export function CommandMenu({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const navigate = useNavigate()
  const { data: apps } = useApps()

  const go = (to: string, params?: Record<string, string>) => {
    onOpenChange(false)
    navigate({ to, params })
  }

  return (
    <CommandDialog open={open} onOpenChange={onOpenChange}>
      <CommandInput placeholder="Search apps, deploys, settings…" />
      <CommandList>
        <CommandEmpty>No results found.</CommandEmpty>
        <CommandGroup heading="Navigate">
          {PAGES.map((p) => (
            <CommandItem key={p.to} value={p.label} onSelect={() => go(p.to)}>
              <p.icon />
              {p.label}
            </CommandItem>
          ))}
        </CommandGroup>
        {apps && apps.length > 0 ? (
          <CommandGroup heading="Apps">
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

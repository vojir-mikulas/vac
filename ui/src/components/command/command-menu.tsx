import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'

import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from '@/components/ui/command'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { GROUP_ORDER, type Command, type CommandGroupId } from './types'
import { useCommands } from './use-commands'

export function CommandMenu({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  // A destructive command waiting on confirmation; the palette closes first so
  // the two dialogs never stack.
  const [pending, setPending] = useState<Command | null>(null)

  const go: (to: string, params?: Record<string, string>) => void = (to, params) => {
    onOpenChange(false)
    navigate({ to, params })
  }
  const { commands, appName } = useCommands(go)

  const run = (cmd: Command) => {
    if (cmd.confirm) {
      onOpenChange(false)
      setPending(cmd)
      return
    }
    cmd.perform()
  }

  const headingFor = (group: CommandGroupId): string => {
    switch (group) {
      case 'app':
        return t('command.app', { name: appName ?? '' })
      case 'navigate':
        return t('command.navigate')
      case 'settings':
        return t('nav.settings')
      case 'actions':
        return t('command.actions')
      case 'apps':
        return t('command.apps')
    }
  }

  const groups = GROUP_ORDER.map((group) => ({
    group,
    items: commands.filter((c) => c.group === group),
  })).filter((g) => g.items.length > 0)

  return (
    <>
      <CommandDialog open={open} onOpenChange={onOpenChange}>
        <CommandInput placeholder={t('command.placeholder')} />
        <CommandList>
          <CommandEmpty>{t('command.empty')}</CommandEmpty>
          {groups.map(({ group, items }) => (
            <CommandGroup key={group} heading={headingFor(group)}>
              {items.map((cmd) => (
                <CommandItem
                  key={cmd.id}
                  value={`${cmd.label} ${cmd.keywords ?? ''}`}
                  onSelect={() => run(cmd)}
                >
                  <cmd.icon />
                  {cmd.label}
                </CommandItem>
              ))}
            </CommandGroup>
          ))}
        </CommandList>
      </CommandDialog>

      <AlertDialog open={pending !== null} onOpenChange={(o) => !o && setPending(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{pending?.confirm?.title}</AlertDialogTitle>
            <AlertDialogDescription>{pending?.confirm?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('command.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                pending?.perform()
                setPending(null)
              }}
            >
              {pending?.confirm?.actionLabel}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
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

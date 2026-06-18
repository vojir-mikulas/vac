import { useTranslation } from 'react-i18next'
import { useParams } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import { toast } from 'sonner'

import { useApps, useStackControl } from '@/lib/api/apps'
import { useDatabases } from '@/lib/api/databases'
import { useTriggerDeploy } from '@/lib/api/deployments'
import { instanceApi, useInstanceInfo } from '@/lib/api/instance'
import { useNavItems } from '@/lib/navigation'
import { useTheme } from '@/components/theme/theme-provider'
import type { Command } from './types'
import {
  actionCommands,
  appContextCommands,
  appListCommands,
  navCommands,
  settingsCommands,
  type Navigate,
} from './commands'

export interface CommandSet {
  commands: Command[]
  /** Name of the app currently being viewed, for the "app" group heading. */
  appName?: string
}

// Assembles the full command list from the live app state. `go` is injected by
// the palette so navigation closes the dialog first. Every mutation hook is
// called unconditionally (rules of hooks) with the active id; commands that need
// an app are only emitted when one is in scope, so the idle hooks never fire.
export function useCommands(go: Navigate): CommandSet {
  const { t } = useTranslation()
  const navItems = useNavItems()
  const { data: apps } = useApps()
  const { data: instance } = useInstanceInfo()
  const managed = instance?.managed_services ?? false
  const { toggleTheme } = useTheme()

  // Active app, read from the route (works on any page; undefined off an app).
  const { appId } = useParams({ strict: false }) as { appId?: string }
  const activeApp = appId ? apps?.find((a) => a.id === appId) : undefined

  // App-scoped mutation hooks. Safe to call with '' — they only build mutations;
  // nothing fires until the corresponding command runs, and those only exist
  // when activeApp is set.
  const deploy = useTriggerDeploy(appId ?? '')
  const stack = useStackControl(appId ?? '')
  const { data: databases } = useDatabases(appId ?? '', managed && !!appId)
  const hasManagedDB = (databases?.length ?? 0) > 0

  // Instance-wide destructive mutations (Phase 3).
  const restartCP = useMutation({
    mutationFn: () => instanceApi.restartControlPlane(),
    onSuccess: () => {
      toast.info(t('command.toast.restartControlPlane'))
      // The API briefly drops; a full reload once it's back is the cleanest reset.
      setTimeout(() => window.location.reload(), 6000)
    },
    onError: (e: Error) => toast.error(e.message),
  })
  const stopAll = useMutation({
    mutationFn: () => instanceApi.stopAllApps(),
    onSuccess: (r) => toast.success(t('command.toast.stoppedAll', { count: r.stopped })),
    onError: (e: Error) => toast.error(e.message),
  })

  const commands: Command[] = []

  if (activeApp) {
    commands.push(
      ...appContextCommands({
        app: activeApp,
        managed,
        hasManagedDB,
        t,
        go,
        deploy: () =>
          deploy.mutate(undefined, {
            onSuccess: () => toast.success(t('command.toast.deployTriggered')),
            onError: (e) => toast.error(e.message),
          }),
        stack: (action) =>
          stack.mutate(action, {
            onSuccess: () => toast.success(t(`command.toast.${action}`)),
            onError: (e) => toast.error(e.message),
          }),
      }),
    )
  }

  commands.push(...navCommands(navItems, t, go))
  commands.push(...settingsCommands(t, go))
  commands.push(
    ...actionCommands({
      t,
      go,
      toggleTheme,
      restartControlPlane: () => restartCP.mutate(),
      stopAllApps: () => stopAll.mutate(),
    }),
  )
  commands.push(...appListCommands(apps, go))

  return { commands, appName: activeApp?.name }
}

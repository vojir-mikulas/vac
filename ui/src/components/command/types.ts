import type { LucideIcon } from 'lucide-react'

// Visual + ordering groups the palette renders, top to bottom.
export type CommandGroupId = 'app' | 'navigate' | 'settings' | 'actions' | 'apps'

// Copy shown in the confirmation dialog before a destructive command runs.
export interface CommandConfirm {
  title: string
  description: string
  actionLabel: string
}

export interface Command {
  id: string
  group: CommandGroupId
  /** Resolved (already-translated) display label. */
  label: string
  icon: LucideIcon
  /** Extra text folded into the cmdk match value (slug, "app", aliases…). */
  keywords?: string
  perform: () => void
  /** When set, the palette opens a confirm dialog and only runs `perform` on OK. */
  confirm?: CommandConfirm
}

// Render order of the groups. App-scoped commands lead when present, then global
// navigation, settings, actions, and finally the dynamic per-app list.
export const GROUP_ORDER: readonly CommandGroupId[] = [
  'app',
  'navigate',
  'settings',
  'actions',
  'apps',
]

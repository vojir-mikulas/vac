import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { AlertTriangle, Blocks, Database, Download, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { BrandIcon, brandFor } from '@/components/common/brand-icon'
import { EmptyState } from '@/components/common/empty-state'
import { Button } from '@/components/ui/button'
import { MotionCard } from '@/components/common/motion-card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { useAddons, useInstallAddon } from '@/lib/api/addons'
import { useApps, useDeleteApp } from '@/lib/api/apps'
import { useAddDatabaseToApp } from '@/lib/api/databases'
import { useDatabaseInventory } from '@/lib/api/db-inventory'
import type { Addon, App } from '@/types/api'

export function AddonsPage() {
  const { t } = useTranslation('addons')
  const { data: addons, isLoading } = useAddons()
  const { data: apps } = useApps()
  const { data: inventory } = useDatabaseInventory()

  // Map template_id → the installed app so the catalog can offer Open instead of
  // Install for add-ons already running on this box.
  const installed = new Map<string, App>()
  for (const app of apps ?? []) {
    if (app.source === 'template' && app.template_id && !installed.has(app.template_id)) {
      installed.set(app.template_id, app)
    }
  }

  // engine → number of managed databases provisioned on it (excluding the
  // pinned control-plane row), so a database add-on can show its live state.
  const dbCounts = new Map<string, number>()
  for (const g of inventory?.engines ?? []) {
    const n = g.databases.filter((d) => !d.is_control_plane).length
    if (n > 0) dbCounts.set(g.engine, n)
  }

  return (
    <PageContainer>
      <PageHeader title={t('page.title')} description={t('page.description')} />

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-44 rounded-xl" />
          <Skeleton className="h-44 rounded-xl" />
        </div>
      ) : addons && addons.length > 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {addons.map((a) =>
            a.kind === 'database' ? (
              <DatabaseAddonCard
                key={a.id}
                addon={a}
                count={dbCounts.get(a.id) ?? 0}
                apps={apps ?? []}
              />
            ) : (
              <AddonCard key={a.id} addon={a} installedApp={installed.get(a.id)} />
            ),
          )}
        </div>
      ) : (
        <EmptyState icon={Blocks} title={t('page.empty')} />
      )}
    </PageContainer>
  )
}

function AddonCard({ addon, installedApp }: { addon: Addon; installedApp?: App }) {
  const { t } = useTranslation('addons')
  return (
    <MotionCard className="flex flex-col gap-3 p-5">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          {brandFor(addon.icon) ? (
            <BrandIcon brand={addon.icon} className="size-4" />
          ) : (
            <Blocks className="size-4 text-muted-foreground" />
          )}
          <span className="text-sm font-semibold">{addon.name}</span>
        </div>
        {installedApp ? (
          <span className="rounded-full border border-ok-border bg-ok-bg px-2 py-0.5 text-2xs text-ok-foreground">
            {t('card.installed')}
          </span>
        ) : (
          <span className="rounded-full border bg-surface-2 px-2 py-0.5 text-2xs text-muted-foreground">
            {t('card.footprint', { mb: addon.footprint_mb })}
          </span>
        )}
      </div>
      <p className="flex-1 text-sm text-muted-foreground">{addon.description}</p>
      {addon.depends_on_db ? (
        <div className="flex items-center gap-1.5 text-2xs text-muted-foreground">
          <Database className="size-3" />
          {t('card.provisionsDb', { engine: addon.depends_on_db })}
        </div>
      ) : null}
      {installedApp ? (
        <InstalledActions addon={addon} app={installedApp} />
      ) : (
        <InstallDialog addon={addon} />
      )}
    </MotionCard>
  )
}

// InstalledActions shows Open + Uninstall for an add-on already running on this
// box. Uninstall is the generic app delete behind an add-on-aware confirm — the
// backend stops the stack, removes its volumes, and deprovisions any managed DB.
function InstalledActions({ addon, app }: { addon: Addon; app: App }) {
  const { t } = useTranslation('addons')
  const navigate = useNavigate()
  const remove = useDeleteApp()
  const uninstall = () =>
    remove.mutate(app.id, {
      onSuccess: () => toast.success(t('installed.toastUninstalled', { name: addon.name })),
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="flex gap-2">
      <Button
        variant="outline"
        size="sm"
        className="flex-1"
        onClick={() => navigate({ to: '/apps/$appId/overview', params: { appId: app.id } })}
      >
        {t('installed.open')}
      </Button>
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button
            variant="outline"
            size="sm"
            aria-label={t('installed.uninstallAria', { name: addon.name })}
          >
            <Trash2 className="size-3.5" />
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('installed.confirmTitle', { name: addon.name })}</AlertDialogTitle>
            <AlertDialogDescription>
              {addon.depends_on_db
                ? t('installed.confirmDescriptionWithDb', { engine: addon.depends_on_db })
                : t('installed.confirmDescription')}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('installed.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={uninstall}
              className="bg-err text-err-foreground hover:bg-err/90"
            >
              {t('installed.uninstall')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

// DatabaseAddonCard cross-lists a heavyweight managed-DB engine (e.g. MariaDB).
// Unlike a template add-on it isn't deployed standalone — it's provisioned into
// an app — so the card routes to an app picker and shows the engine's live state.
function DatabaseAddonCard({ addon, count, apps }: { addon: Addon; count: number; apps: App[] }) {
  const { t } = useTranslation('addons')
  const navigate = useNavigate()
  const active = count > 0

  return (
    <MotionCard className="flex flex-col gap-3 p-5">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          {brandFor(addon.icon) ? (
            <BrandIcon brand={addon.icon} className="size-4" />
          ) : (
            <Database className="size-4 text-muted-foreground" />
          )}
          <span className="text-sm font-semibold">{addon.name}</span>
        </div>
        {active ? (
          <span className="rounded-full border border-ok-border bg-ok-bg px-2 py-0.5 text-2xs text-ok-foreground">
            {t('card.active', { count })}
          </span>
        ) : (
          <span className="rounded-full border bg-surface-2 px-2 py-0.5 text-2xs text-muted-foreground">
            {t('card.footprint', { mb: addon.footprint_mb })}
          </span>
        )}
      </div>
      <p className="flex-1 text-sm text-muted-foreground">{addon.description}</p>
      <div className="flex items-center gap-1.5 text-2xs text-muted-foreground">
        <Database className="size-3" />
        {addon.shared ? t('card.shared') : t('card.perApp')}
      </div>
      <div className="flex gap-2">
        <AddToAppDialog addon={addon} apps={apps} />
        {active ? (
          <Button variant="outline" size="sm" onClick={() => navigate({ to: '/database' })}>
            {t('addToApp.manage')}
          </Button>
        ) : null}
      </div>
    </MotionCard>
  )
}

// AddToAppDialog provisions a database add-on onto a chosen app via the same
// per-app endpoint as the app's Database tab — the catalog is just a second
// entry point. The app must be picked here since the catalog isn't app-scoped.
function AddToAppDialog({ addon, apps }: { addon: Addon; apps: App[] }) {
  const { t } = useTranslation('addons')
  const [open, setOpen] = useState(false)
  const [appId, setAppId] = useState('')
  const add = useAddDatabaseToApp()
  const navigate = useNavigate()
  const noApps = apps.length === 0

  const submit = () => {
    if (!appId) return
    add.mutate(
      { appId, engine: addon.id },
      {
        onSuccess: (res) => {
          setOpen(false)
          toast.success(res.warning || t('addToApp.toastProvisioning', { name: addon.name }))
          navigate({ to: '/apps/$appId/databases', params: { appId } })
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="brand" size="sm" className="flex-1" disabled={noApps}>
          <Download className="size-3.5" />
          {noApps ? t('addToApp.noApps') : t('addToApp.trigger')}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('addToApp.title', { name: addon.name })}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">{t('addToApp.appLabel')}</span>
            <Select value={appId} onValueChange={setAppId}>
              <SelectTrigger>
                <SelectValue placeholder={t('addToApp.appPlaceholder')} />
              </SelectTrigger>
              <SelectContent>
                {apps.map((a) => (
                  <SelectItem key={a.id} value={a.id}>
                    {a.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="flex items-start gap-2 rounded-lg border border-warn-border bg-warn-bg px-3 py-2 text-xs text-warn-foreground">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>
              {addon.shared
                ? t('addToApp.warningShared', { name: addon.name, mb: addon.footprint_mb })
                : t('addToApp.warningPerApp', { mb: addon.footprint_mb })}
            </span>
          </div>

          <p className="text-2xs text-muted-foreground">{t('addToApp.note')}</p>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t('addToApp.cancel')}
          </Button>
          <Button variant="brand" disabled={add.isPending || !appId} onClick={submit}>
            {t('addToApp.provision')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function InstallDialog({ addon }: { addon: Addon }) {
  const { t } = useTranslation('addons')
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(addon.name)
  const install = useInstallAddon()
  const navigate = useNavigate()

  const submit = () => {
    install.mutate(
      { id: addon.id, name: name.trim() || undefined },
      {
        onSuccess: (res) => {
          setOpen(false)
          const secrets = Object.entries(res.generated_secrets ?? {})
          if (secrets.length > 0) {
            // Surfaced once — they're sealed at rest and not re-derivable.
            toast.success(
              t('install.toastSaveSecrets', {
                secrets: secrets.map(([k, v]) => `${k}=${v}`).join(', '),
              }),
              { duration: 30_000 },
            )
          } else {
            toast.success(t('install.toastInstalling'))
          }
          navigate({ to: '/apps/$appId/overview', params: { appId: res.app_id } })
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="brand" size="sm" className="w-full">
          <Download className="size-3.5" />
          {t('install.trigger')}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('install.title', { name: addon.name })}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">{t('install.appName')}</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </div>

          <div className="flex items-start gap-2 rounded-lg border border-warn-border bg-warn-bg px-3 py-2 text-xs text-warn-foreground">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>
              {addon.depends_on_db
                ? t('install.warningWithDb', {
                    mb: addon.footprint_mb,
                    engine: addon.depends_on_db,
                  })
                : t('install.warning', { mb: addon.footprint_mb })}
            </span>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t('install.cancel')}
          </Button>
          <Button variant="brand" disabled={install.isPending} onClick={submit}>
            {t('install.install')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

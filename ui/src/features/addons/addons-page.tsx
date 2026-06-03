import { useState } from 'react'
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
      <PageHeader
        title="Add-ons"
        description="A curated catalog for this box: one-click apps that deploy with backups, routing, and HTTPS, plus managed databases you add to your apps."
      />

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
        <EmptyState icon={Blocks} title="No add-ons available" />
      )}
    </PageContainer>
  )
}

function AddonCard({ addon, installedApp }: { addon: Addon; installedApp?: App }) {
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
            Installed
          </span>
        ) : (
          <span className="rounded-full border bg-surface-2 px-2 py-0.5 text-2xs text-muted-foreground">
            ~{addon.footprint_mb} MB
          </span>
        )}
      </div>
      <p className="flex-1 text-sm text-muted-foreground">{addon.description}</p>
      {addon.depends_on_db ? (
        <div className="flex items-center gap-1.5 text-2xs text-muted-foreground">
          <Database className="size-3" />
          Provisions a managed {addon.depends_on_db} database
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
  const navigate = useNavigate()
  const remove = useDeleteApp()
  const uninstall = () =>
    remove.mutate(app.id, {
      onSuccess: () => toast.success(`Uninstalled ${addon.name}`),
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
        Open
      </Button>
      <AlertDialog>
        <AlertDialogTrigger asChild>
          <Button variant="outline" size="sm" aria-label={`Uninstall ${addon.name}`}>
            <Trash2 className="size-3.5" />
          </Button>
        </AlertDialogTrigger>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Uninstall {addon.name}?</AlertDialogTitle>
            <AlertDialogDescription>
              This removes the app and permanently deletes its data — its containers and volumes
              {addon.depends_on_db ? `, plus its managed ${addon.depends_on_db} database` : ''}.
              This cannot be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={uninstall}
              className="bg-err text-err-foreground hover:bg-err/90"
            >
              Uninstall
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
            Active · {count} {count === 1 ? 'database' : 'databases'}
          </span>
        ) : (
          <span className="rounded-full border bg-surface-2 px-2 py-0.5 text-2xs text-muted-foreground">
            ~{addon.footprint_mb} MB
          </span>
        )}
      </div>
      <p className="flex-1 text-sm text-muted-foreground">{addon.description}</p>
      <div className="flex items-center gap-1.5 text-2xs text-muted-foreground">
        <Database className="size-3" />
        {addon.shared ? 'One shared instance serves every app' : 'Provisioned per app'}
      </div>
      <div className="flex gap-2">
        <AddToAppDialog addon={addon} apps={apps} />
        {active ? (
          <Button variant="outline" size="sm" onClick={() => navigate({ to: '/database' })}>
            Manage
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
          toast.success(res.warning || `Provisioning ${addon.name} for the app`)
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
          {noApps ? 'No apps yet' : 'Add to an app'}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add {addon.name} to an app</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">App</span>
            <Select value={appId} onValueChange={setAppId}>
              <SelectTrigger>
                <SelectValue placeholder="Choose an app" />
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
                ? `The first ${addon.name} database starts a shared instance (~${addon.footprint_mb} MB) on this box. Later databases reuse it.`
                : `Uses roughly ${addon.footprint_mb} MB of RAM on this box.`}
            </span>
          </div>

          <p className="text-2xs text-muted-foreground">
            VAC injects the connection string as an env var and schedules a nightly backup. Redeploy
            the app to pick it up.
          </p>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant="brand" disabled={add.isPending || !appId} onClick={submit}>
            Provision
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function InstallDialog({ addon }: { addon: Addon }) {
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
              `Installed. Save these now: ${secrets.map(([k, v]) => `${k}=${v}`).join(', ')}`,
              { duration: 30_000 },
            )
          } else {
            toast.success('Add-on installing')
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
          Install
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Install {addon.name}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">App name</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} />
          </div>

          <div className="flex items-start gap-2 rounded-lg border border-warn-border bg-warn-bg px-3 py-2 text-xs text-warn-foreground">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>
              Runs on this box and uses roughly {addon.footprint_mb} MB of RAM
              {addon.depends_on_db ? `, plus a managed ${addon.depends_on_db} database.` : '.'}
            </span>
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant="brand" disabled={install.isPending} onClick={submit}>
            Install
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

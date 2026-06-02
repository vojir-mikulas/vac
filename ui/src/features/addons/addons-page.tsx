import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { AlertTriangle, Blocks, Database, Download } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { EmptyState } from '@/components/common/empty-state'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { useAddons, useInstallAddon } from '@/lib/api/addons'
import type { Addon } from '@/types/api'

export function AddonsPage() {
  const { data: addons, isLoading } = useAddons()

  return (
    <PageContainer>
      <PageHeader
        title="Add-ons"
        description="One-click apps from a curated catalog. Each deploys as a normal app on this box — backups, routing, and HTTPS included."
      />

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-44 rounded-xl" />
          <Skeleton className="h-44 rounded-xl" />
        </div>
      ) : addons && addons.length > 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {addons.map((a) => (
            <AddonCard key={a.id} addon={a} />
          ))}
        </div>
      ) : (
        <EmptyState icon={Blocks} title="No add-ons available" />
      )}
    </PageContainer>
  )
}

function AddonCard({ addon }: { addon: Addon }) {
  return (
    <Card className="flex flex-col gap-3 p-5">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <Blocks className="size-4 text-muted-foreground" />
          <span className="text-sm font-semibold">{addon.name}</span>
        </div>
        <span className="rounded-full border bg-surface-2 px-2 py-0.5 text-2xs text-muted-foreground">
          ~{addon.footprint_mb} MB
        </span>
      </div>
      <p className="flex-1 text-sm text-muted-foreground">{addon.description}</p>
      {addon.depends_on_db ? (
        <div className="flex items-center gap-1.5 text-2xs text-muted-foreground">
          <Database className="size-3" />
          Provisions a managed {addon.depends_on_db} database
        </div>
      ) : null}
      <InstallDialog addon={addon} />
    </Card>
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

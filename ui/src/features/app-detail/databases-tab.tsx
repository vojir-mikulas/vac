import { useState } from 'react'
import { AlertTriangle, Database, KeyRound, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { EmptyState } from '@/components/common/empty-state'
import { StatusPill } from '@/components/common/status-pill'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
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
import {
  useAddDatabase,
  useDatabaseEngines,
  useDatabases,
  useRemoveDatabase,
} from '@/lib/api/databases'
import type { ManagedDatabase } from '@/types/api'

// 'ready' renders green by reusing the success tone; everything else passes
// through to StatusPill's own mapping (provisioning → muted, error → red).
function pillStatus(status: string): string {
  return status === 'ready' ? 'success' : status
}

export function DatabasesTab({ appId }: { appId: string }) {
  const { data: databases, isLoading } = useDatabases(appId)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">Managed databases</SectionHeader>
        <AddDatabaseDialog appId={appId} />
      </div>

      {isLoading ? (
        <Skeleton className="h-32 w-full rounded-xl" />
      ) : databases && databases.length > 0 ? (
        <div className="flex flex-col gap-4">
          {databases.map((db) => (
            <DatabaseCard key={db.id} appId={appId} db={db} />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={Database}
          title="No managed databases"
          description="Add a database and VAC provisions it, injects the connection string as an env var, and schedules a nightly backup — no manual config."
        />
      )}
    </div>
  )
}

function DatabaseCard({ appId, db }: { appId: string; db: ManagedDatabase }) {
  const remove = useRemoveDatabase(appId)

  return (
    <Card className="gap-0 p-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <span className="font-mono text-sm font-semibold capitalize">{db.engine}</span>
          <StatusPill status={pillStatus(db.status)} size="sm" />
        </div>
        <Button
          variant="danger"
          size="sm"
          disabled={remove.isPending}
          onClick={() => {
            if (!confirm(`Remove the ${db.engine} database "${db.db_name}"? This drops the data.`))
              return
            remove.mutate(db.id, {
              onSuccess: () => toast.success('Database removed'),
              onError: (e) => toast.error(e.message),
            })
          }}
        >
          <Trash2 className="size-3.5" />
        </Button>
      </div>

      <div className="grid gap-x-6 gap-y-1.5 px-5 py-4 text-sm sm:grid-cols-2">
        <Field label="Database" value={db.db_name} mono />
        <Field label="Connection env var" value={db.env_var_name} mono />
      </div>

      {db.status === 'error' && db.error ? (
        <div className="flex items-start gap-2 border-t border-err-border bg-err-bg px-5 py-3 text-xs text-err-foreground">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
          <span>{db.error}</span>
        </div>
      ) : (
        <div className="flex items-center gap-2 border-t px-5 py-3 text-2xs text-muted-foreground">
          <KeyRound className="size-3" />
          Connection string is injected as <code className="font-mono">{db.env_var_name}</code> —
          reveal it on the Environment tab.
        </div>
      )}
    </Card>
  )
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <div className="text-2xs uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className={mono ? 'mt-0.5 font-mono text-xs' : 'mt-0.5'}>{value}</div>
    </div>
  )
}

function AddDatabaseDialog({ appId }: { appId: string }) {
  const [open, setOpen] = useState(false)
  const { data: engines } = useDatabaseEngines(appId)
  const add = useAddDatabase(appId)
  const [engine, setEngine] = useState('postgres')

  const selected = (engines ?? []).find((e) => e.name === engine)

  const submit = () => {
    add.mutate(engine, {
      onSuccess: (res) => {
        toast.success(res.warning || 'Database provisioning started')
        setOpen(false)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="brand" size="sm">
          <Plus className="size-4" />
          Add database
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Add a managed database</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">Engine</span>
            <Select value={engine} onValueChange={setEngine}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(engines ?? []).map((e) => (
                  <SelectItem key={e.name} value={e.name}>
                    <span className="capitalize">{e.name}</span>
                    {e.shared ? ` — shared, ~${e.footprint_mb} MB` : ' — free (shared vac-db)'}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {selected?.shared ? (
            <div className="flex items-start gap-2 rounded-lg border border-warn-border bg-warn-bg px-3 py-2 text-xs text-warn-foreground">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <span>
                The first {selected.name} database starts a shared instance (~
                {selected.footprint_mb} MB) on this box. Later {selected.name} databases reuse it.
              </span>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">
              Provisioned inside the shared control-plane Postgres — no extra process.
            </p>
          )}

          <p className="text-2xs text-muted-foreground">
            VAC injects the connection string as <code className="font-mono">DATABASE_URL</code> and
            schedules a nightly backup. Redeploy the app to pick up the new env var.
          </p>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            Cancel
          </Button>
          <Button variant="brand" disabled={add.isPending} onClick={submit}>
            Provision
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

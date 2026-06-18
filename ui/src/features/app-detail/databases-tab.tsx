import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { AlertTriangle, Database, KeyRound, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { BrandIcon } from '@/components/common/brand-icon'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { StatusPill } from '@/components/common/status-pill'
import { CardStackSkeleton } from '@/components/common/card-stack-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
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
  const { t } = useTranslation('app-detail')
  const { data: databases, isLoading, isError, refetch } = useDatabases(appId)

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <SectionHeader className="mb-0">{t('databases.title')}</SectionHeader>
        <AddDatabaseDialog appId={appId} />
      </div>

      <SwapFade
        id={
          isLoading
            ? 'loading'
            : isError
              ? 'error'
              : databases && databases.length > 0
                ? 'cards'
                : 'empty'
        }
      >
        {isLoading ? (
          <CardStackSkeleton count={1} rowHeight="h-36" />
        ) : isError ? (
          <ErrorState onRetry={() => refetch()} />
        ) : databases && databases.length > 0 ? (
          <div className="flex flex-col gap-4">
            {databases.map((db) => (
              <DatabaseCard key={db.id} appId={appId} db={db} />
            ))}
          </div>
        ) : (
          <EmptyState
            icon={Database}
            title={t('databases.emptyTitle')}
            description={t('databases.emptyDescription')}
          />
        )}
      </SwapFade>
    </div>
  )
}

function DatabaseCard({ appId, db }: { appId: string; db: ManagedDatabase }) {
  const { t } = useTranslation('app-detail')
  const remove = useRemoveDatabase(appId)

  return (
    <Card className="gap-0 p-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-5 py-3.5">
        <div className="flex items-center gap-2.5">
          <BrandIcon brand={db.engine} className="size-4" />
          <span className="font-mono text-sm font-semibold capitalize">{db.engine}</span>
          <StatusPill status={pillStatus(db.status)} size="sm" />
        </div>
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button variant="danger" size="sm" disabled={remove.isPending}>
              <Trash2 className="size-3.5" />
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t('databases.removeDialogTitle')}</AlertDialogTitle>
              <AlertDialogDescription>
                {t('databases.confirmRemove', { engine: db.engine, name: db.db_name })}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
              <AlertDialogAction
                onClick={() =>
                  remove.mutate(db.id, {
                    onSuccess: () => toast.success(t('databases.removed')),
                    onError: (e) => toast.error(e.message),
                  })
                }
                disabled={remove.isPending}
                className="bg-err text-err-foreground hover:bg-err/90"
              >
                {t('databases.removeAction')}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </div>

      <div className="grid gap-x-6 gap-y-1.5 px-5 py-4 text-sm sm:grid-cols-2">
        <Field label={t('databases.fieldDatabase')} value={db.db_name} mono />
        <Field label={t('databases.fieldEnvVar')} value={db.env_var_name} mono />
      </div>

      {db.status === 'error' && db.error ? (
        <div className="flex items-start gap-2 border-t border-err-border bg-err-bg px-5 py-3 text-xs text-err-foreground">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
          <span>{db.error}</span>
        </div>
      ) : (
        <div className="flex items-center gap-2 border-t px-5 py-3 text-2xs text-muted-foreground">
          <KeyRound className="size-3" />
          <span>
            <Trans
              t={t}
              i18nKey="databases.injectedNote"
              values={{ envVar: db.env_var_name }}
              components={[<code className="font-mono" />]}
            />
          </span>
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
  const { t } = useTranslation('app-detail')
  const [open, setOpen] = useState(false)
  const { data: engines } = useDatabaseEngines(appId)
  const { data: databases } = useDatabases(appId)
  const add = useAddDatabase(appId)
  const [engine, setEngine] = useState('postgres')
  const [binding, setBinding] = useState('')

  const selected = (engines ?? []).find((e) => e.name === engine)
  // DATABASE_URL is taken once any DB is already bound to it — surface the
  // auto-suffix behaviour so a second DB doesn't look like it'll overwrite.
  const databaseUrlTaken = (databases ?? []).some((d) => d.env_var_name === 'DATABASE_URL')

  const submit = () => {
    add.mutate(
      { engine, envVarName: binding.trim() || undefined },
      {
        onSuccess: (res) => {
          toast.success(res.warning || t('databases.dialog.provisioningStarted'))
          setOpen(false)
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="brand" size="sm">
          <Plus className="size-4" />
          {t('databases.dialog.addButton')}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('databases.dialog.title')}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-4 py-2">
          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">{t('databases.dialog.engine')}</span>
            <Select value={engine} onValueChange={setEngine}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(engines ?? []).map((e) => (
                  <SelectItem key={e.name} value={e.name}>
                    <span className="capitalize">{e.name}</span>{' '}
                    {e.shared
                      ? t('databases.dialog.engineShared', { footprint: e.footprint_mb })
                      : t('databases.dialog.engineFree')}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          {selected?.shared ? (
            <div className="flex items-start gap-2 rounded-lg border border-warn-border bg-warn-bg px-3 py-2 text-xs text-warn-foreground">
              <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
              <span>
                {t('databases.dialog.sharedWarning', {
                  name: selected.name,
                  footprint: selected.footprint_mb,
                })}
              </span>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t('databases.dialog.controlPlaneNote')}
            </p>
          )}

          <div className="flex flex-col gap-1.5">
            <span className="text-xs font-medium">{t('databases.dialog.envVarLabel')}</span>
            <Input
              value={binding}
              onChange={(e) => setBinding(e.target.value)}
              placeholder={
                databaseUrlTaken
                  ? t('databases.dialog.envVarPlaceholderTaken')
                  : t('databases.dialog.envVarPlaceholder')
              }
              className="font-mono text-xs"
            />
            <span className="text-2xs text-muted-foreground">
              {databaseUrlTaken
                ? t('databases.dialog.envVarHintTaken')
                : t('databases.dialog.envVarHint')}
            </span>
          </div>

          <p className="text-2xs text-muted-foreground">{t('databases.dialog.injectNote')}</p>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
          <Button variant="brand" disabled={add.isPending} onClick={submit}>
            {t('databases.dialog.provision')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

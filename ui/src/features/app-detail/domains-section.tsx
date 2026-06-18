import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import { ListSkeleton } from '@/components/common/list-skeleton'
import { SwapFade } from '@/components/common/swap-fade'
import { DomainConfigPanel } from '@/features/settings/domain-config-panel'
import { DomainStatusBadge } from '@/features/settings/domain-status-badge'
import { useCreateDomain, useDeleteDomain, useDomains } from '@/lib/api/domains'
import { useServices } from '@/lib/api/services'
import { cn } from '@/lib/utils'
import { isValidHostname } from '@/lib/hostname'
import type { Domain } from '@/types/api'

const selectClass =
  'h-9 rounded-md border border-input bg-transparent px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50'

/**
 * Per-app domain management, surfaced on the app's own Settings tab so the
 * operator doesn't have to detour to global Settings → Domains. Lists the app's
 * custom domains and its derived auto hosts (read-only), and adds/removes custom
 * domains against one of the app's services. Reuses the presentational pieces
 * (status badge + DNS-record config panel) from the global Domains screen.
 */
export function AppDomainsSection({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: domains, isLoading } = useDomains(appId)

  return (
    <div className="flex flex-col gap-4">
      <SwapFade id={isLoading ? 'loading' : domains && domains.length > 0 ? 'rows' : 'empty'}>
        {isLoading ? (
          <ListSkeleton rows={2} />
        ) : domains && domains.length > 0 ? (
          <Card className="gap-0 p-0">
            {domains.map((d, i) => (
              <AppDomainRow key={d.id || d.hostname} appId={appId} domain={d} border={i > 0} />
            ))}
          </Card>
        ) : (
          <Card className="p-5">
            <p className="text-center text-sm text-muted-foreground">{t('domains.noCustom')}</p>
          </Card>
        )}
      </SwapFade>

      <AddAppDomain appId={appId} />
    </div>
  )
}

function AppDomainRow({
  appId,
  domain,
  border,
}: {
  appId: string
  domain: Domain
  border: boolean
}) {
  const { t } = useTranslation('app-detail')
  const [open, setOpen] = useState(false)
  const del = useDeleteDomain(appId)

  return (
    <div className={cn('flex flex-col gap-3 px-5 py-3.5', border && 'border-t')}>
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <span className="truncate font-mono text-sm">{domain.hostname}</span>
          <div className="text-2xs text-muted-foreground">
            {domain.service_name} · {domain.managed ? t('domains.managed') : t('domains.custom')}
          </div>
        </div>
        <DomainStatusBadge status={domain.status} />
        <Button variant="ghost" size="sm" onClick={() => setOpen((o) => !o)}>
          {open ? t('domains.hide') : t('domains.configure')}
        </Button>
        {domain.managed ? (
          <Badge variant="outline" title={t('domains.autoTitle')}>
            {t('domains.auto')}
          </Badge>
        ) : (
          <AlertDialog>
            <AlertDialogTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                className="text-err-foreground"
                disabled={del.isPending}
              >
                {t('domains.delete')}
              </Button>
            </AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>{t('domains.deleteDialogTitle')}</AlertDialogTitle>
                <AlertDialogDescription>
                  {t('domains.confirmDelete', { hostname: domain.hostname })}
                </AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                <AlertDialogAction
                  onClick={() =>
                    del.mutate(domain.id, {
                      onSuccess: () => toast.success(t('domains.deleted')),
                      onError: (e) => toast.error(e.message),
                    })
                  }
                  disabled={del.isPending}
                  className="bg-err text-err-foreground hover:bg-err/90"
                >
                  {t('common.delete')}
                </AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        )}
      </div>
      {open ? <DomainConfigPanel domain={domain} /> : null}
    </div>
  )
}

function AddAppDomain({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: services } = useServices(appId)
  const create = useCreateDomain(appId)
  const [hostname, setHostname] = useState('')
  const [service, setService] = useState('')

  const trimmed = hostname.trim()
  const hostnameValid = isValidHostname(trimmed)
  // The per-app endpoint requires a service binding (unlike the global hub's
  // optional/unassigned add).
  const canSubmit = hostnameValid && service !== '' && !create.isPending

  const onSubmit = () => {
    create.mutate(
      { service, hostname: trimmed },
      {
        onSuccess: () => {
          toast.success(t('domains.added'))
          setHostname('')
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <Card className="gap-4 p-5">
      <div className="grid gap-3 sm:grid-cols-[1fr_auto]">
        <div className="grid gap-2">
          <Label>{t('domains.hostname')}</Label>
          <Input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            placeholder={t('domains.hostnamePlaceholder')}
            aria-invalid={trimmed !== '' && !hostnameValid}
            className={cn(
              'font-mono text-xs',
              trimmed !== '' && !hostnameValid && 'border-err-border',
            )}
          />
          {trimmed !== '' && !hostnameValid ? (
            <p className="text-2xs text-err-foreground">{t('domains.invalidHostname')}</p>
          ) : null}
        </div>
        <div className="grid gap-2">
          <Label>{t('domains.service')}</Label>
          <select
            className={selectClass}
            value={service}
            onChange={(e) => setService(e.target.value)}
          >
            <option value="">{t('domains.selectService')}</option>
            {(services ?? []).map((s) => (
              <option key={s.id} value={s.name}>
                {s.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="flex justify-end">
        <Button variant="brand" size="sm" disabled={!canSubmit} onClick={onSubmit}>
          {t('domains.addDomain')}
        </Button>
      </div>
    </Card>
  )
}

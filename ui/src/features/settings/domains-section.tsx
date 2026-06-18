import { useMemo, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { ChevronDown, MoreVertical, Pencil, Trash2 } from 'lucide-react'

import { SectionHeader } from '@/components/common/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
import { DnsProviderSection } from '@/features/settings/dns-provider-section'
import { DomainConfigPanel } from '@/features/settings/domain-config-panel'
import { DomainStatusBadge } from '@/features/settings/domain-status-badge'
import { DomainEditDialog } from '@/features/settings/domain-edit-dialog'
import { WildcardSuggestion } from '@/features/settings/wildcard-suggestion'
import { useApps } from '@/lib/api/apps'
import {
  useAddDomain,
  useAllDomains,
  useDeleteDomainById,
  type DomainAssignment,
} from '@/lib/api/domains'
import { useServices } from '@/lib/api/services'
import { useBaseDomain, useSetBaseDomain, type BaseDomainInfo } from '@/lib/api/instance'
import { cn } from '@/lib/utils'
import type { Domain } from '@/types/api'

export function DomainsSection() {
  return (
    <div className="flex flex-col gap-8">
      <BaseDomainCard />
      <DnsProviderSection />
      <AddDomainCard />
      <DomainList />
    </div>
  )
}

// Where the effective base domain comes from, so the operator can tell a
// config-supplied value apart from a UI override. The "badge" labels the source
// on the compact summary row; the "origin" names the inherited source for the
// "keep inheriting from {origin}" hint (env/file only). Both are keyed by the
// type-safe source union into the catalog (base.source.*).
type BaseDomainSource = NonNullable<BaseDomainInfo['source']>

function BaseDomainCard() {
  const { t } = useTranslation('settings')
  const { data, isLoading } = useBaseDomain()
  const { data: apps } = useApps()
  const save = useSetBaseDomain()
  const [value, setValue] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [confirm, setConfirm] = useState(false)
  // The input is bound to the *override* only — never pre-filled with an
  // env/file value. Pre-filling would let the next Save persist a DB override
  // that silently shadows the config source. The effective value surfaces as
  // the placeholder + the summary line instead.
  const current = value ?? data?.base_domain ?? ''
  const effective = data?.effective ?? ''
  const source: BaseDomainSource = data?.source ?? 'unset'
  const changed = current.trim() !== (data?.base_domain ?? '')
  const appNames = (apps ?? []).map((a) => a.name)

  const closeEditor = () => {
    setEditing(false)
    setValue(null)
  }

  const doSave = () =>
    save.mutate(current.trim(), {
      onSuccess: () => {
        toast.success(t('domains.base.toast.saved'))
        setConfirm(false)
        closeEditor()
      },
      onError: (e) => toast.error(e.message),
    })

  // Naming the affected apps before a change that moves every auto URL.
  const onSave = () => {
    if (changed && appNames.length > 0) setConfirm(true)
    else doSave()
  }

  return (
    <section>
      <SectionHeader>{t('domains.base.heading')}</SectionHeader>
      <Card className="gap-3 p-4">
        {isLoading ? (
          <Skeleton className="h-9 w-full" />
        ) : (
          <>
            {/* Compact summary row: the resolved value + where it came from, with
                Change tucked to the side. Editing is opt-in so the card stays
                slim until the operator wants to touch it. */}
            <div className="flex items-center gap-3">
              <div className="min-w-0 flex-1">
                {effective ? (
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="font-mono text-sm">{effective}</span>
                    <Badge variant="outline" className="text-2xs">
                      {t(`domains.base.source.${source}.badge`)}
                    </Badge>
                  </div>
                ) : (
                  <span className="text-sm text-muted-foreground">{t('domains.base.none')}</span>
                )}
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={() => (editing ? closeEditor() : setEditing(true))}
              >
                {editing
                  ? t('domains.base.cancel')
                  : effective
                    ? t('domains.base.change')
                    : t('domains.base.set')}
              </Button>
            </div>

            {editing ? (
              <div className="flex flex-col gap-3 rounded-md border bg-surface-1 p-3">
                <p className="text-xs text-muted-foreground">
                  <Trans
                    t={t}
                    i18nKey="domains.base.intro"
                    values={{ example: effective || 'example.com' }}
                    components={[<span className="font-mono" />]}
                  />
                </p>
                <div className="flex items-end gap-2">
                  <div className="grid flex-1 gap-2">
                    <Label>{t('domains.base.domainLabel')}</Label>
                    <Input
                      autoFocus
                      value={current}
                      onChange={(e) => setValue(e.target.value)}
                      placeholder={effective || 'example.com'}
                      className="font-mono text-xs"
                    />
                  </div>
                  <Button
                    variant="brand"
                    size="sm"
                    disabled={save.isPending || !changed}
                    onClick={onSave}
                  >
                    {t('domains.base.save')}
                  </Button>
                </div>
                {source !== 'override' && effective ? (
                  <p className="text-2xs text-muted-foreground">
                    {t('domains.base.inheritHint', {
                      origin: t(`domains.base.source.${source}.origin`),
                    })}
                  </p>
                ) : null}
              </div>
            ) : null}
          </>
        )}

        {effective ? <WildcardSuggestion baseDomain={effective} /> : null}
      </Card>

      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('domains.base.confirm.title')}</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2">
                <p>
                  <Trans
                    t={t}
                    i18nKey="domains.base.confirm.body"
                    count={appNames.length}
                    values={{
                      count: appNames.length,
                      from: data?.base_domain ?? '',
                      to: current.trim() || t('domains.base.confirm.disabled'),
                    }}
                    components={[<span className="font-mono" />]}
                  />
                </p>
                <p className="text-xs text-muted-foreground">{appNames.join(', ')}</p>
                {!current.trim() ? (
                  <p className="text-xs text-warn-foreground">
                    {t('domains.base.confirm.clearWarning')}
                  </p>
                ) : null}
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('domains.base.confirm.cancel')}</AlertDialogCancel>
            <AlertDialogAction onClick={doSave}>
              {t('domains.base.confirm.continue')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

const selectClass =
  'h-9 rounded-md border border-input bg-transparent px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50'

function AddDomainCard() {
  const { t } = useTranslation('settings')
  const { data: apps } = useApps()
  const appList = apps ?? []
  const [appId, setAppId] = useState('')
  const [service, setService] = useState('')
  const [hostname, setHostname] = useState('')

  const { data: services } = useServices(appId)
  const add = useAddDomain()

  const trimmed = hostname.trim()
  // Either pick both app + service, or leave both blank (add unassigned).
  const assignmentValid = (appId === '') === (service === '')
  const canSubmit = trimmed.includes('.') && assignmentValid && !add.isPending

  const onSubmit = () => {
    const assign: DomainAssignment | undefined =
      appId && service ? { app_id: appId, service_name: service } : undefined
    add.mutate(
      { hostname: trimmed, assign },
      {
        onSuccess: (domain) => {
          toast.success(t('domains.add.toast.added'))
          // Surface the DNS-automation outcome (plan A), when attempted.
          const rec = domain.dns_record
          if (rec?.error) toast.error(rec.error)
          else if (rec?.created && rec.detail) toast.success(rec.detail)
          setHostname('')
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <section>
      <SectionHeader>{t('domains.add.heading')}</SectionHeader>
      <Card className="gap-4 p-5">
        <div className="grid gap-2">
          <Label>{t('domains.fields.hostname')}</Label>
          <Input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            placeholder="app.example.com"
            className="font-mono text-xs"
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label>{t('domains.fields.appOptional')}</Label>
            <select
              className={selectClass}
              value={appId}
              onChange={(e) => {
                setAppId(e.target.value)
                setService('')
              }}
            >
              <option value="">{t('domains.fields.unassigned')}</option>
              {appList.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </div>
          <div className="grid gap-2">
            <Label>{t('domains.fields.service')}</Label>
            <select
              className={selectClass}
              value={service}
              onChange={(e) => setService(e.target.value)}
              disabled={!appId}
            >
              <option value="">{t('domains.fields.selectService')}</option>
              {(services ?? []).map((s) => (
                <option key={s.id} value={s.name}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
        </div>
        <p className="text-2xs text-muted-foreground">{t('domains.add.hint')}</p>
        <div className="flex justify-end">
          <Button variant="brand" size="sm" disabled={!canSubmit} onClick={onSubmit}>
            {t('domains.add.submit')}
          </Button>
        </div>
      </Card>
    </section>
  )
}

interface DomainRow extends Domain {
  appName?: string
}

function DomainList() {
  const { t } = useTranslation('settings')
  const { data: domains, isLoading } = useAllDomains()
  const { data: apps } = useApps()
  const appName = useMemo(() => {
    const m = new Map<string, string>()
    for (const a of apps ?? []) m.set(a.id, a.name)
    return m
  }, [apps])

  const rows: DomainRow[] = (domains ?? []).map((d) => ({
    ...d,
    appName: d.app_id ? (appName.get(d.app_id) ?? d.app_id) : undefined,
  }))
  // Hosts that something redirects to are "primary" domains (plan 09 Phase 3).
  const redirectTargets = useMemo(
    () => new Set((domains ?? []).map((d) => d.redirect_to).filter(Boolean) as string[]),
    [domains],
  )

  return (
    <section>
      <SectionHeader>{t('domains.list.heading')}</SectionHeader>
      {isLoading ? (
        <Card className="p-5">
          <Skeleton className="h-20 w-full" />
        </Card>
      ) : rows.length === 0 ? (
        <Card className="p-5">
          <p className="text-center text-sm text-muted-foreground">{t('domains.list.empty')}</p>
        </Card>
      ) : (
        <Card className="gap-0 p-0">
          {rows.map((d, i) => (
            <DomainRowItem
              key={d.id || d.hostname}
              domain={d}
              border={i > 0}
              isPrimary={redirectTargets.has(d.hostname)}
            />
          ))}
        </Card>
      )}
    </section>
  )
}

function DomainRowItem({
  domain,
  border,
  isPrimary,
}: {
  domain: DomainRow
  border: boolean
  isPrimary: boolean
}) {
  const { t } = useTranslation('settings')
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const del = useDeleteDomainById()

  const binding = domain.app_id
    ? `${domain.appName} · ${domain.service_name}`
    : t('domains.fields.unassigned')

  return (
    <div className={cn('flex flex-col gap-3 px-5 py-3.5', border && 'border-t')}>
      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex min-w-0 flex-1 items-center gap-3 text-left"
          aria-expanded={open}
        >
          <ChevronDown
            className={cn(
              'size-4 shrink-0 text-muted-foreground transition-transform',
              open && 'rotate-180',
            )}
          />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate font-mono text-sm">{domain.hostname}</span>
              {isPrimary ? (
                <Badge variant="secondary" className="shrink-0">
                  {t('domains.list.primary')}
                </Badge>
              ) : null}
              {domain.managed ? (
                <Badge
                  variant="outline"
                  className="shrink-0 text-2xs"
                  title={t('domains.list.autoTitle')}
                >
                  {t('domains.list.auto')}
                </Badge>
              ) : null}
            </div>
            <div className="text-2xs text-muted-foreground">
              {domain.redirect_to ? (
                <Trans
                  t={t}
                  i18nKey="domains.list.redirectsTo"
                  values={{ target: domain.redirect_to }}
                  components={[<span className="font-mono" />]}
                />
              ) : (
                <>
                  {binding} ·{' '}
                  {domain.managed ? t('domains.list.managed') : t('domains.list.custom')}
                </>
              )}
            </div>
          </div>
        </button>
        <DomainStatusBadge status={domain.status} />
        {domain.managed ? null : (
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={t('domains.list.actionsLabel')}>
                <MoreVertical />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => setEditing(true)}>
                <Pencil />
                {t('domains.list.edit')}
              </DropdownMenuItem>
              <DropdownMenuItem
                variant="destructive"
                disabled={del.isPending}
                onSelect={() => setConfirmDelete(true)}
              >
                <Trash2 />
                {t('domains.list.delete')}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        )}
      </div>
      {open ? <DomainConfigPanel domain={domain} /> : null}
      {editing ? <DomainEditDialog domain={domain} onClose={() => setEditing(false)} /> : null}
      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('domains.list.deleteDialogTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('domains.list.deleteConfirm', { hostname: domain.hostname })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t('domains.list.cancel')}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                del.mutate(domain.id, {
                  onSuccess: () => toast.success(t('domains.list.toast.deleted')),
                  onError: (e) => toast.error(e.message),
                })
              }
              disabled={del.isPending}
              className="bg-err text-err-foreground hover:bg-err/90"
            >
              {t('domains.list.delete')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

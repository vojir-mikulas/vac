import { useMemo, useState } from 'react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
} from '@/components/ui/alert-dialog'
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
      <AddDomainCard />
      <DomainList />
    </div>
  )
}

// Where the effective base domain comes from, so the operator can tell a
// config-supplied value apart from a UI override. `suffix` completes the
// "Currently effective: … {suffix}" line; `origin` names the inherited source
// for the "keep inheriting from {origin}" hint (env/file only).
const SOURCE_COPY: Record<
  NonNullable<BaseDomainInfo['source']>,
  { suffix: string; origin: string }
> = {
  override: { suffix: 'override set here', origin: '' },
  env: {
    suffix: 'from the VAC_BASE_DOMAIN environment variable',
    origin: 'the VAC_BASE_DOMAIN environment variable',
  },
  file: { suffix: 'from the config file', origin: 'the config file' },
  unset: { suffix: '', origin: '' },
}

function BaseDomainCard() {
  const { data, isLoading } = useBaseDomain()
  const { data: apps } = useApps()
  const save = useSetBaseDomain()
  const [value, setValue] = useState<string | null>(null)
  const [confirm, setConfirm] = useState(false)
  // The input is bound to the *override* only — never pre-filled with an
  // env/file value. Pre-filling would let the next Save persist a DB override
  // that silently shadows the config source. The effective value surfaces as
  // the placeholder + the "Currently effective" line instead.
  const current = value ?? data?.base_domain ?? ''
  const effective = data?.effective ?? ''
  const source = data?.source ?? 'unset'
  const changed = current.trim() !== (data?.base_domain ?? '')
  const appNames = (apps ?? []).map((a) => a.name)

  const doSave = () =>
    save.mutate(current.trim(), {
      onSuccess: () => {
        toast.success('Base domain saved — routes are reconciling')
        setValue(null)
        setConfirm(false)
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
      <SectionHeader>Base domain</SectionHeader>
      <Card className="gap-4 p-5">
        <p className="text-xs text-muted-foreground">
          Apps get an automatic subdomain under this domain (e.g.{' '}
          <span className="font-mono">my-app.{effective || 'example.com'}</span>). Leave blank to
          disable automatic subdomains.
        </p>
        {isLoading ? (
          <Skeleton className="h-9 w-full" />
        ) : (
          <>
            {/* Surface the resolved value + where it came from, so a domain set
                via env/yaml doesn't read as "nothing is configured". */}
            <p className="text-xs">
              {effective ? (
                <>
                  <span className="text-muted-foreground">Currently effective: </span>
                  <span className="font-mono">{effective}</span>
                  <span className="text-muted-foreground"> — {SOURCE_COPY[source].suffix}</span>
                </>
              ) : (
                <span className="text-muted-foreground">
                  No base domain configured — apps get no automatic subdomain.
                </span>
              )}
            </p>
            <div className="flex items-end gap-2">
              <div className="grid flex-1 gap-2">
                <Label>Domain</Label>
                <Input
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
                Save
              </Button>
            </div>
            {source !== 'override' && effective ? (
              <p className="text-2xs text-muted-foreground">
                Leave blank to keep inheriting from {SOURCE_COPY[source].origin}; type a value to
                override it here.
              </p>
            ) : null}
          </>
        )}

        {effective ? <WildcardSuggestion baseDomain={effective} /> : null}
      </Card>

      <AlertDialog open={confirm} onOpenChange={setConfirm}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Change the base domain?</AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2">
                <p>
                  {appNames.length} app{appNames.length === 1 ? '' : 's'} will move their automatic
                  subdomain from <span className="font-mono">*.{data?.base_domain}</span> to{' '}
                  <span className="font-mono">*.{current.trim() || '(disabled)'}</span>. The old
                  URLs stop working immediately.
                </p>
                <p className="text-xs text-muted-foreground">{appNames.join(', ')}</p>
                {!current.trim() ? (
                  <p className="text-xs text-warn-foreground">
                    Clearing the base domain stops every app&rsquo;s automatic subdomain from
                    resolving. Custom domains are unaffected.
                  </p>
                ) : null}
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={doSave}>Continue</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

const selectClass =
  'h-9 rounded-md border border-input bg-transparent px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50'

function AddDomainCard() {
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
        onSuccess: () => {
          toast.success('Domain added')
          setHostname('')
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <section>
      <SectionHeader>Add a domain</SectionHeader>
      <Card className="gap-4 p-5">
        <div className="grid gap-2">
          <Label>Hostname</Label>
          <Input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            placeholder="app.example.com"
            className="font-mono text-xs"
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label>App (optional)</Label>
            <select
              className={selectClass}
              value={appId}
              onChange={(e) => {
                setAppId(e.target.value)
                setService('')
              }}
            >
              <option value="">Unassigned</option>
              {appList.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </div>
          <div className="grid gap-2">
            <Label>Service</Label>
            <select
              className={selectClass}
              value={service}
              onChange={(e) => setService(e.target.value)}
              disabled={!appId}
            >
              <option value="">Select service…</option>
              {(services ?? []).map((s) => (
                <option key={s.id} value={s.name}>
                  {s.name}
                </option>
              ))}
            </select>
          </div>
        </div>
        <p className="text-2xs text-muted-foreground">
          You can add a domain now and assign it to a service later — it&rsquo;ll verify DNS either
          way.
        </p>
        <div className="flex justify-end">
          <Button variant="brand" size="sm" disabled={!canSubmit} onClick={onSubmit}>
            Add domain
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
      <SectionHeader>Domains</SectionHeader>
      {isLoading ? (
        <Card className="p-5">
          <Skeleton className="h-20 w-full" />
        </Card>
      ) : rows.length === 0 ? (
        <Card className="p-5">
          <p className="text-center text-sm text-muted-foreground">No domains configured yet.</p>
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
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState(false)
  const del = useDeleteDomainById()

  const onDelete = () => {
    if (!confirm(`Delete ${domain.hostname}? Its route is removed immediately.`)) return
    del.mutate(domain.id, {
      onSuccess: () => toast.success('Domain deleted'),
      onError: (e) => toast.error(e.message),
    })
  }

  const binding = domain.app_id ? `${domain.appName} · ${domain.service_name}` : 'Unassigned'

  return (
    <div className={cn('flex flex-col gap-3 px-5 py-3.5', border && 'border-t')}>
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-mono text-sm">{domain.hostname}</span>
            {isPrimary ? (
              <Badge variant="secondary" className="shrink-0">
                Primary
              </Badge>
            ) : null}
          </div>
          <div className="text-2xs text-muted-foreground">
            {domain.redirect_to ? (
              <>
                Redirects to <span className="font-mono">{domain.redirect_to}</span>
              </>
            ) : (
              <>
                {binding} · {domain.managed ? 'managed' : 'custom'}
              </>
            )}
          </div>
        </div>
        <DomainStatusBadge status={domain.status} />
        <Button variant="ghost" size="sm" onClick={() => setOpen((o) => !o)}>
          {open ? 'Hide' : 'Configure'}
        </Button>
        {domain.managed ? (
          <Badge variant="outline" title="Managed automatically — change the slug or base domain">
            Auto
          </Badge>
        ) : (
          <>
            <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
              Edit
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="text-err-foreground"
              disabled={del.isPending}
              onClick={onDelete}
            >
              Delete
            </Button>
          </>
        )}
      </div>
      {open ? <DomainConfigPanel domain={domain} /> : null}
      {editing ? <DomainEditDialog domain={domain} onClose={() => setEditing(false)} /> : null}
    </div>
  )
}

import { useState } from 'react'
import { useQueries } from '@tanstack/react-query'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { DnsGuidance } from '@/features/settings/dns-guidance'
import { useApps } from '@/lib/api/apps'
import { domainsApi, useCreateDomain } from '@/lib/api/domains'
import { useServices } from '@/lib/api/services'
import { useBaseDomain, useSetBaseDomain } from '@/lib/api/instance'
import { queryKeys } from '@/lib/query/keys'
import { cn } from '@/lib/utils'
import type { Domain } from '@/types/api'

export function DomainsSection() {
  return (
    <div className="flex flex-col gap-8">
      <BaseDomainCard />
      <DomainList />
      <AddDomainCard />
    </div>
  )
}

function BaseDomainCard() {
  const { data, isLoading } = useBaseDomain()
  const save = useSetBaseDomain()
  const [value, setValue] = useState<string | null>(null)
  const current = value ?? data?.base_domain ?? ''

  const onSave = () =>
    save.mutate(current.trim(), {
      onSuccess: () => {
        toast.success('Base domain saved')
        setValue(null)
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <section>
      <SectionHeader>Base domain</SectionHeader>
      <Card className="gap-4 p-5">
        <p className="text-xs text-muted-foreground">
          Apps get an automatic subdomain under this domain (e.g.{' '}
          <span className="font-mono">my-app.{data?.effective || 'example.com'}</span>). Point a
          wildcard <span className="font-mono">*.{data?.effective || 'example.com'}</span> record at
          this VPS for it to work. Leave blank to disable automatic subdomains.
        </p>
        {isLoading ? (
          <Skeleton className="h-9 w-full" />
        ) : (
          <div className="flex items-end gap-2">
            <div className="grid flex-1 gap-2">
              <Label>Domain</Label>
              <Input
                value={current}
                onChange={(e) => setValue(e.target.value)}
                placeholder="example.com"
                className="font-mono text-xs"
              />
            </div>
            <Button variant="brand" size="sm" disabled={save.isPending} onClick={onSave}>
              Save
            </Button>
          </div>
        )}
      </Card>
    </section>
  )
}

interface DomainRow extends Domain {
  appName: string
}

function DomainList() {
  const { data: apps } = useApps()
  const appList = apps ?? []

  const results = useQueries({
    queries: appList.map((app) => ({
      queryKey: queryKeys.apps.domains(app.id),
      queryFn: () => domainsApi.list(app.id),
    })),
  })

  const isLoading = results.some((r) => r.isLoading)

  const rows: DomainRow[] = []
  results.forEach((r, i) => {
    const app = appList[i]
    if (!app || !r.data) return
    for (const d of r.data) rows.push({ ...d, appName: app.name })
  })
  rows.sort((a, b) => a.hostname.localeCompare(b.hostname))

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
            <DomainRowItem key={d.id} domain={d} border={i > 0} />
          ))}
        </Card>
      )}
    </section>
  )
}

function DomainRowItem({ domain, border }: { domain: DomainRow; border: boolean }) {
  const [open, setOpen] = useState(false)
  const needsHelp = domain.cert_status !== 'active'
  return (
    <div className={cn('flex flex-col gap-3 px-5 py-3.5', border && 'border-t')}>
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-sm">{domain.hostname}</div>
          <div className="text-2xs text-muted-foreground">
            {domain.appName} · {domain.service_name} · {domain.type}
          </div>
        </div>
        <CertBadge status={domain.cert_status} />
        {needsHelp ? (
          <Button variant="ghost" size="sm" onClick={() => setOpen((o) => !o)}>
            {open ? 'Hide DNS' : 'DNS setup'}
          </Button>
        ) : null}
      </div>
      {open ? <DnsGuidance hostname={domain.hostname} /> : null}
    </div>
  )
}

function CertBadge({ status }: { status: string }) {
  if (status === 'active') return <Badge variant="success">SSL active</Badge>
  if (status === 'error') return <Badge variant="destructive">SSL error</Badge>
  return <Badge variant="secondary">SSL pending</Badge>
}

function AddDomainCard() {
  const { data: apps } = useApps()
  const appList = apps ?? []
  const [appId, setAppId] = useState('')
  const [service, setService] = useState('')
  const [hostname, setHostname] = useState('')

  const { data: services } = useServices(appId)
  const create = useCreateDomain(appId)

  const trimmed = hostname.trim()
  const canSubmit = appId && service && trimmed && !create.isPending

  const onSubmit = () =>
    create.mutate(
      { service, hostname: trimmed },
      {
        onSuccess: () => {
          toast.success('Domain added')
          setHostname('')
        },
        onError: (e) => toast.error(e.message),
      },
    )

  const selectClass =
    'h-9 rounded-md border border-input bg-transparent px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50'

  return (
    <section>
      <SectionHeader>Add custom domain</SectionHeader>
      <Card className="gap-4 p-5">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label>App</Label>
            <select
              className={selectClass}
              value={appId}
              onChange={(e) => {
                setAppId(e.target.value)
                setService('')
              }}
            >
              <option value="">Select app…</option>
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
        <div className="grid gap-2">
          <Label>Hostname</Label>
          <Input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            placeholder="app.example.com"
            className="font-mono text-xs"
          />
        </div>
        {trimmed.includes('.') ? <DnsGuidance hostname={trimmed} /> : null}
        <div className="flex justify-end">
          <Button variant="brand" size="sm" disabled={!canSubmit} onClick={onSubmit}>
            Add domain
          </Button>
        </div>
      </Card>
    </section>
  )
}

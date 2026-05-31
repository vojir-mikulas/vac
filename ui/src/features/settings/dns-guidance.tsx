import { useMutation } from '@tanstack/react-query'
import { CheckCircle2, AlertTriangle, Globe } from 'lucide-react'

import { CopyButton } from '@/components/common/copy-button'
import { Button } from '@/components/ui/button'
import { useHostStats } from '@/lib/api/metrics'
import { instanceApi, type DnsCheckResult } from '@/lib/api/instance'
import { cn } from '@/lib/utils'

/**
 * Per-domain DNS-setup guidance: shows the exact record(s) to create (with the
 * real VPS IP) and a live "Check DNS" that reports whether the hostname already
 * resolves to this server. Reused by the Domains settings tab and the per-app
 * domain step.
 */
export function DnsGuidance({ hostname }: { hostname: string }) {
  const { data: host } = useHostStats()
  const ip = host?.host_ip || ''

  // A subdomain (3+ labels) can use either an A record to the IP or a CNAME;
  // an apex (2 labels) must use an A record.
  const isApex = hostname.split('.').filter(Boolean).length <= 2
  const recordName = hostname

  const check = useMutation({
    mutationFn: () => instanceApi.dnsCheck(hostname),
  })

  return (
    <div className="flex flex-col gap-3 rounded-md border bg-surface-1 p-3 text-sm">
      <div className="flex items-center gap-2 font-medium">
        <Globe className="size-4 text-muted-foreground" />
        DNS setup
      </div>

      <p className="text-xs text-muted-foreground">
        Create this record at your DNS provider so <span className="font-mono">{hostname}</span>{' '}
        points at this VPS. HTTPS is issued automatically within ~60s of the first request once DNS
        resolves here.
      </p>

      <div className="overflow-hidden rounded-md border">
        <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b bg-surface-2 px-3 py-1.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
          <span>Type</span>
          <span>Name</span>
          <span>Value</span>
        </div>
        <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3 py-2 font-mono text-xs">
          <span className="rounded bg-surface-2 px-1.5 py-0.5">A</span>
          <span className="truncate">{recordName}</span>
          <span className="flex items-center gap-2">
            <span className="truncate">{ip || '—'}</span>
            {ip ? <CopyButton value={ip} label="" /> : null}
          </span>
        </div>
      </div>

      {!isApex ? (
        <p className="text-2xs text-muted-foreground">
          Alternatively, point a <span className="font-mono">CNAME</span> from{' '}
          <span className="font-mono">{hostname}</span> to this host.
        </p>
      ) : null}

      <div className="flex items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          disabled={check.isPending}
          onClick={() => check.mutate()}
        >
          {check.isPending ? 'Checking…' : 'Check DNS'}
        </Button>
        {check.data ? <DnsStatus result={check.data} /> : null}
      </div>
    </div>
  )
}

function DnsStatus({ result }: { result: DnsCheckResult }) {
  if (result.points_here) {
    return (
      <span className="flex items-center gap-1.5 text-xs text-ok-foreground">
        <CheckCircle2 className="size-4" />
        Pointed at this server
      </span>
    )
  }
  return (
    <span className={cn('flex items-center gap-1.5 text-xs text-warn-foreground')}>
      <AlertTriangle className="size-4" />
      {result.resolved.length > 0
        ? `Resolves to ${result.resolved.join(', ')} — not this server yet`
        : 'Not pointing here yet — add the record above and recheck'}
    </span>
  )
}

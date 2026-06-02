import { CheckCircle2, RotateCw } from 'lucide-react'

import { CopyButton } from '@/components/common/copy-button'
import { Button } from '@/components/ui/button'
import { useHostStats } from '@/lib/api/metrics'
import { useBaseDomain } from '@/lib/api/instance'
import { useRefreshDomainStatus } from '@/lib/api/domains'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import type { Domain } from '@/types/api'

/**
 * The Vercel-style "Valid / Invalid Configuration" card for one domain: the
 * exact DNS record to create (real VPS IP, apex-vs-subdomain aware), the live
 * status with its reason, and a manual Refresh that force-probes the host.
 */
export function DomainConfigPanel({ domain }: { domain: Domain }) {
  const { data: host } = useHostStats()
  const { data: base } = useBaseDomain()
  const refresh = useRefreshDomainStatus()
  const ip = host?.host_ip || ''
  const baseHost = base?.effective || ''

  const labels = domain.hostname.split('.').filter(Boolean).length
  const isApex = labels <= 2
  const active = domain.status === 'active'

  return (
    <div className="flex flex-col gap-3 rounded-md border bg-surface-1 p-3 text-sm">
      {active ? (
        <div className="flex items-center gap-2 font-medium text-ok-foreground">
          <CheckCircle2 className="size-4" />
          Valid configuration
          {domain.cert_not_after ? (
            <span className="text-2xs font-normal text-muted-foreground">
              · certificate valid, renews automatically
            </span>
          ) : null}
        </div>
      ) : (
        <>
          <p className="text-xs text-muted-foreground">
            Create this record at your DNS provider so{' '}
            <span className="font-mono">{domain.hostname}</span> points at this VPS.
            {isApex ? ' An apex domain must use an A record (a CNAME at the apex is invalid).' : ''}
          </p>

          <div className="overflow-hidden rounded-md border">
            <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 border-b bg-surface-2 px-3 py-1.5 text-2xs font-medium uppercase tracking-wider text-muted-foreground">
              <span>Type</span>
              <span>Name</span>
              <span>Value</span>
            </div>
            <div className="grid grid-cols-[auto_1fr_auto] items-center gap-3 px-3 py-2 font-mono text-xs">
              <span className="rounded bg-surface-2 px-1.5 py-0.5">A</span>
              <span className="truncate">{domain.hostname}</span>
              <span className="flex items-center gap-2">
                <span className="truncate">{ip || '—'}</span>
                {ip ? <CopyButton value={ip} label="" /> : null}
              </span>
            </div>
          </div>

          {!isApex && baseHost ? (
            <p className="text-2xs text-muted-foreground">
              Alternatively, point a <span className="font-mono">CNAME</span> from{' '}
              <span className="font-mono">{domain.hostname}</span> to{' '}
              <span className="font-mono">{baseHost}</span>.
            </p>
          ) : null}

          {domain.status_detail ? (
            <p className="text-2xs text-warn-foreground">{domain.status_detail}</p>
          ) : null}

          <p className="text-2xs text-muted-foreground">
            DNS changes can take up to your record&rsquo;s TTL to show here. HTTPS is issued
            automatically within ~60s once DNS resolves to this server.
          </p>
        </>
      )}

      <div className="flex items-center gap-3">
        <Button
          variant="outline"
          size="sm"
          disabled={refresh.isPending}
          onClick={() => refresh.mutate(domain.hostname)}
        >
          <RotateCw className={cn('size-3.5', refresh.isPending && 'animate-spin')} />
          {refresh.isPending ? 'Checking…' : 'Refresh'}
        </Button>
        {domain.last_checked ? (
          <span className="text-2xs text-muted-foreground">
            Checked {relativeTime(domain.last_checked)}
          </span>
        ) : null}
      </div>
    </div>
  )
}

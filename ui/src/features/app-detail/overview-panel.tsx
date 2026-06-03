import { Blocks, GitBranch, GitCommitHorizontal } from 'lucide-react'

import { BrandIcon, brandFor } from '@/components/common/brand-icon'
import { SectionHeader } from '@/components/common/section-header'
import { Card } from '@/components/ui/card'
import { useApp } from '@/lib/api/apps'
import { useDeployments } from '@/lib/api/deployments'
import { useServices } from '@/lib/api/services'
import { shortSha } from '@/lib/format'
import type { BuildKind } from '@/types/api'

// Human labels for the persisted build adapter. Mirrors api/internal/adapter —
// build_kind selects which adapter ran the build.
const BUILD_KIND_LABELS: Record<BuildKind, string> = {
  auto: 'Auto-detected',
  compose: 'Docker Compose',
  dockerfile: 'Dockerfile (wrapped)',
  framework: 'Framework',
  static: 'Static site',
}

// Right-column overview: where the app came from (Source) and how it runs
// (Stack). Every field is already on the client — the app DTO, the services
// list, and the latest deployment — so this is a pure read-side panel.
export function OverviewPanel({ appId }: { appId: string }) {
  const { data: app } = useApp(appId)
  const { data: services } = useServices(appId)
  const { data: deployments } = useDeployments(appId)

  if (!app) return null

  const isAddon = app.source === 'template'
  const latest = deployments?.[0]
  const framework = app.build_config?.framework
  const buildLabel = BUILD_KIND_LABELS[app.build_kind] ?? app.build_kind

  return (
    <div className="flex flex-col gap-6">
      <div>
        <SectionHeader>Source</SectionHeader>
        <Card className="gap-0 p-0 text-sm">
          {isAddon ? (
            <Row label="Add-on">
              <span className="flex items-center gap-1.5">
                {brandFor(app.template_icon) ? (
                  <BrandIcon brand={app.template_icon} className="size-3.5" />
                ) : (
                  <Blocks className="size-3.5 text-muted-foreground" />
                )}
                {app.template_name ?? 'add-on'}
              </span>
            </Row>
          ) : (
            <>
              <Row label="Repository">
                <span className="flex items-center gap-1.5 truncate font-mono text-xs">
                  <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{app.git_url}</span>
                </span>
              </Row>
              <Row label="Branch">
                <span className="font-mono text-xs">{app.git_branch}</span>
              </Row>
              <Row label="Commit">
                {latest?.commit_sha ? (
                  <span className="flex min-w-0 items-center gap-1.5">
                    <GitCommitHorizontal className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="truncate" title={latest.commit_message ?? undefined}>
                      <span className="font-mono text-xs">{shortSha(latest.commit_sha)}</span>
                      {latest.commit_message ? (
                        <span className="ml-1.5 text-muted-foreground">
                          {latest.commit_message}
                        </span>
                      ) : null}
                    </span>
                  </span>
                ) : (
                  <span className="text-muted-foreground">Not deployed yet</span>
                )}
              </Row>
              <Row label="Framework">
                {framework ? (
                  <span>{framework}</span>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </Row>
            </>
          )}
        </Card>
      </div>

      <div>
        <SectionHeader>Stack</SectionHeader>
        <Card className="gap-0 p-0 text-sm">
          <Row label="Build">{buildLabel}</Row>
          <Row label="Services">{services?.length ?? 0}</Row>
          <Row label="RAM cap">
            {app.mem_limit_mb != null ? (
              <span className="font-mono text-xs">{app.mem_limit_mb} MiB</span>
            ) : (
              <span className="text-muted-foreground">Unlimited</span>
            )}
          </Row>
          <Row label="Network">
            <span className="font-mono text-xs">vac-edge</span>
          </Row>
        </Card>
      </div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 px-4 py-2.5 [&:not(:first-child)]:border-t">
      <span className="shrink-0 text-2xs uppercase tracking-wider text-muted-foreground">
        {label}
      </span>
      <div className="min-w-0 truncate text-right">{children}</div>
    </div>
  )
}

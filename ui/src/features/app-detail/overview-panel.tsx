import { useTranslation } from 'react-i18next'
import { Blocks, GitBranch, GitCommitHorizontal } from 'lucide-react'

import { BrandIcon, brandFor } from '@/components/common/brand-icon'
import { SectionHeader } from '@/components/common/section-header'
import { Card } from '@/components/ui/card'
import { useApp } from '@/lib/api/apps'
import { useDeployments } from '@/lib/api/deployments'
import { useServices } from '@/lib/api/services'
import { shortSha } from '@/lib/format'
import type { BuildKind } from '@/types/api'

// The known build adapters. Mirrors api/internal/adapter — build_kind selects
// which adapter ran the build. Kept as a literal union so the
// `overviewPanel.buildKind.<kind>` translation keys stay type-checked.
const BUILD_KINDS = ['auto', 'compose', 'dockerfile', 'framework', 'static'] satisfies BuildKind[]

// Right-column overview: where the app came from (Source) and how it runs
// (Stack). Every field is already on the client — the app DTO, the services
// list, and the latest deployment — so this is a pure read-side panel.
export function OverviewPanel({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: app } = useApp(appId)
  const { data: services } = useServices(appId)
  const { data: deployments } = useDeployments(appId)

  if (!app) return null

  const isAddon = app.source === 'template'
  const latest = deployments?.[0]
  const framework = app.build_config?.framework
  const buildLabel = BUILD_KINDS.includes(app.build_kind)
    ? t(`overviewPanel.buildKind.${app.build_kind}`)
    : app.build_kind

  return (
    <div className="flex flex-col gap-6">
      <div>
        <SectionHeader>{t('overviewPanel.source')}</SectionHeader>
        <Card className="gap-0 p-0 text-sm">
          {isAddon ? (
            <Row label={t('overviewPanel.addon')}>
              <span className="flex items-center gap-1.5">
                {brandFor(app.template_icon) ? (
                  <BrandIcon brand={app.template_icon} className="size-3.5" />
                ) : (
                  <Blocks className="size-3.5 text-muted-foreground" />
                )}
                {app.template_name ?? t('overviewPanel.addonFallback')}
              </span>
            </Row>
          ) : (
            <>
              <Row label={t('overviewPanel.repository')}>
                <span className="flex items-center gap-1.5 truncate font-mono text-xs">
                  <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate">{app.git_url}</span>
                </span>
              </Row>
              <Row label={t('overviewPanel.branch')}>
                <span className="font-mono text-xs">{app.git_branch}</span>
              </Row>
              <Row label={t('overviewPanel.commit')}>
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
                  <span className="text-muted-foreground">{t('overviewPanel.notDeployed')}</span>
                )}
              </Row>
              <Row label={t('overviewPanel.framework')}>
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
        <SectionHeader>{t('overviewPanel.stack')}</SectionHeader>
        <Card className="gap-0 p-0 text-sm">
          <Row label={t('overviewPanel.build')}>{buildLabel}</Row>
          <Row label={t('overviewPanel.services')}>{services?.length ?? 0}</Row>
          <Row label={t('overviewPanel.ramCap')}>
            {app.mem_limit_mb != null ? (
              <span className="font-mono text-xs">
                {t('overviewPanel.ramCapValue', { value: app.mem_limit_mb })}
              </span>
            ) : (
              <span className="text-muted-foreground">{t('overviewPanel.ramCapUnlimited')}</span>
            )}
          </Row>
          <Row label={t('overviewPanel.network')}>
            {/* vac-edge is the fixed Docker network name, not display copy. */}
            {/* eslint-disable-next-line i18next/no-literal-string */}
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

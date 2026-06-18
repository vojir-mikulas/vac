import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { Blocks, Download } from 'lucide-react'

import { BrandIcon, brandFor } from '@/components/common/brand-icon'
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
import { SectionHeader } from '@/components/common/section-header'
import { ErrorState } from '@/components/common/error-state'
import { AutoDeploySection } from '@/features/app-detail/auto-deploy-section'
import { DeployKeyCard } from '@/features/app-detail/deploy-key-card'
import { AppDomainsSection } from '@/features/app-detail/domains-section'
import { BuildSourcePicker, type BuildSourceValue } from '@/features/apps/build-source'
import { useApp, useDeleteApp, useUpdateApp } from '@/lib/api/apps'
import { useBoxBudget } from '@/lib/api/metrics'
import { useExportApp } from '@/lib/api/portability'
import { downloadFile } from '@/lib/log-export'
import { Skeleton } from '@/components/ui/skeleton'
import type { App } from '@/types/api'

export function SettingsTab({ appId }: { appId: string }) {
  const { data: app, isError, refetch } = useApp(appId)
  if (isError) return <ErrorState onRetry={() => refetch()} />
  if (!app) return <Skeleton className="h-96 w-full rounded-xl" />
  // Remount the form when the app identity changes so its fields re-initialise
  // from props without a seed effect.
  return <SettingsForm key={app.id} app={app} />
}

function SettingsForm({ app }: { app: App }) {
  const { t } = useTranslation('app-detail')
  const appId = app.id
  // Add-on apps have no git source / build config of their own — they ship a
  // materialized template. Hide the repo/branch/build/autodeploy controls and
  // show a read-only "Installed from {template}" panel instead.
  const isAddon = app.source === 'template'
  const update = useUpdateApp(appId)
  const remove = useDeleteApp()
  const exportApp = useExportApp()
  const navigate = useNavigate()

  const [name, setName] = useState(app.name)
  const [gitUrl, setGitUrl] = useState(app.git_url)
  const [branch, setBranch] = useState(app.git_branch)
  const [composeFile, setComposeFile] = useState(app.compose_file)
  const [build, setBuild] = useState<BuildSourceValue>({
    build_kind: app.build_kind ?? 'auto',
    build_config: app.build_config ?? {},
  })
  const [ramLimit, setRamLimit] = useState(app.mem_limit_mb != null ? String(app.mem_limit_mb) : '')
  const [diskLimit, setDiskLimit] = useState(
    app.disk_limit_mb != null ? String(app.disk_limit_mb) : '',
  )

  // Soft overcommit warning: would this cap push the box's committed RAM (which
  // already includes this app's current cap) past host RAM? Warn only — saving
  // is never blocked, matching the dashboard's over-commit signal.
  const { data: budget } = useBoxBudget()
  const ramValue = ramLimit.trim() === '' ? 0 : Number(ramLimit.trim())
  const ramOvercommits =
    !!budget &&
    budget.total_ram_mb > 0 &&
    Number.isInteger(ramValue) &&
    ramValue > 0 &&
    budget.allocated_mb - (app.mem_limit_mb ?? 0) + ramValue > budget.total_ram_mb

  const saveRuntime = () => {
    const trimmed = ramLimit.trim()
    // Blank clears the limit (0 → unlimited on the backend).
    const value = trimmed === '' ? 0 : Number(trimmed)
    if (!Number.isInteger(value) || value < 0) {
      toast.error(t('settings.ramLimitInvalid'))
      return
    }
    update.mutate(
      { mem_limit_mb: value },
      {
        onSuccess: () =>
          toast.success(
            value === 0 ? t('settings.ramLimitRemoved') : t('settings.ramLimitUpdated'),
          ),
        onError: (e) => toast.error(e.message),
      },
    )
  }

  const saveDisk = () => {
    const trimmed = diskLimit.trim()
    // Blank clears the soft budget (0 → no alert on the backend).
    const value = trimmed === '' ? 0 : Number(trimmed)
    if (!Number.isInteger(value) || value < 0) {
      toast.error(t('settings.diskLimitInvalid'))
      return
    }
    update.mutate(
      { disk_limit_mb: value },
      {
        onSuccess: () =>
          toast.success(
            value === 0 ? t('settings.diskLimitRemoved') : t('settings.diskLimitUpdated'),
          ),
        onError: (e) => toast.error(e.message),
      },
    )
  }

  const saveBuild = () =>
    update.mutate(
      { build_kind: build.build_kind, build_config: build.build_config },
      {
        onSuccess: () => toast.success(t('settings.buildSourceUpdated')),
        onError: (e) => toast.error(e.message),
      },
    )

  const saveGeneral = () =>
    update.mutate(
      { name },
      {
        onSuccess: () => toast.success(t('settings.appNameUpdated')),
        onError: (e) => toast.error(e.message),
      },
    )

  const saveSource = () =>
    update.mutate(
      { git_url: gitUrl, git_branch: branch, compose_file: composeFile },
      {
        onSuccess: () => toast.success(t('settings.sourceUpdated')),
        onError: (e) => toast.error(e.message),
      },
    )

  const exportSpec = () =>
    exportApp.mutate(appId, {
      onSuccess: (yaml) => {
        downloadFile(`${app.slug}.vac.app.yaml`, yaml, 'application/yaml')
        toast.success(t('settings.specExported'))
      },
      onError: (e) => toast.error(e.message),
    })

  const deleteApp = () =>
    remove.mutate(appId, {
      onSuccess: () => {
        toast.success(t('settings.appDeleted'))
        navigate({ to: '/apps' })
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="flex max-w-3xl flex-col gap-8">
      <section>
        <SectionHeader>{t('settings.general')}</SectionHeader>
        <Card className="gap-4 p-5">
          <div className="grid gap-2">
            <Label htmlFor="name">{t('settings.appName')}</Label>
            <Input id="name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="flex justify-end">
            <Button
              variant="brand"
              size="sm"
              disabled={update.isPending || !name}
              onClick={saveGeneral}
            >
              {t('settings.save')}
            </Button>
          </div>
        </Card>
      </section>

      {isAddon ? (
        <section>
          <SectionHeader>{t('settings.source')}</SectionHeader>
          <Card className="gap-3 p-5">
            <div className="flex items-center gap-2">
              {brandFor(app.template_icon) ? (
                <BrandIcon brand={app.template_icon} className="size-4" />
              ) : (
                <Blocks className="size-4 text-muted-foreground" />
              )}
              <span className="text-sm font-medium">
                {t('settings.installedFrom', {
                  name: app.template_name ?? t('settings.installedFromFallback'),
                })}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">{t('settings.addonSourceNote')}</p>
          </Card>
        </section>
      ) : (
        <>
          <section>
            <SectionHeader>{t('settings.source')}</SectionHeader>
            <Card className="gap-4 p-5">
              <div className="grid gap-2">
                <Label htmlFor="git">{t('settings.repositoryUrl')}</Label>
                <Input
                  id="git"
                  value={gitUrl}
                  onChange={(e) => setGitUrl(e.target.value)}
                  className="font-mono text-xs"
                />
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="grid gap-2">
                  <Label htmlFor="branch">{t('settings.branch')}</Label>
                  <Input
                    id="branch"
                    value={branch}
                    onChange={(e) => setBranch(e.target.value)}
                    className="font-mono text-xs"
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="compose">{t('settings.composeFile')}</Label>
                  <Input
                    id="compose"
                    value={composeFile}
                    onChange={(e) => setComposeFile(e.target.value)}
                    className="font-mono text-xs"
                  />
                </div>
              </div>
              <div className="flex justify-end">
                <Button variant="brand" size="sm" disabled={update.isPending} onClick={saveSource}>
                  {t('settings.save')}
                </Button>
              </div>
            </Card>
            <div className="mt-4">
              <DeployKeyCard appId={appId} gitUrl={app.git_url} />
            </div>
          </section>

          <AutoDeploySection appId={appId} defaultBranch={app.git_branch} />

          <section>
            <SectionHeader>{t('settings.build')}</SectionHeader>
            <Card className="gap-5 p-5">
              <BuildSourcePicker value={build} onChange={setBuild} />
              <div className="flex justify-end">
                <Button variant="brand" size="sm" disabled={update.isPending} onClick={saveBuild}>
                  {t('settings.save')}
                </Button>
              </div>
            </Card>
          </section>
        </>
      )}

      {/* Custom domains are a routing concern adjacent to source/build, and
          apply to add-on apps too (they're routed just like git apps). */}
      <section>
        <SectionHeader>{t('settings.domains')}</SectionHeader>
        <AppDomainsSection appId={appId} />
      </section>

      <section>
        <SectionHeader>{t('settings.runtime')}</SectionHeader>
        <Card className="gap-4 p-5">
          <div className="grid gap-2">
            <Label htmlFor="ram-limit">{t('settings.ramLimit')}</Label>
            <Input
              id="ram-limit"
              inputMode="numeric"
              placeholder={t('settings.ramLimitPlaceholder')}
              value={ramLimit}
              onChange={(e) => setRamLimit(e.target.value)}
              className="max-w-40 font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">{t('settings.ramLimitHint')}</p>
            {ramOvercommits ? (
              <p className="text-2xs text-warn-foreground">{t('settings.ramLimitOvercommit')}</p>
            ) : null}
          </div>
          <div className="flex justify-end">
            <Button variant="brand" size="sm" disabled={update.isPending} onClick={saveRuntime}>
              {t('settings.save')}
            </Button>
          </div>
        </Card>
      </section>

      <section>
        <SectionHeader>{t('settings.storage')}</SectionHeader>
        <Card className="gap-4 p-5">
          <div className="grid gap-2">
            <Label htmlFor="disk-limit">{t('settings.diskLimit')}</Label>
            <Input
              id="disk-limit"
              inputMode="numeric"
              placeholder={t('settings.diskLimitPlaceholder')}
              value={diskLimit}
              onChange={(e) => setDiskLimit(e.target.value)}
              className="max-w-40 font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">{t('settings.diskLimitHint')}</p>
          </div>
          <div className="flex justify-end">
            <Button variant="brand" size="sm" disabled={update.isPending} onClick={saveDisk}>
              {t('settings.save')}
            </Button>
          </div>
        </Card>
      </section>

      <section>
        <SectionHeader>{t('settings.portability')}</SectionHeader>
        <Card className="gap-0 p-0">
          <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
            <div className="max-w-xl">
              <div className="text-sm font-medium">{t('settings.exportSpec')}</div>
              <p className="text-xs text-muted-foreground">
                <Trans
                  t={t}
                  i18nKey="settings.exportSpecNote"
                  components={[<span className="font-mono" />]}
                />
              </p>
            </div>
            <Button variant="outline" size="sm" disabled={exportApp.isPending} onClick={exportSpec}>
              <Download className="size-4" />
              {t('settings.exportSpecButton')}
            </Button>
          </div>
        </Card>
      </section>

      <section>
        <SectionHeader>{t('settings.dangerZone')}</SectionHeader>
        <Card className="gap-0 border-err-border p-0">
          <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
            <div>
              <div className="text-sm font-medium">
                {isAddon ? t('settings.uninstallTitle') : t('settings.deleteTitle')}
              </div>
              <p className="text-xs text-muted-foreground">{t('settings.dangerNote')}</p>
            </div>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="danger" size="sm">
                  {isAddon ? t('settings.uninstall') : t('settings.deleteApp')}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    {isAddon
                      ? t('settings.uninstallConfirmTitle', { name: app.name })
                      : t('settings.deleteConfirmTitle', { name: app.name })}
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    {t('settings.deleteConfirmDescription')}
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t('common.cancel')}</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={deleteApp}
                    className="bg-err text-err-foreground hover:bg-err/90"
                  >
                    {t('settings.delete')}
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          </div>
        </Card>
      </section>
    </div>
  )
}

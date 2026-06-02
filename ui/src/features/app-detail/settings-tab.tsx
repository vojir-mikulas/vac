import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { Blocks, Download } from 'lucide-react'

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
import { AutoDeploySection } from '@/features/app-detail/auto-deploy-section'
import { DeployKeyCard } from '@/features/app-detail/deploy-key-card'
import { BuildSourcePicker, type BuildSourceValue } from '@/features/apps/build-source'
import { useApp, useDeleteApp, useUpdateApp } from '@/lib/api/apps'
import { useExportApp } from '@/lib/api/portability'
import { downloadFile } from '@/lib/log-export'
import { Skeleton } from '@/components/ui/skeleton'
import type { App } from '@/types/api'

export function SettingsTab({ appId }: { appId: string }) {
  const { data: app } = useApp(appId)
  if (!app) return <Skeleton className="h-96 w-full rounded-xl" />
  // Remount the form when the app identity changes so its fields re-initialise
  // from props without a seed effect.
  return <SettingsForm key={app.id} app={app} />
}

function SettingsForm({ app }: { app: App }) {
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

  const saveRuntime = () => {
    const trimmed = ramLimit.trim()
    // Blank clears the limit (0 → unlimited on the backend).
    const value = trimmed === '' ? 0 : Number(trimmed)
    if (!Number.isInteger(value) || value < 0) {
      toast.error('RAM limit must be a whole number of MiB (or blank for unlimited)')
      return
    }
    update.mutate(
      { mem_limit_mb: value },
      {
        onSuccess: () => toast.success(value === 0 ? 'RAM limit removed' : 'RAM limit updated'),
        onError: (e) => toast.error(e.message),
      },
    )
  }

  const saveBuild = () =>
    update.mutate(
      { build_kind: build.build_kind, build_config: build.build_config },
      {
        onSuccess: () => toast.success('Build source updated'),
        onError: (e) => toast.error(e.message),
      },
    )

  const saveGeneral = () =>
    update.mutate(
      { name },
      {
        onSuccess: () => toast.success('App name updated'),
        onError: (e) => toast.error(e.message),
      },
    )

  const saveSource = () =>
    update.mutate(
      { git_url: gitUrl, git_branch: branch, compose_file: composeFile },
      {
        onSuccess: () => toast.success('Source updated'),
        onError: (e) => toast.error(e.message),
      },
    )

  const exportSpec = () =>
    exportApp.mutate(appId, {
      onSuccess: (yaml) => {
        downloadFile(`${app.slug}.vac.app.yaml`, yaml, 'application/yaml')
        toast.success('Exported app spec')
      },
      onError: (e) => toast.error(e.message),
    })

  const deleteApp = () =>
    remove.mutate(appId, {
      onSuccess: () => {
        toast.success('App deleted')
        navigate({ to: '/apps' })
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="flex max-w-3xl flex-col gap-8">
      <section>
        <SectionHeader>General</SectionHeader>
        <Card className="gap-4 p-5">
          <div className="grid gap-2">
            <Label htmlFor="name">App name</Label>
            <Input id="name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="flex justify-end">
            <Button
              variant="brand"
              size="sm"
              disabled={update.isPending || !name}
              onClick={saveGeneral}
            >
              Save
            </Button>
          </div>
        </Card>
      </section>

      {isAddon ? (
        <section>
          <SectionHeader>Source</SectionHeader>
          <Card className="gap-3 p-5">
            <div className="flex items-center gap-2">
              <Blocks className="size-4 text-muted-foreground" />
              <span className="text-sm font-medium">
                Installed from {app.template_name ?? 'an add-on'}
              </span>
            </div>
            <p className="text-xs text-muted-foreground">
              This app was deployed from a curated add-on template. Its repository, build, and
              auto-deploy settings are managed by the template and can't be edited here.
            </p>
          </Card>
        </section>
      ) : (
        <>
          <section>
            <SectionHeader>Source</SectionHeader>
            <Card className="gap-4 p-5">
              <div className="grid gap-2">
                <Label htmlFor="git">Repository URL</Label>
                <Input
                  id="git"
                  value={gitUrl}
                  onChange={(e) => setGitUrl(e.target.value)}
                  className="font-mono text-xs"
                />
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="grid gap-2">
                  <Label htmlFor="branch">Branch</Label>
                  <Input
                    id="branch"
                    value={branch}
                    onChange={(e) => setBranch(e.target.value)}
                    className="font-mono text-xs"
                  />
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="compose">Compose file</Label>
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
                  Save
                </Button>
              </div>
            </Card>
            <div className="mt-4">
              <DeployKeyCard appId={appId} gitUrl={app.git_url} />
            </div>
          </section>

          <AutoDeploySection appId={appId} defaultBranch={app.git_branch} />

          <section>
            <SectionHeader>Build</SectionHeader>
            <Card className="gap-5 p-5">
              <BuildSourcePicker value={build} onChange={setBuild} />
              <div className="flex justify-end">
                <Button variant="brand" size="sm" disabled={update.isPending} onClick={saveBuild}>
                  Save
                </Button>
              </div>
            </Card>
          </section>
        </>
      )}

      <section>
        <SectionHeader>Runtime</SectionHeader>
        <Card className="gap-4 p-5">
          <div className="grid gap-2">
            <Label htmlFor="ram-limit">RAM limit (MiB)</Label>
            <Input
              id="ram-limit"
              inputMode="numeric"
              placeholder="Unlimited"
              value={ramLimit}
              onChange={(e) => setRamLimit(e.target.value)}
              className="max-w-40 font-mono text-xs"
            />
            <p className="text-xs text-muted-foreground">
              Hard memory ceiling per container — VAC kills it before it can OOM the box. Leave
              blank for unlimited. Applied on the next deploy.
            </p>
          </div>
          <div className="flex justify-end">
            <Button variant="brand" size="sm" disabled={update.isPending} onClick={saveRuntime}>
              Save
            </Button>
          </div>
        </Card>
      </section>

      <section>
        <SectionHeader>Portability</SectionHeader>
        <Card className="gap-0 p-0">
          <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
            <div className="max-w-xl">
              <div className="text-sm font-medium">Export app spec</div>
              <p className="text-xs text-muted-foreground">
                Download this app as a portable <span className="font-mono">vac.app.yaml</span> —
                build config, services, domains, triggers, and env keys. Nothing here is
                proprietary: re-import it on another VAC, or keep it in version control for disaster
                recovery. Sensitive secret values are omitted — you re-enter them on the far side.
              </p>
            </div>
            <Button variant="outline" size="sm" disabled={exportApp.isPending} onClick={exportSpec}>
              <Download className="size-4" />
              Export spec
            </Button>
          </div>
        </Card>
      </section>

      <section>
        <SectionHeader>Danger zone</SectionHeader>
        <Card className="gap-0 border-err-border p-0">
          <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
            <div>
              <div className="text-sm font-medium">
                {isAddon ? 'Uninstall this add-on' : 'Delete this app'}
              </div>
              <p className="text-xs text-muted-foreground">
                Stops all services and permanently removes the app, its containers, and data
                volumes.
              </p>
            </div>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="danger" size="sm">
                  {isAddon ? 'Uninstall' : 'Delete app'}
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>
                    {isAddon ? `Uninstall ${app.name}?` : `Delete ${app.name}?`}
                  </AlertDialogTitle>
                  <AlertDialogDescription>
                    This permanently removes the app, its services, configuration, and data volumes
                    — plus any managed database it owns. This action cannot be undone.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>Cancel</AlertDialogCancel>
                  <AlertDialogAction
                    onClick={deleteApp}
                    className="bg-err text-err-foreground hover:bg-err/90"
                  >
                    Delete
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

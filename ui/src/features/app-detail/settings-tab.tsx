import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'

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
  const update = useUpdateApp(appId)
  const remove = useDeleteApp()
  const navigate = useNavigate()

  const [name, setName] = useState(app.name)
  const [gitUrl, setGitUrl] = useState(app.git_url)
  const [branch, setBranch] = useState(app.git_branch)
  const [composeFile, setComposeFile] = useState(app.compose_file)
  const [build, setBuild] = useState<BuildSourceValue>({
    build_kind: app.build_kind ?? 'auto',
    build_config: app.build_config ?? {},
  })

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

      <section>
        <SectionHeader>Danger zone</SectionHeader>
        <Card className="gap-0 border-err-border p-0">
          <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
            <div>
              <div className="text-sm font-medium">Delete this app</div>
              <p className="text-xs text-muted-foreground">
                Stops all services and removes the app permanently.
              </p>
            </div>
            <AlertDialog>
              <AlertDialogTrigger asChild>
                <Button variant="danger" size="sm">
                  Delete app
                </Button>
              </AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>Delete {app.name}?</AlertDialogTitle>
                  <AlertDialogDescription>
                    This permanently removes the app, its services, and configuration. This action
                    cannot be undone.
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

import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import { CheckCircle2, XCircle } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer, PageHeader } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DeployKeyCard } from '@/features/app-detail/deploy-key-card'
import { appsApi, useCreateApp } from '@/lib/api/apps'
import { deploymentsApi } from '@/lib/api/deployments'
import { ApiError } from '@/lib/api/client'
import type { App, TestConnectionResult } from '@/types/api'

export function NewApp() {
  const [created, setCreated] = useState<App | null>(null)
  return (
    <PageContainer>
      <PageHeader
        title="New App"
        description="Connect a Git repository and deploy it as a Docker Compose stack."
      />
      <div className="max-w-2xl">
        {created ? <ConnectStep app={created} /> : <CreateStep onCreated={setCreated} />}
      </div>
    </PageContainer>
  )
}

function CreateStep({ onCreated }: { onCreated: (app: App) => void }) {
  const create = useCreateApp()
  const [name, setName] = useState('')
  const [gitUrl, setGitUrl] = useState('')
  const [branch, setBranch] = useState('main')
  const [composeFile, setComposeFile] = useState('compose.yaml')

  const submit = () =>
    create.mutate(
      {
        name,
        git_url: gitUrl,
        git_branch: branch || 'main',
        compose_file: composeFile || 'compose.yaml',
      },
      {
        onSuccess: (app) => {
          toast.success('App created')
          onCreated(app)
        },
        onError: (e) => toast.error(e.message),
      },
    )

  return (
    <Card className="gap-4 p-5">
      <div className="grid gap-2">
        <Label htmlFor="name">App name</Label>
        <Input
          id="name"
          autoFocus
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="my-app"
        />
      </div>
      <div className="grid gap-2">
        <Label htmlFor="git">Repository URL</Label>
        <Input
          id="git"
          value={gitUrl}
          onChange={(e) => setGitUrl(e.target.value)}
          placeholder="git@github.com:user/repo.git"
          className="font-mono text-xs"
        />
        <p className="text-2xs text-muted-foreground">
          HTTPS for public repos, or SSH (git@…) for private repos with a deploy key.
        </p>
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
        <Button variant="brand" disabled={create.isPending || !name || !gitUrl} onClick={submit}>
          Create app
        </Button>
      </div>
    </Card>
  )
}

function ConnectStep({ app }: { app: App }) {
  const navigate = useNavigate()

  const test = useMutation({
    mutationFn: () => appsApi.testConnection(app.id),
  })

  const deployNow = useMutation({
    mutationFn: () => deploymentsApi.trigger(app.id),
    onSuccess: () => {
      toast.success('Deploy triggered')
      navigate({ to: '/apps/$appId/deploys', params: { appId: app.id } })
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <div className="flex flex-col gap-6">
      <DeployKeyCard appId={app.id} gitUrl={app.git_url} />

      <div>
        <SectionHeader>Verify access</SectionHeader>
        <Card className="gap-3 p-5">
          <p className="text-sm text-muted-foreground">
            Confirm VAC can reach <span className="font-mono text-xs">{app.git_url}</span> before
            the first deploy.
          </p>
          <div className="flex items-center gap-3">
            <Button
              variant="outline"
              size="sm"
              disabled={test.isPending}
              onClick={() => test.mutate()}
            >
              Test connection
            </Button>
            {test.data ? <TestResult result={test.data} /> : null}
            {test.error ? (
              <span className="text-xs text-err-foreground">
                {test.error instanceof ApiError ? test.error.message : 'Failed'}
              </span>
            ) : null}
          </div>
        </Card>
      </div>

      <div className="flex justify-end gap-2">
        <Button
          variant="outline"
          onClick={() => navigate({ to: '/apps/$appId', params: { appId: app.id } })}
        >
          Go to app
        </Button>
        <Button variant="brand" disabled={deployNow.isPending} onClick={() => deployNow.mutate()}>
          Deploy now
        </Button>
      </div>
    </div>
  )
}

function TestResult({ result }: { result: TestConnectionResult }) {
  if (result.ok) {
    return (
      <span className="flex items-center gap-1.5 text-xs text-ok-foreground">
        <CheckCircle2 className="size-4" />
        Connection succeeded
      </span>
    )
  }
  return (
    <span className="flex items-center gap-1.5 text-xs text-err-foreground">
      <XCircle className="size-4" />
      {result.error_message ?? result.error_code ?? 'Connection failed'}
    </span>
  )
}

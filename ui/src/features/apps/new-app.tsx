import { useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import { Check, CheckCircle2, ChevronLeft, ChevronRight, Rocket, XCircle } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer } from '@/components/layout/app-shell'
import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { DeployKeyCard } from '@/features/app-detail/deploy-key-card'
import { BuildSourcePicker, type BuildSourceValue } from '@/features/apps/build-source'
import { appsApi, useCreateApp } from '@/lib/api/apps'
import { deploymentsApi } from '@/lib/api/deployments'
import { ApiError } from '@/lib/api/client'
import { cn } from '@/lib/utils'
import type { App, CreateAppInput, TestConnectionResult } from '@/types/api'

const STEPS = ['Source', 'Build', 'Domain', 'Deploy'] as const

export function NewApp() {
  const [created, setCreated] = useState<App | null>(null)
  return (
    <PageContainer>
      <div className="mx-auto max-w-2xl">
        {created ? (
          <>
            <h1 className="mb-1 text-2xl font-semibold tracking-tight">Almost there</h1>
            <p className="mb-6 text-sm text-muted-foreground">
              {created.name} is created. Verify access, then run the first deploy.
            </p>
            <ConnectStep app={created} />
          </>
        ) : (
          <Wizard onCreated={setCreated} />
        )}
      </div>
    </PageContainer>
  )
}

function Wizard({ onCreated }: { onCreated: (app: App) => void }) {
  const navigate = useNavigate()
  const create = useCreateApp()

  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [gitUrl, setGitUrl] = useState('')
  const [branch, setBranch] = useState('main')
  const [domain, setDomain] = useState('')
  const [build, setBuild] = useState<BuildSourceValue>({ build_kind: 'auto', build_config: {} })

  const effectiveName = name.trim() || repoName(gitUrl)

  const canContinue = step === 0 ? Boolean(gitUrl.trim() && effectiveName) : true

  const submit = () => {
    const input: CreateAppInput = {
      name: effectiveName,
      git_url: gitUrl.trim(),
      git_branch: branch.trim() || 'main',
      build_kind: build.build_kind,
      build_config: build.build_config,
    }
    // Keep compose_file meaningful for back-compat / auto detection.
    if (build.build_kind === 'compose' && build.build_config.composePath) {
      input.compose_file = build.build_config.composePath
    }
    create.mutate(input, {
      onSuccess: (app) => {
        toast.success('App created')
        onCreated(app)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <div>
      <button
        type="button"
        onClick={() => navigate({ to: '/apps' })}
        className="mb-3 flex cursor-pointer items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
      >
        <ChevronLeft className="size-3.5" /> Back to apps
      </button>
      <h1 className="text-2xl font-semibold tracking-tight">New application</h1>
      <p className="mt-1 mb-6 text-sm text-muted-foreground">
        Connect a Git repository — VAC handles build, deploy, HTTPS and routing.
      </p>

      <Stepper step={step} />

      <Card className="gap-5 p-6">
        {step === 0 ? (
          <SourceStep
            name={name}
            setName={setName}
            gitUrl={gitUrl}
            setGitUrl={setGitUrl}
            branch={branch}
            setBranch={setBranch}
          />
        ) : null}

        {step === 1 ? (
          <div>
            <StepHeading
              title="Build configuration"
              subtitle="Choose how VAC builds this repo, or let it auto-detect."
            />
            <BuildSourcePicker value={build} onChange={setBuild} />
          </div>
        ) : null}

        {step === 2 ? (
          <DomainStep domain={domain} setDomain={setDomain} fallback={effectiveName} />
        ) : null}

        {step === 3 ? (
          <ReviewStep
            name={effectiveName}
            gitUrl={gitUrl}
            branch={branch}
            build={build}
            domain={domain}
          />
        ) : null}

        <div className="flex items-center justify-between border-t pt-4">
          <Button
            variant="outline"
            onClick={() => (step > 0 ? setStep(step - 1) : navigate({ to: '/apps' }))}
          >
            {step > 0 ? 'Back' : 'Cancel'}
          </Button>
          {step < STEPS.length - 1 ? (
            <Button variant="brand" disabled={!canContinue} onClick={() => setStep(step + 1)}>
              Continue
              <ChevronRight className="size-4" />
            </Button>
          ) : (
            <Button variant="brand" disabled={create.isPending} onClick={submit}>
              <Rocket className="size-4" />
              Create application
            </Button>
          )}
        </div>
      </Card>
    </div>
  )
}

function Stepper({ step }: { step: number }) {
  return (
    <div className="mb-6 flex items-center gap-3">
      {STEPS.map((label, i) => (
        <div key={label} className="flex flex-1 items-center gap-3 last:flex-none">
          <div className="flex items-center gap-2">
            <div
              className={cn(
                'grid size-6 place-items-center rounded-full border font-mono text-xs font-semibold',
                i < step && 'border-brand bg-brand text-brand-foreground',
                i === step && 'border-brand text-brand',
                i > step && 'border-border text-muted-foreground',
              )}
            >
              {i < step ? <Check className="size-3" /> : i + 1}
            </div>
            <span
              className={cn(
                'text-sm font-medium',
                i <= step ? 'text-foreground' : 'text-muted-foreground',
              )}
            >
              {label}
            </span>
          </div>
          {i < STEPS.length - 1 ? (
            <div className={cn('h-px flex-1', i < step ? 'bg-brand' : 'bg-border')} />
          ) : null}
        </div>
      ))}
    </div>
  )
}

function StepHeading({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <div className="mb-4">
      <h2 className="text-base font-semibold tracking-tight">{title}</h2>
      <p className="mt-0.5 text-xs text-muted-foreground">{subtitle}</p>
    </div>
  )
}

function SourceStep({
  name,
  setName,
  gitUrl,
  setGitUrl,
  branch,
  setBranch,
}: {
  name: string
  setName: (v: string) => void
  gitUrl: string
  setGitUrl: (v: string) => void
  branch: string
  setBranch: (v: string) => void
}) {
  return (
    <div>
      <StepHeading
        title="Connect a repository"
        subtitle="Paste a Git URL. VAC creates a read-only deploy key for private repos."
      />
      <div className="flex flex-col gap-4">
        <div className="grid gap-2">
          <Label htmlFor="git">Repository URL</Label>
          <Input
            id="git"
            autoFocus
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
            <Label htmlFor="name">App name</Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={repoName(gitUrl) || 'my-app'}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="branch">Branch</Label>
            <Input
              id="branch"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              className="font-mono text-xs"
            />
          </div>
        </div>
      </div>
    </div>
  )
}

function DomainStep({
  domain,
  setDomain,
  fallback,
}: {
  domain: string
  setDomain: (v: string) => void
  fallback: string
}) {
  return (
    <div>
      <StepHeading
        title="Domain"
        subtitle="Optional. HTTPS is automatic via Let's Encrypt once a domain points here."
      />
      <div className="flex flex-col gap-4">
        <div className="grid gap-2">
          <Label htmlFor="domain">Custom domain</Label>
          <Input
            id="domain"
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            placeholder={`${fallback || 'my-app'}.example.com`}
            className="font-mono text-xs"
          />
        </div>
        <p className="rounded-md border bg-surface-1 px-3 py-2 text-xs text-muted-foreground">
          Point a <span className="font-mono text-foreground">CNAME</span> (or A record) at this
          VPS, then add the domain from the app's <span className="font-medium">Domains</span> tab
          after the first deploy — certificates are issued on the first request.
        </p>
      </div>
    </div>
  )
}

function ReviewStep({
  name,
  gitUrl,
  branch,
  build,
  domain,
}: {
  name: string
  gitUrl: string
  branch: string
  build: BuildSourceValue
  domain: string
}) {
  return (
    <div>
      <StepHeading
        title="Review and deploy"
        subtitle="VAC will create the app, then run the pipeline."
      />
      <div className="flex flex-col gap-2 rounded-lg border p-4">
        <ReviewLine k="Name" v={name || '—'} />
        <ReviewLine k="Source" v={gitUrl || '—'} mono />
        <ReviewLine k="Branch" v={branch} mono />
        <ReviewLine k="Build" v={buildSummary(build)} />
        {domain ? <ReviewLine k="Domain" v={domain} mono /> : null}
      </div>
    </div>
  )
}

function ReviewLine({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4 text-xs">
      <span className="text-muted-foreground">{k}</span>
      <span className={cn('truncate', mono && 'font-mono')}>{v}</span>
    </div>
  )
}

function buildSummary(build: BuildSourceValue): string {
  const c = build.build_config
  switch (build.build_kind) {
    case 'auto':
      return 'Auto-detect'
    case 'compose':
      return `Compose (${c.composePath || 'compose.yaml'})`
    case 'dockerfile':
      return `Dockerfile (${c.dockerfilePath || 'Dockerfile'})`
    case 'framework':
      return `Framework: ${c.framework || 'react'}`
    case 'static':
      return `Static (${c.staticDir || 'dist'})`
    default:
      return build.build_kind
  }
}

function repoName(gitUrl: string): string {
  const last = gitUrl.trim().split('/').pop() ?? ''
  return last.replace(/\.git$/, '')
}

// ConnectStep is the post-create surface: deploy key (for SSH), connection test,
// and the first deploy. Unchanged behaviour from the prior two-step flow.
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

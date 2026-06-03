import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
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

// The four wizard steps, by id; labels are translated at render (see Stepper).
const STEPS = ['source', 'build', 'domain', 'deploy'] as const

// t() scoped to the `apps` namespace, for helpers that render outside a hook.
type AppsTFunction = ReturnType<typeof useTranslation<'apps'>>['t']

export function NewApp() {
  const { t } = useTranslation('apps')
  const [created, setCreated] = useState<App | null>(null)
  return (
    <PageContainer>
      <div className="mx-auto max-w-2xl">
        {created ? (
          <>
            <h1 className="mb-1 text-2xl font-semibold tracking-tight">{t('new.created.title')}</h1>
            <p className="mb-6 text-sm text-muted-foreground">
              {t('new.created.subtitle', { name: created.name })}
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
  const { t } = useTranslation('apps')
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
        toast.success(t('new.toast.created'))
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
        <ChevronLeft className="size-3.5" /> {t('new.backToApps')}
      </button>
      <h1 className="text-2xl font-semibold tracking-tight">{t('new.title')}</h1>
      <p className="mt-1 mb-6 text-sm text-muted-foreground">{t('new.subtitle')}</p>

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
            <StepHeading title={t('new.build.title')} subtitle={t('new.build.subtitle')} />
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
            {step > 0 ? t('actions.back') : t('actions.cancel')}
          </Button>
          {step < STEPS.length - 1 ? (
            <Button variant="brand" disabled={!canContinue} onClick={() => setStep(step + 1)}>
              {t('actions.continue')}
              <ChevronRight className="size-4" />
            </Button>
          ) : (
            <Button variant="brand" disabled={create.isPending} onClick={submit}>
              <Rocket className="size-4" />
              {t('new.create')}
            </Button>
          )}
        </div>
      </Card>
    </div>
  )
}

function Stepper({ step }: { step: number }) {
  const { t } = useTranslation('apps')
  const labels = [
    t('new.steps.source'),
    t('new.steps.build'),
    t('new.steps.domain'),
    t('new.steps.deploy'),
  ]
  return (
    <div className="mb-6 flex items-center gap-3">
      {labels.map((label, i) => (
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
  const { t } = useTranslation('apps')
  return (
    <div>
      <StepHeading title={t('new.source.title')} subtitle={t('new.source.subtitle')} />
      <div className="flex flex-col gap-4">
        <div className="grid gap-2">
          <Label htmlFor="git">{t('new.source.repoUrl')}</Label>
          <Input
            id="git"
            autoFocus
            value={gitUrl}
            onChange={(e) => setGitUrl(e.target.value)}
            placeholder="git@github.com:user/repo.git"
            className="font-mono text-xs"
          />
          <p className="text-2xs text-muted-foreground">{t('new.source.repoHint')}</p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label htmlFor="name">{t('new.source.appName')}</Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={repoName(gitUrl) || t('new.source.appNameFallback')}
            />
          </div>
          <div className="grid gap-2">
            <Label htmlFor="branch">{t('new.source.branch')}</Label>
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
  const { t } = useTranslation('apps')
  return (
    <div>
      <StepHeading title={t('new.domain.title')} subtitle={t('new.domain.subtitle')} />
      <div className="flex flex-col gap-4">
        <div className="grid gap-2">
          <Label htmlFor="domain">{t('new.domain.label')}</Label>
          <Input
            id="domain"
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            placeholder={`${fallback || t('new.source.appNameFallback')}.example.com`}
            className="font-mono text-xs"
          />
        </div>
        <p className="rounded-md border bg-surface-1 px-3 py-2 text-xs text-muted-foreground">
          <Trans
            t={t}
            i18nKey="new.domain.note"
            components={[
              <span className="font-mono text-foreground" />,
              <span className="font-medium" />,
            ]}
          />
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
  const { t } = useTranslation('apps')
  return (
    <div>
      <StepHeading title={t('new.review.title')} subtitle={t('new.review.subtitle')} />
      <div className="flex flex-col gap-2 rounded-lg border p-4">
        <ReviewLine k={t('new.review.name')} v={name || '—'} />
        <ReviewLine k={t('new.review.source')} v={gitUrl || '—'} mono />
        <ReviewLine k={t('new.review.branch')} v={branch} mono />
        <ReviewLine k={t('new.review.build')} v={buildSummary(build, t)} />
        {domain ? <ReviewLine k={t('new.review.domain')} v={domain} mono /> : null}
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

function buildSummary(build: BuildSourceValue, t: AppsTFunction): string {
  const c = build.build_config
  switch (build.build_kind) {
    case 'auto':
      return t('new.summary.auto')
    case 'compose':
      return t('new.summary.compose', { path: c.composePath || 'compose.yaml' })
    case 'dockerfile':
      return t('new.summary.dockerfile', { path: c.dockerfilePath || 'Dockerfile' })
    case 'framework':
      return t('new.summary.framework', { name: c.framework || 'react' })
    case 'static':
      return t('new.summary.static', { path: c.staticDir || 'dist' })
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
  const { t } = useTranslation('apps')
  const navigate = useNavigate()

  const test = useMutation({
    mutationFn: () => appsApi.testConnection(app.id),
  })

  const deployNow = useMutation({
    mutationFn: () => deploymentsApi.trigger(app.id),
    onSuccess: () => {
      toast.success(t('new.toast.deployTriggered'))
      navigate({ to: '/apps/$appId/deploys', params: { appId: app.id } })
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <div className="flex flex-col gap-6">
      <DeployKeyCard appId={app.id} gitUrl={app.git_url} />

      <div>
        <SectionHeader>{t('new.connect.verifyAccess')}</SectionHeader>
        <Card className="gap-3 p-5">
          <p className="text-sm text-muted-foreground">
            <Trans
              t={t}
              i18nKey="new.connect.reach"
              values={{ url: app.git_url }}
              components={[<span className="font-mono text-xs" />]}
            />
          </p>
          <div className="flex items-center gap-3">
            <Button
              variant="outline"
              size="sm"
              disabled={test.isPending}
              onClick={() => test.mutate()}
            >
              {t('new.connect.testConnection')}
            </Button>
            {test.data ? <TestResult result={test.data} /> : null}
            {test.error ? (
              <span className="text-xs text-err-foreground">
                {test.error instanceof ApiError ? test.error.message : t('new.connect.testFailed')}
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
          {t('new.connect.goToApp')}
        </Button>
        <Button variant="brand" disabled={deployNow.isPending} onClick={() => deployNow.mutate()}>
          {t('new.connect.deployNow')}
        </Button>
      </div>
    </div>
  )
}

function TestResult({ result }: { result: TestConnectionResult }) {
  const { t } = useTranslation('apps')
  if (result.ok) {
    return (
      <span className="flex items-center gap-1.5 text-xs text-ok-foreground">
        <CheckCircle2 className="size-4" />
        {t('new.connect.succeeded')}
      </span>
    )
  }
  return (
    <span className="flex items-center gap-1.5 text-xs text-err-foreground">
      <XCircle className="size-4" />
      {result.error_message ?? result.error_code ?? t('new.connect.connectionFailed')}
    </span>
  )
}

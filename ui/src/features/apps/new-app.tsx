import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import { AnimatePresence, m } from 'motion/react'
import { ArrowRight, Check, ChevronLeft, Loader2, Rocket } from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer } from '@/components/layout/app-shell'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NotificationBar } from '@/components/common/notification-bar'
import { DeployKeyCard } from '@/features/app-detail/deploy-key-card'
import { BuildSourcePicker, type BuildSourceValue } from '@/features/apps/build-source'
import { appsApi, useCreateApp } from '@/lib/api/apps'
import { deploymentsApi } from '@/lib/api/deployments'
import { ApiError } from '@/lib/api/client'
import { cn } from '@/lib/utils'
import { RISE, transition } from '@/lib/motion'
import type { App, CreateAppInput, TestConnectionResult } from '@/types/api'

// The four wizard steps, by id; labels are translated at render (see Stepper).
const STEPS = ['source', 'build', 'domain', 'deploy'] as const

// t() scoped to the `apps` namespace, for helpers that render outside a hook.
type AppsTFunction = ReturnType<typeof useTranslation<'apps'>>['t']

function isSshUrl(url: string): boolean {
  return url.trim().startsWith('git@') || url.trim().startsWith('ssh://')
}

// Directional slide+fade for step transitions. `custom` carries the travel
// direction (+1 forward, -1 back) so steps enter from the side you're heading.
const stepVariants = {
  enter: (dir: number) => ({ opacity: 0, x: dir * 16 }),
  center: { opacity: 1, x: 0, transition: transition.base },
  exit: (dir: number) => ({ opacity: 0, x: dir * -16, transition: transition.fast }),
}

export function NewApp() {
  return (
    <PageContainer>
      <div className="mx-auto max-w-2xl">
        <Wizard />
      </div>
    </PageContainer>
  )
}

function Wizard() {
  const { t } = useTranslation('apps')
  const navigate = useNavigate()
  const create = useCreateApp()

  // [step, direction] — direction drives which way the step slides.
  const [[step, dir], setStepState] = useState<[number, number]>([0, 1])
  const [name, setName] = useState('')
  const [gitUrl, setGitUrl] = useState('')
  const [branch, setBranch] = useState('main')
  const [domain, setDomain] = useState('')
  const [build, setBuild] = useState<BuildSourceValue>({ build_kind: 'auto', build_config: {} })
  const [created, setCreated] = useState<App | null>(null)

  const goTo = (next: number) => setStepState([next, next > step ? 1 : -1])

  const effectiveName = name.trim() || repoName(gitUrl)
  const ssh = isSshUrl(gitUrl)
  const canContinue = step === 0 ? Boolean(gitUrl.trim() && effectiveName) : true

  const deployNow = useMutation({
    mutationFn: (appId: string) => deploymentsApi.trigger(appId),
    onSuccess: (_, appId) => {
      toast.success(t('new.toast.deployTriggered'))
      navigate({ to: '/apps/$appId/deploys', params: { appId } })
    },
    onError: (e) => toast.error(e.message),
  })

  const createApp = (thenDeploy: boolean) => {
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
        setCreated(app)
        if (thenDeploy) deployNow.mutate(app.id)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  const busy = create.isPending || deployNow.isPending

  return (
    <div>
      <button
        type="button"
        onClick={() => navigate({ to: '/apps' })}
        className="mb-3 flex cursor-pointer items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
      >
        <ChevronLeft className="size-3.5" /> {t('new.backToApps')}
      </button>
      <h1 className="text-2xl font-semibold tracking-tight">{t('new.title')}</h1>
      <p className="mt-1 mb-6 text-sm text-muted-foreground">{t('new.subtitle')}</p>

      <Stepper step={step} done={Boolean(created)} />

      <Card className="overflow-hidden p-6">
        <AnimatePresence mode="wait" custom={dir} initial={false}>
          <m.div
            key={step}
            custom={dir}
            variants={stepVariants}
            initial="enter"
            animate="center"
            exit="exit"
          >
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
              <ReviewDeployStep
                app={created}
                name={effectiveName}
                gitUrl={gitUrl}
                branch={branch}
                build={build}
                domain={domain}
                ssh={ssh}
              />
            ) : null}
          </m.div>
        </AnimatePresence>

        <div className="mt-5 flex items-center justify-between gap-3 border-t pt-4">
          {step < STEPS.length - 1 ? (
            <>
              <Button
                variant="outline"
                onClick={() => (step > 0 ? goTo(step - 1) : navigate({ to: '/apps' }))}
              >
                {step > 0 ? t('actions.back') : t('actions.cancel')}
              </Button>
              <Button variant="brand" disabled={!canContinue} onClick={() => goTo(step + 1)}>
                {t('actions.continue')}
                <ArrowRight className="size-4" />
              </Button>
            </>
          ) : created ? (
            <>
              <Button
                variant="outline"
                onClick={() => navigate({ to: '/apps/$appId', params: { appId: created.id } })}
              >
                {t('new.connect.goToApp')}
              </Button>
              <Button
                variant="brand"
                disabled={deployNow.isPending}
                onClick={() => deployNow.mutate(created.id)}
              >
                {deployNow.isPending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <Rocket className="size-4" />
                )}
                {t('new.connect.deployNow')}
              </Button>
            </>
          ) : (
            <>
              <Button variant="outline" disabled={busy} onClick={() => goTo(step - 1)}>
                {t('actions.back')}
              </Button>
              <div className="flex items-center gap-2">
                <Button variant="outline" disabled={busy} onClick={() => createApp(false)}>
                  {create.isPending && !deployNow.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <Check className="size-4" />
                  )}
                  {t('new.createOnly')}
                </Button>
                <Button variant="brand" disabled={busy} onClick={() => createApp(true)}>
                  {busy ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <Rocket className="size-4" />
                  )}
                  {t('new.createAndDeploy')}
                </Button>
              </div>
            </>
          )}
        </div>
      </Card>
    </div>
  )
}

function Stepper({ step, done }: { step: number; done: boolean }) {
  const { t } = useTranslation('apps')
  const labels = [
    t('new.steps.source'),
    t('new.steps.build'),
    t('new.steps.domain'),
    t('new.steps.deploy'),
  ]
  return (
    <div className="mb-6 flex items-center gap-3">
      {labels.map((label, i) => {
        // The final step counts as complete once the app is created.
        const complete = i < step || (done && i === STEPS.length - 1)
        const current = i === step && !complete
        return (
          <div key={label} className="flex flex-1 items-center gap-3 last:flex-none">
            <div className="flex items-center gap-2">
              <m.div
                animate={current ? { scale: [1, 1.08, 1] } : { scale: 1 }}
                transition={transition.base}
                className={cn(
                  'grid size-6 place-items-center rounded-full border font-mono text-xs font-semibold transition-colors',
                  complete && 'border-brand bg-brand text-brand-foreground',
                  current && 'border-brand text-brand',
                  !complete && !current && 'border-border text-muted-foreground',
                )}
              >
                <AnimatePresence mode="wait" initial={false}>
                  {complete ? (
                    <m.span
                      key="check"
                      initial={{ opacity: 0, scale: 0.5 }}
                      animate={{ opacity: 1, scale: 1 }}
                      exit={{ opacity: 0, scale: 0.5 }}
                      transition={transition.fast}
                    >
                      <Check className="size-3" strokeWidth={3} />
                    </m.span>
                  ) : (
                    <m.span
                      key="num"
                      initial={{ opacity: 0 }}
                      animate={{ opacity: 1 }}
                      exit={{ opacity: 0 }}
                      transition={transition.fast}
                    >
                      {i + 1}
                    </m.span>
                  )}
                </AnimatePresence>
              </m.div>
              <span
                className={cn(
                  'text-sm font-medium transition-colors',
                  i <= step || complete ? 'text-foreground' : 'text-muted-foreground',
                )}
              >
                {label}
              </span>
            </div>
            {i < STEPS.length - 1 ? (
              <div className="relative h-px flex-1 overflow-hidden bg-border">
                <m.div
                  className="absolute inset-0 origin-left bg-brand"
                  initial={false}
                  animate={{ scaleX: i < step ? 1 : 0 }}
                  transition={transition.layout}
                />
              </div>
            ) : null}
          </div>
        )
      })}
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

// ReviewDeployStep is the merged final step: before creation it summarises the
// config (plus an SSH deploy-key heads-up); after creation it morphs in place to
// surface the deploy key and a connection test, so the operator never leaves the
// wizard to verify access.
function ReviewDeployStep({
  app,
  name,
  gitUrl,
  branch,
  build,
  domain,
  ssh,
}: {
  app: App | null
  name: string
  gitUrl: string
  branch: string
  build: BuildSourceValue
  domain: string
  ssh: boolean
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

      <AnimatePresence mode="wait" initial={false}>
        {app ? (
          <m.div
            key="created"
            initial={{ opacity: 0, y: RISE }}
            animate={{ opacity: 1, y: 0 }}
            transition={transition.base}
            className="mt-4 flex flex-col gap-4"
          >
            <NotificationBar tone="success" title={t('new.notices.created', { name: app.name })} />
            {ssh ? <DeployKeyCard appId={app.id} gitUrl={app.git_url} /> : null}
            <ConnectionTest app={app} />
          </m.div>
        ) : ssh ? (
          <m.div
            key="ssh-hint"
            initial={{ opacity: 0, y: RISE }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0, y: -RISE }}
            transition={transition.base}
            className="mt-4"
          >
            <NotificationBar tone="info" title={t('new.notices.sshKey.title')}>
              {t('new.notices.sshKey.body')}
            </NotificationBar>
          </m.div>
        ) : null}
      </AnimatePresence>
    </div>
  )
}

function ConnectionTest({ app }: { app: App }) {
  const { t } = useTranslation('apps')
  const test = useMutation({
    mutationFn: () => appsApi.testConnection(app.id),
  })

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-3">
        <p className="min-w-0 flex-1 text-xs text-muted-foreground">
          <Trans
            t={t}
            i18nKey="new.connect.reach"
            values={{ url: app.git_url }}
            components={[<span className="font-mono text-foreground" />]}
          />
        </p>
        <Button variant="outline" size="sm" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? <Loader2 className="size-3.5 animate-spin" /> : null}
          {t('new.connect.testConnection')}
        </Button>
      </div>

      <AnimatePresence mode="wait">
        {test.data ? (
          <TestResultBar key="result" result={test.data} />
        ) : test.error ? (
          <NotificationBar key="error" tone="error" title={t('new.connect.testFailed')}>
            {test.error instanceof ApiError
              ? test.error.message
              : t('new.connect.connectionFailed')}
          </NotificationBar>
        ) : null}
      </AnimatePresence>
    </div>
  )
}

function TestResultBar({ result }: { result: TestConnectionResult }) {
  const { t } = useTranslation('apps')
  if (result.ok) {
    return <NotificationBar tone="success" title={t('new.connect.succeeded')} />
  }
  return (
    <NotificationBar tone="error" title={t('new.connect.connectionFailed')}>
      {result.error_message ?? result.error_code ?? t('new.connect.testFailed')}
    </NotificationBar>
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

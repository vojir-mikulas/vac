import { useEffect, useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from '@tanstack/react-router'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AnimatePresence, m } from 'motion/react'
import {
  ArrowRight,
  Check,
  ChevronLeft,
  FileDown,
  Loader2,
  Lock,
  LockOpen,
  Plus,
  Rocket,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'

import { PageContainer } from '@/components/layout/app-shell'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Switch } from '@/components/ui/switch'
import { NotificationBar } from '@/components/common/notification-bar'
import { DeployKeyCard } from '@/features/app-detail/deploy-key-card'
import { BuildSourcePicker, type BuildSourceValue } from '@/features/apps/build-source'
import { appsApi, useCreateApp } from '@/lib/api/apps'
import { envApi, type EnvVarInput } from '@/lib/api/env'
import { deploymentsApi } from '@/lib/api/deployments'
import { ApiError } from '@/lib/api/client'
import { isSensitiveKey, isValidEnvKey, parseEnvEntries } from '@/lib/env-parse'
import { cn } from '@/lib/utils'
import { RISE, transition } from '@/lib/motion'
import type { App, CreateAppInput, TestConnectionResult } from '@/types/api'

// The wizard steps, by id; labels are translated at render (see Stepper).
const STEPS = ['source', 'build', 'domain', 'env', 'deploy'] as const

// A row in the wizard's env editor. All values are fresh here (no persisted
// secrets to carry forward), so the model is simpler than the app-detail Env tab:
// `sensitive` maps straight to a write-only secret on save.
interface WizardEnvRow {
  uid: number
  key: string
  value: string
  sensitive: boolean
}

let envUid = 0
const newEnvUid = () => ++envUid

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
  const [envRows, setEnvRows] = useState<WizardEnvRow[]>([])
  const [applyingEnv, setApplyingEnv] = useState(false)
  const [created, setCreated] = useState<App | null>(null)
  // For SSH repos, the first deploy can't clone until the operator adds the
  // deploy key to their Git host. A passing connection test is the only proof
  // the key is registered, so it gates the deploy button below.
  const [connectionOk, setConnectionOk] = useState(false)

  const goTo = (next: number) => setStepState([next, next > step ? 1 : -1])

  const effectiveName = name.trim() || repoName(gitUrl)
  const ssh = isSshUrl(gitUrl)
  const canContinue = step === 0 ? Boolean(gitUrl.trim() && effectiveName) : true

  // Once the operator reaches the build step we probe the repo (keyless clone)
  // for a compose file, so we can pre-fill the path and badge the compose card.
  // Like the .env.example probe this only reaches public repos; a private repo's
  // deploy key doesn't exist yet, so it fails quietly and the path stays manual.
  const detectCompose = useQuery({
    queryKey: ['detect-compose', gitUrl.trim(), branch.trim() || 'main'],
    queryFn: () =>
      appsApi.detectCompose({ git_url: gitUrl.trim(), git_branch: branch.trim() || 'main' }),
    enabled: step >= 1 && Boolean(gitUrl.trim()),
    staleTime: Infinity,
    retry: false,
  })
  const detectedComposePath = detectCompose.data?.found ? detectCompose.data.path : undefined

  // Pre-fill the compose path once per detected value, and only when the operator
  // hasn't typed their own — clearing the field afterwards must not re-trigger it.
  const prefilledCompose = useRef<string | undefined>(undefined)
  useEffect(() => {
    if (!detectedComposePath || prefilledCompose.current === detectedComposePath) return
    prefilledCompose.current = detectedComposePath
    setBuild((b) =>
      b.build_config.composePath
        ? b
        : { ...b, build_config: { ...b.build_config, composePath: detectedComposePath } },
    )
  }, [detectedComposePath])

  const deployNow = useMutation({
    mutationFn: (appId: string) => deploymentsApi.trigger(appId),
    onSuccess: (_, appId) => {
      toast.success(t('new.toast.deployTriggered'))
      navigate({ to: '/apps/$appId/deploys', params: { appId } })
    },
    onError: (e) => toast.error(e.message),
  })

  // Collapse the editor rows into the env API's write shape: valid keys only,
  // last-write-wins on duplicates, and `sensitive` → an unrevealable write-only
  // secret (matching the Env tab's import semantics).
  const envInputs = (): EnvVarInput[] => {
    const out = new Map<string, EnvVarInput>()
    for (const r of envRows) {
      const key = r.key.trim()
      if (!isValidEnvKey(key)) continue
      out.set(key, {
        key,
        value: r.value,
        sensitive: r.sensitive,
        write_only: r.sensitive,
        keep: false,
      })
    }
    return [...out.values()]
  }

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
      onSuccess: async (app) => {
        // Apply env vars before the first deploy so the stack comes up with them
        // already set. A save failure aborts the deploy — better to surface it
        // than to deploy with missing config.
        const vars = envInputs()
        if (vars.length) {
          setApplyingEnv(true)
          try {
            await envApi.replace(app.id, vars)
          } catch (e) {
            toast.error(e instanceof Error ? e.message : t('new.env.saveFailed'))
            setApplyingEnv(false)
            // Surface the created state so the operator can retry the deploy.
            setCreated(app)
            return
          }
          setApplyingEnv(false)
        }
        toast.success(t('new.toast.created'))
        // When deploying immediately we redirect to the deploys page, so skip the
        // "created" review panel entirely — otherwise its connection-test card
        // flashes in for a frame before the navigation tears it back down.
        if (thenDeploy) deployNow.mutate(app.id)
        else setCreated(app)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  const busy = create.isPending || applyingEnv || deployNow.isPending

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
                <BuildSourcePicker
                  value={build}
                  onChange={setBuild}
                  detectedKind={detectedComposePath ? 'compose' : undefined}
                  detectedComposePath={detectedComposePath}
                />
              </div>
            ) : null}

            {step === 2 ? (
              <DomainStep domain={domain} setDomain={setDomain} fallback={effectiveName} />
            ) : null}

            {step === 3 ? (
              <EnvStep gitUrl={gitUrl} branch={branch} rows={envRows} setRows={setEnvRows} />
            ) : null}

            {step === 4 ? (
              <ReviewDeployStep
                app={created}
                name={effectiveName}
                gitUrl={gitUrl}
                branch={branch}
                build={build}
                domain={domain}
                envCount={envInputs().length}
                ssh={ssh}
                connectionOk={connectionOk}
                onConnectionResult={setConnectionOk}
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
                disabled={deployNow.isPending || (ssh && !connectionOk)}
                title={ssh && !connectionOk ? t('new.connect.deployLocked') : undefined}
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
              {/* SSH repos can't clone until the deploy key is on the host, so we
                  only create here and unlock deploy after the connection test. */}
              {ssh ? (
                <Button variant="brand" disabled={busy} onClick={() => createApp(false)}>
                  {create.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : (
                    <Check className="size-4" />
                  )}
                  {t('new.createOnly')}
                </Button>
              ) : (
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
              )}
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
    t('new.steps.env'),
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

// EnvStep lets the operator set environment variables before the first deploy.
// The headline affordance is "Load from .env.example": it asks the backend to
// clone the repo (keyless) and hand back the example file, which we parse here.
// That only reaches public repos — for a private repo the deploy key doesn't
// exist yet, so the probe fails and the operator falls back to paste / manual.
function EnvStep({
  gitUrl,
  branch,
  rows,
  setRows,
}: {
  gitUrl: string
  branch: string
  rows: WizardEnvRow[]
  setRows: React.Dispatch<React.SetStateAction<WizardEnvRow[]>>
}) {
  const { t } = useTranslation('apps')
  const [pasteOpen, setPasteOpen] = useState(false)

  // Merge parsed .env entries into the rows (last-wins on an existing key) and
  // return how many valid entries were applied, for the result message. When
  // `autoMark` is set, credential-looking keys are flagged as secret.
  const mergeText = (text: string, autoMark = true): number => {
    const entries = parseEnvEntries(text)
      .filter((e) => isValidEnvKey(e.key))
      .map((e) => ({ key: e.key, value: e.value, sensitive: autoMark && isSensitiveKey(e.key) }))
    setRows((rs) => {
      const out = [...rs]
      for (const e of entries) {
        const existing = out.find((r) => r.key === e.key)
        if (existing) {
          existing.value = e.value
          existing.sensitive = e.sensitive
        } else {
          out.push({ uid: newEnvUid(), key: e.key, value: e.value, sensitive: e.sensitive })
        }
      }
      return out
    })
    return entries.length
  }

  const load = useMutation({
    mutationFn: () =>
      appsApi.envExample({ git_url: gitUrl.trim(), git_branch: branch.trim() || 'main' }),
    onSuccess: (res) => {
      if (res.found && res.content) {
        const n = mergeText(res.content)
        toast.success(t('new.env.loaded', { count: n, file: res.file }))
      }
    },
  })

  const patch = (uid: number, next: Partial<WizardEnvRow>) =>
    setRows((rs) => rs.map((r) => (r.uid === uid ? { ...r, ...next } : r)))
  const remove = (uid: number) => setRows((rs) => rs.filter((r) => r.uid !== uid))
  const addRow = () =>
    setRows((rs) => [...rs, { uid: newEnvUid(), key: '', value: '', sensitive: false }])

  return (
    <div>
      <StepHeading title={t('new.env.title')} subtitle={t('new.env.subtitle')} />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          disabled={load.isPending || !gitUrl.trim()}
          onClick={() => load.mutate()}
        >
          {load.isPending ? (
            <Loader2 className="size-3.5 animate-spin" />
          ) : (
            <FileDown className="size-3.5" />
          )}
          {t('new.env.loadExample')}
        </Button>
        <Button variant="outline" size="sm" onClick={() => setPasteOpen((v) => !v)}>
          <Plus className="size-3.5" />
          {t('new.env.paste')}
        </Button>
        <Button variant="outline" size="sm" onClick={addRow}>
          <Plus className="size-3.5" />
          {t('new.env.addVariable')}
        </Button>
      </div>

      <AnimatePresence>
        {load.data && !load.data.found ? (
          <m.div
            key="load-result"
            initial={{ opacity: 0, y: RISE }}
            animate={{ opacity: 1, y: 0 }}
            exit={{ opacity: 0 }}
            transition={transition.base}
            className="mb-4"
          >
            {load.data.error_code === 'auth_failed' ? (
              <NotificationBar tone="info" title={t('new.env.unreadable.title')}>
                {t('new.env.unreadable.body')}
              </NotificationBar>
            ) : load.data.error_code ? (
              <NotificationBar tone="error" title={t('new.env.loadFailed')}>
                {load.data.error_message ?? null}
              </NotificationBar>
            ) : (
              <NotificationBar tone="info" title={t('new.env.notFound')} />
            )}
          </m.div>
        ) : load.error ? (
          <m.div
            key="load-error"
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            className="mb-4"
          >
            <NotificationBar tone="error" title={t('new.env.loadFailed')}>
              {load.error instanceof ApiError ? load.error.message : null}
            </NotificationBar>
          </m.div>
        ) : null}
      </AnimatePresence>

      {pasteOpen ? (
        <PasteEnvPanel
          onImport={(text, autoMark) => {
            const n = mergeText(text, autoMark)
            if (n > 0) toast.success(t('new.env.pasted', { count: n }))
            setPasteOpen(false)
          }}
          onCancel={() => setPasteOpen(false)}
        />
      ) : null}

      <Card className="gap-0 p-0">
        {rows.length === 0 ? (
          <p className="px-4 py-10 text-center text-sm text-muted-foreground">
            {t('new.env.empty')}
          </p>
        ) : (
          rows.map((row, i) => (
            <WizardEnvRowEditor
              key={row.uid}
              row={row}
              index={i + 1}
              divider={i > 0}
              onKey={(key) => patch(row.uid, { key })}
              onValue={(value) => patch(row.uid, { value })}
              onToggle={() => patch(row.uid, { sensitive: !row.sensitive })}
              onRemove={() => remove(row.uid)}
            />
          ))
        )}
      </Card>

      <p className="mt-3 text-2xs text-muted-foreground">{t('new.env.hint')}</p>
    </div>
  )
}

function WizardEnvRowEditor({
  row,
  index,
  divider,
  onKey,
  onValue,
  onToggle,
  onRemove,
}: {
  row: WizardEnvRow
  index: number
  divider: boolean
  onKey: (key: string) => void
  onValue: (value: string) => void
  onToggle: () => void
  onRemove: () => void
}) {
  const { t } = useTranslation('apps')
  const invalid = row.key.trim() !== '' && !isValidEnvKey(row.key.trim())
  return (
    <div
      className={cn(
        'grid grid-cols-[minmax(0,200px)_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5',
        divider && 'border-t',
      )}
    >
      <Input
        value={row.key}
        onChange={(e) => onKey(e.target.value)}
        placeholder={t('new.env.keyPlaceholder')}
        aria-label={t('new.env.keyAria', { index })}
        aria-invalid={invalid}
        spellCheck={false}
        className={cn('h-8 font-mono text-xs', invalid && 'border-err-border')}
      />
      <Input
        value={row.value}
        type={row.sensitive ? 'password' : 'text'}
        onChange={(e) => onValue(e.target.value)}
        placeholder={t('new.env.valuePlaceholder')}
        aria-label={t('new.env.valueAria', { index })}
        spellCheck={false}
        className="h-8 font-mono text-xs"
      />
      <div className="flex items-center gap-1 text-muted-foreground">
        <button
          type="button"
          title={row.sensitive ? t('new.env.makePlain') : t('new.env.makeSecret')}
          aria-label={row.sensitive ? t('new.env.makePlain') : t('new.env.makeSecret')}
          aria-pressed={row.sensitive}
          onClick={onToggle}
          className="grid size-7 place-items-center rounded-md transition-colors hover:bg-muted hover:text-foreground"
        >
          {row.sensitive ? (
            <Lock className="size-3.5 text-warn" />
          ) : (
            <LockOpen className="size-3.5" />
          )}
        </button>
        <button
          type="button"
          title={t('new.env.delete')}
          aria-label={t('new.env.delete')}
          onClick={onRemove}
          className="grid size-7 place-items-center rounded-md transition-colors hover:bg-muted hover:text-foreground"
        >
          <Trash2 className="size-3.5" />
        </button>
      </div>
    </div>
  )
}

function PasteEnvPanel({
  onImport,
  onCancel,
}: {
  onImport: (text: string, autoMark: boolean) => void
  onCancel: () => void
}) {
  const { t } = useTranslation('apps')
  const [text, setText] = useState('')
  const [autoMark, setAutoMark] = useState(true)
  const parsed = parseEnvEntries(text).filter((e) => isValidEnvKey(e.key))
  return (
    <Card className="mb-4 gap-3 p-5">
      <p className="text-sm text-muted-foreground">{t('new.env.pastePrompt')}</p>
      <Textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={t('new.env.pastePlaceholder')}
        className="min-h-32 font-mono text-xs"
        spellCheck={false}
      />
      <label className="flex items-center gap-2 text-sm">
        <Switch checked={autoMark} onCheckedChange={setAutoMark} />
        <span>{t('new.env.autoMark')}</span>
      </label>
      <div className="flex items-center justify-between">
        <span className="text-2xs text-muted-foreground">
          {t('new.env.pasteParsed', { count: parsed.length })}
        </span>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            {t('actions.cancel')}
          </Button>
          <Button
            variant="brand"
            size="sm"
            disabled={parsed.length === 0}
            onClick={() => onImport(text, autoMark)}
          >
            {t('new.env.pasteImport', { count: parsed.length })}
          </Button>
        </div>
      </div>
    </Card>
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
  envCount,
  ssh,
  connectionOk,
  onConnectionResult,
}: {
  app: App | null
  name: string
  gitUrl: string
  branch: string
  build: BuildSourceValue
  domain: string
  envCount: number
  ssh: boolean
  connectionOk: boolean
  onConnectionResult: (ok: boolean) => void
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
        {envCount > 0 ? (
          <ReviewLine k={t('new.review.env')} v={t('new.review.envCount', { count: envCount })} />
        ) : null}
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
            {ssh ? (
              <>
                <NotificationBar tone="info" title={t('new.notices.addKey.title')}>
                  {t('new.notices.addKey.body')}
                </NotificationBar>
                <DeployKeyCard appId={app.id} gitUrl={app.git_url} />
              </>
            ) : null}
            <ConnectionTest app={app} onResult={onConnectionResult} />
            {ssh && !connectionOk ? (
              <p className="text-2xs text-muted-foreground">{t('new.connect.deployLocked')}</p>
            ) : null}
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

function ConnectionTest({ app, onResult }: { app: App; onResult: (ok: boolean) => void }) {
  const { t } = useTranslation('apps')
  const test = useMutation({
    mutationFn: () => appsApi.testConnection(app.id),
    onSuccess: (res) => onResult(res.ok),
    onError: () => onResult(false),
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

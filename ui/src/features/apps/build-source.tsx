import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Box, FileCode2, Layers, Sparkles, Wand2 } from 'lucide-react'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import type { BuildConfig, BuildKind } from '@/types/api'

export interface BuildSourceValue {
  build_kind: BuildKind
  build_config: BuildConfig
}

// `satisfies` keeps `kind` inferred as the literal union, so the
// `buildSource.kinds.<kind>.*` translation keys below stay type-checked.
const KIND_OPTIONS = [
  { kind: 'auto', icon: Wand2 },
  { kind: 'compose', icon: Layers },
  { kind: 'dockerfile', icon: FileCode2 },
  { kind: 'framework', icon: Sparkles },
  { kind: 'static', icon: Box },
] satisfies { kind: BuildKind; icon: typeof Box }[]

// Frameworks: React works today; the rest are scaffolded as "coming soon".
const FRAMEWORKS: { id: string; label: string; soon?: boolean }[] = [
  { id: 'react', label: 'React' },
  { id: 'nextjs', label: 'Next.js', soon: true },
  { id: 'astro', label: 'Astro', soon: true },
  { id: 'vite', label: 'Vite', soon: true },
  { id: 'node', label: 'Node', soon: true },
  { id: 'python', label: 'Python', soon: true },
]

export function BuildSourcePicker({
  value,
  onChange,
  detectedKind,
}: {
  value: BuildSourceValue
  onChange: (v: BuildSourceValue) => void
  /** When set, the matching kind card shows a "detected" badge. */
  detectedKind?: BuildKind
}) {
  const { t } = useTranslation('apps')
  const setKind = (kind: BuildKind) => onChange({ ...value, build_kind: kind })
  const setConfig = (patch: Partial<BuildConfig>) =>
    onChange({ ...value, build_config: { ...value.build_config, ...patch } })
  const cfg = value.build_config

  return (
    <div className="flex flex-col gap-5">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {KIND_OPTIONS.map((opt) => {
          const active = value.build_kind === opt.kind
          const Icon = opt.icon
          return (
            <button
              key={opt.kind}
              type="button"
              onClick={() => setKind(opt.kind)}
              className={cn(
                'flex cursor-pointer flex-col gap-1 rounded-lg border p-3 text-left transition-colors',
                active ? 'border-brand bg-brand/5' : 'hover:bg-surface-2',
              )}
            >
              <div className="flex items-center gap-2">
                <Icon className={cn('size-4', active ? 'text-brand' : 'text-muted-foreground')} />
                <span className="text-sm font-medium">
                  {t(`buildSource.kinds.${opt.kind}.label`)}
                </span>
                {detectedKind === opt.kind ? (
                  <span className="rounded-full border border-ok-border bg-ok-bg px-1.5 py-0.5 text-2xs font-medium text-ok-foreground">
                    {t('buildSource.detected')}
                  </span>
                ) : null}
              </div>
              <span className="text-2xs text-muted-foreground">
                {t(`buildSource.kinds.${opt.kind}.hint`)}
              </span>
            </button>
          )
        })}
      </div>

      {value.build_kind === 'auto' ? (
        <p className="rounded-md border bg-surface-1 px-3 py-2 text-xs text-muted-foreground">
          {t('buildSource.autoNote')}
        </p>
      ) : null}

      {value.build_kind === 'compose' ? (
        <Field label={t('buildSource.composePath')} hint={t('buildSource.relativeToRoot')}>
          <Input
            value={cfg.composePath ?? ''}
            onChange={(e) => setConfig({ composePath: e.target.value })}
            placeholder="compose.yaml"
            className="font-mono text-xs"
          />
        </Field>
      ) : null}

      {value.build_kind === 'dockerfile' ? (
        <Field label={t('buildSource.dockerfilePath')} hint={t('buildSource.relativeToRoot')}>
          <Input
            value={cfg.dockerfilePath ?? ''}
            onChange={(e) => setConfig({ dockerfilePath: e.target.value })}
            placeholder="Dockerfile"
            className="font-mono text-xs"
          />
        </Field>
      ) : null}

      {value.build_kind === 'framework' ? (
        <div className="flex flex-col gap-5">
          <Field label={t('buildSource.framework')}>
            <div className="grid grid-cols-3 gap-2">
              {FRAMEWORKS.map((f) => {
                const active = (cfg.framework ?? 'react') === f.id
                return (
                  <button
                    key={f.id}
                    type="button"
                    disabled={f.soon}
                    onClick={() => setConfig({ framework: f.id })}
                    className={cn(
                      'rounded-lg border px-3 py-2 text-center text-xs font-medium transition-colors',
                      f.soon && 'cursor-not-allowed opacity-50',
                      !f.soon && 'cursor-pointer',
                      active && !f.soon
                        ? 'border-brand bg-brand/5 text-brand'
                        : 'hover:bg-surface-2',
                    )}
                  >
                    {f.label}
                    {f.soon ? (
                      <span className="ml-1 text-2xs text-muted-foreground">
                        {t('buildSource.soon')}
                      </span>
                    ) : null}
                  </button>
                )
              })}
            </div>
          </Field>
          <Field label={t('buildSource.buildCommand')} hint={t('buildSource.buildCommandHint')}>
            <Input
              value={cfg.buildCommand ?? ''}
              onChange={(e) => setConfig({ buildCommand: e.target.value })}
              placeholder="npm install && npm run build"
              className="font-mono text-xs"
            />
          </Field>
        </div>
      ) : null}

      {value.build_kind === 'static' ? (
        <div className="flex flex-col gap-5">
          <Field label={t('buildSource.outputDir')} hint={t('buildSource.outputDirHint')}>
            <Input
              value={cfg.staticDir ?? ''}
              onChange={(e) => setConfig({ staticDir: e.target.value })}
              placeholder="dist"
              className="font-mono text-xs"
            />
          </Field>
          <label className="flex items-center justify-between gap-3">
            <span className="text-sm">
              {t('buildSource.spaFallback')}
              <span className="block text-2xs text-muted-foreground">
                {t('buildSource.spaFallbackHint')}
              </span>
            </span>
            <Switch
              checked={cfg.spaFallback ?? false}
              onCheckedChange={(v) => setConfig({ spaFallback: v })}
            />
          </label>
        </div>
      ) : null}
    </div>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="grid gap-2">
      <Label>{label}</Label>
      {children}
      {hint ? <p className="text-2xs text-muted-foreground">{hint}</p> : null}
    </div>
  )
}

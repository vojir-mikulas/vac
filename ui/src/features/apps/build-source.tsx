import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { AnimatePresence, m } from 'motion/react'
import { Box, Check, FileCode2, Layers, Sparkles, Wand2 } from 'lucide-react'
import { type IconType } from 'react-icons'
import { SiAstro, SiNextdotjs, SiNodedotjs, SiPython, SiReact, SiVite } from 'react-icons/si'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { cn } from '@/lib/utils'
import { RISE, transition } from '@/lib/motion'
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

// Frameworks VAC can build today. Each carries a react-icons brand glyph + its
// brand color for the picker. The `id` is the key the framework adapter expects.
const FRAMEWORKS: { id: string; label: string; icon: IconType; color: string }[] = [
  { id: 'react', label: 'React', icon: SiReact, color: '#61DAFB' },
  { id: 'nextjs', label: 'Next.js', icon: SiNextdotjs, color: '#888888' },
  { id: 'astro', label: 'Astro', icon: SiAstro, color: '#FF5D01' },
  { id: 'vite', label: 'Vite', icon: SiVite, color: '#646CFF' },
  { id: 'node', label: 'Node', icon: SiNodedotjs, color: '#5FA04E' },
  { id: 'python', label: 'Python', icon: SiPython, color: '#3776AB' },
]

export function BuildSourcePicker({
  value,
  onChange,
  detectedKind,
  detectedComposePath,
  detectedFramework,
}: {
  value: BuildSourceValue
  onChange: (v: BuildSourceValue) => void
  /** When set, the matching kind card shows a "detected" badge. */
  detectedKind?: BuildKind
  /** Compose filename found by probing the repo; surfaced as a hint under the
   *  compose path input so the operator knows the value came from their repo. */
  detectedComposePath?: string
  /** Framework auto-detected from the repo (only when it has no compose file or
   *  Dockerfile); surfaced as a banner so the operator sees what VAC will build. */
  detectedFramework?: string
}) {
  const { t } = useTranslation('apps')
  const setKind = (kind: BuildKind) => onChange({ ...value, build_kind: kind })
  const setConfig = (patch: Partial<BuildConfig>) =>
    onChange({ ...value, build_config: { ...value.build_config, ...patch } })
  const cfg = value.build_config
  const detectedLabel = FRAMEWORKS.find((f) => f.id === detectedFramework)?.label

  return (
    <div className="flex flex-col gap-5">
      {detectedLabel ? (
        <div className="flex items-center gap-2 rounded-md border border-ok-border bg-ok-bg px-3 py-2 text-xs text-ok-foreground">
          <Sparkles className="size-3.5 shrink-0" aria-hidden />
          <span>{t('buildSource.frameworkDetected', { name: detectedLabel })}</span>
        </div>
      ) : null}
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {KIND_OPTIONS.map((opt) => {
          const active = value.build_kind === opt.kind
          const Icon = opt.icon
          return (
            <m.button
              key={opt.kind}
              type="button"
              onClick={() => setKind(opt.kind)}
              whileTap={{ scale: 0.98 }}
              transition={transition.fast}
              className={cn(
                'relative flex cursor-pointer flex-col gap-1 rounded-lg border p-3 text-left transition-colors',
                active ? 'border-brand bg-brand/5' : 'hover:bg-surface-2',
              )}
            >
              <AnimatePresence>
                {active ? (
                  <m.span
                    initial={{ opacity: 0, scale: 0.6 }}
                    animate={{ opacity: 1, scale: 1 }}
                    exit={{ opacity: 0, scale: 0.6 }}
                    transition={transition.fast}
                    className="absolute top-2 right-2 grid size-4 place-items-center rounded-full bg-brand text-brand-foreground"
                  >
                    <Check className="size-2.5" strokeWidth={3} />
                  </m.span>
                ) : null}
              </AnimatePresence>
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
            </m.button>
          )
        })}
      </div>

      {/* Animate the per-kind config panel so switching builds cross-fades the
          inputs in instead of snapping. Keyed by kind; `mode="wait"` lets the
          outgoing panel leave before the new one settles its height. */}
      <AnimatePresence mode="wait" initial={false}>
        <m.div
          key={value.build_kind}
          initial={{ opacity: 0, y: RISE }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -RISE }}
          transition={transition.base}
        >
          {value.build_kind === 'auto' ? (
            <p className="rounded-md border bg-surface-1 px-3 py-2 text-xs text-muted-foreground">
              {t('buildSource.autoNote')}
            </p>
          ) : null}

          {value.build_kind === 'compose' ? (
            <Field
              label={t('buildSource.composePath')}
              hint={
                detectedComposePath
                  ? t('buildSource.composeDetected', { path: detectedComposePath })
                  : t('buildSource.relativeToRoot')
              }
            >
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
                    const Icon = f.icon
                    return (
                      <m.button
                        key={f.id}
                        type="button"
                        onClick={() => setConfig({ framework: f.id })}
                        whileTap={{ scale: 0.97 }}
                        transition={transition.fast}
                        className={cn(
                          'flex cursor-pointer flex-col items-center gap-1.5 rounded-lg border px-3 py-3 text-center text-xs font-medium transition-colors',
                          active ? 'border-brand bg-brand/5 text-brand' : 'hover:bg-surface-2',
                        )}
                      >
                        <Icon
                          className="size-5"
                          style={{ color: active ? undefined : f.color }}
                          aria-hidden
                        />
                        <span>{f.label}</span>
                      </m.button>
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
        </m.div>
      </AnimatePresence>
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

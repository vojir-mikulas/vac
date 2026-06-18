import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { SectionHeader } from '@/components/common/section-header'
import { useDeployWindow, useSaveDeployWindow, type DeployWindow } from '@/lib/api/deploy-window'

// Mon-first display order; value is the time.Weekday number (0=Sun…6=Sat).
const DAYS = [
  { value: 1, key: 'mon' },
  { value: 2, key: 'tue' },
  { value: 3, key: 'wed' },
  { value: 4, key: 'thu' },
  { value: 5, key: 'fri' },
  { value: 6, key: 'sat' },
  { value: 0, key: 'sun' },
] as const

function browserTZ(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function DeployWindowSection({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data } = useDeployWindow(appId)
  const save = useSaveDeployWindow(appId)

  // Draft overlay over the server value (no seeding effect — avoids the
  // cascading-render lint).
  const [draft, setDraft] = useState<DeployWindow[] | null>(null)
  const windows = draft ?? data?.windows ?? []
  const dirty = draft !== null

  const edit = (fn: (ws: DeployWindow[]) => DeployWindow[]) =>
    setDraft(fn(draft ?? data?.windows ?? []))

  const addWindow = () =>
    edit((ws) => [...ws, { days: [], start: '09:00', end: '17:00', tz: browserTZ() }])
  const removeWindow = (i: number) => edit((ws) => ws.filter((_, idx) => idx !== i))
  const patchWindow = (i: number, patch: Partial<DeployWindow>) =>
    edit((ws) => ws.map((w, idx) => (idx === i ? { ...w, ...patch } : w)))
  const toggleDay = (i: number, day: number) =>
    edit((ws) =>
      ws.map((w, idx) =>
        idx === i
          ? {
              ...w,
              days: w.days.includes(day)
                ? w.days.filter((d) => d !== day)
                : [...w.days, day].sort((a, b) => a - b),
            }
          : w,
      ),
    )

  const doSave = () =>
    save.mutate(windows, {
      onSuccess: () => {
        setDraft(null)
        toast.success(t('deployWindow.saved'))
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <section>
      <SectionHeader>{t('deployWindow.title')}</SectionHeader>
      <Card className="gap-4 p-5">
        <p className="text-xs text-muted-foreground">{t('deployWindow.intro')}</p>

        {windows.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t('deployWindow.alwaysAllowed')}</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {windows.map((w, i) => (
              <li key={i} className="grid gap-3 rounded-md border bg-muted/30 p-3">
                <div className="flex flex-wrap gap-1.5">
                  {DAYS.map((d) => (
                    <button
                      key={d.value}
                      type="button"
                      aria-pressed={w.days.includes(d.value)}
                      onClick={() => toggleDay(i, d.value)}
                      className={`rounded-md border px-2 py-1 text-2xs font-medium transition-colors ${
                        w.days.includes(d.value)
                          ? 'border-brand bg-brand/10 text-brand'
                          : 'text-muted-foreground hover:bg-muted'
                      }`}
                    >
                      {t(`deployWindow.days.${d.key}`)}
                    </button>
                  ))}
                  <span className="self-center text-2xs text-muted-foreground">
                    {w.days.length === 0 ? t('deployWindow.everyDay') : ''}
                  </span>
                </div>
                <div className="flex flex-wrap items-end gap-3">
                  <div className="grid gap-1">
                    <Label className="text-2xs text-muted-foreground">
                      {t('deployWindow.start')}
                    </Label>
                    <Input
                      type="time"
                      value={w.start}
                      onChange={(e) => patchWindow(i, { start: e.target.value })}
                      className="w-32 font-mono text-xs"
                    />
                  </div>
                  <div className="grid gap-1">
                    <Label className="text-2xs text-muted-foreground">
                      {t('deployWindow.end')}
                    </Label>
                    <Input
                      type="time"
                      value={w.end}
                      onChange={(e) => patchWindow(i, { end: e.target.value })}
                      className="w-32 font-mono text-xs"
                    />
                  </div>
                  <div className="grid flex-1 gap-1">
                    <Label className="text-2xs text-muted-foreground">{t('deployWindow.tz')}</Label>
                    <Input
                      value={w.tz}
                      placeholder="UTC"
                      onChange={(e) => patchWindow(i, { tz: e.target.value })}
                      className="font-mono text-xs"
                    />
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t('deployWindow.remove')}
                    onClick={() => removeWindow(i)}
                  >
                    <Trash2 className="size-3.5" />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={addWindow}>
            <Plus className="size-4" />
            {t('deployWindow.addWindow')}
          </Button>
          <div className="flex-1" />
          <Button variant="brand" size="sm" disabled={save.isPending || !dirty} onClick={doSave}>
            {t('deployWindow.save')}
          </Button>
        </div>
      </Card>
    </section>
  )
}

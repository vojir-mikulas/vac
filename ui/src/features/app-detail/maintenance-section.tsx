import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Badge } from '@/components/ui/badge'
import { SectionHeader } from '@/components/common/section-header'
import {
  useMaintenance,
  useMaintenancePage,
  useResetMaintenancePage,
  useSaveMaintenancePage,
  useSetMaintenance,
} from '@/lib/api/maintenance'

// 64 KB cap — mirrors maintenance.MaxHTMLBytes on the backend (the page rides in
// Caddy's in-memory config, so it must stay bounded).
const MAX_HTML_BYTES = 64 * 1024

export function MaintenanceSection({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: state } = useMaintenance(appId)
  const setMaintenance = useSetMaintenance(appId)

  const enabled = state?.enabled ?? false
  const auto = state?.auto ?? false

  const toggle = (next: { enabled?: boolean; auto?: boolean }) =>
    setMaintenance.mutate(
      { enabled: next.enabled ?? enabled, auto: next.auto ?? auto },
      { onError: (e) => toast.error(e.message) },
    )

  return (
    <section>
      <SectionHeader>{t('maintenance.title')}</SectionHeader>
      <Card className="gap-5 p-5">
        <p className="text-xs text-muted-foreground">{t('maintenance.intro')}</p>

        <div className="flex items-center justify-between gap-4">
          <div className="grid gap-0.5">
            <div className="flex items-center gap-2">
              <Label htmlFor="maintenance-enabled">{t('maintenance.enable')}</Label>
              {state?.active ? (
                <Badge variant="secondary" className="uppercase">
                  {t('maintenance.activeBadge')}
                </Badge>
              ) : null}
            </div>
            <p className="text-xs text-muted-foreground">{t('maintenance.enableHint')}</p>
          </div>
          <Switch
            id="maintenance-enabled"
            checked={enabled}
            disabled={setMaintenance.isPending}
            onCheckedChange={(v) => toggle({ enabled: v })}
          />
        </div>

        <div className="flex items-center justify-between gap-4">
          <div className="grid gap-0.5">
            <Label htmlFor="maintenance-auto">{t('maintenance.auto')}</Label>
            <p className="text-xs text-muted-foreground">{t('maintenance.autoHint')}</p>
          </div>
          <Switch
            id="maintenance-auto"
            checked={auto}
            disabled={setMaintenance.isPending}
            onCheckedChange={(v) => toggle({ auto: v })}
          />
        </div>

        <div className="border-t" />

        <MaintenancePageEditor appId={appId} />
      </Card>
    </section>
  )
}

function MaintenancePageEditor({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: page } = useMaintenancePage(appId)
  const save = useSaveMaintenancePage(appId)
  const reset = useResetMaintenancePage(appId)

  // Draft overlay over the server value: null = no unsaved edits (show the
  // fetched page), a string = the operator's in-progress edits. This avoids
  // seeding editor state from an effect (cascading-render lint).
  const [draft, setDraft] = useState<string | null>(null)
  const html = draft ?? page?.html ?? ''
  const dirty = draft !== null

  const bytes = new TextEncoder().encode(html).length
  const tooLarge = bytes > MAX_HTML_BYTES

  const doSave = () => {
    if (tooLarge) {
      toast.error(t('maintenance.pageTooLarge'))
      return
    }
    save.mutate(html, {
      onSuccess: () => {
        setDraft(null)
        toast.success(t('maintenance.pageSaved'))
      },
      onError: (e) => toast.error(e.message),
    })
  }

  const doReset = () =>
    reset.mutate(undefined, {
      onSuccess: () => {
        setDraft(null)
        toast.success(t('maintenance.pageReset'))
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="grid gap-3">
      <div className="flex items-center justify-between">
        <Label>{t('maintenance.pageLabel')}</Label>
        {page?.is_default ? (
          <span className="text-2xs text-muted-foreground">{t('maintenance.pageDefault')}</span>
        ) : (
          <span className="text-2xs text-brand">{t('maintenance.pageCustom')}</span>
        )}
      </div>
      <p className="text-xs text-muted-foreground">{t('maintenance.pageHint')}</p>

      <div className="grid gap-3 lg:grid-cols-2">
        <Textarea
          aria-label={t('maintenance.pageLabel')}
          value={html}
          spellCheck={false}
          onChange={(e) => setDraft(e.target.value)}
          className="h-72 resize-y font-mono text-xs"
        />
        {/* Sandboxed preview: srcdoc with no allow-* tokens, so the operator's
            HTML can't run scripts or navigate the dashboard. */}
        <div className="overflow-hidden rounded-md border bg-background">
          <div className="border-b bg-muted/40 px-3 py-1.5 text-2xs text-muted-foreground">
            {t('maintenance.preview')}
          </div>
          <iframe
            title={t('maintenance.preview')}
            sandbox=""
            srcDoc={html}
            className="h-[17rem] w-full bg-white"
          />
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span className={`text-2xs ${tooLarge ? 'text-err' : 'text-muted-foreground'}`}>
          {t('maintenance.pageSize', { kb: (bytes / 1024).toFixed(1) })}
        </span>
        <div className="flex-1" />
        <Button
          variant="outline"
          size="sm"
          disabled={reset.isPending || page?.is_default}
          onClick={doReset}
        >
          {t('maintenance.resetDefault')}
        </Button>
        <Button
          variant="brand"
          size="sm"
          disabled={save.isPending || tooLarge || !dirty}
          onClick={doSave}
        >
          {t('maintenance.savePage')}
        </Button>
      </div>
    </div>
  )
}

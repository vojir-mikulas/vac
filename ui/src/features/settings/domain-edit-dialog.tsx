import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useApps } from '@/lib/api/apps'
import { useServices } from '@/lib/api/services'
import { useUpdateDomain, type DomainAssignment } from '@/lib/api/domains'
import { isValidHostname } from '@/lib/hostname'
import { cn } from '@/lib/utils'
import type { Domain } from '@/types/api'

const selectClass =
  'h-9 rounded-md border border-input bg-transparent px-3 text-sm outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50'

/**
 * Rename a custom domain and/or re-point it to another service or app — an
 * in-place route swap (plan 09 Phase 2), no downtime, no cert re-issue.
 */
export function DomainEditDialog({ domain, onClose }: { domain: Domain; onClose: () => void }) {
  const { t } = useTranslation('settings')
  const { data: apps } = useApps()
  const [hostname, setHostname] = useState(domain.hostname)
  const [appId, setAppId] = useState(domain.app_id)
  const [service, setService] = useState(domain.service_name)
  const [redirectTo, setRedirectTo] = useState(domain.redirect_to ?? '')
  const { data: services } = useServices(appId)
  const update = useUpdateDomain()

  const hostnameValid = isValidHostname(hostname)
  const assignmentValid = (appId === '') === (service === '')
  // A redirect needs the domain assigned to an app and a different target.
  const redirectValid =
    !redirectTo.trim() || (appId !== '' && redirectTo.trim() !== hostname.trim())
  const canSave = hostnameValid && assignmentValid && redirectValid && !update.isPending

  const onSave = () => {
    const assign: DomainAssignment =
      appId && service ? { app_id: appId, service_name: service } : { app_id: '', service_name: '' }
    update.mutate(
      {
        id: domain.id,
        body: { hostname: hostname.trim(), redirect_to: redirectTo.trim(), ...assign },
      },
      {
        onSuccess: () => {
          toast.success(t('domains.edit.toast.updated'))
          onClose()
        },
        onError: (e) => toast.error(e.message),
      },
    )
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('domains.edit.title')}</DialogTitle>
          <DialogDescription>{t('domains.edit.description')}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4 py-2">
          <div className="grid gap-2">
            <Label>{t('domains.fields.hostname')}</Label>
            <Input
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
              aria-invalid={hostname.trim() !== '' && !hostnameValid}
              className={cn(
                'font-mono text-xs',
                hostname.trim() !== '' && !hostnameValid && 'border-err-border',
              )}
            />
            {hostname.trim() !== '' && !hostnameValid ? (
              <p className="text-2xs text-err-foreground">{t('domains.add.invalidHostname')}</p>
            ) : null}
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label>{t('domains.fields.app')}</Label>
              <select
                className={selectClass}
                value={appId}
                onChange={(e) => {
                  setAppId(e.target.value)
                  setService('')
                }}
              >
                <option value="">{t('domains.fields.unassigned')}</option>
                {(apps ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="grid gap-2">
              <Label>{t('domains.fields.service')}</Label>
              <select
                className={selectClass}
                value={service}
                onChange={(e) => setService(e.target.value)}
                disabled={!appId}
              >
                <option value="">{t('domains.fields.selectService')}</option>
                {(services ?? []).map((s) => (
                  <option key={s.id} value={s.name}>
                    {s.name}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="grid gap-2">
            <Label>{t('domains.edit.redirectLabel')}</Label>
            <Input
              value={redirectTo}
              onChange={(e) => setRedirectTo(e.target.value)}
              placeholder="example.com"
              className="font-mono text-xs"
            />
            <p className="text-2xs text-muted-foreground">
              <Trans
                t={t}
                i18nKey="domains.edit.redirectHint"
                components={[<span className="font-mono" />]}
              />
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={onClose}>
            {t('domains.edit.cancel')}
          </Button>
          <Button variant="brand" size="sm" disabled={!canSave} onClick={onSave}>
            {t('domains.edit.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

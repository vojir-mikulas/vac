import { ShieldCheck, Upload } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useClearDomainCert, useUploadDomainCert } from '@/lib/api/domains'
import { relativeTime } from '@/lib/format'
import type { Domain } from '@/types/api'

/**
 * Bring-your-own TLS certificate controls for one domain (plan B). Lets the
 * operator upload a cert+key for a host where ACME can't work (wildcard /
 * internal), or revert an uploaded host back to automatic HTTPS. Step-up 2FA is
 * enforced server-side and handled transparently by the global client.
 */
export function DomainCertPanel({ domain }: { domain: Domain }) {
  const { t } = useTranslation('settings')
  const certId = useId()
  const keyId = useId()
  const upload = useUploadDomainCert()
  const clear = useClearDomainCert()
  const uploaded = domain.tls_cert_source === 'uploaded'
  const [open, setOpen] = useState(false)
  const [certPem, setCertPem] = useState('')
  const [keyPem, setKeyPem] = useState('')

  function submit() {
    upload.mutate(
      { id: domain.id, certPem, keyPem },
      {
        onSuccess: (meta) => {
          toast.success(t('domains.cert.toast.uploaded', { hostname: domain.hostname }))
          if (meta.self_signed) toast.warning(t('domains.cert.selfSignedWarn'))
          setCertPem('')
          setKeyPem('')
          setOpen(false)
        },
      },
    )
  }

  function revert() {
    clear.mutate(domain.id, {
      onSuccess: () =>
        toast.success(t('domains.cert.toast.cleared', { hostname: domain.hostname })),
    })
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border bg-surface-1 p-3 text-sm">
      <div className="flex items-center gap-2 font-medium">
        <ShieldCheck className="size-4 text-muted-foreground" />
        {t('domains.cert.title')}
      </div>
      <p className="text-xs text-muted-foreground">
        {uploaded ? t('domains.cert.uploaded') : t('domains.cert.acme')}
        {uploaded && domain.tls_cert_uploaded_at
          ? ` · ${t('domains.cert.uploadedAt', { time: relativeTime(domain.tls_cert_uploaded_at) })}`
          : ''}
      </p>

      {!open ? (
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
            <Upload className="size-3.5" />
            {uploaded ? t('domains.cert.replace') : t('domains.cert.upload')}
          </Button>
          {uploaded ? (
            <Button
              variant="ghost"
              size="sm"
              className="text-danger-foreground"
              disabled={clear.isPending}
              onClick={revert}
            >
              {clear.isPending ? t('domains.cert.clearing') : t('domains.cert.clear')}
            </Button>
          ) : null}
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <p className="text-2xs text-muted-foreground">{t('domains.cert.byoIntro')}</p>
          <div className="flex flex-col gap-1">
            <Label htmlFor={certId} className="text-xs">
              {t('domains.cert.certLabel')}
            </Label>
            <Textarea
              id={certId}
              rows={4}
              className="font-mono text-2xs"
              placeholder={t('domains.cert.certPlaceholder')}
              value={certPem}
              onChange={(e) => setCertPem(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor={keyId} className="text-xs">
              {t('domains.cert.keyLabel')}
            </Label>
            <Textarea
              id={keyId}
              rows={4}
              className="font-mono text-2xs"
              placeholder={t('domains.cert.keyPlaceholder')}
              value={keyPem}
              onChange={(e) => setKeyPem(e.target.value)}
            />
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              disabled={upload.isPending || !certPem.trim() || !keyPem.trim()}
              onClick={submit}
            >
              {upload.isPending ? t('domains.cert.uploading') : t('domains.cert.upload')}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
              {t('domains.edit.cancel')}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

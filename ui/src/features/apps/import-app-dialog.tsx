import { useState, type ChangeEvent } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Upload } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { useImportApp } from '@/lib/api/portability'

const placeholder = `apiVersion: vac/v1
kind: App
metadata:
  name: My App
source:
  type: git
  url: git@github.com:me/app.git
build:
  kind: compose`

/**
 * Import an app from a portable vac.app.yaml spec — paste or upload, no wizard
 * (plan 18). Importing a spec whose slug already exists updates that app in
 * place. Sensitive env values aren't carried; the operator re-enters them after.
 */
export function ImportAppDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation('apps')
  const [spec, setSpec] = useState('')
  const navigate = useNavigate()
  const importApp = useImportApp()

  const onFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = '' // allow re-selecting the same file
    if (file) setSpec(await file.text())
  }

  const onImport = () => {
    importApp.mutate(spec, {
      onSuccess: (res) => {
        toast.success(
          res.created
            ? t('import.toast.imported', { slug: res.slug })
            : t('import.toast.updated', { slug: res.slug }),
        )
        if (res.secrets_needed?.length) {
          toast.warning(t('import.toast.reenterSecrets', { values: res.secrets_needed.join(', ') }))
        }
        onOpenChange(false)
        setSpec('')
        navigate({ to: '/apps/$appId', params: { appId: res.app_id } })
      },
      onError: (e) => toast.error(e.message),
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t('import.title')}</DialogTitle>
          <DialogDescription>
            <Trans
              t={t}
              i18nKey="import.description"
              components={[<span className="font-mono" />]}
            />
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2 py-2">
          <div className="flex items-center justify-between">
            <Label htmlFor="spec">{t('import.spec')}</Label>
            <label className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">
              <input type="file" accept=".yaml,.yml,.txt" className="hidden" onChange={onFile} />
              {t('import.upload')}
            </label>
          </div>
          <Textarea
            id="spec"
            value={spec}
            onChange={(e) => setSpec(e.target.value)}
            placeholder={placeholder}
            spellCheck={false}
            className="h-72 font-mono text-xs"
          />
        </div>
        <DialogFooter>
          <Button variant="ghost" size="sm" onClick={() => onOpenChange(false)}>
            {t('import.cancel')}
          </Button>
          <Button
            variant="brand"
            size="sm"
            disabled={!spec.trim() || importApp.isPending}
            onClick={onImport}
          >
            <Upload className="size-4" />
            {t('import.import')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

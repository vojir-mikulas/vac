import { useState, type ChangeEvent } from 'react'
import { useNavigate } from '@tanstack/react-router'
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
        toast.success(`${res.created ? 'Imported' : 'Updated'} ${res.slug}`)
        if (res.secrets_needed?.length) {
          toast.warning(`Re-enter secret values: ${res.secrets_needed.join(', ')}`)
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
          <DialogTitle>Import app</DialogTitle>
          <DialogDescription>
            Paste a <span className="font-mono">vac.app.yaml</span> spec (or upload one) to create
            or update an app. Re-importing the same slug updates it in place. Sensitive env values
            aren&apos;t carried — you re-enter them afterwards.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-2 py-2">
          <div className="flex items-center justify-between">
            <Label htmlFor="spec">Spec</Label>
            <label className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">
              <input type="file" accept=".yaml,.yml,.txt" className="hidden" onChange={onFile} />
              Upload file…
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
            Cancel
          </Button>
          <Button
            variant="brand"
            size="sm"
            disabled={!spec.trim() || importApp.isPending}
            onClick={onImport}
          >
            <Upload className="size-4" />
            Import
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

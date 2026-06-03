import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { KeyRound } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { CopyButton } from '@/components/common/copy-button'
import { authApi, useApiTokens } from '@/lib/api/auth'
import { queryKeys } from '@/lib/query/keys'
import { relativeTime } from '@/lib/format'

export function ApiTokensSection() {
  const { t } = useTranslation('settings')
  const { data: tokens, isLoading } = useApiTokens()
  const qc = useQueryClient()

  const revoke = useMutation({
    mutationFn: (id: string) => authApi.revokeApiToken(id),
    onSuccess: () => {
      toast.success(t('apiTokens.toast.revoked'))
      qc.invalidateQueries({ queryKey: queryKeys.auth.apiTokens })
    },
    onError: (e) => toast.error(e.message),
  })

  return (
    <section>
      <SectionHeader action={<CreateTokenDialog />}>{t('apiTokens.heading')}</SectionHeader>
      <Card className="gap-0 p-0">
        {isLoading ? (
          <div className="p-5">
            <Skeleton className="h-16 w-full" />
          </div>
        ) : tokens && tokens.length > 0 ? (
          tokens.map((token, i) => (
            <div
              key={token.id}
              className={`flex items-center gap-3 px-5 py-3.5 ${i > 0 ? 'border-t' : ''}`}
            >
              <KeyRound className="size-4 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{token.name}</div>
                <div className="font-mono text-2xs text-muted-foreground">
                  {t('apiTokens.created', { time: relativeTime(token.created_at) })} ·{' '}
                  {token.last_used_at
                    ? t('apiTokens.lastUsed', { time: relativeTime(token.last_used_at) })
                    : t('apiTokens.neverUsed')}
                  {token.expires_at
                    ? ` · ${t('apiTokens.expires', { time: relativeTime(token.expires_at) })}`
                    : ''}
                </div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                disabled={revoke.isPending}
                onClick={() => revoke.mutate(token.id)}
              >
                {t('apiTokens.revoke')}
              </Button>
            </div>
          ))
        ) : (
          <p className="px-5 py-6 text-center text-sm text-muted-foreground">
            {t('apiTokens.empty')}
          </p>
        )}
      </Card>
    </section>
  )
}

function CreateTokenDialog() {
  const { t } = useTranslation('settings')
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState('')
  const [days, setDays] = useState('90')
  const [token, setToken] = useState<string | null>(null)

  const create = useMutation({
    mutationFn: () => authApi.createApiToken(name, Number(days) || 90),
    onSuccess: (t) => {
      setToken(t.token)
      qc.invalidateQueries({ queryKey: queryKeys.auth.apiTokens })
    },
    onError: (e) => toast.error(e.message),
  })

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setName('')
      setDays('90')
      setToken(null)
      create.reset()
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button variant="ghost" size="sm">
          {t('apiTokens.newToken')}
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {token ? t('apiTokens.dialog.createdTitle') : t('apiTokens.dialog.createTitle')}
          </DialogTitle>
        </DialogHeader>
        {token ? (
          <div className="flex flex-col gap-3">
            <p className="text-sm text-muted-foreground">{t('apiTokens.dialog.copyHint')}</p>
            <div className="flex items-center justify-between gap-2 rounded-md border bg-surface-1 px-3 py-2">
              <code className="truncate font-mono text-xs">{token}</code>
              <CopyButton value={token} />
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            <div className="grid gap-2">
              <Label htmlFor="token-name">{t('apiTokens.dialog.nameLabel')}</Label>
              <Input
                id="token-name"
                autoFocus
                required
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={t('apiTokens.dialog.namePlaceholder')}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="token-days">{t('apiTokens.dialog.expiresLabel')}</Label>
              <Input
                id="token-days"
                inputMode="numeric"
                value={days}
                onChange={(e) => setDays(e.target.value)}
              />
            </div>
          </div>
        )}
        {!token ? (
          <DialogFooter>
            <Button
              variant="brand"
              disabled={create.isPending || !name}
              onClick={() => create.mutate()}
            >
              {t('apiTokens.dialog.submit')}
            </Button>
          </DialogFooter>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

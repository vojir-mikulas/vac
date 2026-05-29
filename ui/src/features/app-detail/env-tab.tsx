import { useMemo, useState } from 'react'
import { AlertTriangle, KeyRound } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { useEnvKeys, useReplaceEnv } from '@/lib/api/env'
import { useStackControl } from '@/lib/api/apps'
import { invalidEnvKeys, parseEnv } from '@/lib/env-parse'

export function EnvTab({ appId }: { appId: string }) {
  const { data: keys, isLoading } = useEnvKeys(appId)
  const replace = useReplaceEnv(appId)
  const stack = useStackControl(appId)
  const [text, setText] = useState('')
  const [restartPending, setRestartPending] = useState(false)

  const parsed = useMemo(() => parseEnv(text), [text])
  const invalid = useMemo(() => invalidEnvKeys(parsed), [parsed])
  const count = Object.keys(parsed).length

  const save = () => {
    if (invalid.length > 0) {
      toast.error(`Invalid keys: ${invalid.join(', ')}`)
      return
    }
    replace.mutate(parsed, {
      onSuccess: (res) => {
        toast.success(`Saved ${res.saved} variable${res.saved === 1 ? '' : 's'}`)
        setRestartPending(true)
        setText('')
      },
      onError: (e) => toast.error(e.message),
    })
  }

  const restart = () =>
    stack.mutate('restart', {
      onSuccess: () => {
        toast.success('Restarting to apply changes')
        setRestartPending(false)
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="flex flex-col gap-6">
      {restartPending ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-warn-border bg-warn-bg px-4 py-3">
          <span className="flex items-center gap-2 text-sm text-warn-foreground">
            <AlertTriangle className="size-4" />
            Changes saved — restart required to take effect.
          </span>
          <Button variant="brand" size="sm" disabled={stack.isPending} onClick={restart}>
            Restart now
          </Button>
        </div>
      ) : null}

      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="min-w-0 flex-1">
          <SectionHeader>Edit variables</SectionHeader>
          <Card className="gap-3 p-5">
            <p className="text-sm text-muted-foreground">
              Paste a <code className="font-mono text-xs">.env</code> file or edit below. Saving{' '}
              <strong>replaces all</strong> variables. Values are encrypted at rest and injected
              when containers start.
            </p>
            <Textarea
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={'DATABASE_URL=postgres://…\nLOG_LEVEL=info'}
              className="min-h-64 font-mono text-xs"
              spellCheck={false}
            />
            {invalid.length > 0 ? (
              <p className="text-xs text-err-foreground">Invalid keys: {invalid.join(', ')}</p>
            ) : null}
            <div className="flex items-center justify-between">
              <span className="text-2xs text-muted-foreground">
                {count} variable{count === 1 ? '' : 's'} parsed
              </span>
              <Button variant="brand" disabled={replace.isPending || count === 0} onClick={save}>
                Save variables
              </Button>
            </div>
          </Card>
        </div>

        <div className="lg:w-72 lg:shrink-0">
          <SectionHeader>Current keys</SectionHeader>
          <Card className="gap-0 p-0">
            {isLoading ? (
              <div className="p-4">
                <Skeleton className="h-24 w-full" />
              </div>
            ) : keys && keys.length > 0 ? (
              keys.map((k, i) => (
                <div
                  key={k.key}
                  className={`flex items-center gap-2 px-4 py-2.5 font-mono text-xs ${i > 0 ? 'border-t' : ''}`}
                >
                  <KeyRound className="size-3 shrink-0 text-muted-foreground" />
                  <span className="truncate">{k.key}</span>
                  <span className="ml-auto text-muted-foreground">••••</span>
                </div>
              ))
            ) : (
              <p className="px-4 py-6 text-center text-sm text-muted-foreground">
                No variables set.
              </p>
            )}
          </Card>
        </div>
      </div>
    </div>
  )
}

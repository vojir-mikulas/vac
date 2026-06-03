import { useId, useState } from 'react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { useDeployConcurrency, useSetDeployConcurrency } from '@/lib/api/instance'

export function DeploymentsSection() {
  const { data, isLoading } = useDeployConcurrency()
  return (
    <section>
      <SectionHeader>Deployments</SectionHeader>
      {isLoading || !data ? (
        <Card className="p-5">
          <Skeleton className="h-32 w-full" />
        </Card>
      ) : (
        <ConcurrencyForm
          key={data.max_concurrent_deploys}
          value={data.max_concurrent_deploys}
          min={data.min}
          max={data.max}
        />
      )}
    </section>
  )
}

function ConcurrencyForm({ value, min, max }: { value: number; min: number; max: number }) {
  const id = useId()
  const set = useSetDeployConcurrency()
  const [n, setN] = useState(String(value))

  const parsed = Number(n)
  const valid = Number.isInteger(parsed) && parsed >= min && parsed <= max
  const dirty = parsed !== value

  const save = () =>
    set.mutate(parsed, {
      onSuccess: () => toast.success('Deploy concurrency saved — applies after the next restart'),
      onError: (e) => toast.error(e.message),
    })

  return (
    <Card className="gap-4 p-5">
      <div className="grid gap-2">
        <Label htmlFor={id}>Maximum concurrent deploys</Label>
        <Input
          id={id}
          type="number"
          inputMode="numeric"
          min={min}
          max={max}
          value={n}
          onChange={(e) => setN(e.target.value)}
          className="w-24"
        />
        <p className="text-sm text-muted-foreground">
          How many app deploys run at the same time when several are queued (e.g. a burst of
          pushes). Different apps only — VAC never runs two deploys for the <em>same</em> app at
          once. Higher values finish a backlog faster but use more CPU, RAM, and disk I/O during
          builds; keep it low on a small VPS. Allowed range {min}–{max}. Takes effect after the next
          control-plane restart.
        </p>
      </div>
      <div className="flex justify-end">
        <Button
          variant="brand"
          size="sm"
          disabled={!valid || !dirty || set.isPending}
          onClick={save}
        >
          Save
        </Button>
      </div>
    </Card>
  )
}

import { useId, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { useDeployConcurrency, useSetDeployConcurrency } from '@/lib/api/instance'

export function DeploymentsSection() {
  const { t } = useTranslation('settings')
  const { data, isLoading } = useDeployConcurrency()
  return (
    <section>
      <SectionHeader>{t('deployments.heading')}</SectionHeader>
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
  const { t } = useTranslation('settings')
  const id = useId()
  const set = useSetDeployConcurrency()
  const [n, setN] = useState(String(value))

  const parsed = Number(n)
  const valid = Number.isInteger(parsed) && parsed >= min && parsed <= max
  const dirty = parsed !== value

  const save = () =>
    set.mutate(parsed, {
      onSuccess: () => toast.success(t('deployments.toast.saved')),
      onError: (e) => toast.error(e.message),
    })

  return (
    <Card className="gap-4 p-5">
      <div className="grid gap-2">
        <Label htmlFor={id}>{t('deployments.maxLabel')}</Label>
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
          <Trans t={t} i18nKey="deployments.hint" values={{ min, max }} components={[<em />]} />
        </p>
      </div>
      <div className="flex justify-end">
        <Button
          variant="brand"
          size="sm"
          disabled={!valid || !dirty || set.isPending}
          onClick={save}
        >
          {t('deployments.save')}
        </Button>
      </div>
    </Card>
  )
}

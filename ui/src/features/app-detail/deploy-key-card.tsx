import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { CopyButton } from '@/components/common/copy-button'
import { appsApi } from '@/lib/api/apps'
import { ApiError } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

function isSshUrl(url: string): boolean {
  return url.startsWith('git@') || url.startsWith('ssh://')
}

export function DeployKeyCard({ appId, gitUrl }: { appId: string; gitUrl: string }) {
  const ssh = isSshUrl(gitUrl)
  const qc = useQueryClient()

  const { data, isLoading, error } = useQuery({
    queryKey: queryKeys.apps.sshKey(appId),
    queryFn: () => appsApi.sshKey(appId),
    enabled: ssh,
    retry: false,
  })

  const regenerate = useMutation({
    mutationFn: () => appsApi.regenerateSshKey(appId),
    onSuccess: (key) => {
      qc.setQueryData(queryKeys.apps.sshKey(appId), key)
      toast.success('Deploy key regenerated')
    },
    onError: (e) => toast.error(e.message),
  })

  if (!ssh) return null

  const notFound = error instanceof ApiError && error.status === 404

  return (
    <Card className="gap-3 p-5">
      <div className="flex items-center justify-between">
        <h4 className="text-sm font-medium">Deploy key</h4>
        {data ? <CopyButton value={data.public_key} label="Copy key" /> : null}
      </div>
      <p className="text-xs text-muted-foreground">
        Add this public key as a read-only deploy key on your Git host so VAC can clone over SSH.
      </p>
      {isLoading ? (
        <Skeleton className="h-16 w-full" />
      ) : notFound ? (
        <div className="flex items-center justify-between gap-3">
          <span className="text-xs text-muted-foreground">No key generated yet.</span>
          <Button
            variant="outline"
            size="sm"
            disabled={regenerate.isPending}
            onClick={() => regenerate.mutate()}
          >
            Generate key
          </Button>
        </div>
      ) : data ? (
        <>
          <pre className="overflow-x-auto rounded-md border bg-surface-1 p-3 font-mono text-2xs">
            {data.public_key}
          </pre>
          <div className="flex items-center justify-between">
            <span className="font-mono text-2xs text-muted-foreground">{data.fingerprint}</span>
            <Button
              variant="ghost"
              size="sm"
              disabled={regenerate.isPending}
              onClick={() => regenerate.mutate()}
            >
              Regenerate
            </Button>
          </div>
        </>
      ) : null}
    </Card>
  )
}

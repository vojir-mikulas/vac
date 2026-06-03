import { useQuery } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { DatabaseInventory } from '@/types/api'

// Box-wide database inventory (plan 20). Kept separate from the per-app
// `databases.ts` client — this is the operator-level lens, not app-scoped CRUD.
export function useDatabaseInventory(enabled = true) {
  return useQuery({
    queryKey: queryKeys.databases.inventory,
    queryFn: () => api.get<DatabaseInventory>('databases'),
    enabled,
    // Poll while any database is still provisioning so its status settles on its
    // own (status is fresh per request; only sizes are server-cached).
    refetchInterval: (query) =>
      (query.state.data?.engines ?? []).some((g) =>
        g.databases.some((d) => d.status === 'provisioning'),
      )
        ? 3_000
        : false,
  })
}

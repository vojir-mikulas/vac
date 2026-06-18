import { useInfiniteQuery } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'

// One runtime-log row from the explorer search endpoint. Carries app_id +
// service because the search spans every app.
export interface SearchLog {
  id: number
  app_id: string
  service: string
  stream: string
  message: string
  at: string
}

export interface SearchLogsResponse {
  logs: SearchLog[]
  // Descending-id cursor for the next (older) page; 0 when this is the tail.
  next_before: number
}

export interface LogSearchFilters {
  app: string
  service: string
  stream: string
  q: string
}

const PAGE_SIZE = 300

function buildQuery(filters: LogSearchFilters, before: number): string {
  const p = new URLSearchParams()
  if (filters.app) p.set('app', filters.app)
  if (filters.service) p.set('service', filters.service)
  if (filters.stream) p.set('stream', filters.stream)
  if (filters.q) p.set('q', filters.q)
  if (before) p.set('before', String(before))
  p.set('limit', String(PAGE_SIZE))
  return p.toString()
}

export const logsApi = {
  search: (filters: LogSearchFilters, before = 0) =>
    api.get<SearchLogsResponse>(`logs/search?${buildQuery(filters, before)}`),
}

// useLogSearch pages newest-first through the runtime-log ring buffer for the
// given filter set. Each page is the next-older window via the `before` cursor;
// `next_before === 0` ends the pagination.
export function useLogSearch(filters: LogSearchFilters) {
  return useInfiniteQuery({
    queryKey: queryKeys.logs.search(filters),
    queryFn: ({ pageParam }) => logsApi.search(filters, pageParam),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.next_before > 0 ? last.next_before : undefined),
  })
}

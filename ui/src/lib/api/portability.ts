import { useMutation, useQueryClient } from '@tanstack/react-query'

import { api } from '@/lib/api/client'
import { queryKeys } from '@/lib/query/keys'
import type { ImportResult } from '@/types/api'

// Portability (plan 18): export an app as a portable vac.app.yaml, or import one
// to create/update an app. Specs travel as YAML text, so these bypass the JSON
// client helpers (getText / postRaw).
export const portabilityApi = {
  exportSpec: (id: string) => api.getText(`apps/${id}/export`),
  importSpec: (yaml: string) => api.postRaw<ImportResult>('apps/import', yaml, 'application/yaml'),
}

// useExportApp returns the app's spec YAML; the caller triggers the download so
// it can name the file from the app slug.
export function useExportApp() {
  return useMutation({
    mutationFn: (id: string) => portabilityApi.exportSpec(id),
  })
}

export function useImportApp() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (yaml: string) => portabilityApi.importSpec(yaml),
    onSuccess: (result) => {
      qc.invalidateQueries({ queryKey: queryKeys.apps.all })
      qc.invalidateQueries({ queryKey: queryKeys.apps.detail(result.app_id) })
    },
  })
}

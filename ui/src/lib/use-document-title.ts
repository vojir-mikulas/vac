import { useEffect } from 'react'

const SUFFIX = 'VAC'

// Sets document.title to "<page> — VAC" for the lifetime of the calling
// component, restoring the previous title on unmount. Pass an empty/undefined
// page to show just the bare suffix.
export function useDocumentTitle(page?: string) {
  useEffect(() => {
    const previous = document.title
    document.title = page ? `${page} — ${SUFFIX}` : SUFFIX
    return () => {
      document.title = previous
    }
  }, [page])
}

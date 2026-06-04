import { useEffect, useRef, useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { toast } from 'sonner'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

/**
 * Copy `value` to the clipboard. Uses the async Clipboard API when available
 * (HTTPS / localhost) and falls back to a hidden-textarea + execCommand path
 * for insecure contexts (plain HTTP), where `navigator.clipboard` is undefined.
 * Returns true on success.
 */
export async function copyToClipboard(value: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // fall through to the legacy path
    }
  }

  try {
    const ta = document.createElement('textarea')
    ta.value = value
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '-9999px'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

export function CopyButton({ value, label }: { value: string; label?: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const resetTimer = useRef<ReturnType<typeof setTimeout> | null>(null)

  // CopyButton lives in lists and dialogs that can unmount inside the 1.5s
  // window, so clear any pending reset on unmount (and before re-scheduling).
  useEffect(() => () => clearTimeout(resetTimer.current ?? undefined), [])

  const copy = async () => {
    const ok = await copyToClipboard(value)
    if (ok) {
      setCopied(true)
      clearTimeout(resetTimer.current ?? undefined)
      resetTimer.current = setTimeout(() => setCopied(false), 1500)
    } else {
      toast.error(t('toast.copyFailed'))
    }
  }

  return (
    <Button variant="outline" size="sm" onClick={copy}>
      {copied ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
      {copied ? t('actions.copied') : (label ?? t('actions.copy'))}
    </Button>
  )
}

import { useMemo, useState } from 'react'
import {
  AlertTriangle,
  Copy,
  Eye,
  EyeOff,
  Lock,
  LockOpen,
  Plus,
  ShieldAlert,
  ShieldCheck,
  Trash2,
  Upload,
} from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { copyToClipboard } from '@/components/common/copy-button'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { envApi, useEnvVars, useReplaceEnv, type EnvVarInput } from '@/lib/api/env'
import { useStackControl } from '@/lib/api/apps'
import { isSensitiveKey, isValidEnvKey, parseEnvEntries } from '@/lib/env-parse'
import type { EnvVar } from '@/types/api'

// A single editable row. `value` is null when we don't yet hold the plaintext —
// i.e. a sensitive var loaded from the server that hasn't been revealed or
// edited, or a write-only secret (whose plaintext can never be fetched).
// `dirty`/`isNew` drive the unsaved-change counter; revealing alone is not a
// change. `writeOnly` is a one-way latch: a persisted write-only row can only be
// replaced or deleted, never revealed, copied, or downgraded.
interface Row {
  uid: number
  key: string
  sensitive: boolean
  writeOnly: boolean
  value: string | null
  revealed: boolean
  dirty: boolean
  isNew: boolean
}

let nextUid = 0
const newUid = () => ++nextUid

export function EnvTab({ appId }: { appId: string }) {
  const { data: vars, isLoading } = useEnvVars(appId)
  const replace = useReplaceEnv(appId)
  const stack = useStackControl(appId)

  const [rows, setRows] = useState<Row[] | null>(null)
  const [deletedKeys, setDeletedKeys] = useState<Set<string>>(new Set())
  const [restartPending, setRestartPending] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  // Surfaced both as a toast (transient) and in an inline role="alert" region so
  // screen-reader users hear validation failures, not just sighted ones.
  const [saveError, setSaveError] = useState<string | null>(null)

  // Seed local editor state from the server list once per fetch, using the
  // sanctioned "adjust state when a prop changes" pattern (tracking the source
  // array in state). A refetch (e.g. after save) yields a new array reference
  // and re-seeds, resetting the dirty/new flags to the persisted state.
  const [seededFor, setSeededFor] = useState<EnvVar[] | null>(null)
  if (vars && seededFor !== vars) {
    setSeededFor(vars)
    setRows(
      vars.map((v) => {
        const writeOnly = v.write_only ?? false
        return {
          uid: newUid(),
          key: v.key,
          sensitive: v.sensitive,
          writeOnly,
          // A write-only row never holds plaintext; a sensitive row holds it only
          // once revealed; a plaintext row always ships its value.
          value: writeOnly ? null : v.sensitive ? (v.value ?? null) : (v.value ?? ''),
          revealed: !v.sensitive && !writeOnly,
          dirty: false,
          isNew: false,
        }
      }),
    )
    setDeletedKeys(new Set())
  }

  const list = rows ?? []
  const unsaved = useMemo(
    () => (rows ?? []).filter((r) => r.dirty || r.isNew).length + deletedKeys.size,
    [rows, deletedKeys],
  )

  const patch = (uid: number, next: Partial<Row>) =>
    setRows((rs) => (rs ?? []).map((r) => (r.uid === uid ? { ...r, ...next } : r)))

  const reveal = async (uid: number) => {
    const row = list.find((r) => r.uid === uid)
    if (!row) return
    // Write-only secrets can never be revealed (server returns 403); the UI
    // hides the eye for them, but guard here too.
    if (row.writeOnly) return
    if (row.value !== null) {
      patch(uid, { revealed: !row.revealed })
      return
    }
    try {
      const res = await envApi.reveal(appId, row.key)
      patch(uid, { value: res.value ?? '', revealed: true })
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not reveal value')
    }
  }

  const toggleSensitive = async (uid: number) => {
    const row = list.find((r) => r.uid === uid)
    if (!row) return
    // A write-only secret is a one-way latch — its lock can't be flipped back.
    if (row.writeOnly) return
    // Flipping sensitive → plaintext needs the value to display it; fetch first.
    if (row.sensitive && row.value === null) {
      try {
        const res = await envApi.reveal(appId, row.key)
        patch(uid, { sensitive: false, value: res.value ?? '', revealed: true, dirty: true })
      } catch (e) {
        toast.error(e instanceof Error ? e.message : 'Could not reveal value')
      }
      return
    }
    patch(uid, { sensitive: !row.sensitive, revealed: row.sensitive, dirty: true })
  }

  // Upgrade a row to write-only — effectively irreversible (the value can never
  // be revealed or copied afterward), so confirm first. A persisted sensitive
  // row keeps its existing sealed value (resent via `keep` on save); a row the
  // operator has typed into re-seals that plaintext.
  const makeWriteOnly = (uid: number) => {
    const row = list.find((r) => r.uid === uid)
    if (!row || row.writeOnly) return
    const ok = window.confirm(
      `Make ${row.key || 'this variable'} write-only? You won't be able to reveal or copy ` +
        `its value afterward — only replace or delete it. This cannot be undone.`,
    )
    if (!ok) return
    // Write-only implies sensitive and is always masked. `value` is left as-is:
    // an unrevealed secret stays null and resends via `keep`; a revealed/new one
    // re-seals its plaintext.
    patch(uid, { writeOnly: true, sensitive: true, revealed: false, dirty: true })
  }

  const remove = (uid: number) => {
    const row = list.find((r) => r.uid === uid)
    if (row && !row.isNew) setDeletedKeys((s) => new Set(s).add(row.key))
    setRows((rs) => (rs ?? []).filter((r) => r.uid !== uid))
  }

  const addRow = () =>
    setRows((rs) => [
      ...(rs ?? []),
      {
        uid: newUid(),
        key: '',
        sensitive: false,
        writeOnly: false,
        value: '',
        revealed: true,
        dirty: true,
        isNew: true,
      },
    ])

  const copyValue = async (row: Row) => {
    // Write-only secrets can never be copied (the reveal call 403s); the UI
    // hides the copy button for them, but guard here too.
    if (row.writeOnly) return
    let value = row.value
    if (value === null) {
      try {
        value = (await envApi.reveal(appId, row.key)).value ?? ''
      } catch (e) {
        toast.error(e instanceof Error ? e.message : 'Could not copy value')
        return
      }
    }
    const ok = await copyToClipboard(value)
    toast[ok ? 'success' : 'error'](ok ? `Copied ${row.key}` : 'Copy failed')
  }

  const importEntries = (entries: { key: string; value: string; sensitive: boolean }[]) => {
    setRows((rs) => {
      const out = [...(rs ?? [])]
      for (const e of entries) {
        const at = out.findIndex((r) => r.key === e.key)
        const existing = at >= 0 ? out[at] : undefined
        // Importing a value for an existing write-only key replaces it but keeps
        // the latch — a downgrade would be rejected server-side anyway.
        const writeOnly = existing?.writeOnly ?? false
        const row: Row = {
          uid: existing?.uid ?? newUid(),
          key: e.key,
          sensitive: writeOnly || e.sensitive,
          writeOnly,
          value: e.value,
          revealed: !writeOnly && !e.sensitive,
          dirty: true,
          isNew: existing ? existing.isNew : true,
        }
        if (existing) out[at] = row
        else out.push(row)
      }
      return out
    })
    setImportOpen(false)
  }

  const save = async () => {
    setSaveError(null)
    const keys = list.map((r) => r.key.trim())
    const bad = keys.filter((k) => !isValidEnvKey(k))
    if (bad.length) {
      const msg = `Invalid keys: ${bad.join(', ')}`
      setSaveError(msg)
      toast.error(msg)
      return
    }
    if (new Set(keys).size !== keys.length) {
      const msg = 'Duplicate keys are not allowed'
      setSaveError(msg)
      toast.error(msg)
      return
    }
    // Full-replace PUT needs plaintext for every row; fetch any sensitive value
    // the operator never revealed or edited. Write-only rows are skipped — their
    // plaintext can't be fetched (reveal 403s); an untouched one rides through on
    // the server's `keep` path instead.
    let resolved: Row[]
    try {
      resolved = await Promise.all(
        list.map(async (r) => {
          if (r.value !== null || r.writeOnly) return r
          const res = await envApi.reveal(appId, r.key)
          return { ...r, value: res.value ?? '' }
        }),
      )
    } catch (e) {
      toast.error(e instanceof Error ? e.message : 'Could not resolve existing values')
      return
    }
    const payload: EnvVarInput[] = resolved.map((r) => {
      // Persisted write-only secret the operator never touched: reuse the sealed
      // bytes server-side (no plaintext to resend) via `keep`.
      if (!r.isNew && r.writeOnly && r.value === null) {
        return { key: r.key.trim(), value: '', sensitive: true, write_only: true, keep: true }
      }
      // New write-only, or a write-only row given a fresh value: re-seal plaintext.
      if (r.writeOnly) {
        return {
          key: r.key.trim(),
          value: r.value ?? '',
          sensitive: true,
          write_only: true,
          keep: false,
        }
      }
      return {
        key: r.key.trim(),
        value: r.value ?? '',
        sensitive: r.sensitive,
        write_only: false,
        keep: false,
      }
    })
    replace.mutate(payload, {
      onSuccess: (res) => {
        toast.success(`Saved ${res.saved} variable${res.saved === 1 ? '' : 's'}`)
        setRestartPending(true)
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
          <div className="mb-3 flex items-center justify-between gap-3">
            <SectionHeader className="mb-0">Environment variables</SectionHeader>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setImportOpen((v) => !v)}>
                <Upload className="size-3.5" />
                Import .env
              </Button>
              <Button variant="outline" size="sm" onClick={addRow}>
                <Plus className="size-3.5" />
                Add variable
              </Button>
            </div>
          </div>

          {importOpen ? (
            <ImportPanel onImport={importEntries} onCancel={() => setImportOpen(false)} />
          ) : null}

          <Card className="gap-0 p-0">
            {isLoading ? (
              <div className="p-4">
                <Skeleton className="h-24 w-full" />
              </div>
            ) : list.length === 0 ? (
              <p className="px-4 py-10 text-center text-sm text-muted-foreground">
                No variables yet. Use <strong>Add variable</strong> or <strong>Import .env</strong>.
              </p>
            ) : (
              list.map((row, i) => (
                <EnvRow
                  key={row.uid}
                  row={row}
                  index={i + 1}
                  divider={i > 0}
                  onKey={(key) => patch(row.uid, { key, dirty: true })}
                  onValue={(value) => patch(row.uid, { value, revealed: true, dirty: true })}
                  onReveal={() => reveal(row.uid)}
                  onToggleSensitive={() => toggleSensitive(row.uid)}
                  onMakeWriteOnly={() => makeWriteOnly(row.uid)}
                  onCopy={() => copyValue(row)}
                  onRemove={() => remove(row.uid)}
                />
              ))
            )}
          </Card>

          {saveError ? (
            <p role="alert" className="mt-3 text-xs text-err-foreground">
              {saveError}
            </p>
          ) : null}

          <div className="mt-4 flex items-center justify-between">
            <span className="text-2xs text-muted-foreground">
              {unsaved > 0 ? (
                <span className="text-warn-foreground">
                  {unsaved} unsaved change{unsaved === 1 ? '' : 's'} — applied on next restart
                </span>
              ) : (
                'No unsaved changes'
              )}
            </span>
            <Button variant="brand" disabled={replace.isPending || unsaved === 0} onClick={save}>
              Save variables
            </Button>
          </div>
        </div>

        <div className="lg:w-72 lg:shrink-0">
          <SectionHeader>About environment</SectionHeader>
          <Card className="gap-2 p-5 text-sm text-muted-foreground">
            <p>
              Variables are <strong>encrypted at rest</strong> with the host master key and injected
              only when containers start. Changes require a restart to take effect.
            </p>
            <p>
              <strong className="text-foreground">Sensitive</strong> values are masked and revealed
              on demand; <strong className="text-foreground">plaintext</strong> values are editable
              inline. Toggle the lock on any row to switch.
            </p>
            <p>
              Mark a secret <strong className="text-foreground">write-only</strong> to make it
              unrevealable: you can replace or delete it, but VAC will never show or copy its value
              again.
            </p>
          </Card>
        </div>
      </div>
    </div>
  )
}

function EnvRow({
  row,
  index,
  divider,
  onKey,
  onValue,
  onReveal,
  onToggleSensitive,
  onMakeWriteOnly,
  onCopy,
  onRemove,
}: {
  row: Row
  index: number
  divider: boolean
  onKey: (key: string) => void
  onValue: (value: string) => void
  onReveal: () => void
  onToggleSensitive: () => void
  onMakeWriteOnly: () => void
  onCopy: () => void
  onRemove: () => void
}) {
  // A write-only row is masked until the operator types a replacement value
  // (then it shows what they're entering). A sensitive row masks until revealed.
  const masked = row.writeOnly ? row.value === null : row.sensitive && !row.revealed
  // Inputs are placeholder-only by design (a per-row visible label would bloat
  // the grid), so each carries an aria-label naming the field and its row.
  const named = row.key || `row ${index}`
  return (
    <div
      className={`grid grid-cols-[minmax(0,200px)_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5 ${
        divider ? 'border-t' : ''
      }`}
    >
      <Input
        value={row.key}
        onChange={(e) => onKey(e.target.value)}
        placeholder="KEY"
        aria-label={`Variable name, row ${index}`}
        spellCheck={false}
        className="h-8 font-mono text-xs"
      />
      <Input
        value={masked ? '' : (row.value ?? '')}
        type={masked ? 'password' : 'text'}
        onChange={(e) => onValue(e.target.value)}
        placeholder={masked ? '••••••••••••' : 'value'}
        aria-label={`Value for ${named}`}
        spellCheck={false}
        className="h-8 font-mono text-xs"
      />
      <div className="flex items-center gap-1 text-muted-foreground">
        {row.writeOnly ? (
          // One-way latch: no reveal, no copy, no lock toggle — only replace
          // (type a new value) or delete. The shield badge marks it visually.
          <span
            title="Write-only — cannot be revealed"
            aria-label={`${named} is write-only and cannot be revealed`}
            className="grid size-7 place-items-center text-warn"
          >
            <ShieldCheck className="size-3.5" />
          </span>
        ) : (
          <>
            {row.sensitive ? (
              <IconButton
                label={`${row.revealed ? 'Hide' : 'Reveal'} value for ${named}`}
                pressed={row.revealed}
                onClick={onReveal}
              >
                {row.revealed ? <EyeOff className="size-3.5" /> : <Eye className="size-3.5" />}
              </IconButton>
            ) : null}
            <IconButton
              label={row.sensitive ? `Mark ${named} plaintext` : `Mark ${named} sensitive`}
              onClick={onToggleSensitive}
            >
              {row.sensitive ? <Lock className="size-3.5" /> : <LockOpen className="size-3.5" />}
            </IconButton>
            <IconButton label={`Copy value for ${named}`} onClick={onCopy}>
              <Copy className="size-3.5" />
            </IconButton>
            <IconButton
              label={`Make ${named} write-only (cannot be revealed)`}
              onClick={onMakeWriteOnly}
            >
              <ShieldAlert className="size-3.5" />
            </IconButton>
          </>
        )}
        <IconButton label={`Delete ${named}`} onClick={onRemove}>
          <Trash2 className="size-3.5" />
        </IconButton>
      </div>
    </div>
  )
}

function IconButton({
  label,
  pressed,
  onClick,
  children,
}: {
  label: string
  pressed?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      aria-pressed={pressed}
      onClick={onClick}
      className="grid size-7 place-items-center rounded-md transition-colors hover:bg-muted hover:text-foreground"
    >
      {children}
    </button>
  )
}

function ImportPanel({
  onImport,
  onCancel,
}: {
  onImport: (entries: { key: string; value: string; sensitive: boolean }[]) => void
  onCancel: () => void
}) {
  const [text, setText] = useState('')
  const [autoDetect, setAutoDetect] = useState(true)

  const preview = useMemo(
    () =>
      parseEnvEntries(text).map((e) => ({
        ...e,
        sensitive: autoDetect ? isSensitiveKey(e.key) : false,
        valid: isValidEnvKey(e.key),
      })),
    [text, autoDetect],
  )
  const invalid = preview.filter((p) => !p.valid)

  return (
    <Card className="mb-4 gap-3 p-5">
      <p className="text-sm text-muted-foreground">
        Paste a <code className="font-mono text-xs">.env</code> file. Rows merge into the editor by
        key; review detected sensitivity before importing.
      </p>
      <Textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={'DATABASE_URL=postgres://…\nLOG_LEVEL=info'}
        className="min-h-40 font-mono text-xs"
        spellCheck={false}
      />
      <label className="flex items-center gap-2 text-sm">
        <Switch checked={autoDetect} onCheckedChange={setAutoDetect} />
        Auto-mark secrets (keys like <code className="font-mono text-xs">TOKEN</code>,{' '}
        <code className="font-mono text-xs">PASSWORD</code>)
      </label>

      {preview.length > 0 ? (
        <div className="overflow-hidden rounded-md border">
          {preview.map((p, i) => (
            <div
              key={p.key + i}
              className={`flex items-center gap-2 px-3 py-1.5 font-mono text-xs ${
                i > 0 ? 'border-t' : ''
              } ${p.valid ? '' : 'bg-err-bg'}`}
            >
              <span className="truncate">{p.key}</span>
              <span className="ml-auto inline-flex items-center gap-1 text-2xs text-muted-foreground">
                {p.sensitive ? (
                  <>
                    <Lock className="size-3" /> sensitive
                  </>
                ) : (
                  <>
                    <LockOpen className="size-3" /> plaintext
                  </>
                )}
              </span>
            </div>
          ))}
        </div>
      ) : null}
      {invalid.length > 0 ? (
        <p className="text-xs text-err-foreground">
          Invalid keys (skipped): {invalid.map((p) => p.key).join(', ')}
        </p>
      ) : null}

      <div className="flex items-center justify-between">
        <span className="text-2xs text-muted-foreground">
          {preview.length} variable{preview.length === 1 ? '' : 's'} parsed
        </span>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            variant="brand"
            size="sm"
            disabled={preview.filter((p) => p.valid).length === 0}
            onClick={() =>
              onImport(
                preview
                  .filter((p) => p.valid)
                  .map((p) => ({ key: p.key, value: p.value, sensitive: p.sensitive })),
              )
            }
          >
            Import {preview.filter((p) => p.valid).length || ''}
          </Button>
        </div>
      </div>
    </Card>
  )
}

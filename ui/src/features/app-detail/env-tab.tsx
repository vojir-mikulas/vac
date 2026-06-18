import { useMemo, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useBlocker } from '@tanstack/react-router'
import { AlertTriangle, Lock, LockOpen, Plus, Trash2, Upload } from 'lucide-react'
import { toast } from 'sonner'

import { SectionHeader } from '@/components/common/section-header'
import { SwapFade } from '@/components/common/swap-fade'
import { ErrorState } from '@/components/common/error-state'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Card } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { useEnvVars, useReplaceEnv, type EnvVarInput } from '@/lib/api/env'
import { useStackControl } from '@/lib/api/apps'
import { isSensitiveKey, isValidEnvKey, parseEnvEntries } from '@/lib/env-parse'
import type { EnvVar } from '@/types/api'

// A single editable row. There are exactly two modes: plain-text (value shown
// and editable inline) and write-only (value masked, never returned by the
// server). `value` is null only for a persisted write-only secret whose
// plaintext we don't hold — switching it back to plain clears it to '' so the
// operator must type a fresh value. `dirty`/`isNew` drive the unsaved counter.
interface Row {
  uid: number
  key: string
  writeOnly: boolean
  value: string | null
  dirty: boolean
  isNew: boolean
}

let nextUid = 0
const newUid = () => ++nextUid

export function EnvTab({ appId }: { appId: string }) {
  const { t } = useTranslation('app-detail')
  const { data: vars, isLoading, isError, refetch } = useEnvVars(appId)
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
        // Any masked secret — write-only or the older revealable "sensitive" —
        // loads into the single write-only mode; its plaintext is not held.
        const writeOnly = (v.write_only ?? false) || v.sensitive
        return {
          uid: newUid(),
          key: v.key,
          writeOnly,
          value: writeOnly ? null : (v.value ?? ''),
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

  // Guard against losing typed values (especially secrets) on navigation away
  // with unsaved edits. The resolver drives a styled confirm rather than the
  // browser's native dialog.
  const blocker = useBlocker({ shouldBlockFn: () => unsaved > 0, withResolver: true })

  // Discard reverts the editor to the persisted server state by forcing a
  // re-seed (clearing seededFor makes the seed effect run against `vars` again).
  const discard = () => setSeededFor(null)

  const patch = (uid: number, next: Partial<Row>) =>
    setRows((rs) => (rs ?? []).map((r) => (r.uid === uid ? { ...r, ...next } : r)))

  // Flip a row between the two modes. plain → write-only keeps the value (just
  // masks it). write-only → plain keeps the value when we hold it (a row still
  // being edited locally); when we don't (a persisted secret), it clears to ''
  // so the operator re-enters a fresh value — the server can never hand the old
  // one back.
  const toggleMode = (uid: number) => {
    const row = list.find((r) => r.uid === uid)
    if (!row) return
    if (row.writeOnly) {
      patch(uid, { writeOnly: false, value: row.value ?? '', dirty: true })
    } else {
      patch(uid, { writeOnly: true, dirty: true })
    }
  }

  const remove = (uid: number) => {
    const row = list.find((r) => r.uid === uid)
    if (row && !row.isNew) setDeletedKeys((s) => new Set(s).add(row.key))
    setRows((rs) => (rs ?? []).filter((r) => r.uid !== uid))
  }

  const addRow = () =>
    setRows((rs) => [
      ...(rs ?? []),
      { uid: newUid(), key: '', writeOnly: false, value: '', dirty: true, isNew: true },
    ])

  const importEntries = (entries: { key: string; value: string; sensitive: boolean }[]) => {
    setRows((rs) => {
      const out = [...(rs ?? [])]
      for (const e of entries) {
        const at = out.findIndex((r) => r.key === e.key)
        const existing = at >= 0 ? out[at] : undefined
        const row: Row = {
          uid: existing?.uid ?? newUid(),
          key: e.key,
          writeOnly: e.sensitive,
          value: e.value,
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
      const msg = t('env.invalidKeys', { keys: bad.join(', ') })
      setSaveError(msg)
      toast.error(msg)
      return
    }
    if (new Set(keys).size !== keys.length) {
      const msg = t('env.duplicateKeys')
      setSaveError(msg)
      toast.error(msg)
      return
    }
    const payload: EnvVarInput[] = list.map((r) => {
      const key = r.key.trim()
      if (r.writeOnly) {
        // A persisted write-only secret the operator never touched: ask the
        // server to carry its sealed bytes forward (we have no plaintext).
        if (!r.isNew && r.value === null) {
          return { key, value: '', sensitive: true, write_only: true, keep: true }
        }
        // New write-only, or one given a fresh value: re-seal the plaintext.
        return { key, value: r.value ?? '', sensitive: true, write_only: true, keep: false }
      }
      // Plain rows always hold their value (the list endpoint returns it).
      return { key, value: r.value ?? '', sensitive: false, write_only: false, keep: false }
    })
    replace.mutate(payload, {
      onSuccess: (res) => {
        toast.success(t('env.saved', { count: res.saved }))
        setRestartPending(true)
      },
      onError: (e) => toast.error(e.message),
    })
  }

  const restart = () =>
    stack.mutate('restart', {
      onSuccess: () => {
        toast.success(t('env.restarting'))
        setRestartPending(false)
      },
      onError: (e) => toast.error(e.message),
    })

  return (
    <div className="flex flex-col gap-6">
      <AlertDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(o) => {
          if (!o && blocker.status === 'blocked') blocker.reset()
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('env.leaveTitle')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('env.leaveDescription', { count: unsaved })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => blocker.status === 'blocked' && blocker.reset()}>
              {t('env.leaveStay')}
            </AlertDialogCancel>
            <AlertDialogAction
              onClick={() => blocker.status === 'blocked' && blocker.proceed()}
              className="bg-err text-err-foreground hover:bg-err/90"
            >
              {t('env.leaveDiscard')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {restartPending ? (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-warn-border bg-warn-bg px-4 py-3">
          <span className="flex items-center gap-2 text-sm text-warn-foreground">
            <AlertTriangle className="size-4" />
            {t('env.restartBanner')}
          </span>
          <Button variant="brand" size="sm" disabled={stack.isPending} onClick={restart}>
            {t('env.restartNow')}
          </Button>
        </div>
      ) : null}

      <div className="flex flex-col gap-6 lg:flex-row">
        <div className="min-w-0 flex-1">
          <div className="mb-3 flex items-center justify-between gap-3">
            <SectionHeader className="mb-0">{t('env.title')}</SectionHeader>
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setImportOpen((v) => !v)}>
                <Upload className="size-3.5" />
                {t('env.importEnv')}
              </Button>
              <Button variant="outline" size="sm" onClick={addRow}>
                <Plus className="size-3.5" />
                {t('env.addVariable')}
              </Button>
            </div>
          </div>

          {importOpen ? (
            <ImportPanel onImport={importEntries} onCancel={() => setImportOpen(false)} />
          ) : null}

          <Card className="gap-0 p-0">
            <SwapFade
              id={isLoading ? 'loading' : isError ? 'error' : list.length === 0 ? 'empty' : 'rows'}
            >
              {isLoading ? (
                <div className="flex flex-col">
                  {Array.from({ length: 4 }).map((_, i) => (
                    <div
                      key={i}
                      className={`flex items-center gap-3 px-4 py-2.5 ${i > 0 ? 'border-t' : ''}`}
                    >
                      <Skeleton className="h-8 w-1/3" />
                      <Skeleton className="h-8 flex-1" />
                    </div>
                  ))}
                </div>
              ) : isError ? (
                <ErrorState onRetry={() => refetch()} className="border-0 bg-transparent py-10" />
              ) : list.length === 0 ? (
                <p className="px-4 py-10 text-center text-sm text-muted-foreground">
                  <Trans t={t} i18nKey="env.emptyState" components={[<strong />, <strong />]} />
                </p>
              ) : (
                list.map((row, i) => (
                  <EnvRow
                    key={row.uid}
                    row={row}
                    index={i + 1}
                    divider={i > 0}
                    onKey={(key) => patch(row.uid, { key, dirty: true })}
                    onValue={(value) => patch(row.uid, { value, dirty: true })}
                    onToggleMode={() => toggleMode(row.uid)}
                    onRemove={() => remove(row.uid)}
                  />
                ))
              )}
            </SwapFade>
          </Card>

          {saveError ? (
            <p role="alert" className="mt-3 text-xs text-err-foreground">
              {saveError}
            </p>
          ) : null}

          <div className="mt-4 flex items-center justify-between">
            <span className="text-2xs text-muted-foreground">
              {unsaved > 0 ? (
                <span className="text-warn-foreground">{t('env.unsaved', { count: unsaved })}</span>
              ) : (
                t('env.noUnsaved')
              )}
            </span>
            <div className="flex items-center gap-2">
              {unsaved > 0 ? (
                <Button variant="ghost" disabled={replace.isPending} onClick={discard}>
                  {t('env.discard')}
                </Button>
              ) : null}
              <Button variant="brand" disabled={replace.isPending || unsaved === 0} onClick={save}>
                {t('env.saveVariables')}
              </Button>
            </div>
          </div>
        </div>

        <div className="lg:w-72 lg:shrink-0">
          <SectionHeader>{t('env.aboutTitle')}</SectionHeader>
          <Card className="gap-2 p-5 text-sm text-muted-foreground">
            <p>
              <Trans t={t} i18nKey="env.about1" components={[<strong />]} />
            </p>
            <p>
              <Trans
                t={t}
                i18nKey="env.about2"
                components={[
                  <strong className="text-foreground" />,
                  <strong className="text-foreground" />,
                ]}
              />
            </p>
            <p>{t('env.about3')}</p>
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
  onToggleMode,
  onRemove,
}: {
  row: Row
  index: number
  divider: boolean
  onKey: (key: string) => void
  onValue: (value: string) => void
  onToggleMode: () => void
  onRemove: () => void
}) {
  const { t } = useTranslation('app-detail')
  // Inputs are placeholder-only by design (a per-row visible label would bloat
  // the grid), so each carries an aria-label naming the field and its row.
  const named = row.key || t('env.rowFallback', { index })
  // A persisted write-only secret we don't hold the plaintext for: the field is
  // empty but the value is set server-side — prompt for a replacement.
  const hidden = row.writeOnly && row.value === null
  return (
    <div
      className={`grid grid-cols-[minmax(0,200px)_minmax(0,1fr)_auto] items-center gap-3 px-4 py-2.5 ${
        divider ? 'border-t' : ''
      }`}
    >
      <Input
        value={row.key}
        onChange={(e) => onKey(e.target.value)}
        placeholder={t('env.keyPlaceholder')}
        aria-label={t('env.variableNameAria', { index })}
        spellCheck={false}
        className="h-8 font-mono text-xs"
      />
      <Input
        value={row.value ?? ''}
        type={row.writeOnly ? 'password' : 'text'}
        onChange={(e) => onValue(e.target.value)}
        placeholder={
          hidden
            ? t('env.valuePlaceholderHidden')
            : row.writeOnly
              ? t('env.valuePlaceholderWriteOnly')
              : t('env.valuePlaceholder')
        }
        aria-label={t('env.valueAria', { name: named })}
        spellCheck={false}
        className="h-8 font-mono text-xs"
      />
      {/* Exactly two controls on every row, in every state — no conditional
          buttons, so the layout never shifts. */}
      <div className="flex items-center gap-1 text-muted-foreground">
        <IconButton
          label={
            row.writeOnly
              ? t('env.makePlainText', { name: named })
              : t('env.makeWriteOnly', { name: named })
          }
          pressed={row.writeOnly}
          onClick={onToggleMode}
        >
          {row.writeOnly ? (
            <Lock className="size-3.5 text-warn" />
          ) : (
            <LockOpen className="size-3.5" />
          )}
        </IconButton>
        <IconButton label={t('env.deleteVar', { name: named })} onClick={onRemove}>
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
  const { t } = useTranslation('app-detail')
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
        <Trans
          t={t}
          i18nKey="env.import.paste"
          components={[<code className="font-mono text-xs" />]}
        />
      </p>
      <Textarea
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={t('env.import.placeholder')}
        className="min-h-40 font-mono text-xs"
        spellCheck={false}
      />
      <label className="flex items-center gap-2 text-sm">
        <Switch checked={autoDetect} onCheckedChange={setAutoDetect} />
        <span>
          <Trans
            t={t}
            i18nKey="env.import.autoMark"
            components={[
              <code className="font-mono text-xs" />,
              <code className="font-mono text-xs" />,
            ]}
          />
        </span>
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
                    <Lock className="size-3" /> {t('env.import.writeOnly')}
                  </>
                ) : (
                  <>
                    <LockOpen className="size-3" /> {t('env.import.plainText')}
                  </>
                )}
              </span>
            </div>
          ))}
        </div>
      ) : null}
      {invalid.length > 0 ? (
        <p className="text-xs text-err-foreground">
          {t('env.import.invalidKeys', { keys: invalid.map((p) => p.key).join(', ') })}
        </p>
      ) : null}

      <div className="flex items-center justify-between">
        <span className="text-2xs text-muted-foreground">
          {t('env.import.parsed', { count: preview.length })}
        </span>
        <div className="flex gap-2">
          <Button variant="ghost" size="sm" onClick={onCancel}>
            {t('common.cancel')}
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
            {preview.filter((p) => p.valid).length > 0
              ? t('env.import.importBtn', { count: preview.filter((p) => p.valid).length })
              : t('env.import.importBtnBlank')}
          </Button>
        </div>
      </div>
    </Card>
  )
}

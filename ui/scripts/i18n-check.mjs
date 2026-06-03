#!/usr/bin/env node
// i18n catalog completeness check.
//
// Treats `en` as the source of truth and verifies every other locale has the
// exact same set of keys in every namespace — no missing keys (untranslated)
// and no orphan keys (stale leftovers). With only `en` present this passes
// trivially; it becomes the gate that keeps cs/de/… complete as they land.
//
// Run: `pnpm i18n:check` (wired into `make lint`).

import { readdirSync, readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const localesDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'src/i18n/locales')
const SOURCE = 'en'

/** Flatten a nested catalog into dotted leaf keys: { "a.b.c": true }. */
function flatten(obj, prefix = '', out = {}) {
  for (const [k, v] of Object.entries(obj)) {
    const key = prefix ? `${prefix}.${k}` : k
    if (v && typeof v === 'object' && !Array.isArray(v)) flatten(v, key, out)
    else out[key] = true
  }
  return out
}

/** Load a locale folder as { namespace: { flatKey: true } }. */
function loadLocale(locale) {
  const dir = join(localesDir, locale)
  const catalog = {}
  for (const file of readdirSync(dir).filter((f) => f.endsWith('.json'))) {
    const ns = file.replace(/\.json$/, '')
    catalog[ns] = flatten(JSON.parse(readFileSync(join(dir, file), 'utf8')))
  }
  return catalog
}

const source = loadLocale(SOURCE)
const locales = readdirSync(localesDir, { withFileTypes: true })
  .filter((d) => d.isDirectory() && d.name !== SOURCE)
  .map((d) => d.name)

let problems = 0
const report = (msg) => {
  console.error(msg)
  problems++
}

for (const locale of locales) {
  const target = loadLocale(locale)
  for (const ns of Object.keys(source)) {
    const srcKeys = source[ns]
    const tgtKeys = target[ns] ?? {}
    for (const key of Object.keys(srcKeys)) {
      if (!(key in tgtKeys)) report(`[missing] ${locale}/${ns}: ${key}`)
    }
    for (const key of Object.keys(tgtKeys)) {
      if (!(key in srcKeys)) report(`[orphan]  ${locale}/${ns}: ${key}`)
    }
  }
  for (const ns of Object.keys(target)) {
    if (!(ns in source)) report(`[orphan-namespace] ${locale}: ${ns}.json has no en counterpart`)
  }
}

if (problems > 0) {
  console.error(`\ni18n: ${problems} problem(s) found.`)
  process.exit(1)
}
console.log(`i18n: '${SOURCE}' source of truth OK; ${locales.length} other locale(s) complete.`)

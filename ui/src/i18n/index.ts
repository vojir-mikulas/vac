import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

import { defaultNS, namespaces, resources } from './resources'

// localStorage key the language detector reads/writes. Switching the language in
// Settings persists here so the choice survives a reload.
export const LANGUAGE_STORAGE_KEY = 'vac-lang'

// Locales the dashboard can switch to. English ships eagerly (resources.ts);
// every other language is code-split and loaded on demand by `loadCatalog`. To
// add one: "copy en/ → <lang>/, translate, register a loader below, add an entry
// here" (and `pnpm i18n:check` keeps the catalog complete).
export const SUPPORTED_LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'cs', label: 'Čeština' },
] as const
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number]['code']

// Dynamic-import loaders for the non-English catalogs. Each is its own chunk, so
// only the active language ever reaches the browser.
const lazyLoaders: Record<string, () => Promise<Record<string, object>>> = {
  cs: () => import('./locales/cs').then((m) => m.csResources),
}

// Ensure the catalog for `lng` is registered before it's used. English is always
// present; an unknown or already-loaded language is a no-op.
async function loadCatalog(lng: string | undefined): Promise<void> {
  const base = (lng ?? 'en').split('-')[0] ?? 'en'
  if (base === 'en' || i18next.hasResourceBundle(base, defaultNS)) return
  const loader = lazyLoaders[base]
  if (!loader) return
  const catalog = await loader()
  for (const [ns, res] of Object.entries(catalog)) {
    i18next.addResourceBundle(base, ns, res, true, true)
  }
}

// Resolves once i18next has initialized AND the detected language's catalog is
// loaded. main.tsx awaits this before the first render so a stored non-English
// choice paints in that language immediately — no English flash, no Suspense.
export const i18nReady: Promise<void> = i18next
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    supportedLngs: SUPPORTED_LANGUAGES.map((l) => l.code),
    // Resolve region-tagged detections (e.g. `en-US`, `cs-CZ`) to the base
    // catalog so we ship one folder per language, not per region.
    load: 'languageOnly',
    ns: namespaces,
    defaultNS,
    detection: {
      order: ['localStorage', 'navigator'],
      lookupLocalStorage: LANGUAGE_STORAGE_KEY,
      caches: ['localStorage'],
    },
    interpolation: { escapeValue: false }, // React already escapes against XSS.
    returnNull: false,
  })
  .then(() => loadCatalog(i18next.resolvedLanguage))

// Switch the active language. Loads the target catalog first so the UI swaps in
// one paint instead of flashing English while the chunk downloads. Prefer this
// over calling `i18next.changeLanguage` directly.
export async function changeLanguage(lng: SupportedLanguage): Promise<void> {
  await loadCatalog(lng)
  await i18next.changeLanguage(lng)
}

// Keep <html lang> in sync with the active language so screen readers and
// hyphenation use the right locale (index.html ships a static "en"). Runs once
// the detector resolves, and again whenever the language is switched.
function syncDocumentLang(lng: string) {
  if (typeof document !== 'undefined') document.documentElement.lang = lng
}
syncDocumentLang(i18next.resolvedLanguage ?? 'en')
i18next.on('languageChanged', syncDocumentLang)

export default i18next

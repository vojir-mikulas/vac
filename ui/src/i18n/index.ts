import i18next from 'i18next'
import { initReactI18next } from 'react-i18next'
import LanguageDetector from 'i18next-browser-languagedetector'

import { defaultNS, namespaces, resources } from './resources'

// localStorage key the language detector reads/writes. Switching the language in
// Settings persists here so the choice survives a reload.
export const LANGUAGE_STORAGE_KEY = 'vac-lang'

// Locales the dashboard can switch to. English ships today; cs/de/es land in
// follow-up PRs — each is "copy en/ → <lang>/, translate, add an entry here".
export const SUPPORTED_LANGUAGES = [{ code: 'en', label: 'English' }] as const
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number]['code']

void i18next
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

export default i18next

import common from './locales/en/common.json'
import apps from './locales/en/apps.json'
import appDetail from './locales/en/app-detail.json'
import deployments from './locales/en/deployments.json'
import logs from './locales/en/logs.json'
import settings from './locales/en/settings.json'
import security from './locales/en/security.json'
import addons from './locales/en/addons.json'
import backups from './locales/en/backups.json'
import database from './locales/en/database.json'
import activity from './locales/en/activity.json'
import onboarding from './locales/en/onboarding.json'
import storage from './locales/en/storage.json'

export const defaultNS = 'common'

// English is the source of truth and the fallback locale. It is bundled eagerly
// so the dashboard renders synchronously — no Suspense flash, and tests stay
// simple. Additional locales (cs, de, …) should be lazy-loaded instead: register
// them with a backend/dynamic import in index.ts so only the active language
// ever ships to the browser. Namespaces mirror ui/src/features/* (plus `common`
// and `logs`) so each feature owns its strings.
export const enResources = {
  common,
  apps,
  'app-detail': appDetail,
  deployments,
  logs,
  settings,
  security,
  addons,
  backups,
  database,
  activity,
  onboarding,
  storage,
} as const

export const namespaces = Object.keys(enResources)

export const resources = { en: enResources } as const

import common from './common.json'
import apps from './apps.json'
import appDetail from './app-detail.json'
import deployments from './deployments.json'
import logs from './logs.json'
import settings from './settings.json'
import security from './security.json'
import addons from './addons.json'
import backups from './backups.json'
import database from './database.json'
import activity from './activity.json'
import onboarding from './onboarding.json'
import storage from './storage.json'

// Czech catalog. Unlike English (eagerly bundled in resources.ts), this whole
// module is code-split: index.ts only `import()`s it when the active language
// resolves to `cs`, so English-only sessions never download these strings.
// Namespaces mirror the English set exactly — `pnpm i18n:check` enforces parity.
export const csResources = {
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

import { useTranslation } from 'react-i18next'

import { SUPPORTED_LANGUAGES, changeLanguage, type SupportedLanguage } from '@/i18n'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'

// Picks the dashboard display language. `changeLanguage` loads the target
// catalog before switching (and the detector persists the choice to
// localStorage), so the UI swaps in one paint and the choice survives a reload.
export function LanguageSelect() {
  const { t, i18n } = useTranslation('settings')
  // `resolvedLanguage` is the base code (`en`/`cs`) thanks to load:'languageOnly'.
  const current = (i18n.resolvedLanguage ?? 'en') as SupportedLanguage
  return (
    <Select
      value={current}
      onValueChange={(value) => void changeLanguage(value as SupportedLanguage)}
    >
      <SelectTrigger size="sm" className="w-40" aria-label={t('language.label')}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {SUPPORTED_LANGUAGES.map((lang) => (
          <SelectItem key={lang.code} value={lang.code}>
            {lang.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

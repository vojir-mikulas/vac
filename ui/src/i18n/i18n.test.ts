import { describe, expect, it } from 'vitest'

import i18n from '@/i18n'
import { namespaces } from '@/i18n/resources'

describe('i18n', () => {
  it('initializes with en as the resolved language and fallback', () => {
    expect(i18n.resolvedLanguage).toBe('en')
    expect(i18n.options.fallbackLng).toContain('en')
  })

  it('resolves keys from the common (default) and feature namespaces', () => {
    expect(i18n.t('actions.copy')).toBe('Copy')
    expect(i18n.t('language.label', { ns: 'settings' })).toBe('Display language')
  })

  it('registers a namespace per feature folder', () => {
    expect(namespaces).toEqual(
      expect.arrayContaining(['common', 'apps', 'app-detail', 'deployments', 'settings']),
    )
  })
})

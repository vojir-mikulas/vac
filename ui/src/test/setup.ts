import '@testing-library/jest-dom/vitest'
import { expect } from 'vitest'
import * as axeMatchers from 'vitest-axe/matchers'

// Initialize the i18n singleton with the bundled `en` catalogs so component
// tests render real strings and RTL `getByText` queries keep working. Importing
// the module runs i18next.init() synchronously (en resources are bundled).
import '@/i18n'

// Register the axe matcher globally (the package's own extend-expect targets an
// older vitest expect and doesn't take under vitest 4).
expect.extend(axeMatchers)

// jsdom ships no matchMedia; the theme provider and reduced-motion checks call
// it at mount. Default to "no preference" (light, motion allowed) for tests.
if (typeof window !== 'undefined' && !window.matchMedia) {
  window.matchMedia = (query: string): MediaQueryList =>
    ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }) as MediaQueryList
}

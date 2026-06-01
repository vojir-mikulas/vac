import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import tseslint from 'typescript-eslint'
import prettier from 'eslint-config-prettier'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist', 'src/routeTree.gen.ts']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      jsxA11y.flatConfigs.recommended,
      prettier,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    rules: {
      'react-refresh/only-export-components': ['warn', { allowExportNames: ['Route'] }],
      // Our toggle rows wrap a Radix <Switch> in a <label>; teach the rule that
      // Switch is the associated control so the nesting is recognised.
      'jsx-a11y/label-has-associated-control': ['error', { controlComponents: ['Switch'] }],
      // autoFocus is used only on the first field of modal dialogs and the
      // dedicated auth/setup screens, where moving focus to the obvious entry
      // point aids rather than harms. Allowed by policy; see docs/kb/conventions.md.
      'jsx-a11y/no-autofocus': 'off',
      // Scrollable live regions (role="log") are made keyboard-focusable so they
      // can be scrolled without a mouse — a sanctioned use of tabIndex={0}.
      'jsx-a11y/no-noninteractive-tabindex': [
        'error',
        { tags: [], roles: ['tabpanel', 'log'], allowExpressionValues: true },
      ],
    },
  },
  {
    files: ['src/routes/**/*.{ts,tsx}'],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
])

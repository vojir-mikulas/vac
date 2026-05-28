export default {
  'ui/**/*.{ts,tsx,js,jsx}': [
    'pnpm --filter ui exec eslint --fix',
    'pnpm --filter ui exec prettier --write',
  ],
  'ui/**/*.{json,css,html,md}': ['pnpm --filter ui exec prettier --write'],
  'api/**/*.go': ['gofmt -w'],
}

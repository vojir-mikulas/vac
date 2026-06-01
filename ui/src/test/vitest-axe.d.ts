// Augments vitest's matcher interfaces with the axe matcher registered in
// src/test/setup.ts. The package's own extend-expect targets an older vitest
// type surface, so we declare it against vitest 4's interfaces directly.
import 'vitest'
import type { AxeMatchers } from 'vitest-axe/matchers'

declare module 'vitest' {
  // The type param must match vitest's own Assertion<T> for declaration merging,
  // even though the axe matchers don't use it.
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type, @typescript-eslint/no-unused-vars
  interface Assertion<T = unknown> extends AxeMatchers {}
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  interface AsymmetricMatchersContaining extends AxeMatchers {}
}

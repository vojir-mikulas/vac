import { readFileSync } from 'node:fs'
import path from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'

// es-toolkit's `./compat/*` subpath exports are CJS-only — `compat/get.js` is just
// `module.exports = require('../dist/.../get.js').get` with no `import` condition.
// recharts pulls these in via *default* imports (`import get from 'es-toolkit/compat/get'`),
// so under the production build rolldown has to interop a deep tree of CJS modules, and
// that interop is mis-compiled (chunk-layout dependent): the named exports come out
// `undefined`, so every recharts dataKey read blows up with "t is not a function" and the
// whole chart subtree hits the error boundary ("Something went wrong").
//
// es-toolkit ships clean ESM (`*.mjs`) alongside the CJS, so we sidestep the interop
// entirely: redirect each `es-toolkit/compat/<name>` import to a tiny virtual module that
// re-exports the matching `.mjs` (which uses a named export) as the default the consumer
// expects. Build-only — dev's esbuild prebundle already handles the CJS correctly.
const VIRTUAL_PREFIX = '\0es-toolkit-compat:'

function esToolkitCompatEsm(): Plugin {
  return {
    name: 'es-toolkit-compat-esm',
    enforce: 'pre',
    apply: 'build',
    async resolveId(source, importer) {
      const match = /^es-toolkit\/compat\/([\w-]+)$/.exec(source)
      if (!match) return null
      const name = match[1]
      // Resolve the real CJS wrapper, then read it to learn the dist subpath
      // (`object/`, `array/`, …) so we can point at the sibling `.mjs`. If any
      // of this stops matching (e.g. a future es-toolkit restructures its
      // wrappers), FAIL THE BUILD rather than silently falling back to the
      // broken CJS interop — a silent regression is exactly how this bug shipped.
      const resolved = await this.resolve(source, importer, { skipSelf: true })
      if (!resolved) {
        this.error(`[es-toolkit-compat-esm] could not resolve ${source}`)
      }
      const wrapper = readFileSync(resolved.id, 'utf8')
      const rel = /require\((['"])(\.\.\/dist\/compat\/.+?)\.js\1\)/.exec(wrapper)
      if (!rel) {
        this.error(
          `[es-toolkit-compat-esm] ${source} no longer looks like a CJS re-export wrapper ` +
            `(${resolved.id}). The es-toolkit layout changed — update this plugin or the ` +
            `recharts charts will crash in the production build.`,
        )
      }
      const mjs = path.resolve(path.dirname(resolved.id), `${rel[2]}.mjs`)
      return `${VIRTUAL_PREFIX}${name}:${mjs}`
    },
    load(id) {
      if (!id.startsWith(VIRTUAL_PREFIX)) return null
      const rest = id.slice(VIRTUAL_PREFIX.length)
      const sep = rest.indexOf(':')
      const name = rest.slice(0, sep)
      const mjs = rest.slice(sep + 1)
      const spec = JSON.stringify(mjs)
      return `export * from ${spec}\nexport { ${name} as default } from ${spec}\n`
    },
  }
}

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // The `mock` mode (see .env.mock / `pnpm build:mock`) produces a standalone
  // static preview that ships the in-browser mock backend and needs no API.
  // It emits to ./dist instead of the Go-embed path so it never clobbers the
  // production bundle.
  const isMock = mode === 'mock'

  return {
    plugins: [
      esToolkitCompatEsm(),
      TanStackRouterVite({ target: 'react', autoCodeSplitting: true }),
      react(),
      tailwindcss(),
    ],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    server: {
      port: 5173,
      proxy: {
        '/api': { target: 'http://localhost:9393', ws: true, changeOrigin: true },
        '/health': 'http://localhost:9393',
      },
    },
    build: {
      outDir: isMock ? 'dist' : '../api/internal/ui/dist',
      emptyOutDir: true,
    },
  }
})

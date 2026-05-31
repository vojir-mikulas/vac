import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // The `mock` mode (see .env.mock / `pnpm build:mock`) produces a standalone
  // static preview that ships the in-browser mock backend and needs no API.
  // It emits to ./dist instead of the Go-embed path so it never clobbers the
  // production bundle.
  const isMock = mode === 'mock'

  return {
    plugins: [
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
        '/api': { target: 'http://localhost:3000', ws: true, changeOrigin: true },
        '/health': 'http://localhost:3000',
      },
    },
    build: {
      outDir: isMock ? 'dist' : '../api/internal/ui/dist',
      emptyOutDir: true,
    },
  }
})

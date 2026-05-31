/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Set via .env.mock (mode=mock). When present, main.tsx boots the mock backend.
  readonly VITE_MOCK?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

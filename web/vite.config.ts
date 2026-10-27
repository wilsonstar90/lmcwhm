import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// In dev, run `lmcwhm serve` (see Makefile `dev`) and open http://localhost:5173/console
// so the security-entry cookie is set through the proxy.
const target = process.env.LMCWHM_DEV_TARGET ?? 'https://127.0.0.1:18888'

export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': { target, secure: false, changeOrigin: true },
      '/console': { target, secure: false, changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    // Keep web/dist/.keep (tracked so go:embed compiles before the UI is built).
    emptyOutDir: false,
    chunkSizeWarningLimit: 1500,
  },
})

import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// During development, `npm run dev` serves the UI and forwards API calls to a
// YardMaster running locally (YARDMASTER_HTTP_PORT, default 8080).
const backend = `http://127.0.0.1:${process.env.YARDMASTER_HTTP_PORT || 8080}`

export default defineConfig({
  plugins: [svelte()],
  build: { outDir: 'dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': { target: backend, changeOrigin: false },
      '/v1': backend,
      '/ca.crt': backend,
      '/setup': backend,
    },
  },
})

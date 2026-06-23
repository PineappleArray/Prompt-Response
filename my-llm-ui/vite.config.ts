import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // In production the app is served from /dashboard/ by the Go binary.
  // Setting base here ensures all asset URLs are prefixed correctly.
  base: '/dashboard/',
  build: {
    // Output goes into the Go embed directory so `go build` bundles the UI.
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/v1": "http://localhost:8080",
    },
  },
});

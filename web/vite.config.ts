import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// This file sets up Vite, which runs the website while coding (npm run dev) and
// builds it for Nginx (npm run build).
export default defineConfig({
  // Adds React support, including instant updates while editing.
  plugins: [react()],

  // While coding, pass /api requests to the Go API on port 8080, removing the
  // /api part first. This matches what Nginx does once deployed. ws: true
  // also passes on chat's WebSocket. /hls requests go to Nginx on port 80,
  // which passes them to the cache.
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''), // This turns "/api/videos" into "/videos".
        ws: true,
      },
      '/hls': 'http://localhost',
    },
  },
})

import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// Settings for Vite, which runs the site while coding (npm run dev) and
// builds it for Nginx (npm run build).
export default defineConfig({
  // Adds React support, including instant updates while editing.
  plugins: [react()],

  // While coding, pass /api requests to the Go API on port 8080, removing the
  // /api part first. This matches what Nginx does once deployed.
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''), // For example, "/api/videos" becomes "/videos".
      },
    },
  },
})

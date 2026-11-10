import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// During development, send /api requests to the Go API on port 8080.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        rewrite: (path) => path.replace(/^\/api/, ''),
      },
    },
  },
})

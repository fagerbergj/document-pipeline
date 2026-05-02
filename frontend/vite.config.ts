import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      // Long timeouts: first chat message can wait minutes while Ollama loads the model.
      '/api': {
        target: 'http://localhost:8000',
        timeout: 600000,
        proxyTimeout: 600000,
      },
      '/webhook': {
        target: 'http://localhost:8000',
        timeout: 600000,
        proxyTimeout: 600000,
      },
    }
  },
  build: {
    outDir: 'dist',
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    globals: true,
  },
})

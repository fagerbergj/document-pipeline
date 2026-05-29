/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { storybookTest } from '@storybook/addon-vitest/vitest-plugin';
import { playwright } from '@vitest/browser-playwright';
const dirname = typeof __dirname !== 'undefined' ? __dirname : path.dirname(fileURLToPath(import.meta.url));

const isTest = process.env.VITEST

export default defineConfig({
  plugins: [react()],
  server: {
    // During tests, disable proxy to avoid connecting to the backend (which isn't running)
    // Proxy is needed for dev to reach the backend for real API calls
    proxy: isTest ? {} : {
      // Long timeouts: first chat message can wait minutes while Ollama loads the model.
      '/api': {
        target: 'http://localhost:8000',
        timeout: 600000,
        proxyTimeout: 600000
      },
      '/webhook': {
        target: 'http://localhost:8000',
        timeout: 600000,
        proxyTimeout: 600000
      }
    }
  },
  build: {
    outDir: 'dist'
  },
  test: {
    projects: [{
      extends: true,
      test: {
        name: 'unit',
        environment: 'jsdom',
        setupFiles: ['./src/test/setup.ts'],
        globals: true
      }
    }, {
      extends: true,
      plugins: [
        storybookTest({
          configDir: path.join(dirname, '.storybook')
        })
      ],
      test: {
        name: 'storybook',
        browser: {
          enabled: true,
          headless: true,
          provider: playwright({}),
          instances: [{
            browser: 'chromium'
          }]
        }
      }
    }]
  },
  resolve: {
    // Browser-only resolve conditions for both tests and prod. Including 'node'
    // here makes Vite pick the Node entry points of packages with conditional
    // exports (e.g. vfile, reached via react-markdown), which call process.cwd()
    // in the browser and crash on the first markdown render.
    conditions: ['browser', 'module', 'import']
  }
});

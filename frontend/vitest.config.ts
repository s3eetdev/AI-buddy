import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    include: ['tests/**/*.test.ts', 'tests/**/*.test.tsx'],
    setupFiles: ['tests/setup.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/types/**'], // type declarations have no executable lines
      reporter: ['text', 'json', 'json-summary', 'lcov', 'html'],
      reportsDirectory: '../artifacts/coverage/frontend',
      thresholds: { lines: 81 },
    },
  },
})

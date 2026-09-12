import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, proxy: { '/health': process.env.API_PROXY_TARGET ?? 'http://localhost:8080' } },
  test: {
    environment: 'jsdom',
    coverage: {
      provider: 'v8', include: ['src/**/*.{ts,tsx}'], exclude: ['src/**/*.test.{ts,tsx}'],
      thresholds: { lines: 80, statements: 80, functions: 80, branches: 80 },
    },
  },
});

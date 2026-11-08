import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Dev server proxies API/WS to the Go hub on :8080 so the browser sees one origin.
// Production builds output to server/internal/webui/dist and are embedded in Go.
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        changeOrigin: false,
        ws: true,
      },
    },
  },
  build: {
    outDir: '../server/internal/webui/dist',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2020',
  },
});

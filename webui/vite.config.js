import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5166,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:5666',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
})

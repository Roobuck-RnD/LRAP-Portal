import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src')
    }
  },
  server: {
    host: true,           // optional: allow LAN access to the dev server
    port: 5173,           // the port in use
    proxy: {
      '/api': {
        target: 'http://10.10.18.1:9080',  // <- the address found above
        changeOrigin: true,
        // secure: false,  // add this if 9080 ever moves to https with a self-signed certificate
      },
    },
  },
})

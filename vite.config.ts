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
    host: true,           // 可选：允许局域网访问你的 dev server
    port: 5173,           // 你现在用的端口
    proxy: {
      '/api': {
        target: 'http://10.10.18.1:9080',  // ← 填上面查到的地址
        changeOrigin: true,
        // secure: false,  // 如果将来你的 9080 改成 https 且证书自签，则加这一行
      },
    },
  },
})

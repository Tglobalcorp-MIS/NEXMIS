import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

// go:embed all:dist 需要 dist 目錄存在：建置後補回 dist/.gitkeep
function keepGitkeep(): Plugin {
  return {
    name: 'nexmis-keep-gitkeep',
    apply: 'build',
    closeBundle() {
      writeFileSync(resolve(import.meta.dirname, 'dist/.gitkeep'), '')
    },
  }
}

const backend = process.env.NEXMIS_BACKEND ?? 'http://localhost:8080'

export default defineConfig({
  plugins: [react(), keepGitkeep()],
  server: {
    port: 5173,
    proxy: {
      '/api': { target: backend, changeOrigin: false, ws: true },
      '/healthz': { target: backend },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
    rolldownOptions: {
      output: {
        codeSplitting: {
          groups: [
            { name: 'react', test: /node_modules[\\/](react|react-dom|react-is|scheduler|react-router|use-sync-external-store)[\\/]/, priority: 30 },
            { name: 'charts', test: /node_modules[\\/](recharts|d3-[^\\/]+|victory-vendor|es-toolkit|immer|redux|@reduxjs|reselect)[\\/]/, priority: 15 },
            { name: 'antd-icons', test: /node_modules[\\/]@ant-design[\\/]icons/, priority: 22 },
            { name: 'antd-rc', test: /node_modules[\\/](rc-[^\\/]+|@rc-component|@babel[\\/]runtime|classnames|clsx|dayjs)[\\/]/, priority: 21 },
            { name: 'antd', test: /node_modules[\\/](antd|@ant-design)[\\/]/, priority: 20 },
            { name: 'vendor', test: /node_modules/, priority: 10 },
          ],
        },
      },
    },
  },
})

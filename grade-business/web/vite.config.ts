import { readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { gzipSync } from 'node:zlib'
import { defineConfig, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

/**
 * 用途 在构建阶段为静态脚本和样式预生成 gzip 文件。
 * 参数 无。
 * 返回值 仅在生产构建生效的 Vite 插件。
 */
function gzipAssets(): Plugin {
  return {
    name: 'gzip-static-assets',
    apply: 'build',
    enforce: 'post',
    /**
     * 用途 压缩实际落盘的脚本和样式，保持编码前后的文件内容一致。
     * 参数 options 为输出配置，bundle 为本次构建的资源集合。
     * 返回值 所有静态压缩文件写入后的任务。
     */
    async writeBundle(options, bundle) {
      if (!options.dir) return
      for (const filename of Object.keys(bundle)) {
        if (!/^assets\/.*\.(?:m?js|css)$/.test(filename)) continue
        const filepath = join(options.dir, filename)
        const bytes = await readFile(filepath)
        if (bytes.length < 1024) continue
        const compressed = gzipSync(bytes)
        if (compressed.length < bytes.length) await writeFile(filepath + '.gz', compressed)
      }
    },
  }
}

export default defineConfig({
  plugins: [vue(), gzipAssets()],
  server: {
    host: '127.0.0.1',
    proxy: { '/api': 'http://127.0.0.1:18444' },
  },
})

import { Texture } from 'three-signin'

/**
 * 用途 在后台生成高精度材质。浏览器无法完成后台绘制时由场景使用原生成器。
 * 参数 size 为贴图边长，signal 用于退出登录页时中止后台任务。
 * 返回值 按部件分组的贴图及资源清理方法，后台不可用时为空。
 */
export async function prepareMaterials(size, signal) {
  signal?.throwIfAborted()
  if (typeof Worker !== 'function' || typeof OffscreenCanvas !== 'function') return null
  let worker
  try {
    worker = new Worker(new URL('./materials.worker.js', import.meta.url), { type: 'module' })
  } catch {
    return null
  }

  const sources = await new Promise(resolve => {
    const finish = value => {
      signal?.removeEventListener('abort', cancel)
      worker.terminate()
      resolve(value)
    }
    const cancel = () => finish(null)
    signal?.addEventListener('abort', cancel, { once: true })
    worker.onmessage = event => finish(event.data)
    worker.onerror = cancel
    worker.onmessageerror = cancel
    worker.postMessage(size)
  })
  signal?.throwIfAborted()
  return sources ? materialTextures(sources) : null
}

/**
 * 用途 恢复贴图颜色空间和采样设置，并记录位图的生命周期。
 * 参数 sources 为后台返回的位图及原贴图参数。
 * 返回值 可交付给场景使用的贴图与释放函数。
 */
function materialTextures(sources) {
  const groups = {}
  const textures = []
  for (const [key, entries] of Object.entries(sources)) {
    groups[key] = {}
    for (const [name, { bitmap, colorSpace, anisotropy }] of Object.entries(entries)) {
      const texture = new Texture(bitmap)
      texture.flipY = false
      texture.colorSpace = colorSpace
      texture.anisotropy = anisotropy
      texture.needsUpdate = true
      textures.push(texture)
      groups[key][name] = texture
    }
  }
  const dispose = () => textures.forEach(texture => {
    texture.dispose()
    texture.image.close()
  })
  return { ...groups, textures, dispose }
}

/**
 * 用途 分批把完整贴图上传到 GPU，给表单输入与绘制留出处理时机。
 * 参数 renderer 为当前三维渲染器，textures 为待上传贴图，signal 为页面退出信号。
 * 返回值 上传任务，页面退出时停止后续上传。
 */
export async function uploadMaterials(renderer, textures, signal) {
  for (const texture of textures) {
    if (globalThis.scheduler?.yield) await globalThis.scheduler.yield()
    else await new Promise(resolve => setTimeout(resolve, 0))
    signal?.throwIfAborted()
    renderer.initTexture(texture)
  }
}

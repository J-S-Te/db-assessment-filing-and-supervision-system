import { createRadialDeckPBR, createShieldPBR, createSidePanelPBR } from './pbr.js'
import { createShieldProfile } from './shieldProfile.js'

/**
 * 用途 按原始参数生成模型贴图，并交付可直接上传到 WebGL 的位图。
 * 参数 size 为盾牌及基座台面的像素边长。
 * 返回值 各组位图和原贴图的采样设置。
 */
async function generateMaterials(size) {
  const context = new OffscreenCanvas(1, 1).getContext('2d')
  if (!context || !('filter' in context)) throw new Error('Offscreen 2D filters unavailable')
  const { outline, pbrOptions } = createShieldProfile(size)
  const sources = {
    shield: createShieldPBR(outline, pbrOptions),
    deck: createRadialDeckPBR({ size }),
    side: createSidePanelPBR({ size: Math.min(size, 1024) }),
  }
  const groups = {}
  const bitmaps = []
  for (const [key, textures] of Object.entries(sources)) {
    groups[key] = {}
    for (const [name, texture] of Object.entries(textures)) {
      // ImageBitmap 上传时不能再使用 UNPACK_FLIP_Y_WEBGL。此处沿用 CanvasTexture 的翻转方向。
      const bitmap = await createImageBitmap(texture.image, {
        imageOrientation: texture.flipY ? 'flipY' : 'from-image',
        premultiplyAlpha: 'none',
        colorSpaceConversion: 'none',
      })
      bitmaps.push(bitmap)
      groups[key][name] = { bitmap, colorSpace: texture.colorSpace, anisotropy: texture.anisotropy }
    }
  }
  return { groups, bitmaps }
}

self.onmessage = async ({ data: size }) => {
  try {
    const { groups, bitmaps } = await generateMaterials(size)
    self.postMessage(groups, bitmaps)
  } catch {
    self.postMessage(null)
  }
}

<script setup lang="ts">
import { onMounted, onUnmounted, ref, useTemplateRef, watch } from 'vue'
import shieldArt from '../assets/signin-security-shield.webp'
import { createShieldRenderer } from '../lib/signinShieldRenderer'

const props = defineProps<{ pitch: number; yaw: number; pulse: number }>()
const stage = useTemplateRef('stage')
const canvas = useTemplateRef('canvas')
const rendered = ref(false)
let renderer: ReturnType<typeof createShieldRenderer> = null
let motion: MediaQueryList | undefined
let resizeObserver: ResizeObserver | undefined
let textureRequest: AbortController | undefined
let layout = { width: 0, height: 0, modelWidth: 0 }
let impactStarted = -Infinity
let frameId = 0
let lastDraw = -Infinity
let needsDraw = true
let mounted = false

/**
 * 用途 合并同一帧内的姿态变更与响应式渲染请求。
 * 参数 无。
 * 返回值 无。
 */
function requestDraw() {
  needsDraw = true
  if (renderer && !frameId) frameId = requestAnimationFrame(draw)
}

/**
 * 用途 根据姿态、呼吸灯和点击时刻重绘同一个盾牌画布。
 * 参数 time 为浏览器当前的动画帧时间。
 * 返回值 无。
 */
function draw(time: number) {
  frameId = 0
  if (!renderer || !layout.width || !layout.height || !layout.modelWidth) return

  if (needsDraw || time - lastDraw >= 30) {
    const reduceMotion = motion?.matches
    renderer.draw({
      ...layout,
      pitch: props.pitch,
      yaw: props.yaw,
      glow: reduceMotion ? .85 : .85 + .15 * Math.cos(time / 3400 * 2 * Math.PI),
      impact: reduceMotion ? 1 : Math.max(0, Math.min(1, (time - impactStarted) / 500)),
    })
    if (!rendered.value) rendered.value = true
    needsDraw = false
    lastDraw = time
  }
  // 转动时按指针更新，待机呼吸灯每秒绘制约 30 帧，悬浮由 CSS 单独合成。
  if (!motion?.matches) frameId = requestAnimationFrame(draw)
}

/**
 * 用途 在布局变化时记录画布和模型的基准尺寸。
 * 参数 无。
 * 返回值 无。
 */
function resize() {
  layout = {
    width: canvas.value?.offsetWidth ?? 0,
    height: canvas.value?.offsetHeight ?? 0,
    modelWidth: stage.value?.offsetWidth ?? 0,
  }
  requestDraw()
}

/**
 * 用途 加载一张压缩尺寸的纹理并初始化可旋转盾牌。
 * 参数 无。
 * 返回值 加载并初始化绘图资源的异步任务。
 */
async function loadShield() {
  const target = canvas.value
  if (!target || !mounted) return
  textureRequest?.abort()
  const request = new AbortController()
  textureRequest = request
  let bitmap: ImageBitmap | undefined

  try {
    const response = await fetch(shieldArt, { signal: request.signal })
    if (!response.ok || request.signal.aborted) return
    bitmap = await createImageBitmap(await response.blob(), {
      imageOrientation: 'flipY', resizeWidth: 768, resizeHeight: 960,
      resizeQuality: 'high', premultiplyAlpha: 'none',
    })
    if (!mounted || request.signal.aborted) return
    renderer = createShieldRenderer(target, bitmap)
    resize()
  } catch (error) {
    // 在 GPU 不可用时仍显示原尺寸盾牌素材，确保登录入口可正常使用。
    if (!request.signal.aborted) console.warn('盾牌画布初始化失败', error)
  } finally {
    bitmap?.close()
    if (textureRequest === request) textureRequest = undefined
  }
}

/**
 * 用途 回收当前 3D 渲染资源，供组件退出或图形上下文丢失时调用。
 * 参数 无。
 * 返回值 无。
 */
function disposeShield() {
  textureRequest?.abort()
  cancelAnimationFrame(frameId)
  frameId = 0
  renderer?.dispose()
  renderer = null
  rendered.value = false
}

watch(() => [props.pitch, props.yaw], requestDraw)
watch(() => props.pulse, () => {
  impactStarted = performance.now()
  requestDraw()
})

onMounted(() => {
  mounted = true
  motion = window.matchMedia('(prefers-reduced-motion: reduce)')
  motion.addEventListener('change', requestDraw)
  resizeObserver = new ResizeObserver(resize)
  if (stage.value) resizeObserver.observe(stage.value)
  void loadShield()
})

onUnmounted(() => {
  mounted = false
  resizeObserver?.disconnect()
  motion?.removeEventListener('change', requestDraw)
  disposeShield()
})
</script>

<template>
  <span ref="stage" class="shield-model" :data-renderer="rendered ? 'webgl' : 'image'">
    <canvas ref="canvas" class="shield-canvas" :class="{ 'shield-canvas-ready': rendered }"
      @webglcontextlost.prevent="disposeShield" @webglcontextrestored="loadShield"></canvas>
    <img v-if="!rendered" class="shield-image" :src="shieldArt" alt="" width="1122" height="1402"
      draggable="false" :style="{ '--pitch': pitch + 'deg', '--yaw': yaw + 'deg' }">
  </span>
</template>

<style scoped>
.shield-model,
.shield-image {
  position: absolute;
  inset: 0;
}

.shield-canvas {
  position: absolute;
  inset: -16% -20%;
  width: 140%;
  height: 132%;
  visibility: hidden;
  filter: brightness(var(--shield-brightness, 1));
}

.shield-canvas-ready { visibility: visible; }

.shield-image {
  width: 100%;
  height: 100%;
  object-fit: contain;
  transform: rotateX(var(--pitch)) rotateY(var(--yaw)) scaleX(.93);
}
</style>

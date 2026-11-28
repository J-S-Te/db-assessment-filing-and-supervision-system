<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import type { SignInSceneController } from '../features/signin3d/mount.js'
import widePreview from '../assets/signin-preview-wide.webp'
import portraitPreview from '../assets/signin-preview-portrait.webp'
import shortPreview from '../assets/signin-preview-short.webp'

const canvas = ref<HTMLCanvasElement>()
const layer = ref<HTMLDivElement>()
const state = ref<'loading' | 'ready' | 'fallback'>('loading')
const scale = ref(1)
const dimensions = ref('')
const qualities = [1, 1.5, 2, 4]
const lifetime = new AbortController()
let frame = 0
let paintObserver: PerformanceObserver | undefined
let scene: SignInSceneController | undefined

/**
 * 用途 延迟载入三维舱室，让认证表单可以独立显示。
 * 参数 无，使用本组件的画布和辅助节点。
 * 返回值 场景加载任务，设备不支持时显示背景。
 */
async function initialize() {
  try {
    const { mountScene } = await import('../features/signin3d/mount.js')
    if (lifetime.signal.aborted || !canvas.value || !layer.value) return
    scene = await mountScene({ canvas: canvas.value, layer: layer.value }, lifetime.signal,
      (next, width, height) => {
        scale.value = next
        dimensions.value = `${width} × ${height}`
      },
      () => { state.value = 'fallback' },
      revealScene,
    )
    if (lifetime.signal.aborted) {
      scene.dispose()
      return
    }
  } catch {
    if (!lifetime.signal.aborted) state.value = 'fallback'
  }
}

/**
 * 用途 待渲染器完成首帧后，在下一次绘制中淡入画布。
 * 参数 无，读取当前场景状态以防已退出或绘图上下文丢失。
 * 返回值 无。
 */
function revealScene() {
  frame = requestAnimationFrame(() => {
    if (!lifetime.signal.aborted && state.value === 'loading') state.value = 'ready'
  })
}

/**
 * 用途 将三维启动排在当前可见页面绘制之后。
 * 参数 无。
 * 返回值 无。
 */
function scheduleScene() {
  frame = requestAnimationFrame(() => {
    frame = requestAnimationFrame(() => { void initialize() })
  })
}

onMounted(() => {
  // 冷启动以实际首次内容绘制为准，避免 GPU 初始化或后台任务占用首屏提交时机。
  if (!window.PerformanceObserver?.supportedEntryTypes.includes('paint') ||
    performance.getEntriesByName('first-contentful-paint', 'paint').length > 0) {
    scheduleScene()
    return
  }
  paintObserver = new PerformanceObserver((entries) => {
    if (!entries.getEntriesByName('first-contentful-paint').length) return
    paintObserver?.disconnect()
    scheduleScene()
  })
  paintObserver.observe({ type: 'paint', buffered: true })
})
onUnmounted(() => {
  paintObserver?.disconnect()
  cancelAnimationFrame(frame)
  lifetime.abort()
  scene?.dispose()
})
</script>

<template>
  <picture class="scene-preview" aria-hidden="true">
    <source :srcset="shortPreview"
      media="(min-aspect-ratio: 1/2) and (max-width: 900px), (min-aspect-ratio: 1/2) and (max-height: 620px)">
    <source :srcset="portraitPreview" media="(max-width: 900px), (max-height: 620px)">
    <img :src="widePreview" alt="" fetchpriority="high" decoding="async">
  </picture>
  <canvas ref="canvas" v-show="state !== 'fallback'" class="signin-stage"
    :class="{ 'is-ready': state === 'ready' }" :aria-hidden="state !== 'ready'"
    aria-label="三维安全舱室场景" />
  <div ref="layer" class="hud-layer" :class="{ 'is-ready': state === 'ready' }" aria-hidden="true" />

  <template v-if="state === 'ready'">
    <div class="scene-hint" aria-hidden="true">
      <span class="scene-hint__item"><b>拖动</b>360° 旋转</span>
      <span class="scene-hint__sep"></span>
      <span class="scene-hint__item"><b>滚轮</b>缩放远近</span>
    </div>
    <div class="quality" role="group" aria-label="渲染倍率">
      <span class="quality__label">渲染倍率</span>
      <div class="quality__opts">
        <button v-for="quality in qualities" :key="quality" type="button" :class="{ 'is-active': quality === scale }"
          :aria-pressed="quality === scale" @click="scene?.setResolution(quality)">{{ quality }}×</button>
      </div>
      <span class="quality__out">{{ dimensions }}</span>
    </div>
  </template>

</template>

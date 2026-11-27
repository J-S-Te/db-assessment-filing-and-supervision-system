<script setup lang="ts">
import { onScopeDispose, ref } from 'vue'
import { Pause, Play } from '@lucide/vue'
import cityImage from '../assets/cockpit-cyber-city.webp'

const scene = ref<HTMLElement>()
const paused = ref(false)
let frame = 0
let x = 0
let y = 0

/**
 * 用途 将鼠标相对画面的位置转成限幅位移，每帧只写入一次。
 * 参数 event 为中央城市画面收到的指针移动。
 * 返回值 无。
 */
function followPointer(event: PointerEvent) {
  if (paused.value || event.pointerType !== 'mouse' || !scene.value) return
  const bounds = scene.value.getBoundingClientRect()
  if (!bounds.width || !bounds.height) return
  x = ((event.clientX - bounds.left) / bounds.width - .5) * 22
  y = ((event.clientY - bounds.top) / bounds.height - .5) * 14
  if (!frame) frame = requestAnimationFrame(() => {
    frame = 0
    scene.value?.style.setProperty('--city-x', x.toFixed(1) + 'px')
    scene.value?.style.setProperty('--city-y', y.toFixed(1) + 'px')
  })
}

/**
 * 用途 停止待应用的视差并使画面回到中心。
 * 参数 无。
 * 返回值 无。
 */
function resetPointer() {
  cancelAnimationFrame(frame)
  frame = 0
  scene.value?.style.removeProperty('--city-x')
  scene.value?.style.removeProperty('--city-y')
}

/**
 * 用途 暂停或恢复城市底图的装饰动效。
 * 参数 无。
 * 返回值 无。
 */
function toggleMotion() {
  paused.value = !paused.value
  resetPointer()
}

onScopeDispose(resetPointer)
</script>

<template>
  <div ref="scene" class="city-scene" :class="{ paused }"
    @pointermove="followPointer" @pointerleave="resetPointer">
    <div class="city-viewport" aria-hidden="true">
      <img :src="cityImage" alt="" decoding="async" fetchpriority="high" />
      <span class="city-sweep"></span>
      <i class="city-beacon"></i><i class="city-beacon"></i>
    </div>
    <div class="city-controls">
      <span>城市态势示意</span>
      <button type="button" class="motion-control" aria-label="暂停城市动效" :aria-pressed="paused"
        :title="paused ? '恢复城市动效' : '暂停城市动效'" @click="toggleMotion">
        <Play v-if="paused" aria-hidden="true" /><Pause v-else aria-hidden="true" />
        {{ paused ? '恢复动效' : '暂停动效' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.city-scene { position: absolute; inset: 0; overflow: hidden; background: #091d36; }
.city-viewport {
  position: absolute; inset: -3%; pointer-events: none;
  transform: translate3d(var(--city-x, 0px), var(--city-y, 0px), 0); transition: transform .3s ease-out;
}
.city-viewport img {
  width: 100%; height: 100%; object-fit: cover; object-position: 50% 48%;
  animation: city-drift 22s ease-in-out infinite alternate;
}
.city-sweep {
  position: absolute; inset: 0 auto 0 -26%; width: 25%;
  background: linear-gradient(90deg, transparent, #2487c818 65%, #66ddff39 84%, transparent);
  opacity: 0; animation: city-sweep 18s ease-in-out infinite;
}
.city-beacon {
  position: absolute; width: 5px; height: 5px; border-radius: 50%; background: #b9f2ff;
  box-shadow: 0 0 9px 2px #29b5fd; opacity: .32; animation: beacon-pulse 8s ease-in-out infinite;
}
.city-beacon:nth-of-type(1) { top: 34%; left: 24%; }
.city-beacon:nth-of-type(2) { top: 24%; left: 56%; animation-delay: -2s; }
.city-scene::after {
  content: ''; position: absolute; inset: 0; pointer-events: none;
  background: linear-gradient(0deg, #05101ded, #0615260a 56%, #04102555);
}
.city-controls { position: absolute; z-index: 1; top: .75em; right: .8em; display: flex; align-items: center; gap: .45em; }
.city-controls > span, .motion-control {
  padding: .24em .45em; border-radius: 3px; background: #071524ea; color: #d0e7f8; font-size: .76em;
}
.motion-control { display: inline-flex; align-items: center; gap: .35em; border: 1px solid #42759b; }
.motion-control:hover { background: #15344e; }
.motion-control svg { width: .95em; height: .95em; }
.paused .city-viewport { transform: none; }
.paused .city-viewport * { animation-play-state: paused; }
@keyframes city-drift { from { transform: scale(1); } to { transform: scale(1.08) translateY(-1%); } }
@keyframes city-sweep {
  0%, 13% { transform: translateX(0); opacity: 0; }
  24%, 69% { opacity: .78; }
  85%, 100% { transform: translateX(530%); opacity: 0; }
}
@keyframes beacon-pulse {
  0%, 100% { scale: .75; opacity: .25; }
  50% { scale: 1.3; opacity: .6; }
}
@media (prefers-reduced-motion: reduce) {
  .city-viewport { transform: none; transition: none; }
  .city-viewport :is(img, .city-sweep, .city-beacon) { animation: none; }
  .motion-control { display: none; }
}
</style>

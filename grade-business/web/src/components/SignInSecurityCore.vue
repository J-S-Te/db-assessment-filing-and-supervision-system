<script setup lang="ts">
import { onMounted, onUnmounted, ref, useTemplateRef } from 'vue'
import { Activity } from '@lucide/vue'
import baseArt from '../assets/signin-security-base.webp'
import SignInShieldModel from './SignInShieldModel.vue'

defineProps<{ loginState: 'idle' | 'checking' | 'success' | 'error' }>()

const tilt = useTemplateRef('tilt')
const orbit = useTemplateRef('orbit')
const impact = useTemplateRef('impact')
const pulse = ref(0)
const impactDuration = 500
const defaultPitch = 3
const defaultYaw = -24
const pitch = ref(defaultPitch)
const yaw = ref(defaultYaw)
let drag: { pointerId: number; x: number; y: number } | undefined
let dragged = false

/**
 * 用途 根据鼠标相对视口中心的位置调整盾牌透视，合成倾角上限为 12 度。
 * 参数 event 为页面鼠标移动事件。
 * 返回值 无。
 */
function followPointer(event: MouseEvent) {
  if (drag) return
  const x = 2 * event.clientX / window.innerWidth - 1
  const y = 2 * event.clientY / window.innerHeight - 1
  const angle = 12 / Math.max(1, Math.hypot(x, y))
  tilt.value?.style.setProperty('--tilt-x', `${-y * angle}deg`)
  tilt.value?.style.setProperty('--tilt-y', `${x * angle}deg`)
}

/**
 * 用途 在鼠标离开视口或窗口失焦时让盾牌和围栏回到默认状态。
 * 参数 event 为视口离开或窗口失焦事件。
 * 返回值 无。
 */
function resetPointer(event: Event) {
  if (event.type === 'blur') drag = undefined
  tilt.value?.style.removeProperty('--tilt-x')
  tilt.value?.style.removeProperty('--tilt-y')
  setOrbitSpeed(1)
}

/**
 * 用途 调整围栏转速，同时保留当前转动位置。
 * 参数 rate 为围栏相对默认转速的倍率。
 * 返回值 无。
 */
function setOrbitSpeed(rate: number) {
  orbit.value?.getAnimations()[0]?.updatePlaybackRate(rate)
}

/**
 * 用途 记录按下位置并捕获指针，让拖动越过盾牌边沿后仍能连续旋转。
 * 参数 event 为盾牌上鼠标或触屏的按下事件。
 * 返回值 无。
 */
function startDrag(event: PointerEvent) {
  if (drag || !event.isPrimary || event.button !== 0) return
  const target = event.currentTarget as HTMLButtonElement
  target.setPointerCapture(event.pointerId)
  drag = { pointerId: event.pointerId, x: event.clientX, y: event.clientY }
  dragged = false
}

/**
 * 用途 横向旋转完整的盾牌模型，纵向调整观察角度。
 * 参数 event 为被捕获指针的移动事件。
 * 返回值 无。
 */
function moveDrag(event: PointerEvent) {
  if (drag?.pointerId !== event.pointerId) return
  if (!(event.buttons & 1)) {
    endDrag(event)
    return
  }

  const x = event.clientX - drag.x
  const y = event.clientY - drag.y
  if (!dragged && Math.hypot(x, y) < 5) return

  dragged = true
  yaw.value += x * 1.1
  pitch.value = Math.max(-60, Math.min(60, pitch.value - y * .45))
  drag.x = event.clientX
  drag.y = event.clientY
}

/**
 * 用途 在释放或取消拖动时保留当前观察角度并结束本次手势。
 * 参数 event 为指针释放、取消或发现主键已松开的移动事件。
 * 返回值 无。
 */
function endDrag(event: PointerEvent) {
  if (drag?.pointerId === event.pointerId) drag = undefined
}

/**
 * 用途 同步触发悬浮组轻震和核心冲击波，区分拖动产生的点击事件。
 * 参数 event 为按钮点击事件，detail 为 0 时代表非指针激活。
 * 返回值 无。
 */
function activate(event: MouseEvent) {
  if (dragged && event.detail !== 0) return

  pulse.value++
  const group = impact.value
  if (!group || window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

  // 只重启受击层，重复点击时保留悬浮和围栏的当前动画进度。
  group.getAnimations().forEach(animation => animation.cancel())
  group.animate([
    { transform: 'translate(0)' },
    { transform: 'translate(-3.5px, 1px)', offset: .12 },
    { transform: 'translate(2.5px, -1px)', offset: .27 },
    { transform: 'translate(-1.5px, .5px)', offset: .44 },
    { transform: 'translate(.6px, 0)', offset: .67 },
    { transform: 'translate(0)' },
  ], { duration: impactDuration, easing: 'ease-out' })
}

/**
 * 用途 通过方向键旋转盾牌，使用 Home 回到初始观察角度。
 * 参数 event 为盾牌按钮获得焦点时的按键事件。
 * 返回值 无。
 */
function rotateWithKeyboard(event: KeyboardEvent) {
  switch (event.key) {
    case 'ArrowLeft':
      yaw.value -= 15
      break
    case 'ArrowRight':
      yaw.value += 15
      break
    case 'ArrowUp':
      pitch.value = Math.min(60, pitch.value + 10)
      break
    case 'ArrowDown':
      pitch.value = Math.max(-60, pitch.value - 10)
      break
    case 'Home':
      yaw.value = defaultYaw + 360 * Math.round((yaw.value - defaultYaw) / 360)
      pitch.value = defaultPitch
      break
    default: return
  }
  event.preventDefault()
}

onMounted(() => {
  window.addEventListener('mousemove', followPointer, { passive: true })
  window.addEventListener('pointermove', moveDrag, { passive: true })
  window.addEventListener('pointerup', endDrag)
  window.addEventListener('pointercancel', endDrag)
  document.documentElement.addEventListener('mouseleave', resetPointer)
  window.addEventListener('blur', resetPointer)
})

onUnmounted(() => {
  window.removeEventListener('mousemove', followPointer)
  window.removeEventListener('pointermove', moveDrag)
  window.removeEventListener('pointerup', endDrag)
  window.removeEventListener('pointercancel', endDrag)
  document.documentElement.removeEventListener('mouseleave', resetPointer)
  window.removeEventListener('blur', resetPointer)
})
</script>

<template>
  <figure class="security-scene" aria-label="安防盾牌"
    :style="{ '--security-impact-duration': impactDuration + 'ms' }">
    <div class="security-stage" :data-login-state="loginState">
      <img class="security-base" :src="baseArt" alt="" width="1122" height="1402">
      <img class="security-base-glow" :src="baseArt" alt="" width="1122" height="1402">
      <div class="security-grid" aria-hidden="true"></div>
      <div class="security-flow" aria-hidden="true"><i></i><i></i><i></i></div>
      <button class="security-core" type="button" aria-label="旋转或激活安防盾牌"
        aria-describedby="security-controls"
        @mouseenter="setOrbitSpeed(1.4)" @mouseleave="setOrbitSpeed(1)"
        @pointerdown="startDrag"
        @keydown="rotateWithKeyboard" @click="activate">
        <span class="security-float" aria-hidden="true">
          <span ref="impact" class="security-impact">
            <span ref="tilt" class="security-tilt">
              <SignInShieldModel :pitch="pitch" :yaw="yaw" :pulse="pulse" />
              <span class="security-orbit">
                <span ref="orbit" class="security-orbit-spin">
                  <span v-for="mark in 18" :key="mark" class="security-orbit-mark"
                    :style="{ rotate: mark * 20 + 'deg' }"></span>
                </span>
              </span>
            </span>
          </span>
        </span>
      </button>
    </div>
    <div class="security-frame" aria-hidden="true"></div>
    <figcaption class="security-caption">
      <span class="security-caption-icon" :class="{ 'security-caption-active': pulse }">
        <Activity :size="17" aria-hidden="true" />
      </span>
      <span>
        <span id="security-controls" class="sr-only">方向键旋转，Home 键复位，回车键激活。</span>
        <span class="security-caption-status" role="status">
          {{ pulse ? '核心已激活 · 拖动旋转' : '拖动旋转 · 点击激活' }}
        </span>
      </span>
    </figcaption>
  </figure>
</template>

<style scoped>
.security-scene {
  --signal: #8de5ff;
  position: relative;
  isolation: isolate;
  width: 100%;
  aspect-ratio: 1.1;
  margin: 0;
  color: #e8f7ff;
}

.security-stage {
  --shield-unit: .43cqw;
  position: absolute;
  inset: 5%;
  container-type: inline-size;
}

.security-base,
.security-base-glow,
.security-grid,
.security-flow {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
}

.security-base {
  object-fit: fill;
  mask-image: linear-gradient(90deg, transparent, #000 10% 90%, transparent),
    linear-gradient(180deg, transparent, #000 22% 87%, transparent);
  mask-composite: intersect;
}

.security-base-glow {
  object-fit: fill;
  opacity: 0;
  filter: var(--base-lamp-filter, brightness(1.2));
  /* 只调红色内环和三条灯带，让底板和蓝色外环保留素材的原色。 */
  mask-image:
    radial-gradient(ellipse 17.5% 3.6% at 50% 73.5%, #000 84%, transparent),
    radial-gradient(ellipse 7% 1.4% at 24% 84.7%, #000 63%, transparent),
    radial-gradient(ellipse 13% 1.5% at 50% 84.4%, #000 63%, transparent),
    radial-gradient(ellipse 7% 1.4% at 76% 84.7%, #000 63%, transparent);
  transition: opacity .3s ease-in-out;
}

.security-stage:has(.security-core:is(:hover, :focus-visible)) .security-base-glow { opacity: 1; }

.security-stage[data-login-state="checking"] {
  --base-lamp-filter: hue-rotate(180deg) saturate(1.35) brightness(1.6);
}

.security-stage[data-login-state="success"] {
  --base-lamp-filter: hue-rotate(120deg) saturate(1.4) brightness(1.65);
}

.security-stage[data-login-state="error"] {
  --base-lamp-filter: saturate(1.35) brightness(1.45);
}

.security-stage[data-login-state]:not([data-login-state="idle"]) .security-base-glow { opacity: 1; }

.security-grid {
  background: radial-gradient(circle, #5bc6ff3d 1px, transparent 1.5px) 0 0 / 22px 22px;
  mask-image: radial-gradient(ellipse at 42% 35%, #000, transparent 66%);
}

.security-flow {
  inset-block: 22% auto;
  height: 49%;
  overflow: hidden;
  mask-image: linear-gradient(90deg, transparent, #000 10% 89%, transparent);
}

.security-flow i {
  position: absolute;
  left: 0;
  top: 22%;
  width: 100%;
  height: 1px;
  background: #69c9ff20;
  transform: skewY(-8deg);
}

.security-flow i:nth-child(2) { top: 49%; transform: skewY(7deg); }
.security-flow i:nth-child(3) { top: 73%; transform: skewY(-5deg); }

.security-flow i::after {
  position: absolute;
  left: 0;
  width: 22%;
  height: 100%;
  content: "";
  background: linear-gradient(90deg, transparent, #68caff, #c9f7ff, transparent);
  box-shadow: 0 0 9px #369dff;
  animation: security-data-flow 6s linear infinite;
}

.security-flow i:nth-child(2)::after { animation-delay: -2.5s; }
.security-flow i:nth-child(3)::after { animation-delay: -4.5s; }

.security-frame {
  position: absolute;
  inset: 3% 0 2%;
  opacity: .65;
  pointer-events: none;
  --corner: linear-gradient(#65b4e386, #65b4e386);
  background:
    var(--corner) left top / 17px 1px, var(--corner) left bottom / 17px 1px,
    var(--corner) right top / 17px 1px, var(--corner) right bottom / 17px 1px,
    var(--corner) left top / 1px 17px, var(--corner) right top / 1px 17px,
    var(--corner) left bottom / 1px 17px, var(--corner) right bottom / 1px 17px;
  background-repeat: no-repeat;
}

.security-core {
  position: absolute;
  left: 50%;
  top: 4.6%;
  width: calc(var(--shield-unit) * 100);
  aspect-ratio: 1122 / 1402;
  padding: 0;
  border: 0;
  border-radius: 24% 24% 46% 46%;
  background: transparent;
  cursor: grab;
  perspective: 1000px;
  touch-action: none;
  transform: translateX(-50%);
}

.security-core:focus-visible { outline-color: var(--signal); }
.security-core:active { cursor: grabbing; }

.security-core::before {
  position: absolute;
  inset: 0 -15%;
  border-radius: 50%;
  background: radial-gradient(ellipse, #6eafff37, transparent 69%);
  opacity: .55;
  pointer-events: none;
  content: "";
  transition: opacity .28s ease-in-out;
}

.security-core:is(:hover, :focus-visible)::before { opacity: 1; }

.security-float,
.security-impact,
.security-tilt {
  display: block;
  position: absolute;
  inset: 0;
  pointer-events: none;
  transform-style: preserve-3d;
}

.security-float { animation: security-hover 3s ease-in-out infinite; }

.security-tilt {
  transform: rotateX(var(--tilt-x, 0deg)) rotateY(var(--tilt-y, 0deg));
  scale: 1;
  transition: transform .38s cubic-bezier(.19, 1, .22, 1), scale .3s ease-in-out;
}

.security-core:is(:hover, :focus-visible) .security-tilt {
  --shield-brightness: 1.1;
  scale: 1.05;
}

.security-orbit {
  position: absolute;
  left: -22%;
  top: 55%;
  width: 144%;
  aspect-ratio: 1;
  transform: translateY(-50%) rotate(-12deg) rotateX(73deg);
  pointer-events: none;
}

.security-orbit-spin {
  position: absolute;
  inset: 0;
  border: 1px solid #a7d8f178;
  border-radius: 50%;
  box-shadow: 0 0 9px #73beff45, inset 0 0 8px #8fccf32d;
  animation: security-orbit-turn 15s linear infinite;
}

.security-orbit-spin::after {
  position: absolute;
  border-radius: 50%;
  inset: -4%;
  content: "";
}

.security-orbit-mark {
  position: absolute;
  inset: 0;
}

.security-orbit-mark::before {
  position: absolute;
  top: 0;
  left: 50%;
  width: clamp(8px, 1.3vw, 19px);
  height: clamp(14px, 2.2vw, 31px);
  translate: -50% -50%;
  background: #b1dffabf;
  clip-path: polygon(0 49%, 33% 49%, 48% 0, 58% 64%, 73% 49%, 100% 49%,
    100% 53%, 74% 53%, 56% 77%, 48% 20%, 36% 53%, 0 53%);
  content: "";
}

.security-orbit-spin::after {
  inset: -.5%;
  background: conic-gradient(from 16deg, #b2e9ff 0deg 5deg, transparent 56deg 175deg,
    #ff7185 176deg 182deg, transparent 205deg 328deg, #b2e9ff 359deg);
  mask-image: radial-gradient(transparent 69.4%, #000 70% 71%, transparent 71.6%);
}

.security-caption {
  position: absolute;
  display: flex;
  align-items: center;
  gap: 11px;
  inset: auto auto 5% 5%;
}

.security-caption-icon {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 1px solid #4d9cce68;
  border-radius: 8px;
  background: #10243ad9;
  color: var(--signal);
}

.security-caption-active { color: #ff8998; border-color: #ff637282; }
.security-caption-status { display: block; color: #9cb1cc; font-size: .8rem; }

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

@keyframes security-data-flow {
  from { translate: -110% 0; }
  to { translate: 560% 0; }
}

@keyframes security-hover {
  0%, 100% { transform: translateY(-17px); }
  50% { transform: translateY(17px); }
}

@keyframes security-orbit-turn { to { rotate: 360deg; } }

@media (max-width: 760px) {
  .security-scene { width: min(100%, 335px); }
  .security-stage { --shield-unit: .40cqw; }
  .security-caption { bottom: 3%; left: 3%; }
  .security-caption-icon { width: 30px; height: 30px; }
}

@media (prefers-reduced-motion: reduce) {
  .security-float, .security-orbit-spin, .security-flow i::after { animation: none; }
  .security-tilt { transform: none; }
  .security-impact { transform: none !important; }
  .security-tilt, .security-base-glow, .security-core::before { transition: none; }
  .security-core:is(:hover, :focus-visible) .security-tilt { scale: 1; }
}
</style>

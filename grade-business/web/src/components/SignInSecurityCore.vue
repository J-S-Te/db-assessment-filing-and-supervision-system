<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useTemplateRef } from 'vue'
import { Activity } from '@lucide/vue'
import baseArt from '../assets/signin-security-base.webp'
import SignInShieldModel from './SignInShieldModel.vue'

const props = defineProps<{ loginState: 'idle' | 'checking' | 'success' | 'error' }>()

const tilt = useTemplateRef('tilt')
const orbit = useTemplateRef('orbit')
const impact = useTemplateRef('impact')
const shieldButton = useTemplateRef('shieldButton')
const pulse = ref(0)
const impactDuration = 500
const resetDuration = 360
const defaultPitch = 3
const defaultYaw = -24
const pitch = ref(defaultPitch)
const yaw = ref(defaultYaw)
let drag: { pointerId: number; x: number; y: number } | undefined
let dragged = false
let restoreAfterMouseDrag = false
let resetFrame = 0

/** 平台台面在舞台高度中的位置，基座裁切后保持在同一水平线上。 */
const platformTop = '87.1%'

/**
 * 用途 生成环绕盾牌的粒子节点，按角度、半径和景深偏移排布成一圈三维轨道。
 * 参数 无。
 * 返回值 粒子的角度、半径、景深与动画错相序列。
 */
function buildParticles(count: number, radius: number, depth: number) {
  return Array.from({ length: count }, (_, index) => ({
    angle: (360 / count) * index,
    radius,
    depth: Math.sin(index * 0.47) * depth,
    delay: -(index * 0.13).toFixed(2),
  }))
}

const particlesNear = buildParticles(40, 32.8, 4.6)
const particlesFar = buildParticles(28, 24.4, -3.2)

/**
 * 用途 生成可无缝横向滚动的信号波形路径，首尾两段波形完全一致。
 * 参数 无。
 * 返回值 两倍宽度的折线路径。
 */
const wavePath = computed(() => {
  const points: string[] = []
  for (let index = 0; index <= 48; index++) {
    const x = index * 5
    const y = 13 + Math.sin(index * 0.9) * 7 + Math.sin(index * 2.3) * 3.2
    points.push(`${index ? 'L' : 'M'}${x} ${y.toFixed(1)}`)
  }
  return points.join('')
})

/**
 * 用途 取消盾牌复位动画，使下一次交互可从当前姿态接管。
 * 参数 无。
 * 返回值 无。
 */
function cancelPoseReset() {
  cancelAnimationFrame(resetFrame)
  resetFrame = 0
}

/**
 * 用途 鼠标完成拖动并离开盾牌时，沿最短角度缓动回初始姿态。
 * 参数 无。
 * 返回值 无。
 */
function resetDraggedPose() {
  if (drag || !restoreAfterMouseDrag) return
  restoreAfterMouseDrag = false
  tilt.value?.style.removeProperty('--tilt-x')
  tilt.value?.style.removeProperty('--tilt-y')
  const startPitch = pitch.value
  const startYaw = yaw.value
  const targetYaw = defaultYaw + 360 * Math.round((startYaw - defaultYaw) / 360)
  const started = performance.now()
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

  /**
   * 用途 计算当前复位帧的横向旋转与俯仰角。
   * 参数 time 为浏览器给出的帧时间。
   * 返回值 无。
   */
  const applyFrame = (time: number) => {
    const progress = Math.max(0, Math.min(1, (time - started) / resetDuration))
    const remaining = (1 - progress) ** 3
    pitch.value = defaultPitch + (startPitch - defaultPitch) * remaining
    yaw.value = progress < 1 ? targetYaw + (startYaw - targetYaw) * remaining : defaultYaw
    resetFrame = progress < 1 ? requestAnimationFrame(applyFrame) : 0
  }

  if (reduced) applyFrame(started + resetDuration)
  else resetFrame = requestAnimationFrame(applyFrame)
}

/**
 * 用途 根据鼠标相对视口中心的位置调整盾牌透视，合成倾角上限为 12 度。
 * 参数 event 为页面鼠标移动事件。
 * 返回值 无。
 */
function followPointer(event: MouseEvent) {
  if (drag || resetFrame) return
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
  resetDraggedPose()
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
 * 用途 鼠标移出盾牌时恢复围栏转速，结束拖动后才复位盾牌姿态。
 * 参数 无。
 * 返回值 无。
 */
function leaveShield() {
  setOrbitSpeed(1)
  resetDraggedPose()
}

/**
 * 用途 记录按下位置并捕获指针，让拖动越过盾牌边沿后仍能连续旋转。
 * 参数 event 为盾牌上鼠标或触屏的按下事件。
 * 返回值 无。
 */
function startDrag(event: PointerEvent) {
  if (drag || !event.isPrimary || event.button !== 0) return
  if (event.pointerType === 'mouse' && resetFrame) restoreAfterMouseDrag = true
  cancelPoseReset()
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
  restoreAfterMouseDrag = event.pointerType === 'mouse'
  yaw.value += x * 1.1
  pitch.value = Math.max(-60, Math.min(60, pitch.value - y * .45))
  drag.x = event.clientX
  drag.y = event.clientY
}

/**
 * 用途 结束拖动手势，鼠标已经移出盾牌时触发复位。
 * 参数 event 为指针释放、取消或发现主键已松开的移动事件。
 * 返回值 无。
 */
function endDrag(event: PointerEvent) {
  if (drag?.pointerId !== event.pointerId) return
  drag = undefined
  if (!restoreAfterMouseDrag) return

  // 释放指针捕获之前，事件目标可能仍是按钮，用实际指针位置判断。
  const hit = document.elementFromPoint(event.clientX, event.clientY)
  if (!shieldButton.value?.contains(hit)) resetDraggedPose()
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
  cancelPoseReset()
  restoreAfterMouseDrag = false
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
  cancelPoseReset()
  window.removeEventListener('mousemove', followPointer)
  window.removeEventListener('pointermove', moveDrag)
  window.removeEventListener('pointerup', endDrag)
  window.removeEventListener('pointercancel', endDrag)
  document.documentElement.removeEventListener('mouseleave', resetPointer)
  window.removeEventListener('blur', resetPointer)
})
</script>

<template>
  <figure class="security-scene" aria-label="安防盾牌与平台安全态势"
    :style="{ '--security-impact-duration': impactDuration + 'ms', '--platform-top': platformTop }">
    <!-- 纵深层：地面网格、体积雾与顶置光锥 -->
    <div class="security-depth" aria-hidden="true">
      <span class="security-floor"><i class="security-floor-grid"></i></span>
      <span class="security-fog"></span>
      <span class="security-spotlight"></span>
    </div>

    <!-- 左舷全息面板：平台真实安全基线 -->
    <aside class="security-holo security-holo-left">
      <span class="security-holo-title">PROTECTION</span>
      <div class="security-holo-card">
        <h4><i></i>安全防护<em>生效中</em></h4>
        <p class="security-holo-metric"><b>PBKDF2</b><span>60 万轮口令散列</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
      <div class="security-holo-card">
        <h4><i></i>态势感知<em>生效中</em></h4>
        <p class="security-holo-metric"><b>8 小时</b><span>会话有效期</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
      <div class="security-holo-card">
        <h4><i></i>预警中心<em class="is-warn">12 次</em></h4>
        <p class="security-holo-metric"><b>5 分钟</b><span>同账号校验上限</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
      <div class="security-holo-card">
        <h4><i></i>应急响应<em>生效中</em></h4>
        <p class="security-holo-metric"><b>已留痕</b><span>登录与越权审计</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
    </aside>

    <!-- 右舷全息面板：平台真实安全基线 -->
    <aside class="security-holo security-holo-right">
      <span class="security-holo-title">MONITORING</span>
      <div class="security-holo-card">
        <h4><i></i>数据监控<em>生效中</em></h4>
        <p class="security-holo-metric"><b>HttpOnly</b><span>会话令牌隔离</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
      <div class="security-holo-card">
        <h4><i></i>威胁分析<em>生效中</em></h4>
        <p class="security-holo-metric"><b>最小权限</b><span>按角色可见范围</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
      <div class="security-holo-card">
        <h4><i></i>实时预警<em class="is-warn">待开通</em></h4>
        <p class="security-holo-metric"><b>强制改密</b><span>首次登录拦截</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
      <div class="security-holo-card">
        <h4><i></i>全网守护<em>生效中</em></h4>
        <p class="security-holo-metric"><b>已开放</b><span>运营单位注册</span></p>
        <svg class="security-holo-wave" viewBox="0 0 240 26" preserveAspectRatio="none" aria-hidden="true">
          <path :d="wavePath" />
        </svg>
        <span class="security-holo-bar" aria-hidden="true"><i></i></span>
      </div>
    </aside>

    <div class="security-vault">
      <div class="security-stage" :data-login-state="loginState">
        <!-- 悬浮光柱：把盾牌与台面连成一个舱体 -->
        <span class="security-beam" aria-hidden="true"></span>
        <span class="security-beam-wide" aria-hidden="true"></span>
        <span class="security-grid" aria-hidden="true"></span>
        <span class="security-flow" aria-hidden="true"><i></i><i></i><i></i></span>

        <!-- 环绕粒子 -->
        <span class="security-particles security-particles-near" aria-hidden="true">
          <i v-for="(node, index) in particlesNear" :key="index" class="security-particle"
            :style="{ '--angle': node.angle + 'deg', '--radius': node.radius + 'cqw', '--depth': node.depth + 'cqw', '--delay': node.delay + 's' }"></i>
        </span>
        <span class="security-particles security-particles-far" aria-hidden="true">
          <i v-for="(node, index) in particlesFar" :key="index" class="security-particle is-small"
            :style="{ '--angle': node.angle + 'deg', '--radius': node.radius + 'cqw', '--depth': node.depth + 'cqw', '--delay': node.delay + 's' }"></i>
        </span>

        <span class="security-halo" aria-hidden="true"></span>
        <span class="security-halo-outer" aria-hidden="true"></span>

        <!-- 前景棘轮轨道环 -->
        <span class="security-orbit-layer" aria-hidden="true">
          <span ref="orbit" class="security-orbit-spin">
            <span v-for="mark in 26" :key="mark" class="security-orbit-mark"
              :style="{ rotate: mark * (360 / 26) + 'deg' }"></span>
          </span>
        </span>

        <div class="security-base-wrap">
          <img class="security-base" :src="baseArt" alt="" width="1122" height="1402">
          <img class="security-base-glow" :src="baseArt" alt="" width="1122" height="1402">
        </div>
        <span class="security-base-reflect" aria-hidden="true"></span>
        <span class="security-lift-pool" aria-hidden="true"></span>
        <span class="security-scan is-a" aria-hidden="true"></span>
        <span class="security-scan is-b" aria-hidden="true"></span>
        <span class="security-scan is-c" aria-hidden="true"></span>

        <button ref="shieldButton" class="security-core" type="button" aria-label="旋转或激活安防盾牌"
          aria-describedby="security-controls"
          @mouseenter="setOrbitSpeed(1.4)" @mouseleave="leaveShield"
          @pointerdown="startDrag"
          @keydown="rotateWithKeyboard" @click="activate">
          <span class="security-float" aria-hidden="true">
            <span class="security-sway" aria-hidden="true">
              <span ref="impact" class="security-impact">
                <span ref="tilt" class="security-tilt">
                  <SignInShieldModel :pitch="pitch" :yaw="yaw" :pulse="pulse" />
                </span>
              </span>
            </span>
          </span>
        </button>

        <!-- HUD 锁定框：舞台保持扁平上下文，避免整幅截图丢失文字层 -->
        <div class="security-reticle" aria-hidden="true">
          <i class="security-reticle-corner is-tl"></i>
          <i class="security-reticle-corner is-tr"></i>
          <i class="security-reticle-corner is-bl"></i>
          <i class="security-reticle-corner is-br"></i>
          <span class="security-reticle-tag is-top">CORE-01 / 安全中枢</span>
          <span class="security-reticle-tag is-right">SESSION · HttpOnly</span>
          <span class="security-reticle-tag is-bottom">UPLINK OK</span>
        </div>
      </div>
    </div>

    <!-- 前景景深碎片 -->
    <div class="security-frag security-frag-left" aria-hidden="true">
      <h5><i></i>登录态势</h5>
      <div class="security-frag-bars">
        <b style="height:38%"></b><b style="height:72%"></b><b style="height:54%"></b><b style="height:92%"></b><b style="height:46%"></b><b style="height:66%"></b>
      </div>
      <p>SESSIONS / 1h</p>
    </div>
    <div class="security-frag security-frag-right" aria-hidden="true">
      <h5><i class="is-ok"></i>等保基线</h5>
      <div class="security-frag-bars">
        <b style="height:70%"></b><b style="height:86%"></b><b style="height:64%"></b><b style="height:78%"></b><b style="height:92%"></b>
      </div>
      <p>BASELINE · 全流程</p>
    </div>

    <figcaption class="security-caption">
      <span class="security-caption-icon" :class="{ 'security-caption-active': pulse }">
        <Activity :size="17" aria-hidden="true" />
      </span>
      <span>
        <span id="security-controls" class="sr-only">方向键旋转，Home 键复位，回车键激活。</span>
        <span class="security-caption-status" role="status">
          {{ pulse ? '核心已激活 · 拖动旋转' : '拖动旋转 · 点击激活' }}
        </span>
        <span class="security-caption-sub">DRAG TO ROTATE · CLICK TO ACTIVATE</span>
      </span>
    </figcaption>
  </figure>
</template>

<style scoped>
.security-scene {
  --signal: #8de5ff;
  position: relative;
  isolation: isolate;
  display: grid;
  align-content: center;
  width: 100%;
  height: 100%;
  min-height: 0;
  margin: 0;
  color: #e8f7ff;
  container-type: inline-size;
}

/* ---------- 纵深层 ---------- */
.security-depth {
  position: absolute;
  inset: 0;
  z-index: 0;
  /* 限制透视网格的滚动溢出，避免装饰层撑高登录页。 */
  overflow: clip;
  pointer-events: none;
}

.security-floor {
  position: absolute;
  left: 50%;
  top: 88%;
  width: 0;
  height: 0;
  perspective: 1500px;
}

.security-floor-grid {
  position: absolute;
  width: 2800px;
  height: 2800px;
  margin: -1400px 0 0 -1400px;
  transform: rotateX(75deg) translateZ(-60px);
  background:
    repeating-linear-gradient(90deg, rgba(96, 196, 255, .22) 0 1.5px, transparent 1.5px 98px),
    repeating-linear-gradient(0deg, rgba(96, 196, 255, .22) 0 1.5px, transparent 1.5px 98px);
  -webkit-mask-image: radial-gradient(closest-side, #000 10%, rgba(0, 0, 0, .62) 42%, transparent 70%);
  mask-image: radial-gradient(closest-side, #000 10%, rgba(0, 0, 0, .62) 42%, transparent 70%);
}

.security-floor-grid::after {
  position: absolute;
  inset: 0;
  background: radial-gradient(closest-side, rgba(40, 150, 240, .3), transparent 66%);
  content: "";
}

.security-fog {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 2%;
  height: 30%;
  background: radial-gradient(52% 100% at 50% 100%, rgba(66, 150, 220, .17), transparent 70%);
  filter: blur(34px);
}

.security-spotlight {
  position: absolute;
  left: 50%;
  top: -12%;
  width: 46%;
  height: 62%;
  margin-left: -23%;
  background: radial-gradient(ellipse 30% 58% at 50% 0, rgba(150, 225, 255, .19), rgba(90, 180, 240, .05) 58%, transparent 80%);
  filter: blur(28px);
  opacity: .9;
}

/* ---------- 全息面板 ---------- */
.security-holo {
  position: absolute;
  top: 50%;
  z-index: 1;
  display: grid;
  gap: 11px;
  width: 228px;
  transform: translateY(-50%);
}

.security-holo-left { left: 0; }
.security-holo-right { right: 0; }

.security-holo-title {
  position: absolute;
  top: -24px;
  right: 0;
  color: #4f6d8c;
  font-size: 10px;
  letter-spacing: .26em;
}

.security-holo-right .security-holo-title { right: auto; left: 0; }

.security-holo-card {
  position: relative;
  overflow: hidden;
  padding: 11px 14px 12px;
  border: 1px solid rgba(111, 198, 255, .3);
  border-radius: 10px;
  background: linear-gradient(140deg, rgba(16, 48, 80, .5), rgba(8, 24, 42, .32));
  -webkit-backdrop-filter: blur(9px);
  backdrop-filter: blur(9px);
  box-shadow: 0 12px 30px rgba(0, 0, 0, .5), inset 0 1px 0 rgba(200, 235, 255, .14);
}

.security-holo-card::before {
  position: absolute;
  inset: 0 auto 0 0;
  width: 2px;
  background: linear-gradient(180deg, #7fe0ff, rgba(127, 224, 255, 0));
  content: "";
}

.security-holo-left .security-holo-card { transform: perspective(820px) rotateY(17deg); }
.security-holo-right .security-holo-card { transform: perspective(820px) rotateY(-17deg); }

.security-holo-card h4 {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #e3f2ff;
  font-size: 14.5px;
  font-weight: 650;
  letter-spacing: .05em;
}

.security-holo-card h4 i {
  flex: none;
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #5fd8ff;
  box-shadow: 0 0 8px #5fd8ff;
}

.security-holo-card h4 em {
  margin-left: auto;
  padding: 1px 6px;
  border: 1px solid rgba(127, 224, 255, .4);
  border-radius: 4px;
  background: rgba(20, 60, 96, .5);
  color: #7fe0ff;
  font-size: 10px;
  font-style: normal;
  letter-spacing: .06em;
}

.security-holo-card h4 em.is-warn {
  border-color: rgba(255, 120, 135, .45);
  background: rgba(92, 24, 38, .45);
  color: #ff9aa7;
}

.security-holo-metric {
  display: flex;
  align-items: baseline;
  gap: 6px;
  margin-top: 7px;
}

.security-holo-metric b {
  color: #bfeaff;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 17px;
  font-weight: 600;
  letter-spacing: .02em;
  text-shadow: 0 0 12px rgba(95, 216, 255, .45);
}

.security-holo-metric span { color: #7f9cba; font-size: 11px; }

.security-holo-wave {
  display: block;
  width: 100%;
  height: 26px;
  margin-top: 6px;
  overflow: hidden;
}

.security-holo-wave path {
  fill: none;
  stroke: #5fd8ff;
  stroke-width: 1.4;
  stroke-linecap: round;
  stroke-linejoin: round;
  filter: drop-shadow(0 0 4px rgba(95, 216, 255, .6));
  animation: security-wave 6s linear infinite;
}

.security-holo-bar {
  display: block;
  overflow: hidden;
  height: 3px;
  margin-top: 7px;
  border-radius: 2px;
  background: rgba(120, 190, 240, .15);
}

.security-holo-bar i {
  display: block;
  width: 34%;
  height: 100%;
  border-radius: 2px;
  background: linear-gradient(90deg, transparent, #2f8fd6, #7fe0ff, transparent);
  animation: security-holo-scan 4s linear infinite;
}

/* ---------- 舞台 ---------- */
.security-vault {
  position: relative;
  z-index: 2;
  display: grid;
  place-items: center;
}

.security-stage {
  --shield-unit: .42;
  position: relative;
  /* 舞台同时受列宽与视口高度约束，保证基座不会溢出到底部标语。 */
  width: min(100%, 820px, calc((100dvh - 170px) * 820 / 790));
  aspect-ratio: 820 / 790;
  container-type: inline-size;
}

/* 点阵与数据光带 */
.security-grid {
  position: absolute;
  inset: 0;
  z-index: 0;
  background: radial-gradient(circle, #5bc6ff3d 1px, transparent 1.5px) 0 0 / 22px 22px;
  -webkit-mask-image: radial-gradient(ellipse at 50% 40%, #000, transparent 66%);
  mask-image: radial-gradient(ellipse at 50% 40%, #000, transparent 66%);
  pointer-events: none;
}

.security-flow {
  position: absolute;
  inset-block: 22% auto;
  z-index: 1;
  height: 49%;
  overflow: hidden;
  -webkit-mask-image: linear-gradient(90deg, transparent, #000 10% 89%, transparent);
  mask-image: linear-gradient(90deg, transparent, #000 10% 89%, transparent);
  pointer-events: none;
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

/* 悬浮光柱 */
.security-beam {  position: absolute;
  left: 50%;
  top: 66%;
  z-index: 1;
  width: 11%;
  height: 23%;
  margin-left: -5.5%;
  background: linear-gradient(180deg, rgba(160, 230, 255, .5), rgba(90, 185, 245, .16) 45%, transparent 88%);
  filter: blur(14px);
  animation: security-beam 4s ease-in-out infinite;
}

.security-beam-wide {
  position: absolute;
  left: 50%;
  top: 62%;
  z-index: 1;
  width: 24%;
  height: 28%;
  margin-left: -12%;
  background: radial-gradient(60% 100% at 50% 0, rgba(90, 190, 250, .28), transparent 72%);
  filter: blur(26px);
  animation: security-beam-wide 8s ease-in-out infinite;
}

/* 环绕粒子 */
.security-particles {
  position: absolute;
  left: 50%;
  top: 43.2%;
  z-index: 4;
  width: 0;
  height: 0;
  perspective: 1200px;
  transform-style: preserve-3d;
  pointer-events: none;
}

.security-particles-near { transform: rotateX(70deg); animation: security-orbit-near 20s linear infinite; }
.security-particles-far { top: 46%; z-index: 3; transform: rotateX(-62deg); animation: security-orbit-far 28s linear infinite; }

.security-particle {
  position: absolute;
  left: 0;
  top: 0;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #e6fbff;
  box-shadow: 0 0 8px #4fd6ff, 0 0 22px rgba(79, 214, 255, .85), 0 0 44px rgba(79, 214, 255, .4);
  transform: rotate(var(--angle)) translateX(var(--radius)) translateY(var(--depth));
  animation: security-particle 4s ease-in-out infinite;
  animation-delay: var(--delay);
}

.security-particle.is-small {
  width: 5px;
  height: 5px;
  opacity: .7;
}

@keyframes security-particle {
  0%, 100% { opacity: .35; scale: .7; }
  50% { opacity: .95; scale: 1.2; }
}

/* 盾牌外的两层光晕 */
.security-halo,
.security-halo-outer {
  position: absolute;
  left: 50%;
  top: 43.2%;
  z-index: 2;
  border-radius: 50%;
  pointer-events: none;
}

.security-halo {
  width: 52.5cqw;
  height: 52.5cqw;
  margin: -26.25cqw 0 0 -26.25cqw;
  border: 1px solid rgba(120, 215, 255, .2);
  box-shadow: 0 0 44px rgba(60, 170, 255, .13), inset 0 0 70px rgba(60, 170, 255, .07);
}

.security-halo-outer {
  width: 66cqw;
  height: 66cqw;
  margin: -33cqw 0 0 -33cqw;
  border: 1px dashed rgba(120, 215, 255, .12);
}

/* 前景棘轮轨道环 */
.security-orbit-layer {
  position: absolute;
  left: 50%;
  top: 43.2%;
  z-index: 6;
  width: 56.7%;
  aspect-ratio: 1;
  margin-left: -28.35%;
  perspective: 1200px;
  pointer-events: none;
  transform: translateY(-50%) rotateX(72deg) rotateZ(-12deg);
}

.security-orbit-spin {
  position: absolute;
  inset: 0;
  border: 1px solid #a7d8f1aa;
  border-radius: 50%;
  box-shadow: 0 0 14px #73beff66, inset 0 0 12px #8fccf344;
  animation: security-orbit-turn 15s linear infinite;
}

.security-orbit-spin::after {
  position: absolute;
  inset: -.5%;
  border-radius: 50%;
  background: conic-gradient(from 16deg, #b2e9ff 0deg 6deg, transparent 56deg 175deg,
    #ff7185 176deg 183deg, transparent 205deg 328deg, #b2e9ff 359deg);
  -webkit-mask-image: radial-gradient(transparent 68.8%, #000 69.6% 71.4%, transparent 72%);
  mask-image: radial-gradient(transparent 68.8%, #000 69.6% 71.4%, transparent 72%);
}

.security-orbit-mark {
  position: absolute;
  inset: 0;
}

.security-orbit-mark::before {
  position: absolute;
  top: 0;
  left: 50%;
  width: 13px;
  height: 19px;
  translate: -50% -50%;
  background: #b1dffad9;
  clip-path: polygon(0 49%, 33% 49%, 48% 0, 58% 64%, 73% 49%, 100% 49%,
    100% 53%, 74% 53%, 56% 77%, 48% 20%, 36% 53%, 0 53%);
  filter: drop-shadow(0 0 4px rgba(150, 220, 255, .75));
  content: "";
}

/* 基座 */
.security-base-wrap {
  position: absolute;
  left: 50%;
  top: 26.1%;
  z-index: 3;
  width: 68.3%;
  height: 88.6%;
  margin-left: -34.15%;
}

.security-base,
.security-base-glow {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: fill;
  /* 沿基座实体轮廓裁切，让房间光轨和地面贯穿整个盾牌区域。 */
  clip-path: polygon(
    50% 68.9%, 60% 69.05%, 69% 69.65%, 76% 70.6%, 82% 72.1%, 86.8% 73.2%,
    90% 75.65%, 92.7% 78.8%, 92.7% 83.15%, 88.8% 84.9%, 82.8% 86.3%, 70% 87.45%,
    59% 87.8%, 40% 87.8%, 29% 87.45%, 17.4% 86.2%, 10.9% 84.9%, 7.3% 83.1%,
    7.3% 78.8%, 10.1% 75.7%, 13% 73.7%, 17.6% 72.2%, 23% 70.9%, 30.6% 69.8%,
    39.2% 69.18%
  );
}

.security-base-glow {
  opacity: 0;
  filter: var(--base-lamp-filter, brightness(1.2));
  /* 只调红色内环和三条灯带，让底板和蓝色外环保留素材的原色。 */
  -webkit-mask-image:
    radial-gradient(ellipse 17.5% 3.6% at 50% 73.5%, #000 84%, transparent),
    radial-gradient(ellipse 7% 1.4% at 24% 84.7%, #000 63%, transparent),
    radial-gradient(ellipse 13% 1.5% at 50% 84.4%, #000 63%, transparent),
    radial-gradient(ellipse 7% 1.4% at 76% 84.7%, #000 63%, transparent);
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
  --base-reflection: #49c3e377;
}

.security-stage[data-login-state="success"] {
  --base-lamp-filter: hue-rotate(120deg) saturate(1.4) brightness(1.65);
  --base-reflection: #45db8377;
}

.security-stage[data-login-state="error"] {
  --base-lamp-filter: saturate(1.35) brightness(1.45);
  --base-reflection: #ff42597a;
}

.security-stage[data-login-state]:not([data-login-state="idle"]) .security-base-glow { opacity: 1; }

/* 台面反射与悬浮光池 */
.security-base-reflect {
  position: absolute;
  left: 50%;
  top: var(--platform-top);
  z-index: 1;
  width: 105%;
  height: 19%;
  margin-left: -52.5%;
  background:
    radial-gradient(ellipse 45% 3.8% at 50% 0, #010711d1 38%, transparent),
    radial-gradient(ellipse 7% 3.4% at 25% 6%, var(--base-reflection, #f43a5638), transparent),
    radial-gradient(ellipse 15% 4% at 50% 11%, var(--base-reflection, #f43a5638), transparent),
    radial-gradient(ellipse 7% 3.4% at 75% 6%, var(--base-reflection, #f43a5638), transparent),
    radial-gradient(ellipse 42% 5.5% at 50% 3%, #16a7ef4d, transparent);
  filter: blur(4px);
  opacity: .85;
}

.security-lift-pool {
  position: absolute;
  left: 50%;
  top: var(--platform-top);
  z-index: 2;
  width: 51%;
  height: 14%;
  margin: -7% 0 0 -25.5%;
  border-radius: 50%;
  background: radial-gradient(closest-side, rgba(140, 225, 255, .5), rgba(70, 180, 245, .14) 55%, transparent 76%);
  filter: blur(10px);
  animation: security-lift 4s ease-in-out infinite;
}

/* 贴地扫描波 */
.security-scan {
  position: absolute;
  left: 50%;
  top: var(--platform-top);
  z-index: 2;
  width: 68.3%;
  aspect-ratio: 1;
  margin: -34.15% 0 0 -34.15%;
  border-radius: 50%;
  background: radial-gradient(closest-side, transparent 66%, rgba(140, 228, 255, .34) 75%, rgba(140, 228, 255, .07) 85%, transparent 93%);
  filter: blur(2px);
  -webkit-mask-image: linear-gradient(90deg, transparent, #000 22% 78%, transparent);
  mask-image: linear-gradient(90deg, transparent, #000 22% 78%, transparent);
  opacity: 0;
  transform: rotateX(85deg);
  pointer-events: none;
}

.security-scan.is-a { animation: security-scan 2s linear infinite; }
.security-scan.is-b { animation: security-scan 2s linear infinite -0.66s; }
.security-scan.is-c { animation: security-scan 2s linear infinite -1.33s; }

/* 盾牌 */
.security-core {
  position: absolute;
  left: 50%;
  top: 16%;
  z-index: 5;
  width: calc(var(--shield-unit) * 100%);
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
.security-sway,
.security-impact,
.security-tilt {
  display: block;
  position: absolute;
  inset: 0;
  pointer-events: none;
  transform-style: preserve-3d;
}

.security-float { animation: security-hover 3s ease-in-out infinite; }
.security-sway { animation: security-sway 8s ease-in-out infinite; }

.security-tilt {
  transform: rotateX(var(--tilt-x, 0deg)) rotateY(var(--tilt-y, 0deg));
  scale: 1;
  transition: transform .38s cubic-bezier(.19, 1, .22, 1), scale .3s ease-in-out;
}

.security-core:is(:hover, :focus-visible) .security-tilt {
  --shield-brightness: 1.1;
  scale: 1.05;
}

/* HUD 锁定框 */
.security-reticle {
  position: absolute;
  left: 50%;
  top: 7.2%;
  z-index: 8;
  width: 54.6%;
  height: 71.9%;
  margin-left: -27.3%;
  pointer-events: none;
}

.security-reticle-corner {
  position: absolute;
  width: 22px;
  height: 22px;
  border: 1px solid rgba(127, 224, 255, .55);
}

.security-reticle-corner.is-tl { top: 0; left: 0; border-right: 0; border-bottom: 0; }
.security-reticle-corner.is-tr { top: 0; right: 0; border-left: 0; border-bottom: 0; }
.security-reticle-corner.is-bl { bottom: 0; left: 0; border-right: 0; border-top: 0; }
.security-reticle-corner.is-br { right: 0; bottom: 0; border-left: 0; border-top: 0; }

.security-reticle-tag {
  position: absolute;
  color: rgba(127, 224, 255, .72);
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 10px;
  letter-spacing: .12em;
  white-space: nowrap;
}

.security-reticle-tag.is-top { top: -18px; left: 6px; }
.security-reticle-tag.is-right { right: 6px; bottom: -20px; }
.security-reticle-tag.is-bottom { bottom: -20px; left: 6px; }

/* 前景景深碎片 */
.security-frag {
  position: absolute;
  z-index: 8;
  padding: 10px 13px;
  border: 1px solid rgba(111, 198, 255, .22);
  border-radius: 9px;
  background: linear-gradient(140deg, rgba(14, 40, 68, .55), rgba(8, 22, 40, .35));
  -webkit-backdrop-filter: blur(6px);
  backdrop-filter: blur(6px);
  box-shadow: 0 14px 34px rgba(0, 0, 0, .55);
  filter: blur(.4px);
  pointer-events: none;
}

.security-frag-left { left: 18%; bottom: 2%; width: 168px; }
.security-frag-right { right: 18%; bottom: 2%; width: 152px; }

.security-frag h5 {
  display: flex;
  align-items: center;
  gap: 6px;
  color: #bcd9f2;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: .1em;
}

.security-frag h5 i {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: #ff6b7f;
  box-shadow: 0 0 8px #ff6b7f;
}

.security-frag h5 i.is-ok { background: #3ce08a; box-shadow: 0 0 8px #3ce08a; }

.security-frag-bars {
  display: flex;
  align-items: flex-end;
  gap: 3px;
  height: 26px;
  margin-top: 8px;
}

.security-frag-bars b {
  flex: 1;
  border-radius: 1px;
  background: linear-gradient(180deg, #5fd8ff, #1c5f96);
}

.security-frag p {
  margin-top: 7px;
  color: #6f8fab;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 10px;
}

/* 交互提示 */
.security-caption {
  position: absolute;
  left: 24px;
  bottom: 10px;
  z-index: 12;
  display: flex;
  align-items: center;
  gap: 11px;
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

.security-caption-sub {
  display: block;
  margin-top: 3px;
  color: #6f89a6;
  font-size: .66rem;
  letter-spacing: .1em;
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

@keyframes security-wave {
  from { transform: translateX(0); }
  to { transform: translateX(-120px); }
}

@keyframes security-holo-scan {
  from { transform: translateX(-110%); }
  to { transform: translateX(330%); }
}

@keyframes security-beam {
  0%, 100% { opacity: .62; transform: translateY(0); }
  50% { opacity: .84; transform: translateY(6px); }
}

@keyframes security-beam-wide {
  0%, 100% { opacity: .5; }
  50% { opacity: .7; }
}

@keyframes security-lift {
  0%, 100% { opacity: .55; }
  50% { opacity: .75; }
}

@keyframes security-scan {
  0% { opacity: 0; transform: rotateX(85deg) scale(.5); }
  35% { opacity: .7; }
  100% { opacity: 0; transform: rotateX(85deg) scale(1.55); }
}

@keyframes security-orbit-near {
  to { transform: rotateX(70deg) rotateZ(360deg); }
}

@keyframes security-orbit-far {
  to { transform: rotateX(-62deg) rotateZ(-360deg); }
}

@keyframes security-data-flow {
  from { translate: -110% 0; }
  to { translate: 560% 0; }
}

@keyframes security-hover {
  0%, 100% { transform: translateY(-17px); }
  50% { transform: translateY(17px); }
}

@keyframes security-sway {
  0%, 100% { transform: rotateY(-5deg); }
  50% { transform: rotateY(5deg); }
}

@keyframes security-orbit-turn { to { rotate: 360deg; } }

@container (max-width: 1050px) {
  .security-holo { display: none; }
  .security-frag { display: none; }
}

@container (max-width: 1400px) {
  .security-holo { width: 196px; }
  .security-holo-metric b { font-size: 15px; }
}

@media (max-width: 980px) {
  .security-stage { width: min(100%, 520px, calc((100dvh - 460px) * 820 / 790)); }
  /* 单列布局下舞台紧贴卡片，交互提示改为跟随在舞台下方，避免压住基座。 */
  .security-caption {
    position: static;
    justify-content: center;
    margin-top: 14px;
  }
}

@media (max-width: 760px) {
  .security-stage { width: min(100%, 380px, calc((100dvh - 520px) * 820 / 790)); }
  .security-caption { bottom: 2%; left: 2%; }
  .security-caption-icon { width: 30px; height: 30px; }
  .security-reticle { display: none; }
}

@media (prefers-reduced-motion: reduce) {
  .security-float, .security-sway, .security-orbit-spin, .security-particle,
  .security-holo-wave path, .security-holo-bar i, .security-scan,
  .security-beam, .security-beam-wide, .security-lift-pool,
  .security-particles-near, .security-particles-far { animation: none; }
  .security-tilt { transform: none; }
  .security-impact { transform: none !important; }
  .security-tilt, .security-base-glow, .security-core::before { transition: none; }
  .security-core:is(:hover, :focus-visible) .security-tilt { scale: 1; }
}
</style>

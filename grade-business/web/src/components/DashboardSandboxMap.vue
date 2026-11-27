<script setup lang="ts">
/**
 * 驾驶舱的真三维区县沙盘（Three.js WebGL）。
 *
 * 依据会话允许的区县边界环挤出立体块，高度与颜色映射综合风险指数；
 * 支持拖动环绕、滚轮缩放、点击或键盘选择区县并飞近，Esc 或再次点击恢复全域。
 * 全域默认视角按地图实际极值顶点实测拟合，保证任何区县不被裁切、
 * 不落入底部预留带（说明框与比例尺区域）。
 */
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import * as THREE from 'three'
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js'
import { RoomEnvironment } from 'three/examples/jsm/environments/RoomEnvironment.js'

type SandboxLevel = 'high' | 'mid' | 'low' | 'none'
type SandboxPoint = [number, number]
export type SandboxRow = {
  district: string
  code?: string
  rings: SandboxPoint[][][]
  label: SandboxPoint
  value?: number
  level: SandboxLevel
  stats?: { systems: number; high: number; medium: number; low: number; overdue: number; pendingRetest: number }
}

const props = defineProps<{
  rows: SandboxRow[]
  selected: string | null
  metersPerUnit?: number
  locked?: boolean
  active?: boolean
  riskPresentation?: boolean
}>()
const emit = defineEmits<{ select: [district: string]; unavailable: [] }>()

const LEVEL_COLORS: Record<SandboxLevel, { top: number; side: number }> = {
  high: { top: 0xe0685a, side: 0x7e3a32 },
  mid: { top: 0xd99b3a, side: 0x7e5f26 },
  low: { top: 0x3b8fc0, side: 0x1d4c6d },
  none: { top: 0x22455f, side: 0x132838 },
}
const RISK_COLORS = { high: 0xd9505e, mid: 0xd9a441, low: 0x4f9dd8, none: 0x35507a }
const ELEVATION = 0.6
const MARGINS = { left: 20, right: 20, top: 16, bottom: 96 }
const SCALE_STEPS = [1000, 2000, 5000, 10_000, 20_000, 50_000, 100_000]

const host = ref<HTMLDivElement>()
const gl = ref<HTMLCanvasElement>()
const labelBox = ref<HTMLDivElement>()
const northEl = ref<HTMLDivElement>()
const scaleBar = ref<HTMLElement>()
const scaleText = ref<HTMLElement>()
const tip = shallowRef<{ district: string; x: number; y: number }>()
const hoveredRow = computed(() => props.rows.find(row => row.district === tip.value?.district))
let texture: THREE.CanvasTexture | undefined
let terrainGlow: THREE.CanvasTexture | undefined
let environmentTarget: THREE.WebGLRenderTarget | undefined
let dust: THREE.Points<THREE.BufferGeometry, THREE.PointsMaterial> | undefined
const dustSpeeds = new Float32Array(240)
const viewHeight = props.riskPresentation ? 1.2 : 1
// 风险页说明框贴在右下角，沙盘可使用更大的中部垂直空间。
const margins = props.riskPresentation ? { left: 24, right: 24, top: 30, bottom: 64 } : MARGINS

const reducedMotion = typeof window.matchMedia === 'function'
  && window.matchMedia('(prefers-reduced-motion: reduce)').matches

let renderer: THREE.WebGLRenderer | undefined
let scene: THREE.Scene | undefined
let camera: THREE.PerspectiveCamera | undefined
let controls: OrbitControls | undefined
let districtGroup: THREE.Group | undefined
let scanRing: THREE.Mesh | undefined
let blockGroup: THREE.Group | undefined
let pickMeshes: THREE.Mesh[] = []
let labelEls: HTMLButtonElement[] = []
let entries: {
  district: string
  worldCenter: THREE.Vector3
  topMat: THREE.MeshStandardMaterial
  edges: THREE.LineBasicMaterial[]
}[] = []
let ovSamples: THREE.Vector3[] = []
let mapCenter = new THREE.Vector3(0, viewHeight, 0)
let fit = 40
let overview = { dist: fit, height: fit * ELEVATION }
let overviewSized = false
let rafId = 0
let running = false
let clock = new THREE.Clock()
let scanT = 0
let hovered: string | null = null
let selected: string | null = null
let downX = 0
let downY = 0
let resizeObserver: ResizeObserver | undefined
const pointer = new THREE.Vector2()
const raycaster = new THREE.Raycaster()
const hudA = new THREE.Vector3()
const hudB = new THREE.Vector3()
const fly = {
  active: false, t0: 0, dur: props.riskPresentation ? 1.15 : 1.05,
  fromPos: new THREE.Vector3(), toPos: new THREE.Vector3(),
  fromTg: new THREE.Vector3(), toTg: new THREE.Vector3(),
}
const clockNow = () => performance.now()
const easeInOutCubic = (t: number) => t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2

/** 顶面细网格纹理：白色基底保留材质色相，格线轻微压暗。 */
function gridTexture(anisotropy: number) {
  const c = document.createElement('canvas')
  c.width = c.height = 128
  const ctx = c.getContext('2d')!
  ctx.fillStyle = '#ffffff'
  ctx.fillRect(0, 0, 128, 128)
  ctx.strokeStyle = 'rgba(0,20,45,0.18)'
  ctx.lineWidth = 2
  for (let i = 0; i <= 128; i += 32) {
    ctx.beginPath(); ctx.moveTo(i, 0); ctx.lineTo(i, 128); ctx.stroke()
    ctx.beginPath(); ctx.moveTo(0, i); ctx.lineTo(128, i); ctx.stroke()
  }
  const tex = new THREE.CanvasTexture(c)
  tex.wrapS = tex.wrapT = THREE.RepeatWrapping
  tex.repeat.set(0.5, 0.5)
  tex.anisotropy = anisotropy
  tex.colorSpace = THREE.SRGBColorSpace
  return tex
}

/** 侧壁渐变发光：底部压暗、顶部提亮，带轻微边缘光。 */
function sideMaterial(colorHex: number, height: number) {
  return new THREE.ShaderMaterial({
    transparent: true,
    depthWrite: false,
    uniforms: {
      uColor: { value: new THREE.Color(colorHex) },
      uHeight: { value: height },
      uOpacity: { value: 0.85 },
    },
    vertexShader: `
      varying float vFrac;
      varying vec3 vNormal;
      uniform float uHeight;
      void main() {
        vFrac = clamp(position.z / uHeight, 0.0, 1.0);
        vNormal = normalize(normalMatrix * normal);
        gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
      }`,
    fragmentShader: `
      varying float vFrac;
      varying vec3 vNormal;
      uniform vec3 uColor;
      uniform float uOpacity;
      void main() {
        vec3 col = mix(uColor * 0.16, uColor, vFrac * 0.8 + 0.08);
        float rim = pow(1.0 - abs(vNormal.z), 2.0) * 0.28;
        float topGlow = smoothstep(0.7, 1.0, vFrac) * 0.4;
        col += uColor * (rim + topGlow);
        gl_FragColor = vec4(col, uOpacity);
      }`,
  })
}

/**
 * 用途 用画布视野计算完整显示地图的距离。
 * 参数 w 和 h 为画布宽高，单位为像素。
 * 返回值 相机目标距离，供缩放限位和相机飞行使用。
 */
function fitDistance(w: number, h: number) {
  const radius = props.riskPresentation ? 24 : ovRadius()
  const vFov = THREE.MathUtils.degToRad(camera!.fov)
  const distV = radius / Math.tan(vFov / 2)
  const distH = radius / (Math.tan(vFov / 2) * camera!.aspect)
  return Math.max(distV, distH)
}

/** 地图外接半径（含块顶高度），供视距计算。 */
function ovRadius() {
  let radius = props.riskPresentation ? 22 : 12
  for (const p of ovSamples) {
    radius = Math.max(radius, Math.hypot(p.x - mapCenter.x, p.z - mapCenter.z))
  }
  return radius + 2
}

/**
 * 用途 检查区县极值点是否处于说明框及地图边缘之外的安全区。
 * 参数 D 为候选相机距离，w 和 h 为画布宽高。
 * 返回值 所有采样点均可见时为真。
 */
function overviewFits(D: number, w: number, h: number) {
  camera!.position.set(mapCenter.x, (props.riskPresentation ? 0 : mapCenter.y) + D * ELEVATION, mapCenter.z + D)
  camera!.lookAt(mapCenter)
  camera!.updateMatrixWorld()
  for (const p of ovSamples) {
    hudA.copy(p).project(camera!)
    if (hudA.z > 1) return false
    const sx = (hudA.x * 0.5 + 0.5) * w
    const sy = (-hudA.y * 0.5 + 0.5) * h
    if (sx < margins.left || sx > w - margins.right || sy < margins.top || sy > h - margins.bottom) return false
  }
  return true
}

/** 二分搜索全域默认距离。 */
function computeOverview(w: number, h: number) {
  if (!ovSamples.length || !camera) return
  const savedPos = camera.position.clone()
  let lo = fit * 0.4
  let hi = fit * 3
  for (let i = 0; i < 18; i++) {
    const mid = (lo + hi) / 2
    if (overviewFits(mid, w, h)) hi = mid
    else lo = mid
  }
  overview = { dist: hi, height: hi * ELEVATION }
  camera.position.copy(savedPos)
  camera.lookAt(controls!.target)
  camera.updateMatrixWorld()
}

/** 相机平滑飞往目标点。 */
function flyTo(target: THREE.Vector3, position: THREE.Vector3) {
  if (!camera || !controls) return
  fly.fromPos.copy(camera.position)
  fly.toPos.copy(position)
  fly.fromTg.copy(controls.target)
  fly.toTg.copy(target)
  fly.t0 = clockNow()
  fly.active = !reducedMotion
  controls.enabled = !fly.active
  if (!fly.active) {
    camera.position.copy(position)
    controls.target.copy(target)
  }
}

/** 相机当前的水平朝向向量。 */
function currentHeading() {
  const dir = camera!.position.clone().sub(controls!.target)
  dir.y = 0
  if (dir.lengthSq() < 1e-6) dir.set(0, 0, 1)
  return dir.normalize()
}

/**
 * 用途 飞近选中的区县或恢复默认视角。
 * 参数 district 为区县名称，null 表示恢复全域。
 * 返回值 无。
 */
function applySelection(district: string | null) {
  selected = district
  syncSelectedMaterials()
  const entry = entries.find(item => item.district === district)
  if (entry) {
    const toTarget = entry.worldCenter.clone().setY(Math.max(1, entry.worldCenter.y * 0.5))
    const toPos = toTarget.clone().addScaledVector(currentHeading(), fit * 0.34)
      .add(new THREE.Vector3(0, fit * 0.19, 0))
    flyTo(toTarget, toPos)
  } else {
    const toTarget = mapCenter.clone().setY(viewHeight)
    const toPos = new THREE.Vector3(mapCenter.x, (props.riskPresentation ? 0 : mapCenter.y) + overview.height, mapCenter.z + overview.dist)
    flyTo(toTarget, toPos)
  }
}

/**
 * 用途 将相机恢复到当前可见区县的默认总览。
 * 参数 无。
 * 返回值 无。
 */
function resetView() {
  if (camera && controls) applySelection(null)
}

defineExpose({ resetView })

/** 拾取指针命中的区县。 */
function pick(event: PointerEvent | MouseEvent): string | null {
  if (!camera) return null
  const rect = renderer!.domElement.getBoundingClientRect()
  pointer.x = ((event.clientX - rect.left) / rect.width) * 2 - 1
  pointer.y = -((event.clientY - rect.top) / rect.height) * 2 + 1
  raycaster.setFromCamera(pointer, camera)
  const hits = raycaster.intersectObjects(pickMeshes, false)
  return hits.length ? hits[0]!.object.userData.district as string : null
}

function setHover(district: string | null) {
  if (hovered === district) return
  const prev = entries.find(item => item.district === hovered)
  if (prev && selected !== prev.district) prev.topMat.emissiveIntensity = 0.14
  hovered = district
  const next = entries.find(item => item.district === district)
  if (next && selected !== next.district) next.topMat.emissiveIntensity = 0.34
  renderer!.domElement.style.cursor = district && !props.locked ? 'pointer' : 'grab'
}

/**
 * 用途 释放场景子树里的网格缓冲和材质。
 * 参数 root 为即将重建或卸载的三维对象树。
 * 返回值 无。全景共享纹理在组件卸载时释放。
 */
function disposeObjects(root: THREE.Object3D) {
  const materials = new Set<THREE.Material>()
  root.traverse(object => {
    const drawable = object as THREE.Mesh
    drawable.geometry?.dispose()
    const material = drawable.material
    if (Array.isArray(material)) material.forEach(item => materials.add(item))
    else if (material) materials.add(material)
  })
  materials.forEach(material => material.dispose())
}

/**
 * 用途 按实际区县统计重建立体块并重新计算默认视角。
 * 参数 无，采用当前会话可见的区县数据。
 * 返回值 无。
 */
function rebuild() {
  if (!scene || !blockGroup || !camera || !props.rows.length) return
  disposeObjects(blockGroup)
  blockGroup.clear()
  pickMeshes = []
  entries = []
  ovSamples = []

  const ex = { minX: 1e9, maxX: -1e9, minY: 1e9, maxY: -1e9 }
  const pick = { minX: [0, 0] as [number, number], maxX: [0, 0] as [number, number],
    minY: [0, 0] as [number, number], maxY: [0, 0] as [number, number] }
  let maxH = 0.9

  for (const row of props.rows) {
    const color = props.riskPresentation ? RISK_COLORS[row.level] : LEVEL_COLORS[row.level].top
    const h = (props.riskPresentation ? 0.7 : 0.9) + ((row.value ?? 0) / 100) * 3.2
    maxH = Math.max(maxH, h)
    const group = new THREE.Group()
    group.rotation.x = -Math.PI / 2
    group.userData.district = row.district

    const topMat = new THREE.MeshStandardMaterial({
      color, emissive: color, emissiveIntensity: 0.14,
      metalness: 0.12, roughness: 0.5, map: texture,
    })
    const sideMat = sideMaterial(color, h)

    for (const polygon of row.rings) {
      const outer = polygon[0]!
      const holes = polygon.slice(1)
      const shape = new THREE.Shape(outer.map(([x, y]) => new THREE.Vector2(x, -y)))
      for (const hole of holes) shape.holes.push(new THREE.Path(hole.map(([x, y]) => new THREE.Vector2(x, -y))))
      const geo = new THREE.ExtrudeGeometry(shape, { depth: h, bevelEnabled: false, steps: 1 })
      const mesh = new THREE.Mesh(geo, [topMat, sideMat])
      mesh.userData.district = row.district
      group.add(mesh)
      pickMeshes.push(mesh)

      const edgePts = outer.map(([x, y]) => new THREE.Vector3(x, -y, h))
      const edgeGeo = new THREE.BufferGeometry().setFromPoints(edgePts)
      const edgeMat = new THREE.LineBasicMaterial({ color: 0x9fc4e8, transparent: true, opacity: 0.6 })
      group.add(new THREE.LineLoop(edgeGeo, edgeMat))
    }
    blockGroup.add(group)

    const [lx, ly] = row.label
    const worldCenter = new THREE.Vector3(lx, h, ly)
    const edges = group.children
      .filter((child: THREE.Object3D) => (child as THREE.Line).isLine)
      .map((child: THREE.Object3D) => (child as THREE.Line).material as THREE.LineBasicMaterial)
    entries.push({ district: row.district, worldCenter, topMat, edges })

    for (const polygon of row.rings) {
      for (const [x, y] of polygon[0]!) {
        if (x < ex.minX) { ex.minX = x; pick.minX = [x, y] }
        if (x > ex.maxX) { ex.maxX = x; pick.maxX = [x, y] }
        if (y < ex.minY) { ex.minY = y; pick.minY = [x, y] }
        if (y > ex.maxY) { ex.maxY = y; pick.maxY = [x, y] }
      }
    }
  }

  for (const [x, y] of Object.values(pick)) {
    ovSamples.push(new THREE.Vector3(x, 0, y), new THREE.Vector3(x, maxH, y))
  }
  mapCenter = props.riskPresentation ? new THREE.Vector3(0, viewHeight, 0)
    : new THREE.Vector3((ex.minX + ex.maxX) / 2, viewHeight, (ex.minY + ex.maxY) / 2)
  rebuildLabels()
  syncSelectedMaterials()
  if (camera && controls) {
    const w = host.value?.clientWidth || 1
    const h = host.value?.clientHeight || 1
    fit = fitDistance(w, h)
    controls.minDistance = fit * (props.riskPresentation ? 0.45 : 0.4)
    controls.maxDistance = fit * (props.riskPresentation ? 1.3 : 1.5)
    computeOverview(w, h)
    applySelection(props.selected)
  }
}

/** 同步选中态的材质高亮与描边。 */
function syncSelectedMaterials() {
  for (const entry of entries) {
    const isSelected = entry.district === selected
    entry.topMat.emissiveIntensity = isSelected ? 0.34 : 0.14
    for (const edge of entry.edges) {
      edge.color.set(isSelected ? 0x8de5ff : 0x9fc4e8)
      edge.opacity = isSelected ? 1 : 0.6
    }
  }
  for (let i = 0; i < labelEls.length; i++) {
    const label = labelEls[i]!
    const isSelected = entries[i]?.district === selected
    label.classList.toggle('selected', isSelected)
    label.setAttribute('aria-pressed', isSelected ? 'true' : 'false')
  }
}

/**
 * 用途 构建支持键盘选择的区县标签并显示实际风险指数。
 * 参数 无。
 * 返回值 无。
 */
function rebuildLabels() {
  const box = labelBox.value
  if (!box) return
  box.innerHTML = ''
  labelEls = []
  for (const row of props.rows) {
    const button = document.createElement('button')
    button.type = 'button'
    button.className = 'sandbox-label'
    button.dataset.district = row.district
    button.setAttribute('aria-label', props.locked ? row.district : `查看${row.district}处置态势`)
    if (props.locked) button.disabled = true
    const chip = document.createElement('span')
    chip.className = `sandbox-chip lv-${row.level}`
    chip.textContent = row.value === undefined ? '暂无' : String(row.value)
    const name = document.createElement('span')
    name.className = 'sandbox-name'
    name.textContent = row.district
    button.append(chip, name)
    button.addEventListener('click', () => {
      if (!props.locked) emit('select', row.district)
    })
    box.appendChild(button)
    labelEls.push(button)
  }
}

/**
 * 用途 根据当前相机更新区县标签、指北针和真实比例尺。
 * 参数 无。
 * 返回值 无。
 */
function updateHud() {
  if (!host.value || !camera) return
  const w = host.value.clientWidth
  const h = host.value.clientHeight
  for (let i = 0; i < entries.length && i < labelEls.length; i++) {
    const entry = entries[i]!
    hudA.copy(entry.worldCenter).setY(entry.worldCenter.y + (props.riskPresentation ? 1.1 : 1)).project(camera)
    const el = labelEls[i]!
    if (hudA.z > 1 || Math.abs(hudA.x) > 1.2 || Math.abs(hudA.y) > 1.2) {
      el.style.opacity = '0'
      continue
    }
    el.style.opacity = '1'
    el.style.left = `${(hudA.x * 0.5 + 0.5) * w}px`
    el.style.top = `${(-hudA.y * 0.5 + 0.5) * h}px`
  }
  if (northEl.value) {
    hudA.set(mapCenter.x, 6, mapCenter.z).project(camera)
    hudB.set(mapCenter.x, 6, mapCenter.z - 1).project(camera)
    northEl.value.style.transform = `rotate(${Math.atan2(hudB.x - hudA.x, hudB.y - hudA.y).toFixed(3)}rad)`
  }
  if (scaleBar.value && scaleText.value && props.metersPerUnit) {
    hudA.set(mapCenter.x, 0, mapCenter.z).project(camera)
    hudB.set(mapCenter.x + 1, 0, mapCenter.z).project(camera)
    const pxUnit = Math.hypot((hudB.x - hudA.x) * w / 2, (hudB.y - hudA.y) * h / 2)
    const maxWidth = props.riskPresentation ? 160 : 110
    let best = SCALE_STEPS[0]!
    let bestPx = (SCALE_STEPS[0]! / props.metersPerUnit) * pxUnit
    for (const step of SCALE_STEPS) {
      const px = (step / props.metersPerUnit) * pxUnit
      if (px <= maxWidth) { best = step; bestPx = px }
    }
    scaleBar.value.style.width = `${Math.max(20, Math.min(maxWidth, bestPx)).toFixed(0)}px`
    scaleText.value.textContent = best >= 1000 ? `${best / 1000} km` : `${best} m`
  }
}

function resize() {
  if (!host.value || !camera || !renderer || !controls) return
  const { clientWidth: w, clientHeight: h } = host.value
  if (!w || !h) return
  camera.aspect = w / h
  camera.updateProjectionMatrix()
  renderer.setSize(w, h, false)
  fit = fitDistance(w, h)
  controls.minDistance = fit * (props.riskPresentation ? 0.45 : 0.4)
  controls.maxDistance = fit * (props.riskPresentation ? 1.3 : 1.5)
  computeOverview(w, h)
  if (!overviewSized) {
    overviewSized = true
    if (selected !== null) applySelection(selected)
    else camera.position.set(mapCenter.x, (props.riskPresentation ? 0 : mapCenter.y) + overview.height, mapCenter.z + overview.dist)
  }
}

/**
 * 用途 推进当前可见沙盘的扫描环、相机和 DOM 标签。
 * 参数 无。
 * 返回值 无。
 */
function frame() {
  rafId = 0
  if (!running || !camera || !controls || !renderer) return
  rafId = requestAnimationFrame(frame)
  const dt = Math.min(clock.getDelta(), 0.1)
  if (scanRing && !reducedMotion) {
    scanT = (scanT + dt / (props.riskPresentation ? 6.5 : 7)) % 1
    scanRing.scale.setScalar(3 + scanT * (props.riskPresentation ? 41 : Math.max(16, ovRadius() * 1.1)))
    ;(scanRing.material as THREE.MeshBasicMaterial).opacity = (props.riskPresentation ? 0.55 : 0.4) * (1 - scanT) ** 1.6
  }
  if (dust && !reducedMotion) {
    const positions = dust.geometry.getAttribute('position')
    for (let index = 0; index < dustSpeeds.length; index++) {
      const height = positions.getY(index) - dustSpeeds[index]! * dt
      positions.setY(index, height < 0 ? 26 : height)
    }
    positions.needsUpdate = true
  }
  if (fly.active) {
    const k = Math.min(1, (clockNow() - fly.t0) / (fly.dur * 1000))
    const t = easeInOutCubic(k)
    camera.position.lerpVectors(fly.fromPos, fly.toPos, t)
    controls.target.lerpVectors(fly.fromTg, fly.toTg, t)
    if (k >= 1) {
      fly.active = false
      controls.enabled = true
    }
  }
  const sel = entries.find(item => item.district === selected)
  if (sel && !reducedMotion) sel.topMat.emissiveIntensity = (props.riskPresentation ? 0.4 : 0.34) + Math.sin(clockNow() / 320) * 0.08
  controls.update()
  updateHud()
  renderer.render(scene!, camera)
}

/**
 * 用途 生成风险分析沙盘的星点背景。
 * 参数 无。
 * 返回值 只使用一次绘制调用的星点对象。
 */
function createRiskStars() {
  const points: number[] = []
  for (let index = 0; index < 900; index++) {
    const radius = 150 + Math.random() * 130
    const angle = Math.random() * Math.PI * 2
    const elevation = Math.acos(2 * Math.random() - 1)
    points.push(radius * Math.sin(elevation) * Math.cos(angle),
      Math.abs(radius * Math.cos(elevation)) * 0.7 - 12, radius * Math.sin(elevation) * Math.sin(angle))
  }
  const geometry = new THREE.BufferGeometry()
  geometry.setAttribute('position', new THREE.Float32BufferAttribute(points, 3))
  const material = new THREE.PointsMaterial({
    color: 0x9fc6ff, size: 1.05, transparent: true, opacity: 0.8, depthWrite: false, fog: false,
  })
  return new THREE.Points(geometry, material)
}

/**
 * 用途 生成原型地台上的低密度飘浮尘埃。
 * 参数 无，粒子尺寸与分布采用风险分析设计稿的参数。
 * 返回值 可由动画帧更新位置的点云。
 */
function createRiskDust() {
  const points: number[] = []
  for (let index = 0; index < dustSpeeds.length; index++) {
    const radius = Math.sqrt(Math.random()) * 52
    const angle = Math.random() * Math.PI * 2
    points.push(radius * Math.cos(angle), Math.random() * 26, radius * Math.sin(angle))
    dustSpeeds[index] = 0.5 + Math.random() * 1.1
  }
  const geometry = new THREE.BufferGeometry()
  geometry.setAttribute('position', new THREE.Float32BufferAttribute(points, 3))
  const material = new THREE.PointsMaterial({
    color: 0x6ea8ff, size: 0.34, transparent: true, opacity: 0.5, depthWrite: false, fog: false,
    blending: THREE.AdditiveBlending,
  })
  return new THREE.Points(geometry, material)
}

/**
 * 用途 构建风格与设计稿一致的圆形经纬灯光地台。
 * 参数 无，使用固定的沙盘模型比例。
 * 返回值 地台、衰减光晕、雷达外环及星点的场景组。
 */
function createRiskTerrain() {
  const stage = new THREE.Group()
  const ground = new THREE.Mesh(new THREE.CircleGeometry(46, 80).rotateX(-Math.PI / 2),
    new THREE.MeshStandardMaterial({ color: 0x0a1a33, roughness: 0.9, metalness: 0.25 }))
  stage.add(ground)
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = 128
  const context = canvas.getContext('2d')
  if (context) {
    const gradient = context.createRadialGradient(64, 64, 5, 64, 64, 64)
    gradient.addColorStop(0, 'rgba(53,214,255,.16)')
    gradient.addColorStop(.5, 'rgba(53,214,255,.05)')
    gradient.addColorStop(1, 'rgba(53,214,255,0)')
    context.fillStyle = gradient
    context.fillRect(0, 0, 128, 128)
    terrainGlow = new THREE.CanvasTexture(canvas)
    terrainGlow.colorSpace = THREE.SRGBColorSpace
    const glow = new THREE.Mesh(new THREE.CircleGeometry(44, 64).rotateX(-Math.PI / 2),
      new THREE.MeshBasicMaterial({ map: terrainGlow, transparent: true, depthWrite: false,
        blending: THREE.AdditiveBlending }))
    glow.position.y = 0.02
    stage.add(glow)
  }
  for (const radius of [45.4, 45.9]) {
    const ring = new THREE.Mesh(new THREE.RingGeometry(radius - 0.08, radius, 128).rotateX(-Math.PI / 2),
      new THREE.MeshBasicMaterial({ color: 0x1c4a78, transparent: true, opacity: 0.55, depthWrite: false }))
    ring.position.y = 0.05
    stage.add(ring)
  }
  dust = createRiskDust()
  stage.add(createRiskStars(), dust)
  return stage
}

onMounted(() => {
  if (!host.value || !gl.value || !labelBox.value) return
  try {
    renderer = new THREE.WebGLRenderer({ canvas: gl.value, antialias: true, alpha: !props.riskPresentation })
  } catch {
    emit('unavailable')
    return
  }
  texture = gridTexture(renderer.capabilities.getMaxAnisotropy())
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2))
  renderer.setClearColor(props.riskPresentation ? 0x050b18 : 0, props.riskPresentation ? 1 : 0)
  scene = new THREE.Scene()
  if (props.riskPresentation) {
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    scene.fog = new THREE.FogExp2(0x050b18, 0.0075)
    const room = new RoomEnvironment()
    const environment = new THREE.PMREMGenerator(renderer)
    environmentTarget = environment.fromScene(room, 0.04)
    scene.environment = environmentTarget.texture
    scene.environmentIntensity = 0.22
    room.dispose()
    environment.dispose()
  }

  camera = new THREE.PerspectiveCamera(props.riskPresentation ? 46 : 42, 1, 0.1, 900)
  camera.position.set(0, 40, 66)

  controls = new OrbitControls(camera, renderer.domElement)
  controls.target.set(0, viewHeight, 0)
  controls.enableDamping = true
  controls.dampingFactor = 0.07
  controls.maxPolarAngle = 1.42
  controls.minPolarAngle = props.riskPresentation ? 0.18 : 0.15

  scene.add(new THREE.HemisphereLight(0x8ab4ff, 0x0a1630, props.riskPresentation ? 0.55 : 0.9))
  const dir = new THREE.DirectionalLight(0xffffff, props.riskPresentation ? 0.75 : 0.85)
  dir.position.set(18, 42, 16)
  scene.add(dir)
  if (props.riskPresentation) {
    const light = new THREE.PointLight(0x35d6ff, 40, 90, 1.6)
    light.position.set(0, 12, 0)
    scene.add(light)
  }

  blockGroup = new THREE.Group()
  scene.add(blockGroup)

  const gridPts: number[] = []
  const gridRadius = props.riskPresentation ? 44 : 60
  for (let v = -gridRadius; v <= gridRadius; v += 2) {
    const half = Math.sqrt(gridRadius * gridRadius - v * v)
    if (half <= 0) continue
    gridPts.push(-half, 0.04, v, half, 0.04, v)
    gridPts.push(v, 0.04, -half, v, 0.04, half)
  }
  const gridGeo = new THREE.BufferGeometry()
  gridGeo.setAttribute('position', new THREE.BufferAttribute(new Float32Array(gridPts), 3))
  const graticule = new THREE.LineSegments(gridGeo,
    new THREE.LineBasicMaterial({ color: 0x2a4a74, transparent: true, opacity: 0.3 }))
  graticule.position.y = 0.02
  scene.add(graticule)
  if (props.riskPresentation) scene.add(createRiskTerrain())

  scanRing = new THREE.Mesh(
    new THREE.RingGeometry(0.97, 1.0, 96).rotateX(-Math.PI / 2),
    new THREE.MeshBasicMaterial({
      color: 0x5fd8ff, transparent: true, opacity: 0.35, depthWrite: false,
      blending: THREE.AdditiveBlending, side: THREE.DoubleSide,
    }),
  )
  scanRing.position.y = 0.06
  scene.add(scanRing)

  rebuild()
  resize()
  resizeObserver = new ResizeObserver(() => resize())
  resizeObserver.observe(host.value)

  const canvas = renderer.domElement
  canvas.addEventListener('pointermove', ev => {
    const district = pick(ev)
    setHover(district)
    if (!props.riskPresentation || !district || ev.buttons) {
      tip.value = undefined
      return
    }
    const bounds = canvas.getBoundingClientRect()
    tip.value = {
      district,
      x: Math.max(4, Math.min(bounds.width - 198, ev.clientX - bounds.left + 12)),
      y: Math.max(4, Math.min(bounds.height - 84, ev.clientY - bounds.top + 12)),
    }
  })
  canvas.addEventListener('pointerleave', () => {
    setHover(null)
    tip.value = undefined
  })
  canvas.addEventListener('pointerdown', ev => { downX = ev.clientX; downY = ev.clientY })
  canvas.addEventListener('click', ev => {
    if (Math.hypot(ev.clientX - downX, ev.clientY - downY) > 6 || props.locked) return
    const district = pick(ev)
    if (district) emit('select', district)
  })
  document.addEventListener('visibilitychange', onVisibility)
  onVisibility()
})

/**
 * 用途 在页面或热力图隐藏时暂停 WebGL，并在重新显示时恢复。
 * 参数 无，读取浏览器可见状态和当前地图模式。
 * 返回值 无。
 */
function onVisibility() {
  running = !document.hidden && props.active !== false
  if (running && !rafId && renderer) {
    clock.getDelta()
    frame()
  } else if (!running) {
    cancelAnimationFrame(rafId)
    rafId = 0
    tip.value = undefined
  }
}

onScopeDispose(() => {
  cancelAnimationFrame(rafId)
  document.removeEventListener('visibilitychange', onVisibility)
  resizeObserver?.disconnect()
  controls?.dispose()
  if (scene) disposeObjects(scene)
  texture?.dispose()
  terrainGlow?.dispose()
  environmentTarget?.dispose()
  renderer?.dispose()
  renderer?.forceContextLoss()
})

watch(() => props.rows, (next, prior) => {
  const unchanged = next.length === prior.length && next.every((row, index) => {
    const previous = prior[index]
    return row.rings === previous?.rings && row.value === previous.value && row.level === previous.level
  })
  if (!unchanged) rebuild()
})
watch(() => props.active, onVisibility, { flush: 'post' })
watch(() => props.selected, district => applySelection(district))
watch(() => props.locked, () => rebuildLabels())
</script>

<template>
  <div ref="host" class="sandbox-map" :class="{ 'risk-sandbox': riskPresentation }">
    <canvas ref="gl" class="sandbox-gl"></canvas>
    <div ref="labelBox" class="sandbox-labels"></div>
    <div v-if="tip && hoveredRow" class="sandbox-tip" :style="{ left: tip.x + 'px', top: tip.y + 'px' }">
      <b>{{ hoveredRow.district }}</b>
      <span v-if="hoveredRow.code">区划代码 {{ hoveredRow.code }}</span>
      <span>风险指数 {{ hoveredRow.value ?? '暂无' }}</span>
      <template v-if="hoveredRow.stats">
        <span>涉及系统 {{ hoveredRow.stats.systems }} · 逾期 {{ hoveredRow.stats.overdue }}</span>
        <span>高 {{ hoveredRow.stats.high }} / 中 {{ hoveredRow.stats.medium }} / 低 {{ hoveredRow.stats.low }}</span>
      </template>
    </div>
    <div ref="northEl" class="sandbox-north" aria-hidden="true"><i>▲</i><span>N</span></div>
    <div class="sandbox-scale" aria-hidden="true"><i ref="scaleBar"></i><span ref="scaleText">—</span></div>
  </div>
</template>

<style scoped>
.sandbox-map{position:absolute;inset:0}
.sandbox-gl{position:absolute;inset:0;width:100%;height:100%;display:block;outline:none}
.sandbox-labels{position:absolute;inset:0;pointer-events:none}
.sandbox-labels :deep(.sandbox-label){
  position:absolute;transform:translate(-50%,-50%);display:flex;flex-direction:column;align-items:center;gap:2px;
  border:0;background:transparent;padding:2px;cursor:pointer;font-family:inherit;pointer-events:auto;transition:opacity .2s ease}
:deep(.sandbox-label:disabled){cursor:default}
.sandbox-labels :deep(.sandbox-chip){font-size:12px;font-weight:700;font-variant-numeric:tabular-nums;padding:2px 10px;border-radius:6px;
  background:rgba(2,8,14,.92);border:1px solid rgba(141,229,255,.6);color:#eaf7ff;line-height:1.4}
:deep(.sandbox-chip.lv-high){color:#ffb9c0;border-color:rgba(224,104,90,.75)}
:deep(.sandbox-chip.lv-mid){color:#ffe2b8;border-color:rgba(217,155,58,.7)}
:deep(.sandbox-chip.lv-low){color:#cfe8ff}
:deep(.sandbox-chip.lv-none){color:#b9cade;border-color:rgba(111,198,255,.4)}
.sandbox-labels :deep(.sandbox-name){font-size:10px;color:#e2f1ff;line-height:1.2;text-shadow:0 1px 4px rgba(3,12,22,.9);white-space:nowrap}
:deep(.sandbox-label.selected .sandbox-chip){border-color:#eaf8ff;box-shadow:0 0 12px rgba(141,229,255,.5)}
:deep(.sandbox-label:focus-visible){outline:1px solid #8de5ff;outline-offset:2px;border-radius:8px}
.sandbox-north{
  position:absolute;left:10px;bottom:44px;z-index:2;width:26px;height:32px;display:flex;flex-direction:column;
  align-items:center;justify-content:center;border-radius:7px;background:rgba(7,22,38,.78);
  border:1px solid rgba(111,198,255,.3);pointer-events:none}
.sandbox-north i{font-style:normal;color:#5fd8ff;font-size:10px;line-height:1}
.sandbox-north span{color:#bdd4e8;font-size:9px;font-weight:700;line-height:1.2}
.sandbox-scale{position:absolute;left:10px;bottom:12px;z-index:2;display:flex;flex-direction:column;gap:2px;pointer-events:none}
.sandbox-scale i{display:block;height:4px;border-left:1px solid #9fc4e8;border-right:1px solid #9fc4e8;border-bottom:2px solid #9fc4e8;transition:width .2s ease}
.sandbox-scale span{color:var(--dashboard-muted);font-size:9.5px;letter-spacing:.05em}
.risk-sandbox .sandbox-north{left:auto;right:10px;top:10px;bottom:auto}
.risk-sandbox .sandbox-scale{bottom:30px}
.sandbox-tip{
  position:absolute;z-index:5;min-width:156px;display:flex;flex-direction:column;gap:2px;padding:7px 9px;
  pointer-events:none;border:1px solid #488cdc73;border-radius:8px;background:#060e1cf0;
  color:#b6c9e3;font-size:11px;box-shadow:0 5px 18px #0008}
.sandbox-tip b{color:#35d6ff}
</style>

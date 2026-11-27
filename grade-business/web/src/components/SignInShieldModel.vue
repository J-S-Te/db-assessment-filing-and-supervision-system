<script setup lang="ts">
import shieldArt from '../assets/signin-security-shield.webp'

type Point = readonly [x: number, y: number]
type Vertex = readonly [x: number, y: number, z: number]

defineProps<{ pitch: number; yaw: number; pulse: number }>()

const textureWidth = 1122
const textureHeight = 1402
const maxDepth = textureWidth * .14
const bevelReach = textureWidth * .035
const facetUnit = 100
const facetOverlap = .019
const coreY = 630
const coreRadius = 174
const collarSegments = 24
const topPoint: Point = [561, 3]
const bottomPoint: Point = [561, 1399]
// 沿纹理外沿取样，前后曲面和侧壁共用同一组高度，接缝保持对齐。
const leftOutline: Point[] = [
  topPoint, [535, 25], [487, 65], [424, 111], [373, 141], [303, 172],
  [199, 206], [91, 235], [30, 255], [11, 287], [13, 361], [14, 421],
  [16, 490], [21, 548], [34, 604], [46, 661], [62, 720], [78, 779],
  [98, 839], [127, 904], [161, 968], [200, 1029], [251, 1097], [302, 1158],
  [361, 1220], [425, 1281], [485, 1334], [540, 1380], bottomPoint,
]
const rightOutline: Point[] = leftOutline.slice(1, -1).reverse()
  .map(([x, y]) => [textureWidth - x, y])
const outline = [...leftOutline, ...rightOutline]
const sections = outline.map((point, index) => edgeSection(point,
  outline[index - 1] ?? outline.at(-1) ?? bottomPoint,
  outline[index + 1] ?? topPoint,
))
const edges = sections.flatMap((section, index) => edgeFacets(section,
  sections[(index + 1) % sections.length] ?? section,
))
const bands = leftOutline.flatMap(([, y], index) => {
  const next = leftOutline[index + 1]
  return next ? [bandStyle(y, next[1])] : []
})
const coreDepth = modelLength(depthAt(coreY))
const coreAngle = Math.atan2(depthAt(coreY + 1) - depthAt(coreY - 1), 2) + 'rad'
const coreDiameter = coreRadius * 2
const coreTextureY = (coreY - coreRadius) / (textureHeight - coreDiameter) * 100
const collarWidth = modelLength(2 * coreRadius * Math.tan(Math.PI / collarSegments))
const facetSize = modelLength(facetUnit * (1 + 3 * facetOverlap))

/**
 * 用途 计算侧边半厚度，中部保持厚实，上下端沿直线渐收。
 * 参数 y 为盾牌表面在纹理上的纵向坐标。
 * 返回值 该纵向位置从模型中面到表面的距离，单位与纹理坐标一致。
 */
function depthAt(y: number) {
  const heightRatio = (y - topPoint[1]) / (bottomPoint[1] - topPoint[1])
  return maxDepth * Math.min(1, .29 + heightRatio * 3.35, .23 + (1 - heightRatio) * 3.7)
}

/**
 * 用途 将纹理坐标系的距离换算为随模型宽度缩放的 CSS 长度。
 * 参数 length 为按纹理像素表达的模型距离。
 * 返回值 可用于表面尺寸和三维平移的 CSS 长度表达式。
 */
function modelLength(length: number) {
  return `calc(var(--shield-unit) * ${length / textureWidth * 100})`
}

/**
 * 用途 求轮廓线段在盾面平面内朝外的单位法向。
 * 参数 start、end 为按轮廓行进顺序排列的两个端点。
 * 返回值 从盾体朝外的单位向量。
 */
function outwardNormal([x, y]: Point, [endX, endY]: Point): Point {
  const dx = endX - x
  const dy = endY - y
  const length = Math.hypot(dx, dy)
  return [-dy / length, dx / length]
}

/**
 * 用途 在同一轮廓点构造前沿、外侧折棱和后沿，供相邻切面共用。
 * 参数 point 为当前轮廓点，previous、next 为相邻轮廓点。
 * 返回值 用于拼接侧边的三维截面与材质亮度。
 */
function edgeSection([x, y]: Point, previous: Point, next: Point) {
  const [incomingX, incomingY] = outwardNormal(previous, [x, y])
  const [outgoingX, outgoingY] = outwardNormal([x, y], next)
  const depth = depthAt(y)
  const alignment = incomingX * outgoingX + incomingY * outgoingY
  const reach = bevelReach * depth / maxDepth / Math.max(1, 1 + alignment)
  const front: Vertex = [x, y, depth]
  const ridge: Vertex = [x + (incomingX + outgoingX) * reach,
    y + (incomingY + outgoingY) * reach, 0]
  const back: Vertex = [x, y, -depth]
  return { front, ridge, back, light: 43 - 10 * outgoingX - 8 * outgoingY }
}

/**
 * 用途 将两个相邻截面连接成正侧与背侧的真实斜面。
 * 参数 start、end 为沿盾牌轮廓排列的相邻截面。
 * 返回值 四个三角面片的定位、朝向和明暗样式。
 */
function edgeFacets(start: ReturnType<typeof edgeSection>, end: ReturnType<typeof edgeSection>) {
  return [
    facetStyle(start.front, end.front, start.ridge, start.light + 6, 'to bottom'),
    facetStyle(end.ridge, start.ridge, end.front, start.light + 6, 'to top'),
    facetStyle(start.ridge, end.ridge, start.back, start.light - 6, 'to top'),
    facetStyle(end.back, start.back, end.ridge, start.light - 6, 'to bottom'),
  ]
}

/**
 * 用途 将 CSS 三角形的两个平面坐标轴映射到侧边的三维顶点。
 * 参数 origin、first、second 为三点，light 为材质亮度，direction 为反射渐变方向。
 * 返回值 可用于显示该切面的定位、三维矩阵及颜色参数。
 */
function facetStyle(
  [x, y, z]: Vertex, first: Vertex, second: Vertex, light: number, direction: string,
) {
  const ux = (first[0] - x) / facetUnit
  const uy = (first[1] - y) / facetUnit
  const uz = (first[2] - z) / facetUnit
  const vx = (second[0] - x) / facetUnit
  const vy = (second[1] - y) / facetUnit
  const vz = (second[2] - z) / facetUnit
  const nx = uy * vz - uz * vy
  const ny = uz * vx - ux * vz
  const nz = ux * vy - uy * vx
  const normalLength = Math.hypot(nx, ny, nz)
  const matrix = [
    ux, uy, uz, 0,
    vx, vy, vz, 0,
    nx / normalLength, ny / normalLength, nz / normalLength, 0,
    0, 0, 0, 1,
  ]
  // 让三角片在共同边缘有极少量重叠，避免透视抗锯齿露出背景。
  const overlap = facetUnit * facetOverlap
  return {
    left: `${(x - (ux + vx) * overlap) / textureWidth * 100}%`,
    top: `${(y - (uy + vy) * overlap) / textureHeight * 100}%`,
    transform: `translateZ(${modelLength(z - (uz + vz) * overlap)})
      matrix3d(${matrix.join(', ')})`,
    '--edge-light': `${light}%`,
    '--edge-direction': direction,
  }
}

/**
 * 用途 将一段正面纹理投影到连续的曲面片上，背面复用相同曲率。
 * 参数 start、end 是面片上下两边在纹理上的纵向坐标。
 * 返回值 面片的深度、斜率和对应的纹理裁切区域。
 */
function bandStyle(start: number, end: number) {
  const height = end - start
  const depth = depthAt(start)
  const rise = depthAt(end) - depth
  const length = Math.hypot(height, rise)
  const textureScale = length / height
  return {
    top: `${start / textureHeight * 100}%`,
    // 圆弧在屏幕上会落到小数像素，轻微重叠面片可避免缝隙露出背景。
    height: `calc(${modelLength(length)} + 1.2px)`,
    transform: `translateZ(${modelLength(depth)}) rotateX(${Math.atan2(rise, height)}rad)`,
    backgroundSize: `100% ${modelLength(textureHeight * textureScale)}`,
    backgroundPositionY: modelLength(-start * textureScale),
  }
}
</script>

<template>
  <span class="shield-model" :style="{
    '--shield-pitch': pitch + 'deg',
    '--shield-yaw': yaw + 'deg',
    '--facet-size': facetSize,
    '--shield-texture': 'url(' + shieldArt + ')',
    '--core-depth': coreDepth,
    '--core-angle': coreAngle,
    '--core-y': coreY / textureHeight * 100 + '%',
    '--core-radius': modelLength(coreRadius),
    '--core-texture-size': `${textureWidth / coreDiameter * 100}% ${textureHeight / coreDiameter * 100}%`,
    '--core-texture-y': coreTextureY + '%',
    '--collar-width': collarWidth,
  }">
    <span v-for="(edge, index) in edges" :key="index" class="shield-edge" :style="edge"></span>
    <span v-for="face in ['back', 'front']" :key="face" class="shield-face" :class="'shield-' + face">
      <span v-for="(band, index) in bands" :key="index" class="shield-band" :style="band"></span>
      <span class="shield-core-surface">
        <span class="shield-reactor-seat"></span>
        <span v-for="segment in collarSegments" :key="segment" class="shield-reactor-side"
          :style="{ '--collar-angle': segment * 360 / collarSegments + 'deg' }"></span>
        <span class="shield-reactor-face">
          <span v-if="pulse" :key="pulse" class="security-pulse"></span>
        </span>
      </span>
    </span>
  </span>
</template>

<style scoped>
.shield-model,
.shield-face {
  position: absolute;
  inset: 0;
  transform-style: preserve-3d;
}

.shield-model {
  transform: rotateX(var(--shield-pitch)) rotateY(var(--shield-yaw)) scaleX(.93);
}
.shield-back { transform: rotateY(180deg); }

.shield-edge {
  position: absolute;
  width: var(--facet-size);
  height: var(--facet-size);
  clip-path: polygon(0 0, 100% 0, 0 100%);
  transform-origin: 0 0;
  background: linear-gradient(var(--edge-direction),
    hsl(204 66% calc(var(--edge-light) + 9%)) 0%,
    hsl(213 64% var(--edge-light)) 21%,
    hsl(217 71% calc(var(--edge-light) - 12%)) 87%,
    hsl(207 72% calc(var(--edge-light) - 2%)) 100%);
}

.shield-band {
  position: absolute;
  left: 0;
  width: 100%;
  transform-origin: 0 0;
  backface-visibility: hidden;
  background: var(--shield-texture) no-repeat;
  opacity: .95;
  filter: brightness(var(--shield-brightness, 1));
}

.shield-back .shield-band { filter: brightness(calc(var(--shield-brightness, 1) * .65)) saturate(.73); }

.shield-core-surface {
  --core-lift: calc(var(--shield-unit) * 2.6);
  position: absolute;
  top: var(--core-y);
  left: 50%;
  width: calc(var(--core-radius) * 2);
  aspect-ratio: 1;
  backface-visibility: hidden;
  translate: -50% -50%;
  transform: translateZ(calc(var(--core-depth) + .5px)) rotateX(var(--core-angle));
  transform-style: preserve-3d;
}

.shield-reactor-seat,
.shield-reactor-face {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  backface-visibility: hidden;
}

.shield-reactor-seat {
  background: #103e69;
  box-shadow: 0 0 0 1px #97d3f2a6;
}

.shield-reactor-face {
  transform: translateZ(var(--core-lift));
  background: var(--shield-texture) no-repeat 50% var(--core-texture-y) / var(--core-texture-size);
  box-shadow: 0 0 0 1px #acedfb9c, 0 0 12px #60c6ff4f;
  filter: brightness(var(--shield-brightness, 1));
}

.shield-reactor-side {
  position: absolute;
  top: 50%;
  left: 50%;
  width: calc(var(--collar-width) + .45px);
  height: calc(var(--core-lift) + .2px);
  background: linear-gradient(#c2e5f9, #3b6d9d 21%, #102945 55%, #386892 77%, #a2dcf5);
  transform: translate(-50%, -50%) translateZ(calc(var(--core-lift) / 2))
    rotate(var(--collar-angle)) translateY(calc(-1 * var(--core-radius))) rotateX(90deg);
}

.shield-reactor-side:nth-of-type(n + 7):nth-of-type(-n + 9) {
  background: linear-gradient(#6487ab, #a93f62 38%, #f7607c 57%, #71415b 70%, #2d5e8f);
}

.shield-back .shield-reactor-face {
  filter: brightness(calc(var(--shield-brightness, 1) * .65)) saturate(.73);
}

.security-pulse {
  position: absolute;
  inset: 12%;
  border: 2px solid #ff8192;
  border-radius: 50%;
  opacity: 0;
  background: radial-gradient(circle, #ff3e5970, transparent 65%);
  box-shadow: 0 0 24px #ff3659, inset 0 0 15px #ff3659;
  animation: security-pulse .86s ease-out;
}

@keyframes security-pulse {
  from { opacity: 1; scale: .65; }
  to { opacity: 0; scale: 3.15; }
}

@media (prefers-reduced-motion: reduce) {
  .security-pulse { animation: none; }
}
</style>

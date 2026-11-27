export const shieldWidth = 1122
export const shieldHeight = 1402
const shieldDepth = shieldWidth * .14
const bevelWidth = shieldWidth * .035
const coreY = 630
const coreRadius = 174
const coreLift = shieldWidth * .026
const rimSegments = 24

type Point = readonly [x: number, y: number]
type Vertex = readonly [x: number, y: number, z: number]
type Color = readonly [r: number, g: number, b: number]
type UV = readonly [u: number, v: number]

const top: Point = [561, 3]
const bottom: Point = [561, 1399]
// 轮廓来自盾牌纹理，前后表面与两道折边使用同一组顶点。
const leftEdge: Point[] = [
  top, [535, 25], [487, 65], [424, 111], [373, 141], [303, 172],
  [199, 206], [91, 235], [30, 255], [11, 287], [13, 361], [14, 421],
  [16, 490], [21, 548], [34, 604], [46, 661], [62, 720], [78, 779],
  [98, 839], [127, 904], [161, 968], [200, 1029], [251, 1097], [302, 1158],
  [361, 1220], [425, 1281], [485, 1334], [540, 1380], bottom,
]
const outline: Point[] = [
  ...leftEdge,
  ...leftEdge.slice(1, -1).reverse().map(([x, y]): Point => [shieldWidth - x, y]),
]
const silver: Color = [1, 1, 1]
const darkSeat: Color = [.062, .18, .31]
const cyan: Color = [.16, .56, .79]
const blue: Color = [.10, .33, .58]
const red: Color = [.87, .23, .36]

/**
 * 用途 计算特定高度上的盾体半厚度，保留中段折棱和上下收边。
 * 参数 y 为原始纹理上的纵向坐标。
 * 返回值 以纹理像素为单位的半厚度。
 */
function depthAt(y: number) {
  const heightRatio = (y - top[1]) / (bottom[1] - top[1])
  return shieldDepth * Math.min(1, .29 + heightRatio * 3.35, .23 + (1 - heightRatio) * 3.7)
}

/**
 * 用途 求轮廓线段在盾面内的朝外单位法向。
 * 参数 start、end 是沿外轮廓方向排列的相邻纹理坐标。
 * 返回值 用来延伸折棱的单位向量。
 */
function normal([x, y]: Point, [endX, endY]: Point): Point {
  const dx = endX - x
  const dy = endY - y
  const length = Math.hypot(dx, dy)
  return [-dy / length, dx / length]
}

/**
 * 用途 生成顶点在正面、折棱与背面的对应位置。
 * 参数 point 为轮廓点，previous、next 为相接的轮廓点。
 * 返回值 供相邻面片共用的截面顶点及侧面基础亮度。
 */
function edgeSection([x, y]: Point, previous: Point, next: Point) {
  const [inX, inY] = normal(previous, [x, y])
  const [outX, outY] = normal([x, y], next)
  const depth = depthAt(y)
  const joint = Math.max(1, 1 + inX * outX + inY * outY)
  const reach = bevelWidth * depth / shieldDepth / joint
  const front: Vertex = [x, y, depth]
  const ridge: Vertex = [x + (inX + outX) * reach, y + (inY + outY) * reach, 0]
  const back: Vertex = [x, y, -depth]
  return { front, ridge, back, light: .83 - .19 * outX - .14 * outY }
}

/**
 * 用途 将一个顶点编码成 GPU 缓冲区中的位置、取样坐标和材质。
 * 参数 target 为输出数组，point、uv 为位置与纹理坐标，color、kind 为颜色与材质编号。
 * 返回值 无。
 */
function addVertex(target: number[], [x, y, z]: Vertex, [u, v]: UV, color: Color, kind: number) {
  target.push((x - shieldWidth / 2) / shieldWidth, (shieldHeight / 2 - y) / shieldWidth,
    z / shieldWidth, u, v, ...color, kind)
}

/**
 * 用途 将四边形按对角线划分为两个共享顶点位置的三角形。
 * 参数 target 为输出数组，points 为四角顶点，color、kind 为材质，facing 控制盾徽纹理方向。
 * 返回值 无。
 */
function addQuad(target: number[], points: readonly [Vertex, Vertex, Vertex, Vertex],
  color: Color, kind: number, facing = 1) {
  for (const index of [0, 1, 2, 0, 2, 3] as const) {
    const point = points[index]
    const u = point[0] / shieldWidth
    addVertex(target, point, [facing === 1 ? u : 1 - u, 1 - point[1] / shieldHeight], color, kind)
  }
}

/**
 * 用途 沿统一轮廓生成带透明切线的正反盾面和金属折边。
 * 参数 target 为待填充的顶点数组。
 * 返回值 无。
 */
function addHull(target: number[]) {
  leftEdge.forEach(([x, y], index) => {
    const next = leftEdge[index + 1]
    if (!next) return
    const [endX, endY] = next
    for (const facing of [-1, 1]) {
      const z = depthAt(y) * facing
      const endZ = depthAt(endY) * facing
      const light = facing === 1 ? silver : [.62, .68, .81] as const
      addQuad(target, [[x, y, z], [shieldWidth - x, y, z],
        [shieldWidth - endX, endY, endZ], [endX, endY, endZ]], light, 1, facing)
    }
  })

  const sections = outline.map((point, index) => edgeSection(point,
    outline[index - 1] ?? outline.at(-1) ?? bottom, outline[index + 1] ?? top))
  sections.forEach((start, index) => {
    const end = sections[index + 1] ?? sections[0]
    if (!end) return
    const tint: Color = [.15 * start.light, .43 * start.light, .80 * start.light]
    addQuad(target, [start.front, end.front, end.ridge, start.ridge], tint, 0)
    addQuad(target, [start.ridge, end.ridge, end.back, start.back],
      [tint[0] * .55, tint[1] * .67, tint[2] * .74], 0)
  })
}

/**
 * 用途 计算安防核心圆周上指定角度的顶点。
 * 参数 angle 为弧度，z 为当前环面的深度，radius 为圆环半径。
 * 返回值 位于「防」字核心圆周上的三维坐标。
 */
function corePoint(angle: number, z: number, radius = coreRadius): Vertex {
  return [shieldWidth / 2 + Math.cos(angle) * radius, coreY + Math.sin(angle) * radius, z]
}

/**
 * 用途 在盾面前方绘制完整的圆形核心、座面和红蓝侧壁。
 * 参数 target 为待填充的顶点数组，facing 为正面或背面的方向符号。
 * 返回值 无。
 */
function addReactor(target: number[], facing: number) {
  const seat = (depthAt(coreY) + 1) * facing
  const cap = (depthAt(coreY) + coreLift) * facing
  const tint: Color = facing === 1 ? silver : [.62, .68, .81]
  for (let index = 0; index < rimSegments; index++) {
    const start = index * 2 * Math.PI / rimSegments
    const end = (index + 1) * 2 * Math.PI / rimSegments
    const center: Vertex = [shieldWidth / 2, coreY, cap]
    const a = corePoint(start, cap)
    const b = corePoint(end, cap)
    for (const point of [center, a, b]) {
      const u = point[0] / shieldWidth
      addVertex(target, point, [facing === 1 ? u : 1 - u, 1 - point[1] / shieldHeight], tint, 1)
    }
    const light = index < 3 || index > rimSegments - 4 ? red : index < 12 ? cyan : blue
    addQuad(target, [a, b, corePoint(end, seat), corePoint(start, seat)], light, 0)
    for (const point of [[shieldWidth / 2, coreY, seat] as const,
      corePoint(start, seat), corePoint(end, seat)]) {
      addVertex(target, point, [0, 0], darkSeat, 0)
    }
  }
}

/**
 * 用途 添加贴在核心正反面的呼吸光环与冲击波绘制区域。
 * 参数 target 为顶点数组，facing 为所在盾面，kind 选择呼吸光或冲击波。
 * 返回值 无。
 */
function addLight(target: number[], facing: number, kind: number) {
  const center = shieldWidth / 2
  const z = (depthAt(coreY) + coreLift + kind) * facing
  const radius = kind === 2 ? coreRadius * 1.04 : coreRadius * .76
  const points: Vertex[] = [
    [center - radius, coreY - radius, z], [center + radius, coreY - radius, z],
    [center + radius, coreY + radius, z], [center - radius, coreY + radius, z],
  ]
  const uv: UV[] = [[0, 1], [1, 1], [1, 0], [0, 0]]
  for (const index of [0, 1, 2, 0, 2, 3] as const) {
    const point = points[index]
    const tex = uv[index]
    if (point && tex) addVertex(target, point, tex, silver, kind)
  }
}

const geometry: number[] = []
addHull(geometry)
addReactor(geometry, 1)
addReactor(geometry, -1)
export const shieldSolidVertices = geometry.length / 9
for (const facing of [-1, 1]) {
  addLight(geometry, facing, 2)
  addLight(geometry, facing, 3)
}
export const shieldMesh = new Float32Array(geometry)
export const shieldCoreOffset = (shieldHeight / 2 - coreY) / shieldWidth

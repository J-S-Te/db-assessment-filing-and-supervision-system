import type { DistrictRing } from './dashboardGeo'

/**
 * 驾驶舱三维沙盘的相机与绘制模型。
 *
 * 平面坐标来自区县边界的等距投影，高度 z 为该区县的综合风险指数。
 * 渲染链路为：绕垂直轴旋转 yaw → 绕东西轴俯仰 tilt → 透视除法。
 * 俯仰角 0 度是正俯视，越大越接近侧视；方位角为正时相机向西偏。
 */
export type MapCamera = { tilt: number; yaw: number; focal: number }
export type RiskLevel = 'high' | 'mid' | 'low' | 'none'

export type District3DItem = {
  district: string
  /** 区县面按多边形、环、顶点三层组织。 */
  rings: DistrictRing[][]
  label: [number, number]
  value: number
  level: RiskLevel
  height: number
  beam?: boolean
}

export type Map3DPath = { d: string; level: RiskLevel; district: string }
export type Map3DLabel = {
  district: string
  value: number
  level: RiskLevel
  x: number
  y: number
  selected: boolean
}
export type Map3DModel = {
  ground: Map3DPath[]
  walls: Map3DPath[]
  faces: Map3DPath[]
  edges: { d: string }[]
  beams: { d: string }[]
  /** 地平面网格线，与地图共用同一投影，因此自带透视关系。 */
  floor: { d: string }[]
  /** 地面同心雷达环，投影后呈椭圆。 */
  radar: { d: string; radius: number; cx: number; cy: number }[]
  labels: Map3DLabel[]
  viewBox: string
  plan: { minX: number; maxX: number; minY: number; maxY: number; cx: number; cy: number }
}

/** 默认相机：侧视 54 度、相机偏西 18 度，与设计稿一致。 */
export const defaultMapCamera: MapCamera = { tilt: 54, yaw: -18, focal: 1600 }

/**
 * 用途 生成把平面点与高度投影到屏幕的相机。
 * 参数 camera 为相机参数，plan 为平面坐标的中心与范围。
 * 返回值 投影函数与相机水平方位。
 */
export function makeMapCamera(camera: MapCamera, plan: Map3DModel['plan']) {
  const rad = (value: number) => value * Math.PI / 180
  const cosTilt = Math.cos(rad(camera.tilt)), sinTilt = Math.sin(rad(camera.tilt))
  const cosYaw = Math.cos(rad(camera.yaw)), sinYaw = Math.sin(rad(camera.yaw))
  const { focal } = camera
  const { cx, cy } = plan
  return {
    /** 相机在平面上的水平方位，用于判断哪些墙面朝向观察者。 */
    horizontal: { x: sinYaw, y: cosYaw },
    /**
     * 用途 把平面坐标与高度投影到屏幕。
     * 参数 x、y 为平面坐标，z 为高度。
     * 返回值 屏幕坐标。
     */
    project(x: number, y: number, z: number) {
      const x0 = x - cx, y0 = y - cy
      const x1 = x0 * cosYaw - y0 * sinYaw, y1 = x0 * sinYaw + y0 * cosYaw
      const screenY = y1 * cosTilt - z * sinTilt
      const toward = y1 * sinTilt + z * cosTilt
      const scale = focal / (focal - toward)
      return { x: cx + x1 * scale, y: cy + screenY * scale }
    },
  }
}

/**
 * 用途 计算平面坐标的范围与中心。
 * 参数 items 为参与展示的区县。
 * 返回值 平面包围盒与中心。
 */
function planOf(items: readonly District3DItem[]): Map3DModel['plan'] {
  let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity
  items.forEach(item => item.rings.forEach(polygon => polygon.forEach(ring => ring.forEach(([x, y]) => {
    if (x < minX) minX = x
    if (x > maxX) maxX = x
    if (y < minY) minY = y
    if (y > maxY) maxY = y
  }))))
  return { minX, maxX, minY, maxY, cx: (minX + maxX) / 2, cy: (minY + maxY) / 2 }
}

/**
 * 用途 计算平面环的有向面积，用于判定绕向并确定外法线方向。
 * 参数 ring 为平面环坐标。
 * 返回值 有向面积。
 */
function signedArea(ring: DistrictRing) {
  let area = 0
  for (let index = 0; index < ring.length; index++) {
    const [x1, y1] = ring[index]!
    const [x2, y2] = ring[(index + 1) % ring.length]!
    area += x1 * y2 - x2 * y1
  }
  return area / 2
}

/**
 * 用途 生成三维沙盘的全部绘制指令。
 * 参数 items 为参与展示的区县，camera 为相机参数，selected 为当前选中的区县名。
 * 返回值 地面、侧壁、顶面、轮廓、光柱、标签与视窗。
 */
export function buildMap3D(
  items: readonly District3DItem[], camera: MapCamera, selected?: string | null,
): Map3DModel {
  const plan = planOf(items)
  const { project, horizontal } = makeMapCamera(camera, plan)
  const ground: Map3DPath[] = []
  const walls: Map3DPath[] = []
  const faces: Map3DPath[] = []
  const edges: { d: string }[] = []
  const beams: { d: string }[] = []
  const labels: Map3DLabel[] = []
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity
  const track = (point: { x: number; y: number }) => {
    if (point.x < minX) minX = point.x
    if (point.x > maxX) maxX = point.x
    if (point.y < minY) minY = point.y
    if (point.y > maxY) maxY = point.y
  }
  const fmt = (point: { x: number; y: number }) => `${point.x.toFixed(1)} ${point.y.toFixed(1)}`
  const sorted = [...items].sort((a, b) => a.label[1] - b.label[1])

  // 第一层：真实地平面，全部区县先按高度 0 画出彩色地面。
  sorted.forEach(item => {
    let path = ''
    item.rings.forEach(polygon => polygon.forEach(ring => {
      path += 'M' + ring.map(([x, y]) => {
        const point = project(x, y, 0)
        track(point)
        return fmt(point)
      }).join('L') + 'Z'
    }))
    ground.push({ d: path, level: item.level, district: item.district })
  })

  // 第二层：抬起的立体板块，侧壁只画朝向相机的边。
  sorted.forEach(item => {
    const height = item.height
    const isSelected = item.district === selected
    item.rings.forEach(polygon => {
      const outer = polygon[0] ?? []
      let wall = ''
      polygon.forEach(ring => {
        const sign = signedArea(ring) > 0 ? 1 : -1
        for (let index = 0; index < ring.length; index++) {
          const [ax, ay] = ring[index]!
          const [bx, by] = ring[(index + 1) % ring.length]!
          const nx = sign * (by - ay), ny = -sign * (bx - ax)
          if (nx * horizontal.x + ny * horizontal.y <= 0) continue
          const topA = project(ax, ay, height), topB = project(bx, by, height)
          const bottomB = project(bx, by, 0), bottomA = project(ax, ay, 0)
          track(topA); track(topB); track(bottomA); track(bottomB)
          wall += `M${fmt(topA)}L${fmt(topB)}L${fmt(bottomB)}L${fmt(bottomA)}Z`
        }
      })
      if (wall && height > 0) walls.push({ d: wall, level: item.level, district: item.district })

      let top = ''
      polygon.forEach(ring => {
        top += 'M' + ring.map(([x, y]) => {
          const point = project(x, y, height)
          track(point)
          return fmt(point)
        }).join('L') + 'Z'
      })
      if (height > 0) faces.push({ d: top, level: item.level, district: item.district })
      if (isSelected) {
        edges.push({ d: 'M' + outer.map(([x, y]) => fmt(project(x, y, 0))).join('L') + 'Z' })
        edges.push({ d: 'M' + outer.map(([x, y]) => fmt(project(x, y, height))).join('L') + 'Z' })
      }
    })

    const anchor = project(item.label[0], item.label[1], height)
    track(anchor)
    labels.push({
      district: item.district, value: item.value, level: item.level,
      x: anchor.x, y: anchor.y, selected: isSelected,
    })

    if (item.beam) {
      const perpendicular = { x: -horizontal.y, y: horizontal.x }
      const half = 15, top = height + 150
      const a = project(item.label[0] + perpendicular.x * half, item.label[1] + perpendicular.y * half, height)
      const b = project(item.label[0] - perpendicular.x * half, item.label[1] - perpendicular.y * half, height)
      const c = project(item.label[0] - perpendicular.x * half, item.label[1] - perpendicular.y * half, top)
      const d = project(item.label[0] + perpendicular.x * half, item.label[1] + perpendicular.y * half, top)
      track(a); track(b); track(c); track(d)
      beams.push({ d: `M${fmt(a)}L${fmt(b)}L${fmt(c)}L${fmt(d)}Z` })
    }
  })

  /* 地台网格：画在零高度平面上，经同一投影后自然呈透视收敛。 */
  const floor: { d: string }[] = []
  const step = 34
  const left = plan.minX - 30, right = plan.maxX + 30
  const topEdge = plan.minY - 30, bottomEdge = plan.maxY + 30
  for (let x = Math.ceil(left / step) * step; x <= right; x += step) {
    const a = project(x, topEdge, 0), b = project(x, bottomEdge, 0)
    floor.push({ d: `M${fmt(a)}L${fmt(b)}` })
  }
  for (let y = Math.ceil(topEdge / step) * step; y <= bottomEdge; y += step) {
    const a = project(left, y, 0), b = project(right, y, 0)
    floor.push({ d: `M${fmt(a)}L${fmt(b)}` })
  }

  /* 雷达环：零高度平面上的同心圆，投影后为椭圆。 */
  const radar: Map3DModel['radar'] = []
  const radarCx = (plan.minX + plan.maxX) / 2, radarCy = (plan.minY + plan.maxY) / 2
  const span = Math.max(plan.maxX - plan.minX, plan.maxY - plan.minY) / 2 + 40
  for (const ratio of [0.42, 0.66, 0.92]) {
    const radius = span * ratio
    const points: string[] = []
    for (let step = 0; step <= 48; step++) {
      const angle = step / 48 * Math.PI * 2
      const point = project(radarCx + Math.cos(angle) * radius, radarCy + Math.sin(angle) * radius, 0)
      track(point)
      points.push(fmt(point))
    }
    radar.push({ d: `M${points.join('L')}Z`, radius, cx: radarCx, cy: radarCy })
  }

  const padding = 16
  return {
    ground, walls, faces, edges, beams, labels, floor, radar, plan,
    viewBox: `${(minX - padding).toFixed(1)} ${(minY - padding).toFixed(1)} `
      + `${(maxX - minX + padding * 2).toFixed(1)} ${(maxY - minY + padding * 2).toFixed(1)}`,
  }
}

/**
 * 用途 按综合风险指数生成三维沙盘中的配色分级。
 * 参数 index 为 0 至 100 的综合风险指数。
 * 返回值 high、mid、low 或 none。
 */
export function riskLevelOf(index: number): RiskLevel {
  if (index >= 66) return 'high'
  if (index >= 34) return 'mid'
  if (index > 0) return 'low'
  return 'none'
}

/**
 * 用途 按综合风险指数换算区块浮起高度。
 * 参数 index 为 0 至 100 的综合风险指数，scale 为满分对应的高度。
 * 返回值 三维空间中的高度。
 */
export function riskHeightOf(index: number, scale: number) {
  return Math.round(index * scale / 100)
}

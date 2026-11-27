import { geoIdentity, geoPath } from 'd3-geo'

export const districtBoundaryMaxBytes = 2 * 1024 * 1024
const maxDistricts = 200
const maxPositions = 50_000
const mapWidth = 440
const mapHeight = 310
const mapPadding = 16

type Position = [number, number]
type Ring = Position[]
type Polygon = Ring[]
type DistrictBoundary = {
  district: string
  geometry: { type: 'MultiPolygon'; coordinates: Polygon[] }
}
type DistrictPath = { district: string; path: string; label: Position }
const planarPath = geoPath()

export type DistrictCounts = { systems: number; high: number; medium: number; low: number }
export type DistrictRiskRow = DistrictCounts & { district: string }
export type DistrictRiskSummary = {
  items: DistrictRiskRow[]
  withoutDistrict: DistrictCounts
  omittedDistricts: number
}
export type DistrictMetric = 'all' | 'high' | 'medium' | 'low'
type Intensity = 'unmatched' | 'zero' | 'light' | 'medium' | 'strong'

/**
 * 用途 读取并验证本地导入的 WGS84 区县多边形。
 * 参数 source 为 UTF-8 GeoJSON 文本。
 * 返回值 区县名和几何边界，尺寸或内容无效时报告可显示的错误。
 */
export function readDistrictBoundaries(source: string): DistrictBoundary[] {
  if (source.length > districtBoundaryMaxBytes ||
      new TextEncoder().encode(source).length > districtBoundaryMaxBytes) {
    throw new Error('区县边界文件不得超过 2 MiB。')
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(source)
  } catch {
    throw new Error('无法解析 GeoJSON 文件。')
  }
  if (!isObject(parsed) || parsed.type !== 'FeatureCollection' || 'crs' in parsed ||
      !Array.isArray(parsed.features) || !parsed.features.length || parsed.features.length > maxDistricts) {
    throw new Error('请导入含 1 至 200 个区县的 WGS84 GeoJSON FeatureCollection。')
  }
  const seen = new Set<string>()
  const limits = { remaining: maxPositions }
  return parsed.features.map((feature: unknown) => {
    const boundary = readFeature(feature, limits)
    if (seen.has(boundary.district)) throw new Error(`区县名称重复：${boundary.district}。`)
    seen.add(boundary.district)
    return boundary
  })
}

/**
 * 用途 用 D3 将导入的市域边界等比例拟合到本地视口并生成路径。
 * 参数 boundaries 为校验通过的 WGS84 区县面，allowed 为会话允许展示的区县。
 * 返回值 各区县路径和主要面标签坐标；无边界时返回空列表。
 */
export function projectDistrictBoundaries(
  boundaries: readonly DistrictBoundary[], allowed?: readonly string[],
): DistrictPath[] {
  const visible = allowed ? boundaries.filter(row => allowed.includes(row.district)) : boundaries
  if (!visible.length) return []
  const geometry = { type: 'GeometryCollection' as const, geometries: visible.map(row => row.geometry) }
  const viewport = geoIdentity().reflectY(true).fitExtent(
    [[mapPadding, mapPadding], [mapWidth - mapPadding, mapHeight - mapPadding]], geometry,
  )
  const draw = geoPath(viewport).digits(2)
  return visible.map(({ district, geometry }) => {
    const polygons = geometry.coordinates.map(coordinates => ({ type: 'Polygon' as const, coordinates }))
    // 沿海区县可能有多个岛屿，将标签放在面积最大的多边形上。
    const mainArea = polygons.reduce((largest, polygon) => draw.area(polygon) > draw.area(largest)
      ? polygon : largest)
    return { district, path: draw(geometry) ?? '', label: draw.centroid(mainArea) }
  })
}

/**
 * 用途 将区县边界与指定风险指标精确对应，并计算分级着色数据。
 * 参数 boundaries 为已生成的边界路径，rows 为真实区县统计，metric 为选择的风险计数。
 * 返回值 区县路径及色阶、找不到边界的统计区县和所选指标最大值。
 */
export function districtHeatMap(
  boundaries: readonly DistrictPath[], rows: readonly DistrictRiskRow[], metric: DistrictMetric,
) {
  const risks = new Map(rows.map(item => [item.district, item]))
  let max = 0
  const matches = boundaries.map(boundary => {
    const counts = risks.get(boundary.district)
    risks.delete(boundary.district)
    const value = counts && (metric === 'all' ? counts.high + counts.medium + counts.low : counts[metric])
    max = Math.max(max, value ?? 0)
    return { ...boundary, counts, value }
  })
  const shapes = matches.map(match => ({ ...match, intensity: heatIntensity(match.value, max) }))
  return { shapes, unmatched: [...risks.keys()], max, viewBox: `0 0 ${mapWidth} ${mapHeight}` }
}

/**
 * 用途 从 GeoJSON Feature 提取名称和区县面。
 * 参数 value 为候选要素，limits 限制完整文件的坐标总数。
 * 返回值 可供绘图的区县边界，无效要素时抛出错误。
 */
function readFeature(value: unknown, limits: { remaining: number }): DistrictBoundary {
  if (!isObject(value) || value.type !== 'Feature' || !isObject(value.properties) ||
      !isObject(value.geometry) || 'crs' in value || 'crs' in value.geometry) {
    throw new Error('每个区县都需要有效的 GeoJSON Feature 和几何对象。')
  }
  const district = typeof value.properties.name === 'string' ? value.properties.name.trim() : ''
  if (!district || district.length > 80 || /\p{C}/u.test(district)) {
    throw new Error('每个区县需要有效的 properties.name，最多 80 个字符。')
  }
  const { type, coordinates } = value.geometry
  if (type !== 'Polygon' && type !== 'MultiPolygon') {
    throw new Error(`${district}的边界须使用 Polygon 或 MultiPolygon。`)
  }
  const items = type === 'Polygon' ? [coordinates] : coordinates
  if (!Array.isArray(items) || !items.length || items.length > limits.remaining) {
    throw new Error(`${district}缺少有效的多边形。`)
  }
  const polygons = items.map((polygon: unknown) => {
    if (!Array.isArray(polygon) || !polygon.length || polygon.length > limits.remaining) {
      throw new Error(`${district}缺少有效的多边形轮廓。`)
    }
    return polygon.map((ring: unknown) => readRing(ring, limits))
  })
  return { district, geometry: { type: 'MultiPolygon', coordinates: polygons } }
}

/**
 * 用途 校验闭合、非退化的 WGS84 多边形轮廓并累计文件坐标数。
 * 参数 value 为候选轮廓，limits 为文件剩余坐标额度。
 * 返回值 经纬度轮廓数组，无效或超过额度时抛出错误。
 */
function readRing(value: unknown, limits: { remaining: number }): Ring {
  if (!Array.isArray(value) || value.length < 4 || value.length > limits.remaining) {
    throw new Error('区县轮廓至少需要 4 个坐标，文件最多允许 50,000 个坐标。')
  }
  limits.remaining -= value.length
  const ring = value.map(readPosition)
  const [originX, originY] = ring[0]!
  const [lastX, lastY] = ring[ring.length - 1]!
  if (originX !== lastX || originY !== lastY) throw new Error('区县多边形轮廓须首尾闭合。')
  if (!planarPath.area({ type: 'Polygon', coordinates: [ring] })) {
    throw new Error('区县轮廓须包含可绘制的面积。')
  }
  return ring
}

/**
 * 用途 校验 WGS84 经度、纬度和可选的有限高程。
 * 参数 raw 为候选 GeoJSON 坐标。
 * 返回值 经纬度坐标，无效时抛出错误。
 */
function readPosition(raw: unknown): Position {
  if (!Array.isArray(raw) || (raw.length !== 2 && raw.length !== 3) ||
      !raw.every(part => typeof part === 'number' && Number.isFinite(part)) ||
      Math.abs(raw[0]) > 180 || Math.abs(raw[1]) > 90) {
    throw new Error('区县轮廓需要有效的 WGS84 经纬度坐标。')
  }
  return [raw[0], raw[1]]
}

/**
 * 用途 根据当前已匹配区县中的最大计数生成相对热度等级。
 * 参数 value 为当前统计值，max 为匹配区县的最大统计值。
 * 返回值 无对应记录、零值或者正数热度等级。
 */
function heatIntensity(value: number | undefined, max: number): Intensity {
  if (value === undefined) return 'unmatched'
  if (value === 0) return 'zero'
  if (value <= max / 3) return 'light'
  return value <= max * 2 / 3 ? 'medium' : 'strong'
}

/**
 * 用途 确认未知 JSON 值可以安全地按对象字段读取。
 * 参数 candidate 为解析后的未知值。
 * 返回值 候选值是否为普通 JSON 对象。
 */
function isObject(candidate: unknown): candidate is Record<string, unknown> {
  return candidate !== null && typeof candidate === 'object' && !Array.isArray(candidate)
}

import { readDistrictBoundaries } from './dashboardGeo'

const districtAssets = import.meta.glob<string>('../assets/district-boundaries.geojson', {
  eager: true, import: 'default', query: '?url&no-inline',
})
export const districtAsset = districtAssets['../assets/district-boundaries.geojson']

/**
 * 用途 读取并验证随驾驶舱发布的行政区边界。
 * 参数 无，边界资源来自同源构建。
 * 返回值 已验证的区县多边形，无法读取时抛出错误。
 */
export async function loadPublishedDistrictBoundaries() {
  if (!districtAsset) throw new Error('尚未配置辖区地图')
  const response = await fetch(districtAsset, {
    signal: AbortSignal.timeout(8_000), redirect: 'error', mode: 'same-origin',
  })
  if (!response.ok) throw new Error('辖区地图读取失败')
  return readDistrictBoundaries(await response.text())
}

const boundaryKey = 'grade-business.dashboard.district-boundaries.v1'
let currentBoundary: string | null = null
let volatile = false

/**
 * 用途 从同源浏览器存储读取边界，无法使用存储时沿用当次会话文件。
 * 参数 无。
 * 返回值 边界源文本和浏览器持久存储是否可用。
 */
export function loadDistrictBoundaryFile() {
  try {
    const stored = localStorage.getItem(boundaryKey)
    if (!volatile) currentBoundary = stored
    return { source: currentBoundary, stored: stored === currentBoundary }
  } catch {
    return { source: currentBoundary, stored: false }
  }
}

/**
 * 用途 保存本地地图文件或移除当前浏览器的副本。
 * 参数 source 为已验证的 GeoJSON 原文，null 表示移除。
 * 返回值 浏览器是否已成功持久化本次变更。
 */
export function saveDistrictBoundaryFile(source: string | null) {
  currentBoundary = source
  try {
    if (source === null) localStorage.removeItem(boundaryKey)
    else localStorage.setItem(boundaryKey, source)
    volatile = false
    return true
  } catch {
    volatile = true
    return false
  }
}

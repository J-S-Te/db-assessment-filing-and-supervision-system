import type { DashboardRiskLevel } from './dashboard'
import type { ResponseCounts } from './districtResponse'

export const riskLevels = { high: '高风险', mid: '中风险', low: '低风险', none: '无风险' } as const

/**
 * 用途 根据后端返回的风险分级确定仪表和区县指数颜色。
 * 参数 level 为当前统计分级。
 * 返回值 对应的设计稿颜色。
 */
export function riskColor(level?: DashboardRiskLevel) {
  return level === 'high' ? '#ff5d6c' : level === 'mid' ? '#ffb84d' : level === undefined ? '#7e93b6' : '#35d6ff'
}

/**
 * 用途 按驾驶舱服务端评分口径拆解未封顶的风险贡献。
 * 参数 counts 为同一范围的实际隐患计数。
 * 返回值 逾期、高风险和待复测的计数及贡献分。
 */
export function riskParts(counts?: ResponseCounts) {
  return ([
    ['overdue', '逾期项', 40, counts?.overdue],
    ['highRisk', '高风险项', 40, counts?.high],
    ['retest', '待复测项', 20, counts?.pendingRetest],
  ] as const).map(([key, name, weight, count]) => ({
    key, name, weight, count, points: count === undefined ? undefined : count * weight,
  }))
}

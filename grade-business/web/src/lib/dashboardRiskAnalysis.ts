import type { AlertCounts } from './alerts'
import type { DashboardRiskLevel } from './dashboard'
import type { DashboardAnalytics } from './dashboardAnalytics'
import type { ResponseCounts } from './districtResponse'

/**
 * 用途 按行业已登记的未销项隐患量计算看板排行。
 * 参数 analytics 为当前辖区经授权返回的分类统计。
 * 返回值 有实际隐患的前五个行业及各自总量。
 */
export function rankRiskIndustries(analytics?: DashboardAnalytics) {
  return (analytics?.industries.items ?? []).map(row => ({
    ...row, total: row.high + row.medium + row.low,
  })).filter(row => row.total > 0).sort((a, b) => b.total - a.total).slice(0, 5)
}

/**
 * 用途 将预警状态计数映射为环图的百分比与起始位置。
 * 参数 counts 为当前辖区的预警状态汇总。
 * 返回值 三种实际状态的绘图数据，空汇总返回空数组。
 */
export function riskAlertSegments(counts?: AlertCounts) {
  if (!counts?.total) return []
  let start = 0
  return ([
    ['pending', '待处理', '#ff5d6c'],
    ['processing', '处理中', '#ffb84d'],
    ['closed', '已关闭', '#3ddc97'],
  ] as const).map(([status, label, color]) => {
    const percent = counts[status] * 100 / counts.total
    const segment = { status, label, color, count: counts[status], percent, start }
    start += percent
    return segment
  })
}

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

/**
 * 用途 生成风险仪表从零到指定分值的上半圆弧线。
 * 参数 percent 为沿半圆的百分比分值。
 * 返回值 可用于 SVG path 的路径。
 */
export function riskGaugeArc(percent: number) {
  const angle = Math.PI * (1 - Math.max(0, Math.min(100, percent)) / 100)
  return `M13,86 A62,62 0 0 1 ${(75 + 62 * Math.cos(angle)).toFixed(1)},${(86 - 62 * Math.sin(angle)).toFixed(1)}`
}

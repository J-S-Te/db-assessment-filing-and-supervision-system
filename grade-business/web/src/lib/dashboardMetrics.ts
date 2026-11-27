import type { DashboardAlert } from './dashboard'

export const dashboardColors = {
  blue: '#58afff', teal: '#36cdb0', amber: '#f2b347', red: '#ff7878', purple: '#af9bff', slate: '#839bbb',
} as const

export type DashboardTone = keyof typeof dashboardColors

export interface MetricSlice {
  label: string
  count: number
  tone: DashboardTone
  /** 可选的自定义色值，用于同一色系的深浅序列。 */
  color?: string
}

export interface DashboardDay {
  date: string
  filingsCreated: number
  reportsCreated: number
  alertsTriggered: number
  hazardsClosed: number
}

export const dashboardTrendSeries = [
  { key: 'filingsCreated', label: '新增备案', tone: 'blue' },
  { key: 'reportsCreated', label: '新增测评报告', tone: 'purple' },
  { key: 'alertsTriggered', label: '触发预警', tone: 'amber' },
  { key: 'hazardsClosed', label: '隐患销项', tone: 'teal' },
] as const satisfies readonly { key: keyof Omit<DashboardDay, 'date'>; label: string; tone: DashboardTone }[]
export type DashboardSeriesKey = typeof dashboardTrendSeries[number]['key']

export const dashboardTrendRanges = [7, 14, 30] as const
export type DashboardTrendRange = typeof dashboardTrendRanges[number]

type AlertSignal = Pick<DashboardAlert, 'id' | 'severity' | 'status' | 'triggeredAt'>

/**
 * 用途 识别一次刷新中真实新增且仍活跃的高风险预警。
 * 参数 previous 为同范围上次成功快照，current 为当前成功快照。
 * 返回值 应短暂突出的预警 ID，首次加载不提示。
 */
export function newHighAlertIds(previous: readonly AlertSignal[] | undefined, current: readonly AlertSignal[]) {
  if (previous === undefined) return []
  const known = new Set(previous.map(item => item.id))
  const latest = Math.max(-Infinity, ...previous.map(item => Date.parse(item.triggeredAt)))
  return current.filter(item => item.severity === 'high' && item.status !== 'closed'
    && !known.has(item.id) && Date.parse(item.triggeredAt) > latest).map(item => item.id)
}

/**
 * 用途 保留实际比例的零值，无可用分母时返回空值。
 * 参数 part 与 total 为相同统计范围的分子和分母。
 * 返回值 四舍五入的百分比整数，或无可用数据时的 undefined。
 */
export function metricPercent(part?: number, total?: number) {
  if (part === undefined || total === undefined || total <= 0) return undefined
  return Math.round(part * 100 / total)
}

/**
 * 用途 从真实分类计数生成 ECharts 环图配置。
 * 参数 slices 为同一统计范围的各类记录数和显示颜色。
 * 返回值 非零分类的扇区配置，零样本只显示空环。
 */
export function dashboardRingOption(slices: readonly MetricSlice[]) {
  const data = slices.filter(slice => slice.count > 0).map(slice => ({
    name: slice.label, value: slice.count,
    itemStyle: { color: slice.color ?? dashboardColors[slice.tone] },
  }))
  return {
    animation: false,
    series: [{
      id: 'distribution', type: 'pie' as const, radius: ['68%', '88%'], data, silent: false,
      emphasis: { focus: 'self' as const, scale: true, scaleSize: 5 },
      label: { show: false }, labelLine: { show: false },
      showEmptyCircle: true, stillShowZeroSum: false, emptyCircleStyle: { color: '#233e57' },
      itemStyle: { borderWidth: 1, borderColor: '#06121f' },
    }],
  }
}

/**
 * 用途 截取近期业务记录并按共享计数尺度生成纵轴和序列合计。
 * 参数 history 为按日升序的业务记录，range 为包括当天的天数。
 * 返回值 已选日期、整数纵轴标签和各序列合计。
 */
export function dashboardTrend(history: readonly DashboardDay[], range: DashboardTrendRange) {
  const days = history.slice(-range)
  const totals = { filingsCreated: 0, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 }
  let largest = 0
  for (const day of days) {
    for (const { key } of dashboardTrendSeries) {
      totals[key] += day[key]
      largest = Math.max(largest, day[key])
    }
  }
  const step = Math.max(1, Math.ceil(largest / 4))
  return { days, totals, ticks: [0, step, 2 * step, 3 * step, 4 * step] }
}

/**
 * 用途 从所选窗口的实际日期和计数配置 ECharts 趋势图。
 * 参数 trend 为同口径纵轴及按日计数，hidden 为暂时隐藏的业务序列。
 * 返回值 共享整数坐标轴、计数曲线和纯文本提示配置。
 */
export function dashboardTrendOption(
  trend: ReturnType<typeof dashboardTrend>, hidden?: ReadonlySet<DashboardSeriesKey>,
) {
  const axisLabel = { color: '#9bb5ca', fontSize: 10, hideOverlap: true }
  return {
    animation: false,
    grid: { top: 9, right: 13, bottom: 8, left: 8, outerBoundsMode: 'same' as const },
    xAxis: { type: 'category' as const, data: trend.days.map(day => day.date.slice(5)), axisLabel,
      axisTick: { show: false }, axisLine: { lineStyle: { color: '#2a4864' } } },
    yAxis: { type: 'value' as const, min: 0, max: trend.ticks.at(-1), interval: trend.ticks[1], axisLabel,
      splitLine: { lineStyle: { color: '#294560', type: 'dashed' as const } } },
    tooltip: { trigger: 'axis' as const, renderMode: 'richText' as const, confine: true,
      textStyle: { color: '#e7f4fb', fontSize: 11 }, backgroundColor: '#0a2035', borderColor: '#35678d' },
    series: dashboardTrendSeries.filter(series => trend.totals[series.key] > 0 && !hidden?.has(series.key)).map(series => ({
      id: series.key, type: 'line' as const, name: series.label, data: trend.days.map(day => day[series.key]),
      symbol: 'circle', symbolSize: trend.days.length > 14 ? 3 : 5,
      itemStyle: { color: dashboardColors[series.tone] },
      lineStyle: { width: 1.75 }, areaStyle: { opacity: .075 },
    })),
  }
}

import type { DashboardComparison, DashboardMetricDelta } from './dashboard'

/** 变化标记的色彩倾向。 */
export type DeltaTone = 'bad' | 'good' | 'flat' | 'neutral'

/** 可直接展示的变化标记。 */
export type DeltaChip = {
  text: string
  tone: DeltaTone
  title: string
}

/**
 * 用途 把相对上期的真实变化格式化为可展示的标记。
 * 参数 delta 为后端按真实窗口计算的变化量，neutral 表示该指标上升并不代表恶化。
 * 返回值 展示文字、色彩倾向与悬停说明。
 */
export function periodChip(delta: DashboardMetricDelta | undefined, neutral = false): DeltaChip {
  if (!delta || delta.previousUnavailable || (delta.current === 0 && delta.previous === 0)) {
    return { text: '较上期 暂无', tone: 'flat', title: '当前与上期没有可用的对比数据' }
  }
  if (delta.change === 0) return { text: '较上期持平', tone: 'flat', title: '与上期窗口持平' }
  const arrow = delta.change > 0 ? '↑' : '↓'
  const magnitude = delta.rate === null ? String(Math.abs(delta.change)) : `${Math.abs(delta.rate)}%`
  const tone: DeltaTone = neutral ? 'neutral' : delta.worse ? 'bad' : 'good'
  const scope = delta.rate === null ? '上期为 0，按数量比较' : '按变化率比较'
  return {
    text: `较上期 ${arrow}${magnitude}`,
    tone,
    title: `当前 ${delta.current}，上期 ${delta.previous}，${scope}`,
  }
}

/**
 * 用途 把相对去年同期的真实变化格式化为可展示的标记。
 * 参数 delta 为后端按真实窗口计算的变化量，available 表示去年同期窗口是否有可用数据。
 * 返回值 展示文字、色彩倾向与悬停说明。
 */
export function yearChip(delta: DashboardMetricDelta | undefined, available: boolean): DeltaChip {
  if (!delta || !available || delta.yearAgoUnavailable || (delta.current === 0 && delta.yearAgo === 0)) {
    return { text: '同比 暂无', tone: 'flat', title: '去年同期窗口没有可用数据' }
  }
  if (delta.current === delta.yearAgo) return { text: '同比持平', tone: 'flat', title: '与去年同期持平' }
  const change = delta.current - delta.yearAgo
  const arrow = change > 0 ? '↑' : '↓'
  const rate = delta.yearAgo > 0
    ? `${Math.abs(Math.round(change * 100 / delta.yearAgo))}%`
    : String(Math.abs(change))
  return { text: `同比 ${arrow}${rate}`, tone: 'neutral', title: `当前 ${delta.current}，去年同期 ${delta.yearAgo}` }
}

/**
 * 用途 把整改完成率的百分点变化格式化为可展示的标记。
 * 参数 delta 为后端按真实窗口计算的变化量。
 * 返回值 展示文字、色彩倾向与悬停说明。
 */
export function completionChip(delta: DashboardComparison['completionRate'] | undefined): DeltaChip {
  if (!delta?.hasCurrent || !delta.hasPrevious) {
    return { text: '较上期 暂无', tone: 'flat', title: '整改完成率对比数据尚未就绪' }
  }
  if (delta.change === 0) return { text: '较上期持平', tone: 'flat', title: '与上期窗口持平' }
  const arrow = delta.change > 0 ? '↑' : '↓'
  return {
    text: `较上期 ${arrow}${Math.abs(delta.change)}pp`,
    tone: delta.change > 0 ? 'good' : 'bad',
    title: `当前 ${delta.current}%，上期 ${delta.previous}%`,
  }
}

/**
 * 用途 把完成率相对去年同期的变化格式化为可展示的标记。
 * 参数 delta 为后端按真实窗口计算的变化量。
 * 返回值 展示文字、色彩倾向与悬停说明。
 */
export function completionYearChip(
  delta: DashboardComparison['completionRate'] | undefined,
): DeltaChip {
  if (!delta?.hasCurrent || !delta.hasYearAgo) {
    return { text: '同比 暂无', tone: 'flat', title: '去年同期窗口没有可用数据' }
  }
  if (delta.current === delta.yearAgo) return { text: '同比持平', tone: 'flat', title: '与去年同期持平' }
  const change = delta.current - delta.yearAgo
  const arrow = change > 0 ? '↑' : '↓'
  return {
    text: `同比 ${arrow}${Math.abs(change)}pp`,
    tone: 'neutral',
    title: `当前 ${delta.current}%，去年同期 ${delta.yearAgo}%`,
  }
}

/**
 * 用途 把标记文案中的"较上期"替换为带窗口天数的说明。
 * 参数 chip 为原始标记，windowDays 为对比窗口天数。
 * 返回值 窗口说明落到文案里的标记。
 */
export function withWindowRange(chip: DeltaChip, windowDays: number): DeltaChip {
  return { ...chip, text: chip.text.replace('较上期', `较上期 ${windowDays} 日`) }
}

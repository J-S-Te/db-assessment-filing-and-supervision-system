import assert from 'node:assert/strict'
import { test } from 'node:test'
import * as metrics from '../src/lib/dashboardMetrics.ts'
const { dashboardTrend, metricPercent } = metrics

test('实际分母为零时不展示通过率，零分子保留真实 0%', () => {
  assert.equal(metricPercent(0, 0), undefined)
  assert.equal(metricPercent(undefined, 3), undefined)
  assert.equal(metricPercent(0, 3), 0)
  assert.equal(metricPercent(2, 3), 67)
})

test('预警流只突出刷新中新触发且仍活跃的高风险预警', () => {
  const at = '2026-11-25T10:00:00+08:00'
  const after = '2026-11-25T02:01:00Z'
  const older = '2026-11-25T01:59:00Z'
  const previous = [{ id: 'seen', severity: 'high', status: 'pending', triggeredAt: at }]
  const current = [
    { id: 'new', severity: 'high', status: 'processing', triggeredAt: after },
    { id: 'closed', severity: 'high', status: 'closed', triggeredAt: after },
    { id: 'medium', severity: 'medium', status: 'pending', triggeredAt: after },
    { id: 'old', severity: 'high', status: 'pending', triggeredAt: older },
    ...previous,
  ]
  assert.deepEqual(metrics.newHighAlertIds(undefined, current), [])
  assert.deepEqual(metrics.newHighAlertIds(previous, current), ['new'])
  assert.deepEqual(metrics.newHighAlertIds(current, current), [])
  assert.deepEqual(metrics.newHighAlertIds([], [current[0]]), ['new'])
})

test('环图使用实际数量，零值分类和全空样本不产生扇区', () => {
  const items = [
    { label: '有效', count: 3, tone: 'blue' },
    { label: '已注销', count: 0, tone: 'red' },
    { label: '草稿', count: 1, tone: 'slate' },
  ]
  const option = metrics.dashboardRingOption(items)
  assert.equal(option.series[0].silent, false)
  assert.deepEqual(option.series[0].emphasis, { focus: 'self', scale: true, scaleSize: 5 })
  assert.deepEqual(option.series[0].data.map(({ name, value }) => [name, value]), [
    ['有效', 3], ['草稿', 1],
  ])
  const empty = metrics.dashboardRingOption([{ label: '无记录', count: 0, tone: 'slate' }])
  assert.deepEqual(empty.series[0].data, [])
  assert.equal(empty.series[0].showEmptyCircle, true)
  assert.equal(empty.series[0].stillShowZeroSum, false)
})

test('趋势窗口保留空白日、整数纵轴和共享计量尺度', () => {
  const trend = [
    { date: '2026-11-17', filingsCreated: 800, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 },
    { date: '2026-11-18', filingsCreated: 0, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 },
    { date: '2026-11-19', filingsCreated: 13, reportsCreated: 0, alertsTriggered: 1, hazardsClosed: 0 },
    { date: '2026-11-20', filingsCreated: 0, reportsCreated: 9, alertsTriggered: 0, hazardsClosed: 1 },
    { date: '2026-11-21', filingsCreated: 0, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 },
    { date: '2026-11-22', filingsCreated: 0, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 },
    { date: '2026-11-23', filingsCreated: 0, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 },
    { date: '2026-11-24', filingsCreated: 1, reportsCreated: 0, alertsTriggered: 0, hazardsClosed: 0 },
  ]
  const chart = dashboardTrend(trend, 7)
  assert.equal(chart.days.length, 7)
  assert.equal(chart.days[0].date, '2026-11-18')
  assert.equal(chart.days[6].date, '2026-11-24')
  assert.deepEqual(chart.ticks, [0, 4, 8, 12, 16])
  assert.deepEqual(chart.totals, { filingsCreated: 14, reportsCreated: 9, alertsTriggered: 1, hazardsClosed: 1 })
  assert.deepEqual(dashboardTrend(trend.slice(1, 2), 7).ticks, [0, 1, 2, 3, 4])
  const option = metrics.dashboardTrendOption(chart)
  assert.deepEqual(option.xAxis.data, ['11-18', '11-19', '11-20', '11-21', '11-22', '11-23', '11-24'])
  assert.deepEqual(option.series[0].data, [0, 13, 0, 0, 0, 0, 1])
  assert.equal(option.yAxis.max, 16)
  assert.equal(option.tooltip.renderMode, 'richText')
  const filtered = metrics.dashboardTrendOption(chart, new Set(['reportsCreated', 'filingsCreated']))
  assert.deepEqual(filtered.series.map(series => series.name), ['触发预警', '隐患销项'])
  assert.deepEqual(filtered.series[0].data, [0, 1, 0, 0, 0, 0, 0])
})

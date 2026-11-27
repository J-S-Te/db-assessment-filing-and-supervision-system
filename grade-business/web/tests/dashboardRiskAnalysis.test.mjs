import assert from 'node:assert/strict'
import test from 'node:test'
import { activeRiskAlertCount, rankRiskIndustries, riskAlertSegments } from '../src/lib/dashboardRiskAnalysis.ts'

test('行业按未销项隐患总量排名，忽略没有隐患的分类', () => {
  const result = rankRiskIndustries({ industries: { items: [
    { code: 'a', label: '甲', systems: 40, high: 0, medium: 0, low: 0 },
    { code: 'b', label: '乙', systems: 1, high: 0, medium: 1, low: 0 },
    { code: 'c', label: '丙', systems: 1, high: 1, medium: 0, low: 2 },
  ] } })
  assert.deepEqual(result.map(({ label, total }) => [label, total]), [['丙', 3], ['乙', 1]])
})

test('预警环图保留实际状态数与大小，零量无扇区', () => {
  assert.deepEqual(riskAlertSegments(undefined), [])
  assert.deepEqual(riskAlertSegments({ total: 0, pending: 0, processing: 0, closed: 0 }), [])
  assert.deepEqual(riskAlertSegments({ total: 4, pending: 1, processing: 0, closed: 3 })
    .map(({ status, percent, start }) => [status, percent, start]), [
    ['pending', 25, 0], ['processing', 0, 25], ['closed', 75, 25],
  ])
})

test('没有预警记录时展示暂无，有已关闭预警时保留活跃为零', () => {
  assert.equal(activeRiskAlertCount(undefined), undefined)
  assert.equal(activeRiskAlertCount({ total: 0, pending: 0, processing: 0, closed: 0 }), undefined)
  assert.equal(activeRiskAlertCount({ total: 3, pending: 0, processing: 0, closed: 3 }), 0)
  assert.equal(activeRiskAlertCount({ total: 5, pending: 1, processing: 2, closed: 2 }), 3)
})

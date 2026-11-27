import assert from 'node:assert/strict'
import test from 'node:test'

import { completionYearChip } from './dashboardComparison.ts'

test('没有同期数据时，完成率不展示同比变化', () => {
  const delta = { current: 40, previous: 25, yearAgo: 0, change: 15, hasCurrent: true, hasPrevious: true }
  assert.equal(completionYearChip({ ...delta, hasYearAgo: false }).text, '同比 暂无')
  assert.equal(completionYearChip({ ...delta, hasYearAgo: true }).text, '同比 ↑40pp')
})

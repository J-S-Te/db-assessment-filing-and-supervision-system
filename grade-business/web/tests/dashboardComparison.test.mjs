import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  completionChip, completionYearChip, periodChip, withWindowRange, yearChip,
} from '../src/lib/dashboardComparison.ts'

const delta = (current, previous, yearAgo, rate, worse) => ({
  current, previous, yearAgo, change: current - previous, rate, worse,
})

test('环比按变化率展示，恶化着红色、变好着绿色', () => {
  const worse = periodChip(delta(12, 10, 15, 20, true))
  assert.equal(worse.text, '较上期 ↑20%')
  assert.equal(worse.tone, 'bad')
  const better = periodChip(delta(5, 6, 11, -17, false))
  assert.equal(better.text, '较上期 ↓17%')
  assert.equal(better.tone, 'good')
})

test('业务量型指标不按方向着色', () => {
  const chip = periodChip(delta(142, 128, 96, 11, false), true)
  assert.equal(chip.tone, 'neutral')
})

test('上期为 0 时改按数量比较并说明口径', () => {
  const chip = periodChip(delta(3, 0, 2, null, true))
  assert.equal(chip.text, '较上期 ↑3')
  assert.ok(chip.title.includes('上期为 0，按数量比较'))
})

test('与上期持平时不展示箭头', () => {
  assert.equal(periodChip(delta(7, 7, 9, 0, false)).text, '较上期持平')
  assert.equal(periodChip(undefined).text, '较上期 —')
})

test('同比按去年同期计算，去年同期无数据时不编造数字', () => {
  const chip = yearChip(delta(100, 80, 120, 25, true), true)
  assert.equal(chip.text, '同比 ↓17%')
  assert.equal(chip.tone, 'neutral')
  const missing = yearChip(delta(100, 80, 0, 25, true), false)
  assert.equal(missing.text, '同比 —')
  assert.ok(missing.title.includes('没有可用数据'))
})

test('整改完成率按百分点比较', () => {
  const chip = completionChip({ current: 77, previous: 72, yearAgo: 64, change: 5, hasCurrent: true })
  assert.equal(chip.text, '较上期 ↑5pp')
  assert.equal(chip.tone, 'good')
  const falling = completionChip({ current: 60, previous: 72, yearAgo: 64, change: -12, hasCurrent: true })
  assert.equal(falling.text, '较上期 ↓12pp')
  assert.equal(falling.tone, 'bad')
  assert.equal(completionYearChip({ current: 77, previous: 72, yearAgo: 64, change: 5, hasCurrent: true }, true).text,
    '同比 ↑13pp')
})

test('窗口天数落到环比文案中', () => {
  const chip = withWindowRange(periodChip(delta(12, 10, 15, 20, true)), 30)
  assert.equal(chip.text, '较上期 30 日 ↑20%')
})

import assert from 'node:assert/strict'
import { test } from 'node:test'
import { describeDeadline, workbenchTaskLink } from '../src/lib/workbenchItems.ts'

test('期限以北京时间的自然日展示，空值和非法日期不会显示为逾期', () => {
  const now = new Date('2026-11-24T16:15:00Z')
  assert.deepEqual(describeDeadline('2026-11-24', now), { text: '已逾期 1 天', urgency: 'overdue' })
  assert.deepEqual(describeDeadline('2026-11-25', now), { text: '今日到期', urgency: 'near' })
  assert.deepEqual(describeDeadline('2026-12-05', now), { text: '剩 10 天', urgency: 'near' })
  assert.deepEqual(describeDeadline('2026-12-06', now), { text: '剩 11 天', urgency: 'upcoming' })
  assert.deepEqual(describeDeadline('2026-02-30', now), { text: '未设时限' })
  assert.deepEqual(describeDeadline(undefined, now), { text: '未设时限' })
})

test('机构初审及运营者预警详情保留各自的范围和返回队列', () => {
  assert.deepEqual(workbenchTaskLink({ resource: 'filings', id: 'filing/a', status: 'pending_initial' }, {
    audience: 'institution', query: { institution: '检测机构 & 合伙' },
  }), {
    path: '/institution/filings/filing%2Fa', query: { institution: '检测机构 & 合伙', review: 'assigned' },
  })
  assert.deepEqual(workbenchTaskLink({ resource: 'alerts', id: 'alert-1', status: 'pending' }, {
    audience: 'operator', query: { organization: '某运营单位' },
  }), { path: '/operator/alerts', query: { organization: '某运营单位', alert: 'alert-1' } })
  assert.deepEqual(workbenchTaskLink({ resource: 'hazards', id: 'h-1', status: 'pending_retest' }, {
    audience: 'institution', query: { institution: '检测机构' },
  }), { path: '/institution/hazards/h-1', query: { institution: '检测机构' } })
})

test('现场跟进、监管复核和年度自查分别定位到正确办理区', () => {
  const site = { resource: 'supervision-site', id: 'ST/1' }
  const operator = { audience: 'operator', query: { organization: '原接收 & 单位' } }
  assert.deepEqual(workbenchTaskLink(site, operator), {
    path: '/operator/supervisions/ST%2F1', query: { organization: '原接收 & 单位', section: 'site' },
  })
  assert.deepEqual(workbenchTaskLink(site), {
    path: '/supervisions/ST%2F1', query: { section: 'site' },
  })
  assert.deepEqual(workbenchTaskLink({ ...site, resource: 'supervisions' }, operator), {
    path: '/operator/supervisions/ST%2F1', query: { organization: '原接收 & 单位' },
  })
})

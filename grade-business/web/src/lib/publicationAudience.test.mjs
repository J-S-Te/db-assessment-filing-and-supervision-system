import assert from 'node:assert/strict'
import { test } from 'node:test'
import * as audience from './publicationDelivery.ts'

test('单条公告回执保留收件范围和公告标识，批量已读使用同一范围', () => {
  assert.equal(
    typeof audience.recipientPublicationUrl,
    'function',
    '收件人请求必须通过同一范围构造规则',
  )
  assert.equal(
    audience.recipientPublicationUrl('institution', '甲 & 乙测评', 'notice/3', 'acknowledge'),
    '/institution/publications/notice%2F3/acknowledge?institution=%E7%94%B2+%26+%E4%B9%99%E6%B5%8B%E8%AF%84',
  )
  assert.equal(
    audience.recipientPublicationUrl('operator', '运营单位', undefined, 'read-all'),
    '/operator/publications/read-all?organization=%E8%BF%90%E8%90%A5%E5%8D%95%E4%BD%8D',
  )
})

test('定向发布只随指定范围发送接收对象，切换范围不会保留旧对象', () => {
  const content = {
    category: 'bulletin', title: '例行检查', publisher: '监管部门', body: '工作安排', pinned: false,
    audience: 'selected', targets: [{ audience: 'institution', recipient: '甲测评' }],
    alertId: 'old-alert',
  }
  assert.equal(typeof audience.publicationDraftPayload, 'function')
  assert.deepEqual(audience.publicationDraftPayload(content, 3), {
    category: 'bulletin', title: '例行检查', publisher: '监管部门', body: '工作安排', pinned: false,
    audience: 'selected', targets: [{ audience: 'institution', recipient: '甲测评' }], expectedVersion: 3,
  })
  assert.deepEqual(audience.publicationDraftPayload({ ...content, audience: 'operator' }), {
    category: 'bulletin', title: '例行检查', publisher: '监管部门', body: '工作安排', pinned: false,
    audience: 'operator',
  })
})

test('定向发布保留收件范围，不提交目录展示名称', () => {
  const content = {
    category: 'announcement', title: '工作通知', publisher: '监管部门', body: '内容', pinned: false,
    audience: 'selected', targets: [{ audience: 'operator', recipient: '原名称', displayName: '当前展示名称' }],
  }
  assert.deepEqual(audience.publicationDraftPayload(content).targets, [{ audience: 'operator', recipient: '原名称' }])
})

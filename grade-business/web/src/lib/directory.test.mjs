import assert from 'node:assert/strict'
import { test } from 'node:test'
import { directoryOptionPath, directoryOptionValue, directorySnapshotLabel } from './directory.ts'

test('更名后的机构保留原办理范围，人员使用稳定 UUID', () => {
  const item = { id: '8dfcdf09-99d6-436d-b578-088967c483a1', name: '现名', scopeName: '原办理名' }
  assert.equal(directoryOptionValue('institution', item), '原办理名')
  assert.equal(directoryOptionValue('person', item), item.id)
  assert.equal(directoryOptionValue('unit', { name: '存量单位' }), '存量单位')
})

test('测评人员查询携带当前机构范围并安全编码', () => {
  const url = new URL(directoryOptionPath('person', 'A + B', ' 测评员 '), 'http://localhost')
  assert.equal(url.pathname, '/directory/options')
  assert.deepEqual(Object.fromEntries(url.searchParams), { kind: 'person', institution: 'A + B', q: '测评员' })
})

test('业务归属使用审批时的名称快照，忽略人员证书等未展示字段', () => {
  const snapshot = {
    unit: { name: '备案单位' },
    institution: { name: '旧测评机构', scopeName: '原办理名' },
    person: { name: '测评员', certificateNo: 'PX-9901' },
  }
  assert.equal(directorySnapshotLabel(snapshot), '备案单位 · 旧测评机构 · 测评员')
  assert.equal(directorySnapshotLabel(undefined), '')
})

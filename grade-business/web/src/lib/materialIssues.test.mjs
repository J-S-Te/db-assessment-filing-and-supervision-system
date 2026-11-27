import assert from 'node:assert/strict'
import { test } from 'node:test'
import * as materials from './materialIssues.ts'

test('补正入口优先使用选中的异常，查看记录时保留业务归属', () => {
  assert.equal(materials.materialIssuePath('institution', '甲 & 乙', { id: 'issue', status: 'open' }),
    '/institution/material-issues?institution=%E7%94%B2+%26+%E4%B9%99&id=issue&status=all')
  assert.equal(materials.materialIssuePath('operator', '单位 甲', { status: 'closed', before: '41' }),
    '/operator/material-issues?organization=%E5%8D%95%E4%BD%8D+%E7%94%B2&status=closed&before=41')
  assert.equal(materials.materialIssuePath(undefined, '', { caseKind: 'report' }),
    '/material-issues?caseKind=report')
})

test('重发材料的表单只携带分类、当前选择的文件和异常标识', () => {
  const file = new File(['valid document'], '报告.pdf', { type: 'application/pdf' })
  const form = materials.retryMaterialForm('report_file', file, 'failed-1')
  assert.deepEqual([...form.keys()], ['kind', 'file', 'issueId'])
  assert.equal(form.get('kind'), 'report_file')
  assert.equal(form.get('issueId'), 'failed-1')
  assert.equal(form.get('file'), file)
})

test('通过校验的 ZIP 可以满足两个备案必传类别', () => {
  const required = ['filing_form', 'classification_report', 'supporting']
  assert.deepEqual(materials.missingCaseMaterials(required, [{ kind: 'filing_package' }]), ['supporting'])
  assert.deepEqual(materials.missingCaseMaterials(required, [{ kind: 'filing_form' }]),
    ['classification_report', 'supporting'])
})

test('上传保留服务器识别分类的请求头，草稿删除可处理无响应体', async (t) => {
  const { api } = await import('./api.ts')
  const fetch = t.mock.method(globalThis, 'fetch', async () => new Response(null, { status: 204 }))
  const file = new File(['material'], '报告.pdf')
  const form = materials.retryMaterialForm('report_file', file, 'failed-1')
  await api('/institution/reports/report-1/attachments?institution=company', {
    method: 'POST', body: form, materialKind: 'report_file',
  })
  const result = await api('/institution/reports/report-1?institution=company', {
    method: 'DELETE', body: { expectedVersion: 4 },
  })
  const [, upload] = fetch.mock.calls[0].arguments
  const [, remove] = fetch.mock.calls[1].arguments
  assert.equal(upload.headers.get('X-Material-Kind'), 'report_file')
  assert.equal(upload.headers.get('Content-Type'), null)
  assert.equal(upload.body, form)
  assert.equal(remove.method, 'DELETE')
  assert.equal(remove.headers.get('X-Material-Kind'), null)
  assert.equal(remove.body, '{"expectedVersion":4}')
  assert.equal(result, undefined)
})

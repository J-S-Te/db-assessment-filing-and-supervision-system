import assert from 'node:assert/strict'
import { test } from 'node:test'
import { ApiError, api, isAbort, params } from '../src/lib/api.ts'

test('只发送当前动作申明的字段，业务请求同源', async (t) => {
  const fetch = t.mock.method(globalThis, 'fetch', async () =>
    Response.json({ id: 'f-1', status: 'pending_initial', version: 2 }),
  )
  assert.equal((await api('/filings/f-1/submit', {
    method: 'POST',
    body: { expectedVersion: 1 },
  })).version, 2)
  const [url, request] = fetch.mock.calls[0].arguments
  assert.equal(url, '/api/v1/filings/f-1/submit')
  assert.equal(request.body, '{"expectedVersion":1}')
  assert.equal(request.headers.get('Content-Type'), 'application/json')
})

test('状态冲突保留后端业务码与提示', async (t) => {
  t.mock.method(globalThis, 'fetch', async () =>
    Response.json({ code: 'stale', message: '备案已更新' }, { status: 409 }),
  )
  await assert.rejects(api('/filings/f-1'), (error) =>
    error instanceof ApiError && error.status === 409 && error.code === 'stale'
      && error.message === '备案已更新',
  )
})

test('省略空筛选字段并区分已取消请求', () => {
  assert.equal(params({ q: '', status: undefined, limit: 20 }), '?limit=20')
  assert.equal(isAbort(new DOMException('aborted', 'AbortError')), true)
  assert.equal(isAbort(new Error('failed')), false)
})

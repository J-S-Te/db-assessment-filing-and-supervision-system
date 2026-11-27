import assert from 'node:assert/strict'
import { test } from 'node:test'
import * as publication from './publicationDelivery.ts'

test('公文附件下载目标固定公告、版本及收件范围', () => {
  assert.equal(typeof publication.publicationAttachmentDownload, 'function')
  assert.deepEqual(publication.publicationAttachmentDownload('notice/1', 7), {
    recordPath: '/publications/notice%2F1', query: '?version=7',
  })
  assert.deepEqual(publication.publicationAttachmentDownload('notice/1', 5, {
    audience: 'institution', recipient: '甲 & 乙测评',
  }), {
    recordPath: '/institution/publications/notice%2F1',
    query: '?version=5&institution=%E7%94%B2+%26+%E4%B9%99%E6%B5%8B%E8%AF%84',
  })
  assert.deepEqual(publication.publicationAttachmentDownload('notice/1', 6, {
    audience: 'operator', recipient: '运营单位',
  }), {
    recordPath: '/operator/publications/notice%2F1',
    query: '?version=6&organization=%E8%BF%90%E8%90%A5%E5%8D%95%E4%BD%8D',
  })
  assert.equal(publication.publicationAttachmentDownload('notice/1', 6, {
    audience: 'operator', recipient: '  ',
  }), undefined)
  assert.equal(publication.publicationAttachmentDownload('notice/1', 0), undefined)
})

test('公文附件批量提交只包含预期版本和多个文件字段', () => {
  assert.equal(typeof publication.publicationAttachmentForm, 'function')
  const pdf = new File(['pdf'], '通知.pdf')
  const word = new File(['word'], '模板.docx')
  const body = publication.publicationAttachmentForm(2, [pdf, word])
  assert.deepEqual([...body.keys()], ['expectedVersion', 'files', 'files'])
  assert.equal(body.get('expectedVersion'), '2')
  assert.deepEqual(body.getAll('files').map(file => file.name), ['通知.pdf', '模板.docx'])
})

test('选择公文文件时验证格式、空文件、数量和体积', () => {
  assert.equal(typeof publication.publicationAttachmentSelectionError, 'function')
  const selected = (...suffixes) => suffixes.map(suffix => ({ name: '公文.' + suffix, size: 1 }))
  assert.equal(publication.publicationAttachmentSelectionError(selected('PDF', 'docx')), '')
  assert.match(publication.publicationAttachmentSelectionError(selected('doc')), /PDF.*DOCX/)
  assert.match(publication.publicationAttachmentSelectionError([{ name: '空.pdf', size: 0 }]), /空/)
  assert.match(publication.publicationAttachmentSelectionError(Array(9).fill(selected('pdf')[0])), /8/)
  const big = { name: '公文.pdf', size: 20 * 1024 ** 2 }
  assert.match(publication.publicationAttachmentSelectionError([{ ...big, size: big.size + 1 }]), /20/)
  assert.match(publication.publicationAttachmentSelectionError(Array(3).fill(big)), /40/)
})

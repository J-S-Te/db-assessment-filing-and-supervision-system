import assert from 'node:assert/strict'
import { test } from 'node:test'
import { wordEvidenceTarget, wordPreviewParagraph } from './filingReviewEvidence.ts'

test('按附件标识和归档成员完整路径定位同名 Word 的原文段号', () => {
  const first = { id: 'a', fileName: '材料.zip', kind: 'filing_package' }
  const chosen = { ...first, id: 'b' }
  assert.deepEqual(wordEvidenceTarget({ attachmentId: 'b', fileName: '材料.zip / 定稿/备案表.docx',
    location: '第 5 段', quote: '系统名称：平台' }, [first, chosen]),
  { attachment: chosen, member: '定稿/备案表.docx', paragraph: 5 })
})

test('仅使用解析器返回的原始段号，图片、缺失和跳过的段落不产生定位', () => {
  const source = { id: 'word', fileName: '材料.DOC', kind: 'filing_form' }
  const evidence = { attachmentId: source.id, fileName: source.fileName, location: '第 2 段' }
  assert.equal(wordEvidenceTarget({ ...evidence, kind: 'image' }, [source])?.paragraph, undefined)
  assert.equal(wordEvidenceTarget({ ...evidence, attachmentId: 'removed' }, [source]), undefined)
  const document = { sections: [{ title: '一、单位情况', titleParagraph: 2,
    paragraphs: ['系统名称：平台', '系统名称：平台'], paragraphNumbers: [3, 5] }] }
  assert.equal(wordPreviewParagraph(document, 2), '一、单位情况')
  assert.equal(wordPreviewParagraph(document, 4), undefined)
  assert.equal(wordPreviewParagraph(document, 5), '系统名称：平台')
})

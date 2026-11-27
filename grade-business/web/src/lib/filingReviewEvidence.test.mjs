import assert from 'node:assert/strict'
import { test } from 'node:test'
import { reviewRows, wordEvidenceTarget, wordPreviewParagraph } from './filingReviewEvidence.ts'

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

test('切换附件只显示当前附件的审核依据，并保留适用的无出处规则', () => {
  const form = { id: 'form', fileName: '材料.docx', kind: 'filing_form' }
  const report = { ...form, id: 'report', kind: 'classification_report' }
  const items = [{ ruleId: 'object_number' }, { ruleId: 'assets' }, { ruleId: 'protection_level' }]
  const formEvidence = { attachmentId: 'form', fileName: form.fileName, location: '第 2 段', quote: '备案表级别' }
  const reportEvidence = { ...formEvidence, attachmentId: 'report', quote: '定级报告级别' }
  const checks = new Map([
    ['assets', { evidence: [reportEvidence] }],
    ['protection_level', { evidence: [reportEvidence, formEvidence] }],
  ])
  const selected = reviewRows(items, checks, form)
  assert.deepEqual(selected.map(row => row.item.ruleId), ['object_number', 'protection_level'])
  assert.deepEqual(selected[1].citations, [{ evidence: formEvidence, index: 1 }])
  assert.deepEqual(reviewRows(items, checks, report).map(row => row.item.ruleId), ['assets', 'protection_level'])
  assert.equal(reviewRows(items, checks).length, 3)
})

test('证据打开归档成员后按完整路径收窄批注', () => {
  const archive = { id: 'archive', fileName: '材料包.zip', kind: 'filing_package' }
  const item = { ruleId: 'seals' }
  const first = { attachmentId: archive.id, fileName: '材料包.zip / 草稿/备案表.docx' }
  const second = { ...first, fileName: '材料包.zip / 定稿/备案表.docx' }
  const checks = new Map([['seals', { evidence: [first, second] }]])
  assert.equal(reviewRows([item], checks, archive)[0].citations.length, 2)
  assert.deepEqual(reviewRows([item], checks, archive, '定稿/备案表.docx')[0].citations,
    [{ evidence: second, index: 1 }])
})

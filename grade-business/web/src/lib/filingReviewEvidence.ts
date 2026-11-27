import type { Attachment, FilingRuleEvidence } from './domain'

export interface FilingTextDocument {
  fileName: string
  member?: string
  category: string
  sections: Array<{
    title: string
    titleParagraph?: number
    paragraphs: string[]
    paragraphNumbers?: number[]
  }>
  truncated?: boolean
  message?: string
}

export interface WordEvidenceTarget {
  attachment: Attachment
  member?: string
  paragraph?: number
}

/**
 * 用途 判断所选材料是否支持现有 Word 文字预览。
 * 参数 attachment 为备案附件，member 为可选的备案包成员路径。
 * 返回值 可尝试解析时为真。
 */
export function canPreviewWord(attachment: Attachment, member?: string) {
  return member ? /\.docx?$/i.test(member) : /\.docx?$/i.test(attachment.fileName) ||
    attachment.kind === 'filing_package' && /\.zip$/i.test(attachment.fileName)
}

/**
 * 用途 将已有审核依据映射到准确的附件、归档成员和原文段号。
 * 参数 evidence 为审核依据，attachments 为当前有权读取的附件列表。
 * 返回值 真实出处，缺少对应附件时为空。
 */
export function wordEvidenceTarget(evidence: FilingRuleEvidence, attachments: Attachment[]): WordEvidenceTarget | undefined {
  const attachment = attachments.find(item => item.id === evidence.attachmentId)
  if (!attachment) return
  const prefix = attachment.fileName + ' / '
  const member = attachment.kind === 'filing_package' && evidence.fileName.startsWith(prefix)
    ? evidence.fileName.slice(prefix.length) || undefined : undefined
  const found = /^第\s*([1-9]\d*)\s*段$/.exec(evidence.location)
  const paragraph = found && !evidence.kind && (attachment.kind !== 'filing_package' || member) &&
    canPreviewWord(attachment, member) ? Number(found[1]) : undefined
  return { attachment, member, paragraph }
}

/**
 * 用途 判断指定原始段号是否存在于本次实际返回的文字预览。
 * 参数 document 为已加载的 Word，number 为原件的解析段号。
 * 返回值 标题或段落文字，缺失或超出预览范围时为空。
 */
export function wordPreviewParagraph(document: FilingTextDocument, number: number) {
  for (const section of document.sections) {
    if (section.titleParagraph === number) return section.title
    const index = section.paragraphNumbers?.indexOf(number) ?? -1
    if (index >= 0) return section.paragraphs[index]
  }
}

import type { PublicationContent } from './publications'
import type { BusinessAudience } from './scopedBusiness'

export const institutionPublicationPath = '/institution/publications'
export const operatorPublicationPath = '/operator/publications'
export const recipientLabels = { operator: '运营单位', institution: '测评机构' } as const
export const publicationAudiences = {
  all: '全部单位与机构', operator: '仅运营单位', institution: '仅测评机构', selected: '指定单位与机构',
} as const

export type PublicationAudience = keyof typeof publicationAudiences
export type PublicationReceiptAction = 'read' | 'acknowledge'

export interface PublicationTarget {
  audience: BusinessAudience
  recipient: string
  displayName?: string
}

export interface PublicationAttachment {
  id: string
  fileName: string
  size: number
  uploadedAt: string
}

export interface PublicationReceipt {
  deliveredAt?: string
  readAt?: string
  acknowledgedAt?: string
}

export interface PublicationDelivery extends PublicationTarget {
  deliveredAt: string
  readAt?: string
  acknowledgedAt?: string
}

export interface PublicationDeliveries {
  version: number
  counts: { delivered: number; read: number; acknowledged: number }
  items: PublicationDelivery[]
  nextBefore?: string
  attachments?: PublicationAttachment[]
}

/**
 * 用途 读取门户公告的查询字段，保证签收和附件下载使用同一收件范围。
 * 参数 audience 为当前办理端。
 * 返回值 URL 中收件范围的字段名。
 */
function publicationScopeField(audience: BusinessAudience) {
  return audience === 'institution' ? 'institution' : 'organization'
}

/**
 * 用途 生成收件端公告详情、已读或回执请求的同源地址。
 * 参数 audience 为办理端，scope 为收件单位或机构，id 为可选公告标识，action 为可选操作。
 * 返回值 包含收件范围的相对接口地址。
 */
export function recipientPublicationUrl(
  audience: BusinessAudience, scope: string, id?: string,
  action?: PublicationReceiptAction | 'read-all' | 'unread-count',
) {
  const path = audience === 'institution' ? institutionPublicationPath : operatorPublicationPath
  const record = id ? '/' + encodeURIComponent(id) : ''
  const suffix = action ? '/' + action : ''
  const query = scope ? new URLSearchParams({
    [publicationScopeField(audience)]: scope,
  }).toString() : ''
  return path + record + suffix + (query ? '?' + query : '')
}

/**
 * 用途 固定公文附件下载所用的公告版本和实际收件范围。
 * 参数 id 与 version 为公告标识及明确版本，target 为门户收件对象，监管端省略。
 * 返回值 供同源附件链接使用的记录路径与查询，范围或版本未就绪时为空。
 */
export function publicationAttachmentDownload(id: string, version: number, target?: PublicationTarget) {
  if (!id || !Number.isSafeInteger(version) || version < 1 || (target && !target.recipient.trim())) return
  const query = new URLSearchParams({ version: String(version) })
  const recordPath = target ? recipientPublicationUrl(target.audience, '', id)
    : '/publications/' + encodeURIComponent(id)
  if (target) query.set(publicationScopeField(target.audience), target.recipient.trim())
  return { recordPath, query: '?' + query.toString() }
}

/**
 * 用途 生成使用同一公告版本校验的原子批量上传表单。
 * 参数 version 为监管端正在编辑的版本，files 为浏览器选中的公文文件。
 * 返回值 只包含预期版本和多个文件部分的表单。
 */
export function publicationAttachmentForm(version: number, files: readonly File[]) {
  const form = new FormData()
  form.append('expectedVersion', String(version))
  for (const file of files) form.append('files', file)
  return form
}

/**
 * 用途 在发起网络请求前提示常见选件错误。
 * 参数 files 为本批文件名称和大小，已留存文件及精确去重由服务端校验。
 * 返回值 通过时返回空字符串，失败时返回界面提示。
 */
export function publicationAttachmentSelectionError(files: readonly Pick<File, 'name' | 'size'>[]) {
  const mib = 1024 ** 2
  if (files.length > 8) return '一次最多选择 8 份公文附件。'
  if (files.some(file => !/\.(pdf|docx)$/i.test(file.name))) return '公文仅支持 PDF 或 DOCX 格式。'
  if (files.some(file => file.size === 0)) return '请选择非空的公文附件。'
  if (files.some(file => file.size > 20 * mib)) return '单份公文附件不能超过 20 MB。'
  if (files.reduce((total, file) => total + file.size, 0) > 40 * mib) return '本批公文附件合计不能超过 40 MB。'
  return ''
}

/**
 * 用途 根据当前发布范围和类别生成草稿写入数据。
 * 参数 content 为编辑表单，expectedVersion 为编辑既有记录时的原版本。
 * 返回值 仅携带当前范围及类别使用的字段的请求体。
 */
export function publicationDraftPayload(content: PublicationContent, expectedVersion?: number) {
  const { audience, targets, alertId, ...fields } = content
  return {
    ...fields,
    ...(audience ? { audience } : {}),
    ...(audience === 'selected' ? {
      targets: targets?.map(({ audience, recipient }) => ({ audience, recipient })),
    } : {}),
    ...(fields.category === 'security_advisory' && alertId ? { alertId } : {}),
    ...(expectedVersion !== undefined ? { expectedVersion } : {}),
  }
}

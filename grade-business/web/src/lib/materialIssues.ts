import type { BusinessAudience } from './scopedBusiness'

export type MaterialIssueStatus = 'open' | 'closed'
export type MaterialCaseKind = 'filing' | 'report'

export interface MaterialIssue {
  id: string
  caseId?: string
  caseKind: MaterialCaseKind
  reference: string
  systemName: string
  organizationName?: string
  institution?: string
  materialKind: string
  fileName?: string
  reasonCode: string
  reason: string
  status: MaterialIssueStatus
  resolution?: 'corrected' | 'discarded'
  openedAt: string
  closedAt?: string
  caseVersion?: number
  canRetry?: boolean
  canDelete?: boolean
}

export interface MaterialIssueFilters {
  id?: string
  status?: MaterialIssueStatus | 'all'
  caseKind?: MaterialCaseKind
  before?: string
}

export type MaterialIssueAudience = BusinessAudience | undefined

export const filingPackageKind = 'filing_package'
export const materialRecoveryErrors: ReadonlySet<string> = new Set([
  'attachment_type', 'attachment_size', 'attachment_interrupted', 'filing_package', 'material_issues_open',
])
export const materialIssueLabels: Readonly<Record<string, string>> = {
  open: '待补正',
  closed: '已关闭',
  material_failed: '材料异常',
  material_closed: '材料异常已关闭',
}
export const materialIssueResolutions = {
  corrected: '材料已补正',
  discarded: '草稿已删除',
}

/**
 * 用途 判断当前类别是否允许上传旧版 Word 备案材料。
 * 参数 kind 为表单材料类别。
 * 返回值 备案表和定级报告类别为真。
 */
export function isFilingWordKind(kind: string) {
  return kind === 'filing_form' || kind === 'classification_report'
}

/**
 * 用途 判断已登记附件是否有备案文字预览入口。
 * 参数 file 为附件的业务类别和文件名。
 * 返回值 已登记的备案 Word 或备案 ZIP 为真。
 */
export function canPreviewFilingWord(file: { kind: string; fileName: string }) {
  return file.kind === filingPackageKind
    ? /\.zip$/i.test(file.fileName)
    : isFilingWordKind(file.kind) && /\.docx?$/i.test(file.fileName)
}

/**
 * 用途 生成材料异常的同源查询并保留收件范围，按异常标识查询时兼容已关闭记录。
 * 参数 audience 为收件端，scope 为机构或运营单位，filters 为筛选、游标或选中异常。
 * 返回值 仅包含服务端识别参数的接口地址。
 */
export function materialIssuePath(audience: MaterialIssueAudience, scope: string, filters: MaterialIssueFilters) {
  const query = new URLSearchParams()
  if (audience && scope) query.set(audience === 'institution' ? 'institution' : 'organization', scope)
  if (filters.id) {
    query.set('id', filters.id)
    query.set('status', 'all')
  } else {
    if (filters.status) query.set('status', filters.status)
    if (filters.caseKind) query.set('caseKind', filters.caseKind)
    if (filters.before) query.set('before', filters.before)
  }
  return (audience ? '/' + audience : '') + '/material-issues' + (query.size ? '?' + query.toString() : '')
}

/**
 * 用途 绑定待重发的异常和本次重新选择的材料。
 * 参数 kind 为异常对应的材料类别，file 为用户重新选择的文件，id 为异常记录标识。
 * 返回值 仅包含材料类别、文件及异常标识的表单。
 */
export function retryMaterialForm(kind: string, file: File, id: string) {
  const form = new FormData()
  form.append('kind', kind)
  form.append('file', file)
  form.append('issueId', id)
  return form
}

/**
 * 用途 查出仍未上传的必传材料，已校验的备案 ZIP 可同时满足两类备案材料。
 * 参数 required 为当前业务必传类别，files 为服务端已登记的附件。
 * 返回值 按业务配置顺序排列的缺失类别。
 */
export function missingCaseMaterials(required: readonly string[], files: readonly { kind: string }[]) {
  const uploaded = new Set(files.map(file => file.kind))
  if (uploaded.has(filingPackageKind)) {
    uploaded.add('filing_form')
    uploaded.add('classification_report')
  }
  return required.filter(kind => !uploaded.has(kind))
}

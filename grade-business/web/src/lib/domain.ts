import type { DirectorySnapshot } from './directory'
import type { ClassificationAssessment } from './classification'
import type { FilingMaterialRule } from './filingPolicy'

export type CaseKind = 'filings' | 'reports'
export type Status = 'draft' | 'pending_initial' | 'pending_final' | 'approved' | 'returned' | 'withdrawn'
export type Risk = 'low' | 'medium' | 'high'
export type Category = 'regulation' | 'standard' | 'vulnerability' | 'case' | 'template'
export type ReviewAction = 'approve' | 'return_for_correction'
export type CaseAction = ReviewAction | 'edit' | 'submit'

export interface CaseSummary {
  id: string
  number: string
  systemName: string
  organizationName: string
  assessmentOrg: string
  status: Status
  level?: string
  risk?: Risk
  filingNumber?: string
  submittedAt?: string
  cancelledAt?: string
  initialReviewAudience?: 'institution'
}

export interface CaseDetail extends CaseSummary {
  classificationAssessment?: ClassificationAssessment
  policyMaterials?: FilingMaterialRule[]
  version: number
  allowedActions: CaseAction[]
  filingId?: string
  requiredMaterials?: string[]
  receiptAvailable?: boolean
  assessmentPersonId?: string
  directorySnapshot?: DirectorySnapshot
}

export interface CaseMutation {
  id: string
  status: Status
  version: number
}

export interface Attachment {
  id: string
  kind: string
  fileName: string
  size: number
  uploadedAt: string
}

export interface Review {
  action: string
  fromStatus: Status
  toStatus: Status
  operator: string
  comment?: string
  occurredAt: string
  directorySnapshot?: DirectorySnapshot
}

export interface RuleFinding {
  ruleId: string
  message: string
  attachmentId?: string
}

export interface Precheck {
  id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  stale: boolean
  findings?: RuleFinding[]
  checks?: FilingRuleCheck[]
  checkSource?: 'deepseek' | 'local_rules'
  fallbackReason?: string
  failure?: string
  progressStage?: 'materials' | 'deepseek' | 'local_rules' | 'fallback'
  recommendation?: {
    action: 'correct' | 'manual_review' | 'pass'
    missingMaterials?: string[]
    failedChecks?: string[]
    manualChecks?: string[]
    incomplete?: boolean
  }
}

export interface FilingRuleEvidence {
  attachmentId: string
  fileName: string
  location: string
  quote: string
  kind?: 'image'
}

export interface FilingRuleCheck {
  ruleId: string
  status: 'pass' | 'fail' | 'manual' | 'not_applicable'
  message: string
  evidence?: FilingRuleEvidence[]
}

export type FilingReviewStatus = 'pending' | 'pass' | 'fail'

export interface FilingRuleReview {
  ruleId: string
  status: FilingReviewStatus
  remark: string
  version: number
  reviewer: string
  reviewedAt: string
}

export interface FilingReviewItem {
  ruleId: string
  title: string
  method: string
  review?: FilingRuleReview
}

export interface FilingMaterialReview {
  materialRevision: number
  items: FilingReviewItem[]
}

export interface KnowledgeSummary {
  id: string
  title: string
  category: Category
  source: string
  updatedAt: string
  tags: string[]
}

export interface KnowledgeDetail extends KnowledgeSummary {
  body: string
  version: number
}

export interface Page<T> {
  items: T[]
  nextCursor?: string
}

export const statuses: Record<Status, string> = {
  draft: '草稿',
  pending_initial: '待初审',
  pending_final: '待终审',
  approved: '已通过',
  returned: '退回补正',
  withdrawn: '已撤回',
}

export const risks: Record<Risk, string> = { low: '低风险', medium: '中风险', high: '高风险' }
export const levels: Record<string, string> = {
  '1': '第一级', '2': '第二级', '3': '第三级', '4': '第四级', '5': '第五级',
}
export const categories: Record<Category, string> = {
  regulation: '法规政策',
  standard: '标准规范',
  vulnerability: '漏洞信息',
  case: '案例参考',
  template: '材料模板',
}
export const caseLabels: Record<CaseKind, { module: string; record: string; number: string }> = {
  filings: { module: '备案管理', record: '备案', number: '备案编号' },
  reports: { module: '测评管理', record: '测评报告', number: '报告编号' },
}
export const materials: Record<CaseKind, Record<string, string>> = {
  filings: { filing_form: '备案表', classification_report: '定级报告', supporting: '其他材料', filing_package: '备案材料包（ZIP）' },
  reports: { report_file: '测评报告', supporting: '配套材料' },
}
export const reportSubmissionMaterials: Record<string, string> = {
  report_file: '报告文件',
  remediation_plan: '整改规划',
  assessment_conclusion: '等级测评结论',
  issues_recommendations: '主要安全问题及整改建议',
  cover_basic_statement: '报告封面、基本信息表和声明',
  cloud_extension: '测评结论扩展表（云计算）',
  big_data_extension: '测评结论扩展表（大数据）',
  major_risk_recommendations: '重大风险隐患整改建议',
  supporting: '其他文件',
}
export const knownActions: Record<string, string> = {
  submit: '提交审核',
  approve: '审核通过',
  return_for_correction: '退回补正',
  withdraw: '确认撤回',
  create: '新建草稿',
  edit: '编辑草稿',
}

const moment = new Intl.DateTimeFormat('zh-CN', {
  dateStyle: 'medium', timeStyle: 'short', timeZone: 'Asia/Shanghai',
})

/**
 * 用途 将服务端时间转换为监管工作台使用的本地时间。
 * 参数 timestamp 为接口返回的时间戳。
 * 返回值 可读时间，缺失或无效时返回占位符。
 */
export function formatTime(timestamp?: string): string {
  if (!timestamp) return '—'
  const date = new Date(timestamp)
  return Number.isNaN(date.valueOf()) ? '—' : moment.format(date)
}

/**
 * 用途 为页面显示材料文件大小。
 * 参数 bytes 为附件字节数。
 * 返回值 带适当单位的可读大小。
 */
export function formatSize(bytes: number): string {
  if (bytes >= 1_048_576) return (bytes / 1_048_576).toFixed(1) + ' MB'
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return bytes + ' B'
}

/**
 * 用途 区分运营者备案流程中两级审核的实际办理方。
 * 参数 status 为当前审核状态，audience 为服务端分派的初审端。
 * 返回值 涉及机构初审时的状态标签，其余状态沿用已有文案。
 */
export function filingReviewLabel(status: string, audience?: 'institution') {
  if (audience === 'institution' && status === 'pending_initial') return '待监管初审'
  if (audience === 'institution' && status === 'pending_final') return '待监管终审'
}

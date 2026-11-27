import type { Risk } from './domain'

export type RemediationStatus = 'draft' | 'pending_review' | 'approved' | 'returned' | 'withdrawn'
export type RemediationDecision = 'approve' | 'return_for_correction'
export type RemediationAction = 'edit' | 'submit' | 'change_report' | 'withdraw' | RemediationDecision

export interface RemediationRisk {
  risk: Risk | ''
  description: string
  measure: string
  responsibility: string
  deadline: string
}

export interface RemediationSummary {
  id: string
  reportId: string
  reportNumber: string
  filingNumber: string
  systemName: string
  organizationName: string
  deadline: string
  status: RemediationStatus
  submission: number
  submittedAt?: string
  riskCounts: Record<Risk, number>
}

export interface RemediationDetail extends RemediationSummary {
  version: number
  allowedActions: RemediationAction[]
  risks: RemediationRisk[]
  receiptAvailable?: boolean
}

export interface RemediationMutation {
  id: string
  status: RemediationStatus
  version: number
}

export interface EligibleReport {
  id: string
  number: string
  filingNumber: string
  systemName: string
  organizationName: string
}

export interface RemediationStatistics {
  pending: number
  returned: number
  dueWithin10Days: number
  firstPassRate: number | null
}

export interface RemediationReview {
  action: 'submit' | 'withdraw' | RemediationDecision
  fromStatus: RemediationStatus
  toStatus: RemediationStatus
  operator: string
  comment?: string
  reason?: string
  deadline: string
  submission: number
  occurredAt: string
}

export const remediationPath = '/remediations'
export const operatorRemediationPath = '/operator/remediations'
export const remediationStatuses: Record<RemediationStatus, string> = {
  draft: '草稿',
  pending_review: '待审核',
  approved: '已通过',
  returned: '退回补正',
  withdrawn: '已撤回',
}
export const remediationMaterials: Record<string, string> = {
  plan: '整改规划方案', responsibility: '整改责任分工表', schedule: '整改实施计划表',
  high_risk_evidence: '高风险项整改证据（可选）',
}
export const remediationRequiredMaterials = ['plan', 'responsibility', 'schedule'] as const
export const remediationDue = {
  overdue: '已逾期',
  within_10_days: '10 日内到期',
  within_30_days: '30 日内到期',
}

/**
 * 用途 生成配置有整改唯一标识的同源业务地址。
 * 参数 id 为整改规划标识，base 为办理端的集合地址。
 * 返回值 计划详情根路径。
 */
export function remediationRecordPath(id: string, base = remediationPath) {
  return base + '/' + encodeURIComponent(id)
}

/**
 * 用途 复用审核状态的颜色并提供整改待审核样式。
 * 参数 status 为整改审核状态。
 * 返回值 全局状态标签颜色类。
 */
export function remediationBadge(status: RemediationStatus) {
  if (status === 'pending_review') return 'status-pending_initial'
  return status === 'withdrawn' ? 'status-draft' : 'status-' + status
}

/**
 * 用途 为可关联的测评报告生成下拉选项名称。
 * 参数 report 为当前报告简表信息。
 * 返回值 便于区分报告的编号、系统、单位及备案信息。
 */
export function eligibleName(report: EligibleReport) {
  return report.number + ' · ' + report.systemName + ' · ' + report.organizationName
    + '（' + report.filingNumber + '）'
}

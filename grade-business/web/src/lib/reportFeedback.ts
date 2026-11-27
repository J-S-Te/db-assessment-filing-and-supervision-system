import type { Attachment, Risk, Status } from './domain'
import type { DirectorySnapshot } from './directory'

export const feedbackPath = '/institution/report-feedback'
export const feedbackDecisions = { returned: '退回补正', approved: '终审通过' }

export interface FeedbackReceipt {
  acknowledgedAt: string
}

export interface FeedbackMessage {
  id: string
  reportId: string
  number: string
  systemName: string
  decision: keyof typeof feedbackDecisions
  comment?: string
  status?: Status
  sentAt: string
  acknowledgedAt?: string
}

export interface FeedbackDelivery {
  institution: string
  decision: keyof typeof feedbackDecisions
  sentAt: string
  acknowledgedAt?: string
}

export interface InstitutionReport {
  id: string
  number: string
  systemName: string
  risk: Risk
  status: Status
  version: number
  canCorrect: boolean
  allowedActions?: Array<'edit' | 'submit' | 'delete'>
  requiredMaterials?: string[]
  returnComment?: string
  attachments: Attachment[]
  assessmentPersonId?: string
  directorySnapshot?: DirectorySnapshot
}

/**
 * 用途 生成机构办理页或同源报告接口路径。
 * 参数 id 为受机构范围约束的报告标识。
 * 返回值 不含机构查询参数的路径。
 */
export function institutionReportPath(id: string) {
  return '/institution/reports/' + encodeURIComponent(id)
}

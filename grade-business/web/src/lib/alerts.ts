import type { Risk } from './domain'
import type { HazardStatus } from './hazards'

export const alertPath = '/alerts'
export const operatorAlertPath = '/operator/alerts'
export const institutionAlertPath = '/institution/alerts'

export const alertStatuses = { pending: '待处理', processing: '处理中', closed: '已关闭' } as const
export const alertTypes = { high_risk: '高风险隐患待整改', overdue: '隐患整改逾期' } as const
export const alertActions = {
  create: '生成预警', process: '开始处理', close: '关闭预警',
  convert: '转入整改', request_extension: '申请延期', report_false_alarm: '反馈误报',
  submit_feedback: '提交处置反馈',
  approve: '通过申请', reject: '驳回申请',
} as const
export const alertBadgeStyles = { pending: 'status-pending_final', processing: 'status-pending_initial', closed: 'status-approved' }
export const alertDispositionStatuses = {
  completed: '已转入整改', pending: '待审核', approved: '已通过', rejected: '已驳回',
} as const
export const alertDispositionStyles = {
  completed: 'status-approved', pending: 'status-pending_final',
  approved: 'status-approved', rejected: 'status-returned',
} as const
export const alertActionNotices = {
  process: '预警已进入处理中。', close: '预警已关闭。', convert: '预警已关联现有整改单。',
  request_extension: '延期申请已提交，等待审核。', report_false_alarm: '误报反馈已提交，等待审核。',
  submit_feedback: '处置反馈已提交，等待监管审核。',
  approve: '申请已通过。', reject: '申请已驳回。',
} as const

export type AlertStatus = keyof typeof alertStatuses
export type AlertAction = Exclude<keyof typeof alertActions, 'create'>
type AlertDispositionAction = 'convert' | 'request_extension' | 'report_false_alarm' | 'submit_feedback'

export interface AlertDisposition {
  id: string
  action: AlertDispositionAction
  status: keyof typeof alertDispositionStatuses
  comment?: string
  requestedDeadline?: string
  requestedBy: string
  requesterAudience?: 'institution' | 'operator'
  requestedAt: string
  decisionComment?: string
  decidedBy?: string
  decidedAt?: string
}

export interface AlertActionInput {
  action: AlertAction
  expectedVersion: number
  comment?: string
  requestedDeadline?: string
}

export interface AlertItem {
  id: string
  hazardId: string
  type: keyof typeof alertTypes
  severity: Risk
  systemName: string
  organizationName: string
  deadline: string
  triggeredDeadline?: string
  currentlyOverdue?: boolean
  status: AlertStatus
  triggeredAt: string
  closedAt?: string
}

export interface AlertDetail extends AlertItem {
  version: number
  hazardStatus: HazardStatus
  description: string
  allowedActions: AlertAction[]
  approvalBlockedReason?: string
  disposition?: AlertDisposition
  history: Array<{
    action: keyof typeof alertActions
    dispositionAction?: AlertDispositionAction
    requestedDeadline?: string
    fromStatus?: AlertStatus
    toStatus: AlertStatus
    operator: string
    comment?: string
    occurredAt: string
  }>
}

export interface AlertMutation {
  hazardId?: string
  disposition?: AlertDisposition
}

export interface AlertCounts {
  total: number
  pending: number
  processing: number
  closed: number
  pendingDispositions?: number
}

/**
 * 用途 按动作核对必填处置输入，并只传送当前动作使用的字段。
 * 参数 action 为动作，expectedVersion 为已查看版本，comment 为说明，requestedDeadline 为延期日期。
 * 返回值 可以提交的请求体，或字段未填齐时的空值。
 */
export function alertActionPayload(
  action: AlertAction, expectedVersion: number, comment: string, requestedDeadline: string,
): AlertActionInput | undefined {
  const note = comment.trim()
  if (expectedVersion < 1 || (action !== 'convert' && !note)) return
  if (action === 'request_extension' && !/^\d{4}-\d{2}-\d{2}$/.test(requestedDeadline)) return
  return {
    action, expectedVersion, ...(note ? { comment: note } : {}),
    ...(action === 'request_extension' ? { requestedDeadline } : {}),
  }
}

import type { Risk } from './domain'

export type HazardLedger = 'vulnerability' | 'major_risk'
export type HazardSource = 'manual' | 'assessment' | 'scan'
export type HazardStatus = 'pending_dispatch' | 'open' | 'pending_retest' | 'pending_review' | 'returned' | 'closed'
export type HazardAction = 'dispatch' | 'supervise' | 'submit' | 'return_for_correction' | 'close'
export type HazardRetestAction = 'retest_pass' | 'retest_return'
export type HazardEventAction = HazardAction | HazardRetestAction | 'extend_deadline' | 'register'
export interface HazardSummary {
  id: string
  ledger: HazardLedger
  title?: string
  source?: HazardSource
  discoveredOn?: string
  dispatchedAt?: string
  planId?: string
  reportId?: string
  filingId: string
  filingNumber: string
  reportNumber?: string
  systemName: string
  organizationName: string
  assessmentOrg?: string
  severity: Risk
  description: string
  responsibility: string
  deadline: string
  status: HazardStatus
  createdAt: string
}

export interface HazardDetail extends HazardSummary {
  measure: string
  version: number
  allowedActions: Array<HazardAction | HazardRetestAction>
  retestReportReady?: boolean
  closedAt?: string
  receiptAvailable?: boolean
}

export interface HazardEvent {
  action: HazardEventAction
  fromStatus: HazardStatus | ''
  toStatus: HazardStatus
  operator: string
  comment?: string
  occurredAt: string
}

export interface HazardMutation {
  id: string
  status: HazardStatus
  version: number
}

export interface HazardRegistration {
  ledger: HazardLedger
  filingId: string
  title: string
  severity: Risk
  source: HazardSource
  discoveredOn: string
  deadline: string
  description: string
  measure: string
  responsibility: string
}

export interface HazardFilingOption {
  id: string
  number: string
  systemName: string
  organizationName: string
  assessmentOrg: string
}

export interface RemediationWorkbench {
  pendingPlanReview: number
  pendingReview: number
  dueWithin10Days: number
  overdue: number
  items: Array<Pick<HazardSummary, 'id' | 'systemName' | 'description' | 'deadline' | 'status'>>
  pendingPlans: Array<Pick<HazardSummary, 'id' | 'systemName' | 'deadline'> & { reportNumber: string }>
}

export const hazardLedgers: Record<HazardLedger, string> = {
  vulnerability: '系统漏洞台账', major_risk: '重大风险台账',
}
export const hazardRegistrations: Record<HazardLedger, string> = {
  vulnerability: '登记漏洞', major_risk: '登记重大风险',
}
export const hazardSources: Record<HazardSource, string> = {
  manual: '人工录入', assessment: '测评发现', scan: '漏洞扫描',
}
export const hazardStatuses: Record<HazardStatus, string> = {
  pending_dispatch: '待派发', open: '待整改', pending_retest: '待机构复测',
  pending_review: '待监管复核', returned: '退回补正', closed: '已销项',
}
export const hazardActions: Record<HazardEventAction, string> = {
  register: '监管登记', dispatch: '派发整改', supervise: '督办',
  submit: '申请机构复测', return_for_correction: '监管退回补正', close: '复核销项',
  retest_pass: '机构复测通过', retest_return: '机构复测退回', extend_deadline: '整改延期获批',
}
export const hazardDue = { within_10_days: '10 日内到期', overdue: '已逾期' }
const badgeColors: Record<HazardStatus, string> = {
  pending_dispatch: 'pending_final', open: 'draft', pending_retest: 'pending_initial',
  pending_review: 'pending_final', returned: 'returned', closed: 'approved',
}
const shanghaiDate = new Intl.DateTimeFormat('sv-SE', { dateStyle: 'short', timeZone: 'Asia/Shanghai' })

/**
 * 用途 判断监管办理动作是否必须提供说明。
 * 参数 action 为当前确认的隐患办理动作。
 * 返回值 督办、整改提交或退回补正要求说明时为真。
 */
export function hazardRequiresComment(action: HazardAction) {
  return action === 'supervise' || action === 'submit' || action === 'return_for_correction'
}

/**
 * 用途 展示历史事件的实际动作，兼容直接提交监管复核的历史记录。
 * 参数 event 为服务端返回的隐患办理记录。
 * 返回值 时间线中的动作名称。
 */
export function hazardEventLabel(event: HazardEvent) {
  return event.action === 'submit' && event.toStatus === 'pending_review'
    ? '提交监管复核' : hazardActions[event.action]
}

/**
 * 用途 获取业务使用的上海自然日。
 * 参数 date 为要转换的时间，默认使用当前时间。
 * 返回值 用于日期输入及展示的年月日。
 */
export function hazardDay(date = new Date()) {
  return shanghaiDate.format(date)
}

/**
 * 用途 检查监管登记时发现日期和整改期限的先后关系。
 * 参数 discoveredOn 为发现日，deadline 为截止日，today 为上海当前日。
 * 返回值 错误提示，日期关系有效时为空。
 */
export function hazardDateError(discoveredOn: string, deadline: string, today: string) {
  if (discoveredOn > today) return '发现日期不能晚于今天。'
  if (deadline < discoveredOn) return '整改期限不能早于发现日期。'
  return ''
}

/**
 * 用途 将隐患状态映射到现有状态颜色。
 * 参数 status 为隐患状态。
 * 返回值 页面状态样式类。
 */
export function hazardBadge(status: HazardStatus) {
  return 'status-' + badgeColors[status]
}

/**
 * 用途 生成同源隐患详情地址。
 * 参数 id 为隐患标识。
 * 返回值 详情页面或 API 的相对路径。
 */
export function hazardPath(id: string) {
  return '/hazards/' + encodeURIComponent(id)
}

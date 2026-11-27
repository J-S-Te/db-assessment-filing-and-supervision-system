import { alertStatuses, alertDispositionStatuses } from './alerts'
import { onMounted, onScopeDispose } from 'vue'
import { statuses, type CaseDetail } from './domain'
import { filingApplicationStatuses, type FilingApplicationDetail } from './filingApplications'
import { hazardStatuses, type HazardDetail } from './hazards'
import { implementationStatuses, type ImplementationDetail } from './implementations'
import { materialIssueLabels } from './materialIssues'
import { remediationStatuses, type RemediationDetail } from './remediations'

export type BusinessAudience = 'institution' | 'operator'

export const businessResources = {
  filings: { audience: 'institution', label: '备案材料', statuses: { ...statuses, cancelled: '已注销' } },
  'filing-applications': {
    audience: 'institution', label: '备案变更或注销', statuses: filingApplicationStatuses,
  },
  implementations: { audience: 'institution', label: '实施报备', statuses: implementationStatuses },
  remediations: { audience: 'operator', label: '整改规划', statuses: { ...remediationStatuses, reminder: '整改催办' } },
  hazards: { audience: 'operator', label: '隐患整改', statuses: hazardStatuses },
} as const

export type BusinessResource = keyof typeof businessResources

export interface BusinessFeedbackMessage {
  id: string
  resource: BusinessResource | 'alerts' | 'reports' | 'material-issues'
    | 'supervisions' | 'supervision-site' | 'material-expiries' | 'assessment-dues'
  resourceId: string
  reference: string
  subject: string
  state: string
  comment?: string
  status?: string
  sentAt: string
  acknowledgedAt?: string
}

export interface BusinessDetails {
  filings: CaseDetail
  'filing-applications': FilingApplicationDetail
  implementations: ImplementationDetail
  remediations: RemediationDetail
  hazards: HazardDetail
}

export type BusinessRecord = {
  [Resource in BusinessResource]: { resource: Resource; detail: BusinessDetails[Resource] }
}[BusinessResource]
export type EditableBusinessRecord = Exclude<BusinessRecord, { resource: 'hazards' }>

export const businessScopes = {
  institution: { key: 'institution', label: '测评机构名称', title: '审核意见查收' },
  operator: { key: 'organization', label: '运营单位名称', title: '运营者审核意见' },
} as const

/**
 * 用途 切回监管业务详情时刷新跨端办理状态。
 * 参数 refresh 为含操作保护的刷新动作，intervalMs 为可选的可见页面刷新间隔。
 * 返回值 无。
 */
export function refreshBusinessOnFocus(refresh: () => void, intervalMs?: number) {
  let timer: ReturnType<typeof setInterval> | undefined
  const onVisibility = () => {
    if (document.visibilityState === 'visible') refresh()
  }
  onMounted(() => {
    document.addEventListener('visibilitychange', onVisibility)
    window.addEventListener('focus', onVisibility)
    if (intervalMs) timer = setInterval(onVisibility, intervalMs)
  })
  onScopeDispose(() => {
    document.removeEventListener('visibilitychange', onVisibility)
    window.removeEventListener('focus', onVisibility)
    clearInterval(timer)
  })
}

const badgeColors: Record<string, string> = {
  open: 'draft',
  pending: 'pending_final',
  processing: 'pending_initial',
  pending_review: 'pending_initial',
  pending_retest: 'pending_initial',
  pending_dispatch: 'pending_initial',
  pending_supervision: 'pending_final',
  submitted: 'pending_initial',
  following_up: 'returned',
  follow_up_submitted: 'pending_initial',
  reminder: 'returned',
  retest_reminder: 'returned',
  hazard_supervision: 'returned',
  closed: 'approved',
  withdrawn: 'draft',
}

const reminderLabels: Readonly<Record<string, string>> = {
  retest_reminder: '复测催办', deadline_due_soon: '即将到期',
  deadline_due: '今日到期', deadline_overdue: '已逾期',
  hazard_supervision: '重大风险督办',
  withdrawal_requested: '已申请备案撤回', withdrawal_approved: '撤回已确认', withdrawal_rejected: '撤回申请未通过',
  site_scheduled: '现场检查已安排', site_result: '现场结果已登记', site_follow_up: '现场检查整改跟进',
  site_returned: '现场整改补正', site_closed: '现场检查已办结', site_cancelled: '现场检查已取消',
  material_due: '材料即将到期', material_expired: '材料已到期', material_renewal_approved: '材料续办已确认',
  material_renewal_rejected: '材料续办退回', material_expiry_retired: '材料有效期已停用',
  assessment_due_soon: '系统测评即将到期', assessment_due: '系统测评到期',
}
export const assessmentDueLabels: Readonly<Record<string, string>> = {
  upcoming: '待到期', due_soon: '临近到期', due: '今日到期', expired: '已逾期', closed: '已办结', suspended: '已暂停',
}
const supervisionLabels: Readonly<Record<string, string>> = {
  draft: '草稿', open: '待自查', submitted: '待监管审核',
  returned: '待补正', approved: '已办结', cancelled: '已取消',
  scheduled: '已安排现场检查', recorded: '现场结果已登记', following_up: '待运营者跟进',
  follow_up_submitted: '待监管复核', closed: '现场检查已办结',
}
const expiryLabels: Readonly<Record<string, string>> = {
  valid: '有效', due: '临近到期', expired: '已过期', suspended: '已暂停', retired: '已停用',
}

/**
 * 用途 判断记录类型是否属于当前端的业务范围。
 * 参数 raw 为路由或消息中的业务类型，audience 为当前办理端。
 * 返回值 类型受当前端支持时为真。
 */
export function acceptsResource(raw: unknown, audience: BusinessAudience): raw is BusinessResource {
  if (typeof raw !== 'string' || !Object.hasOwn(businessResources, raw)) return false
  return businessResources[raw as BusinessResource].audience === audience
    || (audience === 'institution' && raw === 'hazards')
    || (audience === 'operator' && ['filings', 'filing-applications', 'implementations'].includes(raw))
}

/**
 * 用途 生成当前办理端的业务详情地址。
 * 参数 audience 为办理端，resource 为业务类型，id 为服务端记录标识。
 * 返回值 同源页面或接口相对地址。
 */
export function scopedBusinessPath(audience: BusinessAudience, resource: BusinessResource, id: string) {
  return '/' + audience + '/' + resource + '/' + encodeURIComponent(id)
}

/**
 * 用途 将反馈、报告或详情中的状态映射为当前业务的中文说明。
 * 参数 resource 为业务类型，status 为服务端状态码。
 * 返回值 已定义的中文说明或未识别的状态码。
 */
export function businessStatus(resource: BusinessFeedbackMessage['resource'], status: string): string {
  if (Object.hasOwn(reminderLabels, status)) return reminderLabels[status] ?? status
  if (resource === 'material-issues') return materialIssueLabels[status] ?? status
  if (resource === 'material-expiries') return expiryLabels[status] ?? status
  if (resource === 'assessment-dues') return assessmentDueLabels[status] ?? status
  if (resource === 'supervisions' || resource === 'supervision-site') return supervisionLabels[status] ?? status
  const labels: Readonly<Record<string, string>> | undefined = resource === 'alerts'
    ? { ...alertDispositionStatuses, ...alertStatuses }
    : resource === 'reports' ? statuses : businessResources[resource]?.statuses
  return labels && Object.hasOwn(labels, status) ? labels[status] ?? status : status
}

/**
 * 用途 为当前业务状态复用工作台的状态标签颜色。
 * 参数 status 为服务端状态码。
 * 返回值 状态标签样式类。
 */
export function businessBadge(status: string): string {
  return 'status-' + (Object.hasOwn(badgeColors, status) ? badgeColors[status] : status)
}

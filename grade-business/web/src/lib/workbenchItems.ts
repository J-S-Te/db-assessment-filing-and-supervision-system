import type { BusinessAudience } from './scopedBusiness'

export type WorkbenchResource = 'filings' | 'filing-applications' | 'implementations' | 'reports'
  | 'remediations' | 'hazards' | 'alerts' | 'supervisions' | 'supervision-site'
export type WorkbenchUrgency = 'overdue' | 'near' | 'upcoming'

export interface WorkbenchTask {
  id: string
  resource: WorkbenchResource
  label: string
  systemName: string
  number: string
  status?: string
  deadline?: string
  urgency?: WorkbenchUrgency
  canRemind?: boolean
  waitingFor?: string
}

export interface WorkbenchCounts {
  pending: number
  near: number
  overdue: number
  unread: number
}

const shanghaiDate = new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai', dateStyle: 'short' })
const millisecondsPerDay = 86_400_000
const reviewers: Readonly<Record<string, string>> = {
  regulator: '等待监管办理', institution: '等待机构办理', operator: '等待运营单位办理',
}

/**
 * 用途 说明正在处理此单据的另一业务端。
 * 参数 value 为时限接口返回的办理端，可为空。
 * 返回值 可显示的等待说明。
 */
export function reviewAssignee(value?: string) {
  return value && Object.hasOwn(reviewers, value) ? reviewers[value] : ''
}

/**
 * 用途 按北京时间自然日区分逾期、当日到期和剩余时限。
 * 参数 deadline 为业务截止日，now 为计算时使用的当前时间。
 * 返回值 显示文字及适用的期限等级，空值及无效日期视为未设置。
 */
export function describeDeadline(deadline?: string, now = new Date()): {
  text: string; urgency?: WorkbenchUrgency
} {
  if (!deadline || !/^\d{4}-\d{2}-\d{2}$/.test(deadline)) return { text: '未设时限' }
  const cutoff = Date.parse(deadline)
  if (!Number.isFinite(cutoff) || new Date(cutoff).toISOString().slice(0, 10) !== deadline) {
    return { text: '未设时限' }
  }
  const days = (cutoff - Date.parse(shanghaiDate.format(now))) / millisecondsPerDay
  if (days < 0) return { text: `已逾期 ${-days} 天`, urgency: 'overdue' }
  if (days === 0) return { text: '今日到期', urgency: 'near' }
  const urgency = days <= 10 ? 'near' : days <= 30 ? 'upcoming' : undefined
  return { text: `剩 ${days} 天`, urgency }
}

/**
 * 用途 将工作台记录映射到当前办理端的可直达详情。
 * 参数 task 包含资源及记录标识，context 携带办理端和已选范围。
 * 返回值 保留业务范围及详情定位条件的导航对象。
 */
export function workbenchTaskLink(task: Pick<WorkbenchTask, 'resource' | 'id' | 'status' | 'waitingFor'>, context?: {
  audience: BusinessAudience; query: Record<string, string>
}) {
  const prefix = context ? '/' + context.audience : ''
  const query = { ...context?.query }
  if (task.resource === 'supervision-site') {
    return { path: prefix + '/supervisions/' + encodeURIComponent(task.id), query: { ...query, section: 'site' } }
  }
  const path = prefix + '/' + task.resource
  if (task.resource === 'alerts') return { path, query: { ...query, alert: task.id } }
  if (context?.audience === 'institution' && task.status === 'pending_initial' && !task.waitingFor
    && (task.resource === 'filings' || task.resource === 'filing-applications')) {
    query.review = 'assigned'
  }
  return { path: path + '/' + encodeURIComponent(task.id), query }
}

import { watch } from 'vue'
import type { DirectorySnapshot } from './directory'

export const implementationPath = '/implementations'

export const implementationTypes = {
  routine: '常规实施报备',
  change: '变更实施报备',
} as const

export const implementationStatuses = {
  draft: '草稿',
  submitted: '待受理',
  pending_initial: '待初审',
  pending_final: '待终审',
  pending_supervision: '待监管复核',
  approved: '已通过',
  returned: '待补正',
  withdrawn: '已撤销',
} as const

export const implementationActions = {
  create: '创建报备',
  edit: '编辑报备',
  submit: '提交报备',
  accept: '受理报备',
  approve: '审核通过',
  return_for_correction: '退回补正',
  withdraw: '撤销报备',
  archive: '归档报备',
} as const

export type ImplementationType = keyof typeof implementationTypes
export type ImplementationStatus = keyof typeof implementationStatuses
export type ImplementationAction = Exclude<keyof typeof implementationActions, 'create' | 'edit'>

export interface ImplementationInput {
  filingId: string
  type: ImplementationType
  reporter: string
  reporterId?: string
  contact?: string
  assessmentOrg?: string
  plannedStart: string
  plannedEnd: string
  plan: string
}

export interface ImplementationSummary {
  id: string
  filingNumber: string
  systemName: string
  organizationName: string
  assessmentOrg: string
  type: ImplementationType
  status: ImplementationStatus
  plannedStart: string
  plannedEnd: string
  submittedAt?: string
  archivedAt?: string
}

interface ImplementationRuleTimes {
  planEditDeadline?: string
  resubmitAvailableAt?: string
}

export interface ImplementationDetail extends ImplementationSummary, ImplementationInput, ImplementationRuleTimes {
  assessmentOrg: string
  version: number
  acceptedAt?: string
  allowedActions: Array<ImplementationAction | 'edit'>
  canPrecheck: boolean
  directorySnapshot?: DirectorySnapshot
}

export interface ImplementationPrecheckFinding {
  ruleId: string
  status: 'pass' | 'warning' | 'unavailable'
  message: string
  evidence?: { attachmentId: string; fileName: string }
}

export interface ImplementationPrecheck {
  reportVersion: number
  filingVersion?: number
  materialRevision?: number
  findings: ImplementationPrecheckFinding[]
}

export interface ImplementationMutation {
  id: string
  status: ImplementationStatus
  version: number
  archivedAt?: string
}

export interface FilingOption {
  id: string
  number: string
  systemName: string
  organizationName: string
  assessmentOrg: string
}

export interface ImplementationQuota {
  year: number
  used: number
  limit: number
  resubmitAvailableAt?: string
}

export interface ImplementationEvent {
  action: keyof typeof implementationActions
  fromStatus?: ImplementationStatus
  toStatus: ImplementationStatus
  operator: string
  comment?: string
  occurredAt: string
  directorySnapshot?: DirectorySnapshot
}

export interface ImplementationHistory {
  items: ImplementationEvent[]
  nextBeforeVersion?: number
}

/**
 * 用途 生成当前实施报备的详情路径。
 * 参数 id 为报备公开标识。
 * 返回值 同源业务路径和页面路径。
 */
export function implementationRecord(id: string): string {
  return implementationPath + '/' + encodeURIComponent(id)
}

/**
 * 用途 获取已有视觉状态类。
 * 参数 status 为实施报备状态。
 * 返回值 状态标记样式类。
 */
export function implementationStatusClass(status: ImplementationStatus): string {
  if (status === 'submitted') return 'status-pending_initial'
  if (status === 'pending_supervision') return 'status-pending_final'
  if (status === 'withdrawn') return 'status-draft'
  return 'status-' + status
}

/**
 * 用途 在操作按钮与流转记录中显示实际审核阶段。
 * 参数 action 为动作，from 为动作发生前的报备状态。
 * 返回值 可展示的业务动作。
 */
export function implementationActionLabel(
  action: keyof typeof implementationActions,
  from?: ImplementationStatus,
): string {
  if (action === 'approve') {
    if (from === 'pending_initial') return '初审通过'
    if (from === 'pending_final') return '终审通过'
    if (from === 'pending_supervision') return '监管复核通过'
  }
  return implementationActions[action]
}

const implementationTimeFormat = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})

/**
 * 用途 将实施报备的规则时间按上海时区显示。
 * 参数 time 为服务端提供的截止或恢复提交时间。
 * 返回值 可展示的北京时间。
 */
export function formatImplementationRuleTime(time: string): string {
  return implementationTimeFormat.format(new Date(time)) + '（北京时间）'
}

/**
 * 用途 在实施计划截止或冷却到期时刷新页面可用动作。
 * 参数 readTimes 提供当前详情的规则时刻，refresh 在到达最近时限时运行。
 * 返回值 无，定时器随当前详情和组件生命周期清理。
 */
export function watchImplementationRuleTimes(
  readTimes: () => ImplementationRuleTimes | undefined,
  refresh: () => void,
) {
  watch(readTimes, (times, _, onCleanup) => {
    const now = Date.now()
    const boundaries = [times?.planEditDeadline, times?.resubmitAvailableAt]
      .map(value => value ? Date.parse(value) : 0)
      .filter(value => value > now)
    if (!boundaries.length) return
    const boundary = Math.min(...boundaries)
    let timeout: number | undefined

    /**
     * 用途 在浏览器计时器的时间范围内等待规则时刻。
     * 参数 无，截止取自当前详情。
     * 返回值 无。
     */
    function scheduleBoundary() {
      const remaining = boundary - Date.now()
      if (remaining <= 0) {
        refresh()
        return
      }
      timeout = window.setTimeout(scheduleBoundary, Math.min(remaining, 2_147_483_647))
    }

    scheduleBoundary()
    onCleanup(() => window.clearTimeout(timeout))
  }, { immediate: true })
}

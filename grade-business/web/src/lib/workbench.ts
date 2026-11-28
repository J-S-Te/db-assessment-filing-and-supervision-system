import { readonly, shallowRef, ref } from 'vue'
import { api, errorText, isAbort, params } from './api'
import { canVisit, session } from './session'
import type { RemediationWorkbench } from './hazards'
import type { WorkbenchCounts, WorkbenchTask } from './workbenchItems'

interface Overview {
  filingTotal: number
  filingsThisYear: number
  implementationOpen: number
  implementationInReview: number
  reportInitial: number
  reportFinal: number
  hazardTotal: number
  hazardClosed: number
  hazardDueWithin30Days: number
  tasks: WorkbenchTask[]
  workflow?: WorkbenchCounts
  remediation: RemediationWorkbench
  reviewDeadlines?: { overdue: number; near: number; upcoming: number }
  activeAlerts: number
  pendingAlertReviews?: number
  openMaterialIssues?: number
}

const overview = shallowRef<Overview>()
const loading = ref(false)
const failure = ref('')
let request: AbortController | undefined

export const workbench = {
  overview: readonly(overview),
  loading: readonly(loading),
  failure: readonly(failure),
}

/**
 * 用途 从业务服务更新顶栏提醒和工作台共享的状态快照。
 * 参数 无。
 * 返回值 查询任务，失败时保留上次快照并显示读取错误。
 */
export async function refreshWorkbench() {
  if (!session.value || !canVisit(session.value, '/')) return
  request?.abort()
  const next = new AbortController()
  request = next
  loading.value = true
  failure.value = ''
  try {
    const result = await api<Overview>('/workbench/overview', { signal: next.signal })
    if (request === next && !next.signal.aborted) overview.value = result
  } catch (error) {
    if (request === next && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === next) {
      request = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 在离开管理端布局时停止未完成的工作台查询。
 * 参数 无。
 * 返回值 无。
 */
export function cancelWorkbenchRefresh() {
  request?.abort()
  request = undefined
  loading.value = false
}

/**
 * 用途 为当前运营单位的整改单向承办机构发起站内复测催办。
 * 参数 organization 为运营单位，id 为整改单标识，signal 为当前页面的取消信号。
 * 返回值 本次送达或当日已催办的业务提示，请求失败时抛出接口错误。
 */
export async function sendRetestReminder(organization: string, id: string, signal: AbortSignal) {
  const result = await api<{ sent: boolean }>('/operator/workbench/retest-reminders' + params({ organization }), {
    method: 'POST', body: { id }, signal,
  })
  return result.sent ? '已向负责复测的测评机构发送站内催办。' : '该事项今日已催办。'
}

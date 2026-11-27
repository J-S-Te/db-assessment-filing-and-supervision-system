import { computed, onScopeDispose, ref, shallowRef, watch, type Ref } from 'vue'
import { api } from './api'
import type { AlertCounts } from './alerts'
import {
  dashboardPath, type DashboardAlert, type DashboardComparison, type DashboardDutySummary,
  type DashboardRiskIndex, type DashboardRiskLevel, type DashboardStatistics, type DashboardSystemRisk,
} from './dashboard'
import type { DashboardAnalytics } from './dashboardAnalytics'
import type { DistrictCounts, DistrictRiskRow, DistrictRiskSummary } from './dashboardGeo'
import type { Risk } from './domain'
import type { HazardStatus } from './hazards'
import { refreshBusinessOnFocus } from './scopedBusiness'

export type ResponseCounts = DistrictCounts & { overdue: number; pendingRetest: number }
type ResponseRow = DistrictRiskRow & {
  overdue: number
  pendingRetest: number
  riskIndex: number
  riskLevel: DashboardRiskLevel
}
export type ResponseSummary = Omit<DistrictRiskSummary, 'items' | 'withoutDistrict'> & {
  items: ResponseRow[]
  withoutDistrict: ResponseCounts
}

export type DistrictTask = {
  id: string; title: string; systemName: string; organizationName: string; status: HazardStatus
  severity: Risk; deadline: string; overdue: boolean
}

export type DistrictResponse = {
  district: string | null
  counts: ResponseCounts & {
    pendingReview: number; pendingPlanReview: number; pendingAlerts: number
  }
  risk: DashboardRiskIndex
  duty: DashboardDutySummary
  comparison: DashboardComparison
  tasks: DistrictTask[]
  highRiskSystems: DashboardSystemRisk[]; alerts: DashboardAlert[]
  statistics?: Omit<DashboardStatistics, 'highRiskSystems'>
  overview?: {
    filingTotal: number; filingsThisYear: number; implementationOpen: number
    reportInitial: number; reportFinal: number; hazardTotal: number; hazardClosed: number
    alertCounts: AlertCounts; reviewDeadlines?: { overdue: number }
  }
  analytics?: DashboardAnalytics
}

/**
 * 用途 加载总览的区县排名及数据读取状态。
 * 参数 enabled 表示总览区域是否在显示。
 * 返回值 可用区县快照、读取状态及手动刷新入口。
 */
export function useDistrictSummary(enabled: Readonly<Ref<boolean>>) {
  const summary = shallowRef<ResponseSummary>()
  const loading = ref(false)
  const failed = ref(false)
  let pending: AbortController | undefined

  /**
   * 用途 非重叠读取区县排名，刷新失败时保留上一份有效快照。
   * 参数 无，查询范围为有效备案系统的区县。
   * 返回值 本次请求的任务。
   */
  async function refresh() {
    if (!enabled.value || pending) return
    const request = new AbortController()
    const timeout = window.setTimeout(() => request.abort(), 12_000)
    pending = request
    loading.value = true
    try {
      const data = await api<ResponseSummary>(dashboardPath + '/risk-by-district?limit=200', { signal: request.signal })
      if (pending === request) {
        summary.value = data
        failed.value = false
      }
    } catch {
      if (pending === request) failed.value = true
    } finally {
      window.clearTimeout(timeout)
      if (pending === request) {
        pending = undefined
        loading.value = false
      }
    }
  }

  watch(enabled, show => { if (show) void refresh() }, { immediate: true })
  refreshBusinessOnFocus(() => void refresh(), 30_000)
  onScopeDispose(() => { const request = pending; pending = undefined; request?.abort() })
  return { summary, loading, failed, refresh }
}

/**
 * 用途 读取所选范围的事项和联动面板，快速切换时丢弃旧请求。
 * 参数 district 为区县名称，空字符串表示未归区，null 表示全域；enabled 表示视图是否需要该响应。
 * 返回值 最近成功的整屏快照、读取状态和刷新入口。
 */
export function useDistrictDetail(district: Readonly<Ref<string | null>>, enabled: Readonly<Ref<boolean>>) {
  const response = shallowRef<DistrictResponse>()
  const snapshot = computed(() => enabled.value ? response.value : undefined)
  const loading = ref(false)
  const failed = ref(false)
  let pending: AbortController | undefined

  /**
   * 用途 查询当前范围，区分全域和未归区。
   * 参数 无，读取当前视图的范围选择。
   * 返回值 本次查询任务。
   */
  async function refresh() {
    const selected = district.value
    if (!enabled.value || pending) return
    const request = new AbortController()
    const timeout = window.setTimeout(() => request.abort(), 12_000)
    pending = request
    loading.value = true
    try {
      const query = new URLSearchParams(selected === null ? { scope: 'all' } : { district: selected })
      const data = await api<DistrictResponse>(dashboardPath + '/district-response?' + query, { signal: request.signal })
      if (pending === request && district.value === selected && enabled.value) {
        response.value = data
        failed.value = false
      }
    } catch {
      if (pending === request) failed.value = true
    } finally {
      window.clearTimeout(timeout)
      if (pending === request) {
        pending = undefined
        loading.value = false
      }
    }
  }

  watch([district, enabled], () => {
    const request = pending
    pending = undefined
    request?.abort()
    loading.value = failed.value = false
    void refresh()
  }, { immediate: true })
  refreshBusinessOnFocus(() => void refresh(), 30_000)
  onScopeDispose(() => { const request = pending; pending = undefined; request?.abort() })
  return { snapshot, loading, failed, refresh }
}

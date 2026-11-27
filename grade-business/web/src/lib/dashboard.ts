import { onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { alertPath, type AlertCounts, type AlertItem } from './alerts'
import { api } from './api'
import type { DashboardAnalytics } from './dashboardAnalytics'
import { metricPercent, type DashboardDay } from './dashboardMetrics'
import type { Page } from './domain'
import { refreshBusinessOnFocus } from './scopedBusiness'
import type { workbench } from './workbench'

export const dashboardPath = '/dashboard'
export const dashboardTabFlag = 'standalone'
export const dashboardTabLocation = { path: dashboardPath, query: { [dashboardTabFlag]: '1' } }
export type DashboardAlert = Pick<AlertItem, 'id' | 'type' | 'severity' | 'systemName' |
  'organizationName' | 'status' | 'triggeredAt'>

export interface DashboardRisks {
  high: number
  medium: number
  low: number
}

export interface DashboardSystemRisk {
  filingId: string
  systemName: string
  organizationName: string
  count: number
}

export interface DashboardStatistics {
  updatedAt: string
  filings: { active: number; cancelled: number; underReview: number; returned: number; draft: number; activeOrganizations: number }
  hazards: DashboardRisks
  openHazards: DashboardRisks
  highRiskSystems: DashboardSystemRisk[]
  reportFirstReview: { approved: number; decided: number }
  trend: DashboardDay[]
  filingCoverage: { covered: number; eligible: number }
  filingCoverageByDistrict: { district: string; covered: number; eligible: number }[]
  registeredAssets: {
    ip: number; domain: number; topology: number; supplier: number; securityEvent: number; cloud: number; bigData: number
  }
}

type DashboardOverview = NonNullable<(typeof workbench.overview)['value']>

const refreshInterval = 30_000
const requestTimeout = 12_000
const localNumber = new Intl.NumberFormat('zh-CN')
const snapshotTime = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit',
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})

/**
 * 用途 格式化业务快照中的实际计数。
 * 参数 value 为可选计数。
 * 返回值 格式化数字或缺少数据标记。
 */
export function dashboardNumber(value?: number) {
  return value === undefined ? '—' : localNumber.format(value)
}

/**
 * 用途 格式化有可用统计分母的业务比例。
 * 参数 part 与 total 为同一范围下的分子和分母。
 * 返回值 百分比文字或缺少数据标记。
 */
export function dashboardPercent(part?: number, total?: number) {
  const result = metricPercent(part, total)
  return result === undefined ? '—' : `${result}%`
}

/**
 * 用途 格式化最近一次成功读取业务数据的北京时间。
 * 参数 value 为服务端或客户端记录的刷新时间戳。
 * 返回值 适用于大屏顶栏的时间文字。
 */
export function dashboardTime(value: string) {
  return snapshotTime.format(new Date(value))
}

/**
 * 用途 获取独立驾驶舱的数据来源，并在页面可见时更新。
 * 参数 无。
 * 返回值 当前可用的业务快照及刷新状态。
 */
export function useDashboard() {
  const statistics = shallowRef<DashboardStatistics>()
  const analytics = shallowRef<DashboardAnalytics>()
  const overview = shallowRef<DashboardOverview>()
  const alerts = shallowRef<AlertItem[]>()
  const alertCounts = shallowRef<AlertCounts>()
  const failure = ref(false)
  const loading = ref(true)
  const alertsUpdatedAt = ref<string>()
  let request: AbortController | undefined
  let refreshQueued = false

  /**
   * 用途 以不重叠的请求更新各数据源并保留最近一次可用快照。
   * 参数 无。
   * 返回值 本轮刷新结束后的任务。
   */
  async function refresh() {
    if (request) {
      refreshQueued = true
      return
    }
    const next = new AbortController()
    request = next
    loading.value = true
    const timeout = window.setTimeout(() => next.abort(), requestTimeout)
    const options = { signal: next.signal }
    const [stats, analysis, work, list, counts] = await Promise.allSettled([
      api<DashboardStatistics>(dashboardPath + '/statistics', options),
      api<DashboardAnalytics>(dashboardPath + '/analytics', options),
      api<DashboardOverview>('/workbench/overview', options),
      api<Page<AlertItem>>(alertPath + '?limit=20', options),
      api<AlertCounts>(alertPath + '/summary', options),
    ])
    window.clearTimeout(timeout)
    if (request !== next) return
    if (stats.status === 'fulfilled') statistics.value = stats.value
    if (analysis.status === 'fulfilled') analytics.value = analysis.value
    if (work.status === 'fulfilled') overview.value = work.value
    if (counts.status === 'fulfilled') alertCounts.value = counts.value
    if (list.status === 'fulfilled') {
      alerts.value = list.value.items
      alertsUpdatedAt.value = new Date().toISOString()
    }
    failure.value = [stats, analysis, work, list, counts].some(result => result.status === 'rejected')
    loading.value = false
    request = undefined
    if (refreshQueued) {
      refreshQueued = false
      void refresh()
    }
  }

  onMounted(() => void refresh())
  refreshBusinessOnFocus(() => void refresh(), refreshInterval)
  onScopeDispose(() => {
    request?.abort()
    request = undefined
  })
  return { statistics, analytics, overview, alerts, alertCounts, alertsUpdatedAt, failure, loading, refresh }
}

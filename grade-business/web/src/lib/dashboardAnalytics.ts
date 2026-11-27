import { api, params } from './api'
import type { DistrictCounts } from './dashboardGeo'

export type DashboardIndustry = DistrictCounts & { code: string; label: string }
export type DashboardIssue = { code: string; label: string; count: number }
export type DashboardIndustryAnalysis = { items: DashboardIndustry[]; unclassified: DistrictCounts }
export type DashboardIssueAnalysis = { items: DashboardIssue[]; unclassified: number }
export type DashboardAnalytics = {
  industries: DashboardIndustryAnalysis
  commonIssues: DashboardIssueAnalysis
  recentCommonIssues?: DashboardIssueAnalysis
}

export type ClassificationKind = 'industry' | 'issue'
type ClassificationOption = { code: string; label: string }
type ClassificationValue = { code: string; version: number }
export type DashboardClassification = ClassificationValue & {
  id: string
  number: string
  name: string
  context: string
}
export type DashboardClassificationPage = {
  items: DashboardClassification[]
  options: ClassificationOption[]
  hasMore: boolean
}

/**
 * 用途 查询驾驶舱可维护分类的业务记录。
 * 参数 kind 为业务类别，q 和 page 为检索和页码，signal 用于取消旧请求。
 * 返回值 记录、分类选项和是否有后续页。
 */
export function listDashboardClassifications(kind: ClassificationKind, q: string, page: number, signal: AbortSignal) {
  return api<DashboardClassificationPage>('/dashboard/classifications/' + kind + params({ q, page }), { signal })
}

/**
 * 用途 按用户查看的版本更新一个真实业务记录的分类。
 * 参数 kind 为业务类别，id 为记录标识，code 和 expectedVersion 为保存内容，signal 用于取消等待。
 * 返回值 已保存的分类编码及新版本，或接口错误。
 */
export function saveDashboardClassification(
  kind: ClassificationKind, id: string, code: string, expectedVersion: number, signal: AbortSignal,
) {
  return api<ClassificationValue>('/dashboard/classifications/' + kind + '/' + encodeURIComponent(id), {
    method: 'PATCH', body: { code, expectedVersion }, signal,
  })
}

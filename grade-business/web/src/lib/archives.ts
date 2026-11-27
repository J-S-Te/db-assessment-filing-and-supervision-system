import { computed, onScopeDispose, ref, shallowRef, watch, type Ref } from 'vue'
import { useRoute, type LocationQuery } from 'vue-router'
import { api, errorText, isAbort, params } from './api'
import { queryValue } from './paging'
import type { Page, Risk, Status } from './domain'
import type { SystemAsset } from './archiveInventory'
import type { HazardStatus } from './hazards'
import type { RemediationStatus } from './remediations'

export const archivePaths = { units: '/archives/units', systems: '/archives/systems' } as const
export const archiveSystemStatuses = { active: '有效', cancelled: '已注销' } as const

interface ArchiveCounters {
  latestReportAt?: string
  openHazardCount: number
  closedHazardCount: number
}

export interface ArchiveUnit extends ArchiveCounters {
  key: string
  organizationName: string
  directoryName?: string
  creditCode?: string
  classification?: string
  district?: string
  directoryStatus?: 'active' | 'inactive'
  systemCount: number
  activeSystemCount: number
}

export interface ArchiveSystem extends ArchiveCounters {
  id: string
  filingNumber: string
  systemName: string
  organizationName: string
  assessmentOrg: string
  level: string
  status: keyof typeof archiveSystemStatuses
  assetCount: number
  cancelledAt?: string
}

export interface ArchiveReport {
  id: string
  filingId: string
  systemName: string
  number: string
  assessmentOrg: string
  risk: Risk
  status: Exclude<Status, 'draft'>
  submittedAt?: string
}

export interface ArchiveRemediation {
  id: string
  reportId: string
  systemName: string
  reportNumber: string
  deadline: string
  status: Exclude<RemediationStatus, 'draft'>
  submittedAt?: string
}

export interface ArchiveHazard {
  id: string
  planId: string
  systemName: string
  severity: Risk
  description: string
  deadline: string
  status: HazardStatus
  closedAt?: string
}

export interface ArchiveRelations {
  reports: Page<ArchiveReport>
  remediations: Page<ArchiveRemediation>
  hazards: Page<ArchiveHazard>
}

export interface ArchiveUnitProfile extends ArchiveRelations {
  unit: ArchiveUnit
  systems: Page<ArchiveSystem>
  assetRecords: Page<SystemAsset & { systemName: string }>
}

export interface ArchiveSystemProfile extends ArchiveRelations {
  system: ArchiveSystem
}

export type ArchiveTab = keyof typeof archivePaths
export type ArchiveCursor = 'systemsCursor' | 'assetRecordsCursor' | 'reportsCursor' | 'remediationsCursor' | 'hazardsCursor'

/**
 * 用途 生成单位或系统档案的业务路径。
 * 参数 kind 为档案类别，key 为服务端返回的路由键。
 * 返回值 API 和页面使用的相对路径。
 */
export function archiveRecordPath(kind: ArchiveTab, key: string) {
  return archivePaths[kind] + '/' + encodeURIComponent(key)
}

/**
 * 用途 返回列表时保留原查询条件。
 * 参数 routeQuery 为当前路由查询，kind 为档案类别。
 * 返回值 适用于目标列表的筛选和分页字段。
 */
export function archiveListQuery(routeQuery: LocationQuery, kind: ArchiveTab) {
  return {
    q: queryValue(routeQuery.q) || undefined,
    cursor: queryValue(routeQuery.cursor) || undefined,
    status: kind === 'systems' ? queryValue(routeQuery.status) || undefined : undefined,
    level: kind === 'systems' ? queryValue(routeQuery.level) || undefined : undefined,
  }
}

/**
 * 用途 读取档案当前页面和各分区的独立分页快照。
 * 参数 kind 为档案类别，key 为当前路由中的记录键。
 * 返回值 响应、加载状态、错误信息和重新加载方法。
 */
export function useArchiveProfile<T extends ArchiveRelations>(kind: ArchiveTab, key: Ref<string>) {
  const route = useRoute()
  const profile = shallowRef<T>()
  const failure = ref('')
  const loading = ref(false)
  let request: AbortController | undefined
  const url = computed(() => !key.value ? '' : archiveRecordPath(kind, key.value) + params({
    reportsCursor: queryValue(route.query.reportsCursor),
    remediationsCursor: queryValue(route.query.remediationsCursor),
    hazardsCursor: queryValue(route.query.hazardsCursor),
    systemsCursor: kind === 'units' ? queryValue(route.query.systemsCursor) : undefined,
    assetRecordsCursor: kind === 'units' ? queryValue(route.query.assetRecordsCursor) : undefined,
  }))

  /**
   * 用途 取消旧请求并读取当前档案快照。
   * 参数 无，范围与分页由当前路由确定。
   * 返回值 读取任务，列表页跳过请求。
   */
  async function load() {
    request?.abort()
    request = undefined
    loading.value = false
    failure.value = ''
    profile.value = undefined
    if (!url.value) return
    const next = new AbortController()
    request = next
    loading.value = true
    try {
      const result = await api<T>(url.value, { signal: next.signal })
      if (request === next) profile.value = result
    } catch (error) {
      if (request === next && !isAbort(error)) failure.value = errorText(error)
    } finally {
      if (request === next) loading.value = false
    }
  }

  watch(url, load, { immediate: true })
  onScopeDispose(() => request?.abort())
  return { profile, failure, loading, load }
}

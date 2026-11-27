import { onScopeDispose, ref, shallowRef, watch, type Ref } from 'vue'
import { api, errorText, isAbort, params } from './api'
import type { Page } from './domain'

export const systemArchivePath = '/archives/systems'
export const coverageTargetPath = '/archives/coverage-targets'
export const supplierArchivePath = '/archives/suppliers'

export const systemAssetKinds = {
  ip: 'IP 地址', domain: '域名', topology: '拓扑连接', supplier: '供应商',
  security_event: '安全事件', cloud: '云平台资产', big_data: '大数据资产',
} as const

export type SystemAssetKind = keyof typeof systemAssetKinds

export interface AssetMetadata {
  label: string
  source: string
  description: string
  occurredOn?: string
}

export interface SystemAsset extends AssetMetadata {
  id: string
  filingId: string
  kind: SystemAssetKind
  identifier: string
  relatedEndpoint: string
  supplierId?: string
  supplierName?: string
  active: boolean
  version: number
  updatedAt: string
}

export interface SupplierMaster {
  id: string
  name: string
  serviceType: string
  unitId?: string
  unitName: string
  startedOn?: string
  contactName?: string
  contactPhone?: string
  source: string
  active: boolean
  version: number
  updatedAt: string
}

export interface SupplierProfile {
  supplier: SupplierMaster
  systems: Page<SystemAsset & {
    systemName: string
    organizationName: string
    filingNumber: string
    filingStatus: 'active' | 'cancelled'
  }>
}

/**
 * 用途 生成单位档案内供应商主档列表或详情的导航位置。
 * 参数 id 为可选主档标识。
 * 返回值 Vue Router 可导航的位置。
 */
export function archiveSupplierLocation(id?: string) {
  return { path: '/archives/units', query: { tab: 'suppliers', supplier: id } }
}

export interface CoverageTarget {
  id: string
  unitId: string
  unitName: string
  district: string
  filingId?: string
  filingNumber: string
  systemName: string
  reference: string
  source: string
  covered: boolean
  active: boolean
  version: number
}

/**
 * 用途 读取当前档案登记项并提供有界分页与版本写入。
 * 参数 path 为当前登记的同源地址，filters 为可选列表筛选条件。
 * 返回值 分页状态、请求状态、分页、刷新和保存操作。
 */
export function useArchiveInventory<T>(path: Ref<string>, filters?: Ref<Record<string, string | number | undefined>>) {
  const page = shallowRef<Page<T>>({ items: [] })
  const cursors = ref<string[]>([''])
  const loading = ref(false)
  const saving = ref(false)
  const failure = ref('')
  const mutationFailure = ref('')
  let readRequest: AbortController | undefined
  let writeRequest: AbortController | undefined

  /**
   * 用途 读取当前页，取消旧范围的读请求并保留错误状态。
   * 参数 cursor 为已有游标路径，缺省为当前页。
   * 返回值 读取任务。
   */
  async function load(cursor = cursors.value) {
    readRequest?.abort()
    const request = new AbortController()
    readRequest = request
    loading.value = true
    failure.value = ''
    try {
      const query = params({ ...filters?.value, cursor: cursor.at(-1) })
      const result = await api<Page<T>>(path.value + query, { signal: request.signal })
      if (readRequest === request) {
        cursors.value = cursor
        page.value = result
      }
    } catch (error) {
      if (readRequest === request && !isAbort(error)) failure.value = errorText(error)
    } finally {
      if (readRequest === request) loading.value = false
    }
  }

  /**
   * 用途 将新登记或预计版本下的修改落库，再刷新当前查询。
   * 参数 body 为编辑字段，entry 描述已有记录标识和预计版本。
   * 返回值 保存成功时返回登记记录，失败时将业务错误写入响应状态。
   */
  async function save(body: object, entry?: { id: string; version: number }): Promise<T | undefined> {
    if (writeRequest) return
    const request = new AbortController()
    writeRequest = request
    saving.value = true
    mutationFailure.value = ''
    const url = path.value + (entry ? '/' + encodeURIComponent(entry.id) : '')
    try {
      const result = await api<T>(url, {
        method: entry ? 'PATCH' : 'POST', body, signal: request.signal, expectedVersion: entry?.version,
      })
      if (writeRequest !== request) return
      await load(entry ? cursors.value : [''])
      return writeRequest === request ? result : undefined
    } catch (error) {
      if (writeRequest === request && !isAbort(error)) mutationFailure.value = errorText(error)
    } finally {
      if (writeRequest === request) {
        writeRequest = undefined
        saving.value = false
      }
    }
  }

  watch([path, () => filters?.value], () => {
    writeRequest?.abort()
    writeRequest = undefined
    saving.value = false
    mutationFailure.value = ''
    page.value = { items: [] }
    cursors.value = ['']
    void load()
  }, { immediate: true })

  onScopeDispose(() => {
    readRequest?.abort()
    writeRequest?.abort()
    readRequest = undefined
    writeRequest = undefined
  })

  return { page, cursors, loading, saving, failure, mutationFailure, load, save }
}

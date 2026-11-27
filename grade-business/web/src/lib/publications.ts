import { computed, onScopeDispose, readonly, ref, shallowRef, watch } from 'vue'
import { api, errorText, isAbort, params } from './api'
import {
  recipientPublicationUrl, type PublicationAttachment, type PublicationAudience,
  type PublicationReceipt, type PublicationTarget,
} from './publicationDelivery'
import type { BusinessAudience } from './scopedBusiness'

export const publicationPath = '/publications'
export const publicationMaterialTypes = { policy_standard: '政策标准', common_material: '常用资料' }
export const publicationTypes = {
  bulletin: '通知通报', security_advisory: '安全预警公告', announcement: '通知公告',
  ...publicationMaterialTypes,
}
export const publicationStatuses = { draft: '草稿', published: '已发布', withdrawn: '已撤回' }
export const publicationActions = { create: '起草', edit: '修订', publish: '发布', withdraw: '撤回' }
export type PublicationCategory = keyof typeof publicationTypes
export type PublicationStatus = keyof typeof publicationStatuses
export type PublicationAction = 'publish' | 'withdraw'

export interface PublicationContent {
  category: PublicationCategory
  title: string
  publisher: string
  body: string
  pinned: boolean
  alertId?: string
  audience?: PublicationAudience
  targets?: PublicationTarget[]
}

export interface PublicationSummary extends Omit<PublicationContent, 'body' | 'alertId' | 'targets'>, PublicationReceipt {
  id: string
  status: PublicationStatus
  version: number
  publishedVersion?: number
  publishedAt?: string
  updatedAt: string
  attachments?: PublicationAttachment[]
}

export interface PublicationDetail extends PublicationSummary {
  body: string
  alertId?: string
  targets?: PublicationTarget[]
  allowedActions?: Array<PublicationAction | 'edit'>
}

export interface PublicationRevision extends PublicationContent {
  version: number
  status: PublicationStatus
  action: keyof typeof publicationActions
  operator: string
  occurredAt: string
  attachments?: PublicationAttachment[]
}

export interface PublicationPage<T> { items: T[]; nextBefore?: string }

/**
 * 用途 判断站内发布是否用作运营者资料。
 * 参数 category 为查询或发布表单中的类别。
 * 返回值 政策标准和常用资料时为真。
 */
export function isPublicationMaterial(category: string): category is keyof typeof publicationMaterialTypes {
  return Object.hasOwn(publicationMaterialTypes, category)
}

const unread = shallowRef<{ url: string; count: number }>()
let unreadRequest: { url: string; controller: AbortController } | undefined
export const publicationUnread = readonly(unread)

/**
 * 用途 刷新当前收件范围的公告未读数，供列表和侧栏共享。
 * 参数 audience 为办理端，scope 为已选择的收件范围，force 表示写入后立即重查。
 * 返回值 未读计数刷新任务。
 */
export async function refreshPublicationUnread(audience?: BusinessAudience, scope = '', force = false) {
  const url = audience && scope ? recipientPublicationUrl(audience, scope, undefined, 'unread-count') : ''
  if (unreadRequest?.url === url && !force) return
  unreadRequest?.controller.abort()
  unreadRequest = undefined
  if (unread.value?.url !== url) unread.value = undefined
  if (!url) return
  const controller = new AbortController()
  unreadRequest = { url, controller }
  try {
    const result = await api<{ count: number }>(url, { signal: controller.signal })
    if (!controller.signal.aborted) unread.value = { url, count: result.count }
  } catch (error) {
    if (!controller.signal.aborted && !isAbort(error)) unread.value = undefined
  } finally {
    if (unreadRequest?.controller === controller) unreadRequest = undefined
  }
}

/**
 * 用途 查询站内发布列表并保存实际应用的筛选与分页位置。
 * 参数 endpoint 为公告集合地址或其读取函数，getCriteria 为由路由保存的筛选及收件范围。
 * 返回值 列表、查询表单状态及分页操作。
 */
export function usePublicationList(
  endpoint: string | (() => string), getCriteria?: () => Record<string, string | undefined>,
) {
  const page = shallowRef<PublicationPage<PublicationSummary>>({ items: [] })
  const category = ref<PublicationCategory | ''>('')
  const status = ref<PublicationStatus | ''>('')
  const keyword = ref('')
  const loading = ref(false)
  const failure = ref('')
  const cursors = ref<string[]>([])
  let active: AbortController | undefined
  let applied: Record<string, string> = {}
  const collection = computed(() => typeof endpoint === 'function' ? endpoint() : endpoint)
  const routeCriteria = computed(() => params(getCriteria?.() ?? {}))

  /**
   * 用途 读取已应用筛选下的一页公告。
   * 参数 before 为目标分页游标，prev 为成功加载后使用的回退页栈，quiet 控制是否保留当前列表。
   * 返回值 列表读取任务。
   */
  async function load(before = '', prev: string[] = [], quiet = false) {
    active?.abort()
    if (!collection.value) return
    const request = new AbortController()
    active = request
    loading.value = !quiet
    failure.value = ''
    try {
      const query = params({ ...(getCriteria?.() ?? applied), before })
      const data = await api<PublicationPage<PublicationSummary>>(collection.value + query, {
        signal: request.signal,
      })
      if (request.signal.aborted) return
      page.value = data
      cursors.value = [...prev, before]
    } catch (error) {
      if (!isAbort(error)) failure.value = errorText(error)
    } finally {
      if (active === request) {
        active = undefined
        loading.value = false
      }
    }
  }

  /**
   * 用途 应用表单条件并从第一页查看公告。
   * 参数 无，使用当前关键词、分类和状态。
   * 返回值 首页加载任务。
   */
  function search() {
    applied = { category: category.value, status: status.value, q: keyword.value.trim() }
    return load()
  }

  /**
   * 用途 使用服务端游标读取下一页。
   * 参数 无，使用当前查询结果。
   * 返回值 下一页加载任务或空。
   */
  function nextPage() {
    if (!page.value.nextBefore || loading.value) return
    return load(page.value.nextBefore, cursors.value)
  }

  /**
   * 用途 返回已经浏览过的上一页。
   * 参数 无，使用已保存页栈。
   * 返回值 上一页加载任务或空。
   */
  function previousPage() {
    if (cursors.value.length < 2 || loading.value) return
    return load(cursors.value.at(-2), cursors.value.slice(0, -2))
  }

  /**
   * 用途 后台刷新当前可见页并保留用户的分页位置。
   * 参数 无。
   * 返回值 当前页读取任务或在已有请求时返回空值。
   */
  function refresh() {
    if (active) return
    const before = cursors.value.at(-1)
    return load(before, cursors.value.slice(0, -1), true)
  }

  watch([collection, routeCriteria], () => {
    page.value = { items: [] }
    cursors.value = []
    failure.value = ''
    loading.value = false
    void load()
  }, { immediate: true })
  onScopeDispose(() => active?.abort())
  return { page, category, status, keyword, loading, failure, cursors, load, refresh, search, nextPage, previousPage }
}

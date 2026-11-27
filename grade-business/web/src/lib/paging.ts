import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryValue } from 'vue-router'
import { api, errorText, isAbort, params } from './api'
import type { Page } from './domain'

/**
 * 用途 从路由查询中提取唯一字符串条件。
 * 参数 raw 为 Vue Router 的一个查询项。
 * 返回值 字符串值，重复或缺失时为空。
 */
export function queryValue(raw: LocationQueryValue | LocationQueryValue[] | undefined): string {
  return typeof raw === 'string' ? raw : ''
}

/**
 * 用途 统一处理业务列表筛选、下一页游标和切换请求。
 * 参数 getPath 为当前集合路径，空路径代表尚未指定办理范围。filterKey 为筛选字段，getExtraKey 为辅助筛选字段。
 * 参数 getViewFilters 为当前视图固定的查询条件。
 * 返回值 列表状态和查询、翻页操作。
 */
export function usePagedList<T>(
  getPath: () => string, filterKey: 'status' | 'category' | 'resource',
  getExtraKey?: () => string | undefined, getViewFilters?: () => Record<string, string>,
) {
  const route = useRoute()
  const router = useRouter()
  const keyword = ref('')
  const filter = ref('')
  const extraFilter = ref('')
  const page = shallowRef<Page<T>>({ items: [] })
  const loading = ref(false)
  const failure = ref('')
  const previousCursors = ref<string[]>([])
  let request: AbortController | undefined
  let previousCollection = ''

  /**
   * 用途 获取当前路由指定的一页业务记录。
   * 参数 quiet 控制后台更新是否保留正在编辑的筛选表单。
   * 返回值 当前加载完成后的任务。
   */
  async function fetchPage(quiet: boolean) {
    request?.abort()
    const next = new AbortController()
    request = next
    const path = getPath()
    const q = queryValue(route.query.q)
    const selected = queryValue(route.query[filterKey])
    const extra = getExtraKey?.()
    const criteria = {
      ...getViewFilters?.(), q, [filterKey]: selected,
      ...(extra ? { [extra]: queryValue(route.query[extra]) } : {}),
    }
    const cursor = queryValue(route.query.cursor)
    const collection = path + params(criteria)
    if (collection !== previousCollection) page.value = { items: [] }
    if (collection !== previousCollection || !cursor) previousCursors.value = []
    previousCollection = collection
    if (!quiet) {
      keyword.value = q
      filter.value = selected
      extraFilter.value = extra ? criteria[extra] ?? '' : ''
      failure.value = ''
    }
    loading.value = Boolean(path) && !quiet
    if (!path) {
      request = undefined
      return
    }
    try {
      const result = await api<Page<T>>(path + params({
        ...criteria, cursor, limit: 20,
      }), { signal: next.signal })
      if (request === next) {
        page.value = result
        failure.value = ''
      }
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
   * 用途 从当前 URL 加载列表并同步已应用的筛选。
   * 参数 无。
   * 返回值 当前页查询任务。
   */
  function load() {
    return fetchPage(false)
  }

  /**
   * 用途 后台刷新当前页，无在途请求时才发起读取。
   * 参数 无。
   * 返回值 当前页查询任务或空值。
   */
  function refresh() {
    if (!request) return fetchPage(true)
  }

  /**
   * 用途 应用用户录入的筛选并返回第一页。
   * 参数 无，使用当前筛选表单内容。
   * 返回值 查询导航或重新加载的任务。
   */
  function search() {
    const q = keyword.value.trim()
    const extra = getExtraKey?.()
    if (q === queryValue(route.query.q) && filter.value === queryValue(route.query[filterKey])
      && (!extra || extraFilter.value === queryValue(route.query[extra]))
      && !queryValue(route.query.cursor)) return load()
    return router.push({ path: getPath(), query: {
      ...getViewFilters?.(),
      q: q || undefined, [filterKey]: filter.value || undefined,
      ...(extra ? { [extra]: extraFilter.value || undefined } : {}),
    } })
  }

  /**
   * 用途 清除全部筛选条件并返回第一页。
   * 参数 无。
   * 返回值 清除筛选后执行的查询任务。
   */
  function reset() {
    keyword.value = ''
    filter.value = ''
    extraFilter.value = ''
    return search()
  }

  /**
   * 用途 按服务端游标前进一页。
   * 参数 无，使用最近一次有效响应的下一页游标。
   * 返回值 导航任务；无下一页时为空。
   */
  function nextPage() {
    if (!page.value.nextCursor || loading.value) return
    previousCursors.value.push(queryValue(route.query.cursor))
    return router.push({ query: { ...route.query, cursor: page.value.nextCursor } })
  }

  /**
   * 用途 返回已访问的上一页；直接打开游标链接时回到第一页。
   * 参数 无。
   * 返回值 导航任务。
   */
  function previousPage() {
    const last = previousCursors.value.pop()
    return router.push({ query: { ...route.query, cursor: last || undefined } })
  }

  watch(() => {
    const extra = getExtraKey?.()
    const view = params(getViewFilters?.() ?? {})
    return [getPath(), route.query.q, route.query[filterKey], route.query.cursor, extra && route.query[extra], view]
  }, load, { immediate: true })
  onScopeDispose(() => request?.abort())

  return { keyword, filter, extraFilter, page, loading, failure, load, refresh, search, reset, nextPage, previousPage }
}

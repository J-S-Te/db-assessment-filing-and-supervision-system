import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { api } from './api'
import { directoryOptionPath, type DirectoryOption } from './directory'

/**
 * 用途 在表单范围稳定后辨认已登记机构，以便提示必须选用测评人员。
 * 参数 getScope 返回表单正在使用的机构办理名称。
 * 返回值 当前查到的机构目录项，查询失败时沿用服务端最终校验。
 */
export function useDirectoryInstitution(getScope: () => string) {
  const option = shallowRef<DirectoryOption>()
  const loading = ref(false)
  let scheduled: ReturnType<typeof setTimeout> | undefined
  let active: AbortController | undefined

  /**
   * 用途 查找办理名称精确匹配的机构，忽略较早且已取消的响应。
   * 参数 scope 为当前办理机构。
   * 返回值 当前范围查询任务。
   */
  async function load(scope: string) {
    const request = new AbortController()
    active = request
    try {
      const result = await api<{ items: DirectoryOption[] }>(
        directoryOptionPath('institution', '', scope),
        { signal: request.signal },
      )
      if (active === request) option.value = result.items.find(item => (item.scopeName || item.name) === scope)
    } catch {
      // 名录暂时无法读取时，保留业务表单并交由保存接口判断归属。
    } finally {
      if (active === request) {
        active = undefined
        loading.value = false
      }
    }
  }

  watch(getScope, scope => {
    clearTimeout(scheduled)
    active?.abort()
    active = undefined
    option.value = undefined
    loading.value = Boolean(scope)
    if (scope) scheduled = setTimeout(() => { void load(scope) }, 230)
  }, { immediate: true })
  onScopeDispose(() => {
    clearTimeout(scheduled)
    active?.abort()
  })
  return { option, loading }
}

import { shallowRef } from 'vue'
import { api, ApiError } from './api'

export interface Session {
  id: string
  name: string
  roleCode: 'ROLE_SYS_ADMIN' | 'ROLE_REGULATOR' | 'ROLE_ORG_ADMIN' | 'ROLE_OPERATOR'
  mustChangePassword: boolean
  portal: 'admin' | 'institution' | 'operator'
  dataScope: 'ALL' | 'AREA' | 'ORG' | 'UNIT'
  areaCode?: string
  subjectId?: string
  subjectName?: string
  home: string
  pages: string[]
  menu: string[]
  permissions: string[]
}

export const session = shallowRef<Session | null>(null)

/**
 * 用途 从服务端恢复会话和菜单，区分未登录与服务故障。
 * 参数 无，浏览器携带当前会话 Cookie。
 * 返回值 已验证的会话或 null，服务读取错误继续抛出。
 */
export async function refreshSession() {
  try {
    session.value = await api<Session>('/auth/session')
  } catch (error) {
    if (!(error instanceof ApiError && error.status === 401)) throw error
    session.value = null
  }
  return session.value
}

/**
 * 用途 使用服务端下发的页面前缀约束前端导航和深链接。
 * 参数 actor 为当前会话，path 为拟访问的站内页面。
 * 返回值 会话有权进入页面时为真。
 */
export function canVisit(actor: Session, path: string) {
  const clean = path.replace(/\/$/, '') || '/'
  if (actor.roleCode === 'ROLE_REGULATOR' &&
    /\/(new|edit|tags|history-import)$/.test(clean)) return false
  return actor.pages.some(prefix => clean === prefix || (prefix !== '/' && clean.startsWith(prefix + '/')))
}

/**
 * 用途 核对当前会话是否有指定操作权限。
 * 参数 code 为服务端下发的权限码。
 * 返回值 允许显示对应操作时为真。
 */
export function hasPermission(code: string) {
  return session.value?.permissions.includes(code) ?? false
}

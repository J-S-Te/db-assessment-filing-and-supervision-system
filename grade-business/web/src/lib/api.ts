const basePath = '/api/v1'
export const sessionChangeKey = 'grade-business.auth-change'
export const dashboardAccessEvent = 'grade-business-dashboard-access-changed'
const sessionMutations = new Set(['/auth/login', '/auth/logout', '/auth/password'])

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: object | FormData
  signal?: AbortSignal
  expectedVersion?: number
  materialKind?: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  /**
   * 用途 保存服务端可安全展示的业务错误。
   * 参数 status 为状态码，code 为业务错误码，message 为界面提示。
   * 返回值 新业务错误对象。
   */
  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

/**
 * 用途 通过同源接口读取或写入业务数据。
 * 参数 path 为业务路径，options 描述方法、数据和取消信号。
 * 返回值 业务响应，接口异常时抛出 ApiError。
 */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers = new Headers({ Accept: 'application/json' })
  if (options.expectedVersion !== undefined) headers.set('If-Match', '"' + options.expectedVersion + '"')
  if (options.materialKind) headers.set('X-Material-Kind', options.materialKind)
  let body: BodyInit | undefined
  if (options.body instanceof FormData) body = options.body
  else if (options.body) {
    headers.set('Content-Type', 'application/json')
    body = JSON.stringify(options.body)
  }
  const response = await fetch(basePath + path, {
    method: options.method ?? 'GET',
    headers,
    body,
    signal: options.signal,
    redirect: 'error',
  })
  if (!response.ok) {
    const error = await responseError(response)
    if (error.code === 'authentication_required' && !path.startsWith('/auth/')) {
      window.dispatchEvent(new Event('grade-business-session-expired'))
    }
    if (path.startsWith('/dashboard/') &&
      (error.status === 401 || error.status === 403)) {
      window.dispatchEvent(new Event(dashboardAccessEvent))
    }
    throw error
  }
  if (sessionMutations.has(path) && options.method === 'POST') {
    try {
      localStorage.setItem(sessionChangeKey, String(Date.now()) + ':' + String(Math.random()))
    } catch {
      // 驾驶舱在聚焦及定时刷新时也会核对会话。
    }
  }
  return response.status === 204 ? undefined as T : await response.json() as T
}

/**
 * 用途 将异常响应转换为可读错误，避免显示内部信息。
 * 参数 response 为失败的 HTTP 响应。
 * 返回值 携带业务状态和提示的错误。
 */
async function responseError(response: Response): Promise<ApiError> {
  const failure: unknown = await response.json().catch(() => null)
  const values = failure && typeof failure === 'object' ? failure as Record<string, unknown> : {}
  const code = typeof values.code === 'string' ? values.code : ''
  const message = typeof values.message === 'string' && values.message
    ? values.message : '业务服务暂时不可用，请稍后重试。'
  return new ApiError(response.status, code, message)
}

/**
 * 用途 生成仅含当前有效条件的查询字符串。
 * 参数 entries 为准备向业务接口提交的筛选项。
 * 返回值 整理后的查询字符串。
 */
export function params(entries: Record<string, string | number | undefined>): string {
  const query = new URLSearchParams()
  for (const [key, value] of Object.entries(entries)) {
    if (value !== undefined && value !== '') query.set(key, String(value))
  }
  return query.size > 0 ? '?' + query.toString() : ''
}

/**
 * 用途 判断是否为路由或筛选变化导致的主动取消。
 * 参数 error 为请求异常。
 * 返回值 是否应忽略该请求错误。
 */
export function isAbort(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

/**
 * 用途 从请求错误中提取用户可理解的提示。
 * 参数 error 为捕获的业务或网络异常。
 * 返回值 页面可展示的提示语。
 */
export function errorText(error: unknown): string {
  return error instanceof ApiError ? error.message : '连接业务服务失败，请稍后重试。'
}

/**
 * 用途 生成和当前业务记录绑定的同源材料下载地址。
 * 参数 path 为记录地址，id 为附件标识。
 * 返回值 附件下载地址。
 */
export function attachmentURL(path: string, id: string): string {
  return basePath + path + '/attachments/' + encodeURIComponent(id)
}

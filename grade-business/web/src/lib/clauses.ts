import { api, params } from './api'

export interface ClauseSnapshot { number: string; version: number; text: string }
export interface ClauseEntry extends ClauseSnapshot { active: boolean }
export interface ClausePage { items: ClauseEntry[]; knowledgeVersion: number; nextAfter?: string }

/**
 * 用途 查询启用知识条目当前版本的条款。
 * 参数 path 为知识详情接口地址，signal 为取消信号，after 为上一页的最后条款号。
 * 返回值 条款页及所属知识版本。
 */
export function clausePage(path: string, signal: AbortSignal, after = '') {
  return api<ClausePage>(path + '/clauses' + params({ after }), { signal })
}

/**
 * 用途 为同一份写入内容生成可重试的业务标识，支持本地 HTTP 测试端口。
 * 参数 无。
 * 返回值 按本次表单内容返回请求标识或清除上一笔提交的函数。
 */
export function writeIdentity() {
  let previous: { fingerprint: string; requestId: string } | undefined
  /**
   * 用途 复用相同业务内容的标识，并在成功后允许下一次独立登记。
   * 参数 body 为提交字段；省略时清除上次标识。
   * 返回值 当前业务请求标识；清除时返回空字符串。
   */
  return (body?: object) => {
    if (!body) {
      previous = undefined
      return ''
    }
    const fingerprint = JSON.stringify(body)
    if (previous?.fingerprint === fingerprint) return previous.requestId
    const bytes = crypto.getRandomValues(new Uint8Array(16))
    bytes[6] = (bytes[6]! & 15) | 64
    bytes[8] = (bytes[8]! & 63) | 128
    const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
    const requestId = [hex.slice(0, 8), hex.slice(8, 12), hex.slice(12, 16),
      hex.slice(16, 20), hex.slice(20)].join('-')
    previous = { fingerprint, requestId }
    return requestId
  }
}

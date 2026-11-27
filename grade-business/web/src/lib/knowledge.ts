import type { Category, KnowledgeDetail, KnowledgeSummary } from './domain'

export type KnowledgeEntrySummary = KnowledgeSummary & { inactiveAt?: string }
export type KnowledgeEntry = KnowledgeDetail & { inactiveAt?: string }
export type KnowledgeStatus = 'active' | 'inactive' | 'all'

export const revisionActions = {
  baseline: '迁移起点',
  create: '新增',
  import: '批量导入',
  edit: '编辑正文',
  tags: '维护标签',
  disable: '停用',
  restore: '启用',
} as const

export interface KnowledgeRevision {
  version: number
  action: keyof typeof revisionActions
  updatedAt: string
  title: string
  category: Category
  source: string
  tags: string[]
  inactiveAt?: string
}

export type KnowledgeRevisionDetail = KnowledgeRevision & { body: string }

export interface KnowledgeImportPreview {
  total: number
  items: Array<{ row: number; title: string; category: Category; source: string; tags: string[] }>
  issues: Array<{ row: number; message: string }>
}

export interface KnowledgeImportResult {
  count: number
  ids: string[]
}

export const knowledgePath = '/knowledge'
export const knowledgeImportPath = knowledgePath + '/import'

/**
 * 用途 生成知识条目的同源详情或编辑基础地址。
 * 参数 id 为服务端公开的知识条目标识。
 * 返回值 条目详情路由或业务接口相对路径。
 */
export function knowledgeRecord(id: string): string {
  return knowledgePath + '/' + encodeURIComponent(id)
}

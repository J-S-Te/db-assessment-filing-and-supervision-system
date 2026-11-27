import { knownActions, levels, statuses, type CaseAction, type CaseMutation, type Review,
  type ReviewAction, type Status } from './domain'

import type { ImplementationStatus } from './implementations'
import type { FilingMaterialRule } from './filingPolicy'

export const filingApplicationsPath = '/filing-applications'
export type FilingApplicationType = 'change' | 'cancel'
export type FilingApplicationStatus = Status | 'withdrawn'
export type FilingApplicationReviewAction = ReviewAction | 'withdraw'
export type FilingApplicationAction = CaseAction | 'withdraw'
export type FilingApplicationMutation = Omit<CaseMutation, 'status'> & { status: FilingApplicationStatus }
export type FilingApplicationReview = Omit<Review, 'fromStatus' | 'toStatus'> & {
  fromStatus: Status
  toStatus: FilingApplicationStatus
}
export const filingApplicationStatuses: Record<FilingApplicationStatus, string> = { ...statuses, withdrawn: '已撤回' }
export const filingApplicationActions: Record<string, string> = { ...knownActions, withdraw: '撤回申请' }

export const filingFields = {
  systemName: '系统名称',
  organizationName: '所属单位',
  assessmentOrg: '测评机构',
  level: '定级',
} as const

export type FilingField = keyof typeof filingFields
export type FilingValues = Record<FilingField, string>
export const filingFieldKeys = Object.keys(filingFields) as FilingField[]

export interface FilingApplicationSummary {
  id: string
  filingId: string
  filingNumber: string
  systemName: string
  organizationName: string
  type: FilingApplicationType
  status: FilingApplicationStatus
  createdAt: string
  initialReviewAudience?: 'institution'
}

export interface FilingApplicationDetail extends FilingApplicationSummary {
  reason: string
  changes?: { field: FilingField; before: string; after: string }[]
  version: number
  allowedActions: FilingApplicationAction[]
  appliedAt?: string
  implementationId?: string
  retestImplementation?: { id: string; status: ImplementationStatus }
  requiredMaterials?: string[]
  policyMaterials?: FilingMaterialRule[]
  receiptAvailable?: boolean
}

export const applicationMaterials = {
  change_statement: '变更说明',
  cancellation_statement: '注销及数据留存或销毁说明',
  supporting: '配套证明',
}

export const applicationTypes: Record<FilingApplicationType, string> = { change: '变更', cancel: '注销' }

/**
 * 用途 生成备案变更或注销申请的同源详情路径。
 * 参数 id 为申请标识。
 * 返回值 申请详情路由或 API 相对路径。
 */
export function filingApplicationPath(id: string): string {
  return filingApplicationsPath + '/' + encodeURIComponent(id)
}

/**
 * 用途 生成只包含实际差异的备案变更请求。
 * 参数 source 为备案原值，proposal 为用户填写后的字段值。
 * 返回值 去除首尾空白后仍有差异的字段，未改变时为空对象。
 */
export function proposedChanges(source: FilingValues, proposal: FilingValues): Partial<FilingValues> {
  const result: Partial<FilingValues> = {}
  for (const key of filingFieldKeys) {
    const value = proposal[key].trim()
    if (value !== source[key]) result[key] = value
  }
  return result
}

/**
 * 用途 按备案字段类型显示申请快照的内容。
 * 参数 field 为备案字段，value 为该字段的原值或申请值。
 * 返回值 定级中文名称或文本字段原值。
 */
export function displayFilingValue(field: FilingField, value: string): string {
  return field === 'level' ? levels[value] ?? value : value
}

import { params } from './api'

export const supervisionPath = '/supervisions'

export const supervisionStates = {
  draft: '待下发', open: '待填报', submitted: '待监管审查', returned: '退回补正',
  approved: '已办结', cancelled: '已取消',
} as const

export const supervisionActionLabels = {
  publish: '下发任务', submit: '提交自查', return: '退回补正', approve: '通过并办结', cancel: '取消任务',
} as const

export const materialLabels = {
  inspection_template: '监督检查自查表模板',
  summary_template: '整改情况汇总表模板',
  inspection_form: '监督检查自查表',
  rectification_summary: '整改情况汇总表',
} as const

export type Status = keyof typeof supervisionStates
export type Action = keyof typeof supervisionActionLabels
export type Material = keyof typeof materialLabels
export type Correction = 'inspection_form' | 'rectification_summary' | 'both'

export interface Task {
  id: string
  filingId: string
  filingNumber: string
  systemName: string
  organizationName: string
  title: string
  dueOn: string
  status: Status
  version: number
  leadAlertId?: string
  publishedAt?: string
  submittedAt?: string
}

export interface SupervisionAttachment {id: string; kind: Material; fileName: string; size: number; uploadedAt: string}
export interface RectificationCounts {open: number; closed: number}

export interface TaskDetail extends Task {
  attachments: SupervisionAttachment[]
  allowedActions: (Action | 'upload')[]
  correctionKind?: Correction
  rectification?: RectificationCounts
}

export interface TaskEvent {
  version: number
  action: Action | 'create'
  fromStatus?: Status
  toStatus: Status
  actor: string
  comment?: string
  correctionKind?: Correction
  createdAt: string
  materials?: SupervisionAttachment[]
  rectification?: RectificationCounts
}

export interface SupervisionPage<T> { items: T[]; nextBefore?: string }
export interface SupervisionFiling { id: string; number: string; systemName: string; organizationName: string }

/**
 * 用途 构造同源监督任务文件下载地址。
 * 参数 path 为当前席位任务，file 为文件标识，organization 为可选的运营单位。
 * 返回值 仅带业务必要范围的相对下载地址。
 */
export function supervisionDownload(path: string, file: string, organization?: string) {
  return '/api/v1' + path + '/attachments/' + encodeURIComponent(file) + params({ organization })
}

export const nextBusinessDate = new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai',
  year: 'numeric', month: '2-digit', day: '2-digit' })
  .format(new Date(Date.now() + 86_400_000))

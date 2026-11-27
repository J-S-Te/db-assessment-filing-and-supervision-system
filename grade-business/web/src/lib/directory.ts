export type DirectoryKind = 'unit' | 'institution' | 'person'
export type DirectoryCollection = 'units' | 'institutions' | 'people'
export type DirectoryStatus = 'active' | 'inactive'

export interface DirectoryOption {
  id?: string
  name: string
  scopeName?: string
  code?: string
  status: DirectoryStatus
  usable: boolean
}

export interface DirectoryEntry extends DirectoryOption {
  classification?: string
  district?: string
  qualification?: string
  validUntil?: string
  institutionId?: string
  institutionName?: string
  certificateNo?: string
  version?: number
  canEdit: boolean
  systemCount?: number
  inReviewCount?: number
}

export interface DirectoryHistory {
  sequence: number
  version: number
  action: string
  actor: string
  changedAt: string
  before?: DirectoryEntry
  after?: DirectoryEntry
}

export interface DirectorySnapshot {
  unit?: Pick<DirectoryEntry, 'id' | 'name' | 'scopeName' | 'code'>
  institution?: Pick<DirectoryEntry, 'id' | 'name' | 'scopeName' | 'code' | 'qualification' | 'validUntil'>
  person?: Pick<DirectoryEntry, 'id' | 'name' | 'code' | 'certificateNo' | 'qualification' | 'institutionId'>
}

const directoryNames = { units: 'unit', institutions: 'institution', people: 'person' } as const

/**
 * 用途 根据目录页签找出名录业务类型。
 * 参数 collection 为当前活动的名录页签。
 * 返回值 候选查询使用的单数目录类型。
 */
export function directoryKind(collection: DirectoryCollection): DirectoryKind {
  return directoryNames[collection]
}

/**
 * 用途 生成按名称、工号或证书查询的候选地址。
 * 参数 kind 为目录类型，institution 为机构业务范围，q 为筛选关键词。
 * 返回值 同源候选请求路径。
 */
export function directoryOptionPath(kind: DirectoryKind, institution: string, q: string) {
  const filters = new URLSearchParams({ kind })
  if (institution) filters.set('institution', institution)
  if (q.trim()) filters.set('q', q.trim())
  return '/directory/options?' + filters.toString()
}

/**
 * 用途 获取选择器保存的稳定业务值。
 * 参数 kind 为目录类型，item 为当前选项。
 * 返回值 人员使用 UUID，单位或机构使用稳定业务名称。
 */
export function directoryOptionValue(kind: DirectoryKind, item: DirectoryOption) {
  return kind === 'person' ? item.id ?? '' : item.scopeName || item.name
}

/**
 * 用途 显示目录记录当前可用性。
 * 参数 item 为服务端返回的目录记录。
 * 返回值 区分现行记录、未建档、停用以及有效期不满足的状态文案。
 */
export function directoryStatusText(item: DirectoryEntry) {
  if (!item.id) return '待完善'
  if (item.status === 'inactive') return '已停用'
  return item.usable ? '有效' : '资质无效'
}

/**
 * 用途 根据业务办理当时固定的名录展示归属。
 * 参数 snapshot 为保存于报备、报告或审核记录的业务快照。
 * 返回值 可展示的单位、机构和人员名称，未引用名录的历史记录返回空字符串。
 */
export function directorySnapshotLabel(snapshot?: DirectorySnapshot) {
  return [snapshot?.unit?.name, snapshot?.institution?.name, snapshot?.person?.name].filter(Boolean).join(' · ')
}

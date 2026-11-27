export const expiryStatuses = { valid: '有效', due: '即将到期', expired: '已过期', suspended: '归属变化，已暂停', retired: '已结束登记' } as const

export interface ExpiryRenewal {
  id: string
  audience: 'operator' | 'institution'
  status: 'pending' | 'approved' | 'rejected'
  validUntil: string
  evidence?: string
  fileName?: string
  size?: number
  download?: boolean
  submittedAt: string
}

export interface ExpiryEvent {
  version: number
  cycle: number
  action: string
  evidence: string
  validUntil: string
  actor: string
  createdAt: string
}

export interface ExpirySummary {
  id: string
  caseId: string
  caseKind: 'filing' | 'report'
  number: string
  systemName: string
  organizationName: string
  institutionName?: string
  attachmentKind: string
  validUntil: string
  warningDays: number
  dateStatus: 'valid' | 'due' | 'expired' | 'suspended' | 'retired'
  renewalPending: boolean
  version: number
}

export interface ExpiryDetail extends ExpirySummary {
  sourceAttachmentId?: string
  sourceFileName: string
  evidence: string
  allowedActions: string[]
  history: ExpiryEvent[]
  renewals: ExpiryRenewal[]
}


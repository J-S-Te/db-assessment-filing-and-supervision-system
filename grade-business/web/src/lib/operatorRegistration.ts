export const applicantRegistrationPath = '/auth/operator-registrations'
export const reviewRegistrationPath = '/operator-registrations'

export interface RegistrationArea {
  code: string
  label: string
}

export const registrationStatuses = {
  pending: '待审核',
  approved: '已通过',
  rejected: '已驳回',
}

export interface RegistrationResult {
  status: keyof typeof registrationStatuses
  createdAt: string
  reviewedAt?: string
  reason?: string
}

export interface RegistrationReceipt extends RegistrationResult {
  id: string
  lookupCode: string
}

export interface RegistrationItem extends RegistrationResult {
  id: string
  unitName: string
  creditCode: string
  contactName: string
  phone: string
  username: string
  areaCode: string
  district: string
  requestedDistrict?: string
  pendingCount?: number
  reviewer?: string
  reviewerRole?: 'ROLE_SYS_ADMIN' | 'ROLE_REGULATOR'
  subjectId?: string
}

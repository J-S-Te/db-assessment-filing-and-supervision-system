export const impactSubjects = {
  rights: '个人与组织合法权益',
  public: '社会秩序与公共利益',
  national: '国家安全',
} as const

export const impactDamages = {
  general: '一般损害',
  serious: '严重损害',
  severe: '特别严重损害',
} as const

export type Impact = {
  subject: keyof typeof impactSubjects
  damage: keyof typeof impactDamages
}

export type ClassificationInput = {
  information: Impact[]
  service: Impact[]
  basis: string
  competentAuthority?: string
  filingAuthority?: string
}

export type ClassificationAssessment = ClassificationInput & {
  standard: string
  informationLevel: number
  serviceLevel: number
  suggestedLevel: number
  expertReview: boolean
}

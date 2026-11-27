import type { Risk } from './domain'

export const operatorAssessmentPath = '/operator/assessment-history'

export const assessmentConclusions = {
  conforming: '符合',
  basically_conforming: '基本符合',
  nonconforming: '不符合',
} as const

export type AssessmentConclusion = keyof typeof assessmentConclusions

export interface AssessmentSystem {
  id: string
  number: string
  systemName: string
}

export interface AssessmentSummary {
  score: number
  conclusion: AssessmentConclusion
  high: number
  medium: number
  low: number
}

export interface AssessmentPoint {
  id: string
  number: string
  assessmentOrg: string
  risk: Risk
  assessedAt: string
  result?: AssessmentSummary
}

export interface AssessmentArchive {
  system: AssessmentSystem
  items: AssessmentPoint[]
  nextCursor?: string
}

export interface AssessmentComparison {
  from: AssessmentPoint
  to: AssessmentPoint
  riskChange: 'increased' | 'decreased' | 'same'
  scoreDelta?: number
  highDelta?: number
  mediumDelta?: number
  lowDelta?: number
}

export interface AnnualAssessment {
  year: number
  reportNumber?: string
  score?: number
}

export interface IssueComparison {
  fromAvailable: boolean
  toAvailable: boolean
  items: {
    description: string
    change: 'recurring' | 'added' | 'not_listed'
    fromRisk?: Risk
    toRisk?: Risk
  }[]
}

export interface AssessmentRevision extends AssessmentSummary {
  version: number
  evidenceId: string
  actor: string
  recordedAt: string
}

export interface AssessmentEvidence {
  id: string
  fileName: string
  kind: 'assessment_conclusion' | 'report_file'
}

export interface AssessmentEditor {
  result?: AssessmentRevision
  evidence: AssessmentEvidence[]
  editable: boolean
}

export interface AssessmentRevisionsPage {
  items: AssessmentRevision[]
  nextBefore?: number
}

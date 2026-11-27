export const filingPolicyStages = { initial: '备案首报', change: '运营者备案变更', cancel: '运营者备案注销' } as const
export type FilingPolicyStage = keyof typeof filingPolicyStages

export const filingPolicyKinds: Record<FilingPolicyStage, Record<string, string>> = {
  initial: { filing_form: '备案表', classification_report: '定级报告', filing_package: '备案材料 ZIP', supporting: '其他备案材料' },
  change: { change_statement: '变更说明', supporting: '其他变更材料' },
  cancel: { cancellation_statement: '注销说明', supporting: '其他注销材料' },
}

export type FilingMaterialRule = {
  stage: FilingPolicyStage
  kind: string
  label: string
  filenameContains?: string
}

export type FilingMaterialPolicy = { version: number; rules: FilingMaterialRule[] }

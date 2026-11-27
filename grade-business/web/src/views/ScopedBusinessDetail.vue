<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { ArrowLeft, Download, Upload } from '@lucide/vue'
import { useRoute } from 'vue-router'
import InstitutionFilingReview from '../components/InstitutionFilingReview.vue'
import FilingWithdrawalPanel from '../components/FilingWithdrawalPanel.vue'
import ImplementationPrecheck from '../components/ImplementationPrecheck.vue'
import FilingWordPreview from '../components/FilingWordPreview.vue'
import ClassificationSummary from '../components/ClassificationSummary.vue'
import FilingPolicyMaterials from '../components/FilingPolicyMaterials.vue'
import RemediationMaterialTemplates from '../components/RemediationMaterialTemplates.vue'
import ScopedReviewDeadline from '../components/ScopedReviewDeadline.vue'
import AsyncState from '../components/AsyncState.vue'
import ScopedBusinessCorrection from '../components/ScopedBusinessCorrection.vue'
import { api, ApiError, attachmentURL, errorText, isAbort, params } from '../lib/api'
import { filingReviewLabel, formatSize, formatTime, knownActions, levels, materials, risks, type Attachment, type Page, type Review } from '../lib/domain'
import { canPreviewFilingWord, filingPackageKind, isFilingWordKind, materialRecoveryErrors, missingCaseMaterials } from '../lib/materialIssues'
import { applicationMaterials, applicationTypes, displayFilingValue, filingFields, type FilingApplicationReview } from '../lib/filingApplications'
import { hazardEventLabel, hazardSources, type HazardEvent } from '../lib/hazards'
import { formatImplementationRuleTime, implementationActionLabel, implementationStatuses, implementationTypes,
  watchImplementationRuleTimes, type ImplementationEvent } from '../lib/implementations'
import { queryValue } from '../lib/paging'
import { remediationMaterials, remediationRequiredMaterials, type RemediationReview } from '../lib/remediations'
import {
  acceptsResource, businessBadge, businessResources, businessScopes, businessStatus, scopedBusinessPath,
  type BusinessAudience, type BusinessDetails, type BusinessRecord, type BusinessResource,
} from '../lib/scopedBusiness'
import { sendRetestReminder } from '../lib/workbench'

const props = defineProps<{ audience: BusinessAudience }>()
const route = useRoute()
const operator = computed(() => props.audience === 'operator')
const scopeKey = computed(() => businessScopes[props.audience].key)
const scope = computed(() => queryValue(route.query[scopeKey.value]).trim())
const query = computed(() => params({ [scopeKey.value]: scope.value }))
const resource = computed(() => {
  const kind = route.params.resource
  return acceptsResource(kind, props.audience) ? kind : undefined
})
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const path = computed(() => id.value && resource.value
  ? scopedBusinessPath(props.audience, resource.value, id.value) : '')
const backApplicationId = computed(() => props.audience === 'operator' && resource.value === 'implementations'
  ? queryValue(route.query.applicationId) : '')
const backLink = computed(() => ({
  path: backApplicationId.value ? '/operator/filing-applications/' + encodeURIComponent(backApplicationId.value)
    : '/' + props.audience + '/' + (resource.value ?? 'feedback'),
  query: resource.value === 'hazards' ? {
    organization: scope.value || undefined, planId: queryValue(route.query.planId) || undefined,
    filingId: queryValue(route.query.filingId) || undefined, q: queryValue(route.query.q) || undefined,
    severity: queryValue(route.query.severity) || undefined, status: queryValue(route.query.status) || undefined,
    due: queryValue(route.query.due) || undefined, cursor: queryValue(route.query.cursor) || undefined,
  } : { [scopeKey.value]: scope.value || undefined,
    review: !operator.value && queryValue(route.query.review) === 'assigned' ? 'assigned' : undefined },
}))
const title = computed(() => resource.value ? businessResources[resource.value].label : '业务详情')
const record = shallowRef<BusinessRecord>()
const editableRecord = computed(() => record.value?.resource !== 'hazards' ? record.value : undefined)
const files = shallowRef<Attachment[]>([])
type BusinessEvent = Review | FilingApplicationReview | ImplementationEvent | RemediationReview | HazardEvent
const history = shallowRef<BusinessEvent[]>([])
const loading = ref(false)
const operation = ref<'save' | 'upload' | 'submit' | 'withdraw' | 'remind'>()
const confirmedAction = ref<'submit' | 'withdraw' | 'remind'>('submit')
const editing = ref(false)
const stale = ref(false)
const failure = ref('')
const filesError = ref('')
const historyError = ref('')
const actionError = ref('')
const showMaterialRecovery = ref(false)
const notice = ref('')
const kind = ref('')
const comment = ref('')
const fileInput = ref<HTMLInputElement>()
const uploadVersion = ref<number>()
const dialog = ref<HTMLDialogElement>()
const locked = computed(() => Boolean(operation.value || loading.value || stale.value || failure.value))
const fileKinds: Partial<Record<BusinessResource, Record<string, string>>> = {
  filings: materials.filings, remediations: remediationMaterials, hazards: { evidence: '整改证据' },
}
const historySupported = computed(() => operator.value
  || resource.value === 'filings' || resource.value === 'filing-applications')
const canReviewFiling = computed(() => props.audience === 'institution'
  && (record.value?.resource === 'filings' || record.value?.resource === 'filing-applications')
  && record.value.detail.allowedActions.includes('approve'))
const phaseLabel = computed(() => {
  const item = record.value
  if (item?.resource === 'filings' || item?.resource === 'filing-applications') {
    return filingReviewLabel(item.detail.status, item.detail.initialReviewAudience)
  }
})
const applicationType = computed(() => record.value?.resource === 'filing-applications' ? record.value.detail.type : undefined)
const fileOptions = computed(() => {
  if (resource.value === 'filing-applications' && (operator.value
    || (record.value?.resource === 'filing-applications' && record.value.detail.requiredMaterials?.length))) {
    return applicationType.value === 'cancel'
      ? { cancellation_statement: applicationMaterials.cancellation_statement, supporting: applicationMaterials.supporting }
      : { change_statement: applicationMaterials.change_statement, supporting: applicationMaterials.supporting }
  }
  return resource.value ? fileKinds[resource.value] : undefined
})
const canEdit = computed(() => Boolean(editableRecord.value?.detail.allowedActions?.includes('edit')))
const canSubmit = computed(() => Boolean(record.value?.detail.allowedActions?.includes('submit')))
const canWithdraw = computed(() => (record.value?.detail.allowedActions as readonly string[] | undefined)
  ?.includes('withdraw') ?? false)
const canRemind = computed(() => operator.value && record.value?.resource === 'hazards'
  && record.value.detail.status === 'pending_retest')
const canDownloadReceipt = computed(() => operator.value && record.value
  && 'receiptAvailable' in record.value.detail && record.value.detail.receiptAvailable)
const canStartApplication = computed(() => operator.value && record.value?.resource === 'filings'
  && record.value.detail.status === 'approved' && !record.value.detail.cancelledAt)
const retestTarget = computed(() => record.value?.resource === 'filing-applications'
  ? record.value.detail.changes?.find(change => change.field === 'assessmentOrg')?.after : undefined)
const relatedImplementation = computed(() => {
  if (record.value?.resource !== 'filing-applications') return
  const { implementationId, retestImplementation } = record.value.detail
  return implementationId ? { id: implementationId, status: 'approved' as const } : retestImplementation
})
const retestWaiting = computed(() => operator.value && Boolean(retestTarget.value)
  && relatedImplementation.value?.status !== 'approved')
const retestPath = computed(() => {
  if (!operator.value || record.value?.resource !== 'filing-applications' || !retestTarget.value) return
  const applicationId = id.value
  return relatedImplementation.value
    ? { path: '/operator/implementations/' + encodeURIComponent(relatedImplementation.value.id),
      query: { organization: scope.value, applicationId } }
    : { path: '/operator/implementations/new',
      query: { organization: scope.value, filingId: record.value.detail.filingId,
        target: retestTarget.value, applicationId } }
})
const isDraft = computed(() => record.value?.detail.status === 'draft')
const draftEditRoute = computed(() => path.value && isDraft.value && canEdit.value &&
  (resource.value === 'implementations' || resource.value === 'remediations')
  ? { path: path.value + '/edit', query: { [scopeKey.value]: scope.value } } : undefined)
const canUpload = computed(() => Boolean(fileOptions.value
  && (resource.value !== 'filing-applications' || operator.value)
  && (resource.value === 'hazards' ? canSubmit.value : canEdit.value)))
const uploadCategories = computed(() => Object.entries(fileOptions.value ?? {})
  .filter(([name]) => name !== filingPackageKind || !operator.value))
const submitLabel = computed(() => resource.value === 'hazards' ? '申请机构复测'
  : resource.value === 'implementations' ? '提交报备' : '提交审核')
const missingMaterials = computed(() => {
  const item = record.value
  const required = item?.resource === 'filings' ? (item.detail.requiredMaterials ?? ['filing_form', 'classification_report'])
    : item?.resource === 'filing-applications' ? (item.detail.requiredMaterials ?? [])
    : item?.resource === 'remediations' ? remediationRequiredMaterials : []
  return missingCaseMaterials(required, files.value)
})
const confirmLabel = computed(() => confirmedAction.value === 'withdraw' ? '撤回申请'
  : confirmedAction.value === 'remind' ? '确认复测催办' : submitLabel.value)
const confirmHelp = computed(() => confirmedAction.value === 'withdraw'
  ? resource.value === 'implementations'
    ? '撤销后该报备停止办理，已提交的材料与历史仍可查询。同一备案在 15 天内不能重新提交报备。'
    : '确认撤回后停止办理，已提交材料和办理历史仍可查询。'
  : confirmedAction.value === 'remind' ? '向负责本轮复测的测评机构发送站内催办。'
    : '确认提交后，业务将进入下一审核阶段。')
let readRequest: AbortController | undefined
let actionRequest: AbortController | undefined
let interval: ReturnType<typeof setInterval> | undefined

/**
 * 用途 同步详情、版本、服务端允许的操作和当前材料。
 * 参数 quiet 表示前台自动检查，可继续展示现有内容。
 * 返回值 当前详情及附件的加载任务。
 */
async function load(quiet = false) {
  readRequest?.abort()
  const currentResource = resource.value
  if (!currentResource || !path.value || !scope.value) {
    readRequest = undefined
    loading.value = false
    return
  }
  const request = new AbortController()
  const base = path.value
  const suffix = query.value
  readRequest = request
  loading.value = !quiet
  const options = { signal: request.signal }
  const [detail, attachments, events] = await Promise.allSettled([
    api<BusinessDetails[typeof currentResource]>(base + suffix, options),
    fileOptions.value || currentResource === 'filing-applications'
      ? api<Page<Attachment>>(base + '/attachments' + suffix, options) : undefined,
    historySupported.value
      ? api<Page<BusinessEvent>>(base + (currentResource === 'hazards' ? '/history' : '/review-history') + suffix, options)
      : undefined,
  ])
  if (request.signal.aborted || readRequest !== request) return
  readRequest = undefined
  loading.value = false
  if (detail.status === 'rejected') {
    failure.value = errorText(detail.reason)
    if (detail.reason instanceof ApiError && [403, 404].includes(detail.reason.status)) {
      record.value = undefined
      files.value = []
    }
    return
  }
  record.value = { resource: currentResource, detail: detail.value } as BusinessRecord
  if (!fileOptions.value?.[kind.value]) kind.value = Object.keys(fileOptions.value ?? {})[0] ?? ''
  stale.value = false
  failure.value = ''
  filesError.value = attachments.status === 'rejected' ? errorText(attachments.reason) : ''
  if (attachments.status === 'fulfilled') files.value = attachments.value?.items ?? []
  historyError.value = events.status === 'rejected' ? errorText(events.reason) : ''
  if (events.status === 'fulfilled') history.value = events.value?.items ?? []
}

/**
 * 用途 切换范围或记录时清除旧状态并加载新详情。
 * 参数 无，使用当前路由上的业务记录及范围。
 * 返回值 新详情的加载任务。
 */
function openRecord() {
  actionRequest?.abort()
  actionRequest = undefined
  dialog.value?.close()
  record.value = undefined
  files.value = []
  uploadVersion.value = undefined
  history.value = []
  failure.value = filesError.value = historyError.value = actionError.value = notice.value = comment.value = ''
  showMaterialRecovery.value = false
  operation.value = undefined
  confirmedAction.value = 'submit'
  editing.value = stale.value = false
  kind.value = Object.keys(fileOptions.value ?? {})[0] ?? ''
  return load()
}

/**
 * 用途 进入当前允许编辑的补正表单，并保留该版本。
 * 参数 无。
 * 返回值 无。
 */
function startEdit() {
  if (locked.value || !canEdit.value) return
  readRequest?.abort()
  readRequest = undefined
  dialog.value?.close()
  notice.value = actionError.value = ''
  editing.value = true
}

/**
 * 用途 初审完成后显示已保存结果并读取新的办理阶段。
 * 参数 无，当前记录和业务范围由详情页保持。
 * 返回值 最新状态的加载任务。
 */
function refreshAfterReview() {
  notice.value = '机构初审结果已保存。'
  return load()
}

/**
 * 用途 退出编辑或冲突状态，按服务端当前版本更新页面。
 * 参数 无。
 * 返回值 最新详情的加载任务。
 */
function reload() {
  if (operation.value) return
  dialog.value?.close()
  if (resource.value === 'hazards') {
    if (fileInput.value) fileInput.value.value = ''
    uploadVersion.value = undefined
  }
  editing.value = false
  actionError.value = ''
  return load()
}

/**
 * 用途 执行当前范围内的编辑、上传或状态流转并刷新服务端结果。
 * 参数 action 为当前动作，endpoint 为动作路径后缀，body 为实际请求字段，success 为成功提示。
 * 返回值 办理与结果刷新任务。
 */
async function mutate(action: Exclude<NonNullable<typeof operation.value>, 'remind'>,
  endpoint: string, body: object, success: string) {
  readRequest?.abort()
  readRequest = undefined
  const request = new AbortController()
  actionRequest = request
  operation.value = action
  actionError.value = notice.value = ''
  showMaterialRecovery.value = false
  try {
    await api(path.value + endpoint + query.value, {
      method: action === 'save' ? 'PATCH' : 'POST', body, signal: request.signal,
      expectedVersion: action === 'upload' && resource.value === 'hazards' ? uploadVersion.value : undefined,
      materialKind: action === 'upload' && !operator.value && resource.value === 'filings' ? kind.value : undefined,
    })
    if (request.signal.aborted) return
    if (action === 'save') editing.value = false
    if (action === 'submit' || action === 'withdraw') {
      dialog.value?.close()
      comment.value = ''
    }
    if (action === 'upload' && fileInput.value) fileInput.value.value = ''
    notice.value = success
    await load()
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    if (action === 'submit' || action === 'withdraw') dialog.value?.close()
    actionError.value = errorText(error)
    showMaterialRecovery.value = !operator.value && resource.value === 'filings' &&
      error instanceof ApiError && materialRecoveryErrors.has(error.code)
    stale.value = error instanceof ApiError && error.status === 409
      && error.code !== 'retest_required' && error.code !== 'implementation_required'
      && error.code !== 'material_issues_open'
  } finally {
    if (actionRequest === request) {
      actionRequest = undefined
      operation.value = undefined
    }
  }
}

/**
 * 用途 从整改单向当前轮次的测评机构催办复测。
 * 参数 无，整改单标识和运营单位来自已加载详情。
 * 返回值 发送结果及必要的状态同步任务。
 */
async function remindRetest() {
  if (locked.value || !canRemind.value || !scope.value) return
  const request = new AbortController()
  actionRequest = request
  operation.value = 'remind'
  notice.value = actionError.value = ''
  try {
    const message = await sendRetestReminder(scope.value, id.value, request.signal)
    if (request.signal.aborted) return
    notice.value = message
    dialog.value?.close()
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    actionError.value = errorText(error)
    dialog.value?.close()
    if (error instanceof ApiError && [404, 409].includes(error.status)) await load(true)
  } finally {
    if (actionRequest === request) {
      actionRequest = undefined
      operation.value = undefined
    }
  }
}

/**
 * 用途 保存通过本地校验的补正字段。
 * 参数 body 为与当前详情版本对应的编辑请求。
 * 返回值 保存和重新读取详情的任务。
 */
function save(body: object) {
  if (locked.value || !editing.value || !canEdit.value) return
  return mutate('save', '', body, isDraft.value ? '草稿已保存。' : '补正内容已保存。')
}

/**
 * 用途 向当前备案、整改规划或隐患登记材料。
 * 参数 无，使用表单中的文件及分类。
 * 返回值 上传和重新读取材料的任务。
 */
function upload() {
  if (locked.value || editing.value || !canUpload.value || !record.value) return
  const file = fileInput.value?.files?.[0]
  if (!file || !fileOptions.value?.[kind.value]) {
    actionError.value = '请选择材料类别和文件。'
    return
  }
  const institutionFiling = !operator.value && record.value.resource === 'filings'
  const legacyWord = record.value.resource === 'filings' && isFilingWordKind(kind.value)
  const accepted = /\.(pdf|docx|xlsx|zip)$/i.test(file.name) || legacyWord && /\.doc$/i.test(file.name)
  if (!institutionFiling && (!file.size || !accepted)) {
    actionError.value = legacyWord ? '请选择非空的 PDF、DOC 或 DOCX 备案材料。'
      : '请选择非空的 PDF、DOCX、XLSX 或 ZIP 文件。'
    return
  }
  const body = new FormData()
  body.append('kind', kind.value)
  body.append('file', file)
  if (record.value.resource === 'remediations') body.append('reportId', record.value.detail.reportId)
  return mutate('upload', '/attachments', body, '材料已登记。')
}

/**
 * 用途 打开当前记录的办理确认框。
 * 参数 action 为提交、撤回或复测催办，仅当前允许的动作可进入确认。
 * 返回值 无。
 */
function confirmAction(action: NonNullable<typeof confirmedAction.value>) {
  if (locked.value || editing.value) return
  if (action === 'submit' && (!canSubmit.value || missingMaterials.value.length || retestWaiting.value)) return
  if (action === 'withdraw' && !canWithdraw.value) return
  if (action === 'remind' && !canRemind.value) return
  readRequest?.abort()
  readRequest = undefined
  actionError.value = ''
  confirmedAction.value = action
  dialog.value?.showModal()
}

/**
 * 用途 执行已确认的业务动作，隐患提交时同时登记整改说明。
 * 参数 无，办理内容来自当前详情和确认表单。
 * 返回值 办理和重新读取状态的任务。
 */
function submitDecision() {
  if (locked.value || editing.value || !record.value) return
  if (confirmedAction.value === 'remind') return remindRetest()
  const body = { expectedVersion: record.value.detail.version }
  const current = record.value.resource
  if (confirmedAction.value === 'withdraw' && canWithdraw.value) {
    return current === 'implementations' && !operator.value
      ? mutate('withdraw', '/actions', { ...body, action: 'withdraw' }, '实施报备已撤回。')
      : mutate('withdraw', '/withdraw', body, '申请已撤回。')
  }
  if (confirmedAction.value !== 'submit' || !canSubmit.value || missingMaterials.value.length || retestWaiting.value) return
  if (current === 'hazards') {
    const note = comment.value.trim()
    if (!note) return
    return mutate('submit', '/actions', { ...body, action: 'submit', comment: note }, '已提交机构复测。')
  }
  if (current === 'implementations' && !operator.value) {
    return mutate('submit', '/actions', { ...body, action: 'submit' }, '实施报备已提交。')
  }
  return mutate('submit', '/submit', body,
    current === 'implementations' ? '实施报备已提交。' : '已提交审核。')
}

/**
 * 用途 在页面可见且没有本地正在办理的操作时同步状态。
 * 参数 无。
 * 返回值 无。
 */
function refreshVisible() {
  if (document.visibilityState === 'visible' && !readRequest && !actionRequest
    && !editing.value && !stale.value && !dialog.value?.open) void load(true)
}

/**
 * 用途 根据事件发生前的状态显示实际办理动作及所在审核环节。
 * 参数 event 为服务端记录的业务流转事件。
 * 返回值 用于审核时间线的动作名称。
 */
function eventLabel(event: BusinessEvent) {
  if (resource.value === 'implementations') {
    const implementation = event as ImplementationEvent
    return implementationActionLabel(implementation.action, implementation.fromStatus)
  }
  if (resource.value === 'hazards') return hazardEventLabel(event as HazardEvent) ?? event.action
  if (event.action === 'withdraw') return '撤回申请'
  if (event.action === 'approve') {
    return event.toStatus === 'pending_final' ? '初审通过' : '终审通过'
  }
  return knownActions[event.action] ?? event.action
}

/**
 * 用途 把对应业务的详情字段转换为可展示的信息项。
 * 参数 item 为已核对归属的业务记录。
 * 返回值 业务相关的标签与字段值。
 */
function facts(item: BusinessRecord): Array<[string, string | undefined]> {
  const base: Array<[string, string]> = [['所属单位', item.detail.organizationName]]
  if (item.resource === 'filings') {
    const { number, assessmentOrg, level, submittedAt } = item.detail
    const lastSubmission = submittedAt ?? history.value.find(event => event.action === 'submit')?.occurredAt
    return [...base, ['备案编号', number], ['测评机构', assessmentOrg],
      ['定级', level ? levels[level] : undefined], ['提交时间', formatTime(lastSubmission)]]
  }
  if (item.resource === 'filing-applications') {
    const { filingNumber, type, reason, createdAt } = item.detail
    return [...base, ['备案编号', filingNumber], ['申请类型', applicationTypes[type]],
      ['申请时间', formatTime(createdAt)], ['申请理由', reason]]
  }
  if (item.resource === 'implementations') {
    const { filingNumber, assessmentOrg, type, reporter, contact, plannedStart, plannedEnd, plan, directorySnapshot } = item.detail
    const snapshot = props.audience === 'institution' ? directorySnapshot : undefined
    return [['所属单位', snapshot?.unit?.name || item.detail.organizationName], ['备案编号', filingNumber],
      ['测评机构', snapshot?.institution?.name || assessmentOrg], ['报备类型', implementationTypes[type]],
      ['报备人员', reporter], ['联系电话', contact], ['计划开始', plannedStart], ['计划完成', plannedEnd],
      ['实施计划与范围', plan]]
  }
  const { reportNumber, filingNumber, deadline } = item.detail
  const common: Array<[string, string]> = [...base, ['备案编号', filingNumber], ['整改期限', deadline]]
  if (reportNumber) common.splice(common.length - 1, 0, ['测评报告', reportNumber])
  if (item.resource === 'remediations') {
    return [...common, ['提交时间', formatTime(item.detail.submittedAt)]]
  }
  const { assessmentOrg, closedAt, description, discoveredOn, dispatchedAt, ledger, measure,
    responsibility, severity, source } = item.detail
  if (assessmentOrg) common.push(['测评机构', assessmentOrg])
  if (source) common.push(['来源', hazardSources[source]])
  if (discoveredOn) common.push(['发现日期', discoveredOn])
  if (dispatchedAt) common.push(['派发时间', formatTime(dispatchedAt)])
  return [...common, ['整改单号', item.detail.id], ['风险等级', risks[severity]],
    [ledger === 'major_risk' ? '处置人或部门' : '责任人或部门', responsibility],
    ['隐患说明', description], ['整改措施', measure], ['销项时间', closedAt && formatTime(closedAt)]]
}

watch([path, query], openRecord, { immediate: true })
watchImplementationRuleTimes(() => record.value?.resource === 'implementations' ? record.value.detail : undefined,
  refreshVisible)
onMounted(() => {
  interval = setInterval(refreshVisible, 10_000)
  document.addEventListener('visibilitychange', refreshVisible)
})
onScopeDispose(() => {
  readRequest?.abort()
  actionRequest?.abort()
  clearInterval(interval)
  document.removeEventListener('visibilitychange', refreshVisible)
})
</script>

<template>
  <section class="scoped-detail" :aria-label="title">
    <RouterLink class="back-link" :to="backLink">
      <ArrowLeft :size="16" aria-hidden="true" />{{ resource === 'hazards' ? '返回整改单' : backApplicationId ? '返回备案变更申请' : '返回业务列表' }}
    </RouterLink>
    <p v-if="!path" class="form-message warning-message">当前入口无法打开此类业务。</p>
    <p v-else-if="!scope" class="form-message warning-message">请先在业务列表选择办理范围。</p>
    <AsyncState v-else :loading="loading && !record" :error="record ? '' : failure" @retry="load()">
      <template v-if="record">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>{{ (record.resource === 'hazards' && record.detail.title) || record.detail.systemName }}</h1>
              <span v-if="record.resource === 'hazards' && record.detail.ledger === 'major_risk'"
                class="level-tag">重大风险</span>
              <span v-if="record.resource === 'filings' && record.detail.cancelledAt" class="level-tag">已注销</span>
              <span v-else class="status-badge" :class="businessBadge(record.detail.status)">
                <span aria-hidden="true"></span>{{ phaseLabel ?? businessStatus(record.resource, record.detail.status) }}
              </span>
            </div>
            <p>{{ title }} · {{ scope }}<template v-if="record.resource === 'hazards' && record.detail.title">
              · {{ record.detail.systemName }}</template></p>
          </div>
          <div v-if="!editing" class="page-actions">
            <InstitutionFilingReview v-if="canReviewFiling" :path="path" :suffix="query"
              :version="record.detail.version" :disabled="locked" @saved="refreshAfterReview" @reload="reload" />
            <a v-if="canDownloadReceipt" class="button neutral" :href="'/api/v1' + path + '/receipt' + query" download>
              <Download :size="16" aria-hidden="true" />下载回执
            </a>
            <RouterLink v-if="operator && record.resource === 'remediations' && record.detail.status === 'approved'"
              class="button neutral" :to="{ path: '/operator/hazards', query: { organization: scope, planId: id } }">
              查看整改单
            </RouterLink>
            <template v-if="canStartApplication">
              <RouterLink v-for="type in (['change', 'cancel'] as const)" :key="type" class="button neutral"
                :to="{ path: '/operator/filing-applications/new', query: { organization: scope, filingId: id, type } }">
                发起{{ applicationTypes[type] }}
              </RouterLink>
            </template>
            <RouterLink v-if="draftEditRoute && !locked" class="button neutral" :to="draftEditRoute">编辑草稿</RouterLink>
            <button v-else-if="canEdit" class="button neutral" type="button" :disabled="locked"
              @click="startEdit">{{ isDraft ? '编辑草稿' : '编辑补正' }}</button>
            <button v-if="canWithdraw" class="button neutral" type="button" :disabled="locked"
              @click="confirmAction('withdraw')">撤回申请</button>
            <button v-if="canRemind" class="button neutral" type="button" :disabled="locked"
              @click="confirmAction('remind')">催办复测</button>
            <button v-if="canSubmit" class="button primary" type="button" :disabled="locked || Boolean(missingMaterials.length) || retestWaiting"
              @click="confirmAction('submit')">{{ submitLabel }}</button>
          </div>
        </header>
        <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
        <p v-if="actionError" class="form-message error-message" role="alert">
          {{ actionError }}
          <RouterLink v-if="showMaterialRecovery" class="text-action"
            :to="{ path: '/institution/alerts', query: { institution: scope, view: 'materials' } }">查看材料异常</RouterLink>
          <button v-if="stale" class="text-action" type="button" @click="reload">
            {{ editing ? '放弃编辑并刷新' : '刷新详情' }}
          </button>
        </p>
        <p v-if="failure" class="form-message warning-message" role="status">
          {{ failure }}
          <button class="text-action" type="button" :disabled="Boolean(operation)" @click="reload">刷新详情</button>
        </p>
        <ScopedReviewDeadline v-if="resource" :audience="audience" :scope="scope" :resource="resource" :id="id"
          :version="record.detail.version" />
        <template v-if="record.resource === 'implementations'">
          <p v-if="record.detail.resubmitAvailableAt" class="form-message warning-message" role="status">
            本备案的撤销冷却期截至 {{ formatImplementationRuleTime(record.detail.resubmitAvailableAt) }}。
          </p>
          <p v-if="canEdit && record.detail.planEditDeadline" class="muted">
            计划修改截止于 {{ formatImplementationRuleTime(record.detail.planEditDeadline) }}。
          </p>
        </template>
        <FilingWithdrawalPanel v-if="!editing && (record.resource === 'filings' || record.resource === 'filing-applications')
          && record.detail.initialReviewAudience" :path="path" :suffix="query" :version="record.detail.version"
          :disabled="locked" @changed="reload" />

        <p v-if="canSubmit && missingMaterials.length && !filesError && !loading" class="form-message warning-message">
          待上传 {{ missingMaterials.map(kind => fileOptions?.[kind] ?? kind).join('、') }}
        </p>
        <div class="detail-main">
          <ScopedBusinessCorrection v-if="editing && editableRecord" :record="editableRecord"
            :query="query" :audience="audience" :busy="Boolean(operation)" :stale="locked && !operation"
            @save="save" @cancel="reload" />
          <section v-if="!editing" class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>业务信息</h2></header>
            <dl class="facts">
              <div v-for="[label, value] in facts(record)" :key="label">
                <dt>{{ label }}</dt><dd class="scoped-value">{{ value || '—' }}</dd>
              </div>
            </dl>
          </section>

          <ClassificationSummary v-if="!editing && record.resource === 'filings' && record.detail.classificationAssessment"
            :assessment="record.detail.classificationAssessment" :declared="record.detail.level" />
          <FilingPolicyMaterials v-if="!editing && (record.resource === 'filings' || record.resource === 'filing-applications')
            && record.detail.policyMaterials?.length" :rules="record.detail.policyMaterials" :files="files" />
          <ImplementationPrecheck v-if="!editing && record.resource === 'implementations' && record.detail.canPrecheck"
            :path="path" :filing-id="record.detail.filingId" :version="record.detail.version"
            :audience="audience" :scope="scope" :disabled="locked" @refresh="reload" />

          <section v-if="!editing && record.resource === 'filing-applications' && record.detail.changes?.length"
            class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>申请变更项</h2></header>
            <div class="table-wrap">
              <table class="data-table">
                <thead><tr><th>字段</th><th>原值</th><th>申请值</th></tr></thead>
                <tbody>
                  <tr v-for="change in record.detail.changes" :key="change.field">
                    <td>{{ filingFields[change.field] }}</td>
                    <td>{{ displayFilingValue(change.field, change.before) }}</td>
                    <td>{{ displayFilingValue(change.field, change.after) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <section v-if="!editing && record.resource === 'remediations'" class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>风险项与整改措施</h2></header>
            <div class="table-wrap">
              <table class="data-table">
                <thead><tr><th>风险等级</th><th>说明</th><th>措施</th><th>责任人或部门</th><th>期限</th></tr></thead>
                <tbody>
                  <tr v-for="(item, index) in record.detail.risks" :key="index">
                    <td>{{ item.risk ? risks[item.risk] : '—' }}</td><td class="scoped-value">{{ item.description }}</td>
                    <td class="scoped-value">{{ item.measure }}</td><td>{{ item.responsibility }}</td>
                    <td>{{ item.deadline }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
          <section v-if="!editing && retestPath" class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>换机构复测</h2></header>
            <dl class="facts">
              <div><dt>目标测评机构</dt><dd>{{ retestTarget }}</dd></div>
              <div><dt>实施报备状态</dt><dd>
                {{ relatedImplementation ? implementationStatuses[relatedImplementation.status] : '待发起' }}
              </dd></div>
            </dl>
            <div class="retest-actions">
              <span v-if="canSubmit && retestWaiting" class="muted">实施报备通过监管复核后可提交备案变更。</span>
              <RouterLink v-if="relatedImplementation || canEdit" class="button neutral" :to="retestPath">
                {{ relatedImplementation ? '查看关联实施报备' : '发起实施报备' }}
              </RouterLink>
            </div>
          </section>
          <section v-if="!editing && historySupported" class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>审核留痕</h2></header>
            <AsyncState :error="historyError" :empty="!history.length"
              empty-text="尚无提交或审核记录。" @retry="load()">
              <ol class="timeline">
                <li v-for="(event, index) in history" :key="event.occurredAt + '-' + index">
                  <span class="timeline-dot" aria-hidden="true"></span>
                  <strong>{{ eventLabel(event) }}</strong>
                  <span v-if="record.resource === 'hazards' && event.fromStatus && event.fromStatus !== event.toStatus">
                    {{ businessStatus('hazards', event.fromStatus) }} → {{ businessStatus('hazards', event.toStatus) }}
                  </span>
                  <span v-else-if="record.resource === 'hazards' && !event.fromStatus">
                    {{ businessStatus('hazards', event.toStatus) }}</span>
                  <small>{{ event.operator }} · {{ formatTime(event.occurredAt) }}</small>
                  <p v-if="event.comment">{{ event.comment }}</p>
                  <p v-if="'reason' in event && event.reason">退回原因 {{ event.reason }}</p>
                </li>
              </ol>
            </AsyncState>
          </section>

          <section v-if="fileOptions && !editing" class="panel detail-panel">
            <header class="panel-heading panel-compact">
              <h2>{{ resource === 'hazards' ? '整改证据' : '业务材料' }}</h2>
            </header>
            <RemediationMaterialTemplates v-if="operator && record.resource === 'remediations'"
              :base-path="path" :organization="scope" />
            <AsyncState :loading="loading && !files.length" :error="filesError" :empty="!files.length"
              empty-text="暂无已登记材料。" @retry="load()">
              <ul class="scoped-files">
                <li v-for="file in files" :key="file.id">
                  <div><strong>{{ file.fileName }}</strong>
                    <small class="muted">{{ fileOptions[file.kind] ?? file.kind }} · {{ formatSize(file.size) }}
                      · {{ formatTime(file.uploadedAt) }}</small>
                  </div>
                  <FilingWordPreview v-if="resource === 'filings' && canPreviewFilingWord(file)"
                    :file="file" :path="path" :query="query" />
                  <a class="text-action" :href="attachmentURL(path, file.id) + query">
                    <Download :size="16" aria-hidden="true" />下载</a>
                </li>
              </ul>
            </AsyncState>
            <form v-if="canUpload" class="scoped-upload" @submit.prevent="upload">
              <p v-if="resource === 'hazards'" class="muted">请上传本轮整改证据后申请机构复测。</p>
              <p v-if="kind === filingPackageKind" class="muted">
                ZIP 包须包含备案表和定级报告，支持 PDF、DOC 和 DOCX，通过校验后可一并满足两项必传材料。
              </p>
              <fieldset class="form-grid" :disabled="locked">
                <label v-if="resource !== 'hazards'" class="field"><span>材料类别</span>
                  <select v-model="kind" required>
                    <option v-for="[value, label] in uploadCategories" :key="value" :value="value">{{ label }}</option>
                  </select>
                </label>
                <label class="field"><span>选择文件</span>
                  <input ref="fileInput" @change="uploadVersion = record.detail.version" type="file"
                    :accept="kind === filingPackageKind ? '.zip'
                      : resource === 'filings' && isFilingWordKind(kind) ? '.pdf,.doc,.docx,.xlsx,.zip'
                      : '.pdf,.docx,.xlsx,.zip'" required>
                </label>
              </fieldset>
              <div class="form-actions">
                <button class="button neutral" type="submit" :disabled="locked">
                  <Upload :size="16" aria-hidden="true" />{{ operation === 'upload' ? '上传中…' : '上传材料' }}
                </button>
              </div>
            </form>
          </section>
        </div>
      </template>
    </AsyncState>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="scoped-submit-title"
      @cancel.prevent="!operation && dialog?.close()">
      <form @submit.prevent="submitDecision">
        <h2 id="scoped-submit-title">{{ confirmLabel }}</h2>
        <p>{{ confirmHelp }}</p>
        <label v-if="resource === 'hazards' && confirmedAction === 'submit'" class="field submission-comment"><span>整改说明</span>
          <textarea v-model="comment" required maxlength="1000" rows="4" :disabled="Boolean(operation)" />
        </label>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="Boolean(operation)" @click="dialog?.close()">取消</button>
          <button class="button primary" type="submit"
            :disabled="locked || (resource === 'hazards' && confirmedAction === 'submit' && !comment.trim())">
            {{ operation ? '处理中…' : confirmedAction === 'withdraw' ? '确认撤回'
              : confirmedAction === 'remind' ? '发送催办' : '确认提交' }}
          </button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.retest-actions { display: flex; align-items: center; gap: 12px; padding: 0 16px 16px; }
.scoped-value { white-space: pre-wrap; overflow-wrap: anywhere; }
.scoped-files { list-style: none; margin: 0; padding: 12px 16px; }
.scoped-files li { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 6px 0; }
.scoped-files li div { min-width: 0; overflow-wrap: anywhere; }
.scoped-files small { display: block; }
.scoped-upload { padding: 16px; border-top: 1px solid var(--line); }
.scoped-upload > p { margin-bottom: 10px; }
.submission-comment { margin-top: 14px; }
</style>

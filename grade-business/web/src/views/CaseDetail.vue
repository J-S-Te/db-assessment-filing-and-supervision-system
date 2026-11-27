<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, Check, ChevronRight, History, Pencil, Send } from '@lucide/vue'
import AsyncState from '../components/AsyncState.vue'
import BusinessReceiptDialog from '../components/BusinessReceiptDialog.vue'
import FilingWithdrawalPanel from '../components/FilingWithdrawalPanel.vue'
import AttachmentPanel from '../components/AttachmentPanel.vue'
import PrecheckPanel from '../components/PrecheckPanel.vue'
import ClassificationSummary from '../components/ClassificationSummary.vue'
import FilingPolicyMaterials from '../components/FilingPolicyMaterials.vue'
import ReportFeedbackReceipts from '../components/ReportFeedbackReceipts.vue'
import { filingReviewLabel } from '../lib/domain'
import StatusBadge from '../components/StatusBadge.vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { directorySnapshotLabel } from '../lib/directory'
import { refreshWorkbench } from '../lib/workbench'
import { caseLabels, formatTime, knownActions, levels, risks, statuses,
  type Attachment, type CaseDetail, type CaseKind, type CaseMutation, type Page, type Review, type ReviewAction,
} from '../lib/domain'

const props = defineProps<{ kind: CaseKind }>()
const ReportExtractionPanel = defineAsyncComponent(() => import('../components/ReportExtractionPanel.vue'))
const ReportHistory = defineAsyncComponent(() => import('../components/ReportHistory.vue'))
const ReportCompliance = defineAsyncComponent(() => import('../components/ReportCompliance.vue'))
const AssessmentResultPanel = defineAsyncComponent(() => import('../components/AssessmentResultPanel.vue'))
const route = useRoute()
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const path = computed(() => '/' + props.kind + '/' + encodeURIComponent(id.value))
const label = computed(() => caseLabels[props.kind])
const record = shallowRef<CaseDetail>()
const reportTab = ref<'overview' | 'extraction' | 'compliance' | 'comparison' | 'result' | 'history' | 'attachments'>('overview')
const attachments = shallowRef<Attachment[]>([])
const history = shallowRef<Review[]>([])
const loading = ref(false)
const attachmentsLoading = ref(false)
const historyLoading = ref(false)
const failure = ref('')
const attachmentError = ref('')
const historyError = ref('')
const dialog = ref<HTMLDialogElement>()
const command = ref<ReviewAction | 'submit'>('submit')
const comment = ref('')
const changing = ref(false)
const actionError = ref('')
const actionNotice = ref('')
const stale = ref(false)
let summaryRequest: AbortController | undefined
let attachmentRequest: AbortController | undefined
let historyRequest: AbortController | undefined
let reportRefresh: ReturnType<typeof setInterval> | undefined
const decisionText = computed(() => record.value?.status === 'pending_final' ? '终审通过' : '初审通过')
const returnedCase = computed(() => record.value?.status === 'returned')
const submitText = computed(() => returnedCase.value ? '重新提交' : '提交审核')
const returnReason = computed(() => returnedCase.value
  ? history.value.find(item => item.action === 'return_for_correction')?.comment
  : undefined)
const dialogHeading = computed(() => {
  if (command.value === 'submit') return submitText.value
  return command.value === 'approve' ? decisionText.value : knownActions[command.value]
})
const revision = computed(() => (record.value?.version ?? '') + ':' + attachments.value.map(({ id }) => id).join(','))

/**
 * 用途 刷新业务详情及当前可提交动作。
 * 参数 observe 表示前台检查，仅在版本变化并且没有确认窗口时应用结果。
 * 返回值 详情加载任务。
 */
async function loadRecord(observe = false) {
  summaryRequest?.abort()
  const next = new AbortController()
  summaryRequest = next
  loading.value = !record.value
  failure.value = ''
  try {
    const detail = await api<CaseDetail>(path.value, { signal: next.signal })
    if (summaryRequest !== next || next.signal.aborted) return
    if (observe && (dialog.value?.open || changing.value || record.value?.version === detail.version)) return
    record.value = detail
    if (observe) {
      actionNotice.value = ''
      void refreshWorkbench()
      await Promise.all([loadAttachments(), loadHistory()])
    }
  } catch (error) {
    if (summaryRequest === next && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (summaryRequest === next) {
      summaryRequest = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 获取当前记录的附件摘要。
 * 参数 无。
 * 返回值 附件加载任务。
 */
async function loadAttachments() {
  attachmentRequest?.abort()
  const next = new AbortController()
  attachmentRequest = next
  attachmentsLoading.value = true
  attachmentError.value = ''
  try {
    const page = await api<Page<Attachment>>(path.value + '/attachments', { signal: next.signal })
    if (attachmentRequest === next) attachments.value = page.items
  } catch (error) {
    if (attachmentRequest === next && !isAbort(error)) attachmentError.value = errorText(error)
  } finally {
    if (attachmentRequest === next) attachmentsLoading.value = false
  }
}

/**
 * 用途 从服务端重新获取审核时间线。
 * 参数 无。
 * 返回值 时间线加载任务。
 */
async function loadHistory() {
  historyRequest?.abort()
  const next = new AbortController()
  historyRequest = next
  historyLoading.value = true
  historyError.value = ''
  try {
    const page = await api<Page<Review>>(path.value + '/review-history', { signal: next.signal })
    if (historyRequest === next) history.value = page.items
  } catch (error) {
    if (historyRequest === next && !isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === next) historyLoading.value = false
  }
}

/**
 * 用途 并行刷新详情、材料和流转记录。
 * 参数 无。
 * 返回值 三项读取完成后的任务。
 */
function refresh() {
  return Promise.all([loadRecord(), loadAttachments(), loadHistory()])
}

/**
 * 用途 打开业务提交与意见确认窗口。
 * 参数 value 为当前记录支持的流转动作。
 * 返回值 无。
 */
function confirm(value: ReviewAction | 'submit') {
  if (changing.value || loading.value || !record.value?.allowedActions.includes(value)) return
  command.value = value
  comment.value = ''
  actionError.value = ''
  stale.value = false
  dialog.value?.showModal()
}

/**
 * 用途 结束过期确认并刷新详情，要求基于当前审核阶段重新发起动作。
 * 参数 无。
 * 返回值 详情刷新任务。
 */
function reloadForReview() {
  dialog.value?.close()
  comment.value = ''
  return refresh()
}

/**
 * 用途 按当前版本提交业务决定。
 * 参数 无，使用确认窗口的审核意见。
 * 返回值 提交任务。
 */
async function act() {
  const snapshot = record.value
  const pathAtStart = path.value
  const selected = command.value
  if (!snapshot || changing.value || stale.value || !snapshot.allowedActions.includes(selected)) return
  const note = comment.value.trim()
  if (selected === 'return_for_correction' && !note) {
    actionError.value = '请填写退回原因。'
    return
  }
  changing.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const endpoint = selected === 'submit' ? '/submit' : '/review-actions'
    const body = selected === 'submit' ? { expectedVersion: snapshot.version }
      : { decision: selected, expectedVersion: snapshot.version, ...(note ? { comment: note } : {}) }
    await api<CaseMutation>(pathAtStart + endpoint, { method: 'POST', body })
    void refreshWorkbench()
    if (path.value !== pathAtStart) return
    dialog.value?.close()
    actionNotice.value = selected === 'submit' ? '已提交审核。' : '审核结果已保存。'
    await refresh()
  } catch (error) {
    if (path.value !== pathAtStart) return
    actionError.value = errorText(error)
    if (error instanceof ApiError && error.status === 409) stale.value = true
  } finally {
    changing.value = false
  }
}

/**
 * 用途 在切换记录时清空前一项界面状态并加载新数据。
 * 参数 无。
 * 返回值 刷新任务。
 */
function navigateRecord() {
  reportTab.value = 'overview'
  record.value = undefined
  attachments.value = []
  history.value = []
  actionNotice.value = ''
  dialog.value?.close()
  return refresh()
}

/**
 * 用途 在监管备案和测评详情同步机构补正和其他会话已推进的状态。
 * 参数 无。
 * 返回值 无，确认中的审核继续使用其原先读取的版本。
 */
function syncVisibleReport() {
  if (document.visibilityState !== 'visible' || !record.value ||
    summaryRequest || changing.value || dialog.value?.open) return
  void loadRecord(true)
}

watch(path, navigateRecord, { immediate: true })
onMounted(() => {
  reportRefresh = setInterval(syncVisibleReport, 10_000)
  document.addEventListener('visibilitychange', syncVisibleReport)
})
onBeforeUnmount(() => {
  clearInterval(reportRefresh)
  document.removeEventListener('visibilitychange', syncVisibleReport)
  summaryRequest?.abort()
  attachmentRequest?.abort()
  historyRequest?.abort()
})
</script>

<template>
  <section>
    <RouterLink class="back-link" :to="'/' + kind"><ArrowLeft :size="16" aria-hidden="true" />返回{{ label.module }}</RouterLink>

    <AsyncState :loading="loading" :error="failure" @retry="refresh">
      <template v-if="record">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>{{ record.systemName }}</h1>
              <span v-if="record.cancelledAt" class="level-tag">已注销</span>
              <StatusBadge v-else :status="record.status" :label="filingReviewLabel(record.status, record.initialReviewAudience)" />
            </div>
            <p>{{ label.number }} <span class="code">{{ record.number }}</span></p>
          </div>
          <div class="page-actions">
            <BusinessReceiptDialog v-if="kind === 'filings'" resource="filings" :record-id="record.id" />
            <RouterLink v-if="kind === 'reports' && record.status === 'approved'" class="button neutral"
              :to="{ path: '/material-expiries', query: { reportId: record.id } }">测评材料有效期</RouterLink>
            <RouterLink v-if="kind === 'filings' && record.status === 'approved' && !record.cancelledAt"
              class="button neutral" :to="{ path: '/assessment-dues', query: { filingId: record.id } }">
              系统测评到期
            </RouterLink>
            <RouterLink v-if="!changing && record.allowedActions.includes('edit')" class="button neutral"
              :to="{ path: path + '/edit', query: route.query }">
              <Pencil :size="16" aria-hidden="true" />{{ returnedCase ? '编辑补正' : '编辑草稿' }}
            </RouterLink>
            <button v-if="record.allowedActions.includes('submit')" type="button" class="button primary"
              :disabled="changing" @click="confirm('submit')">
              <Send :size="16" aria-hidden="true" />{{ submitText }}
            </button>
            <button v-if="record.allowedActions.includes('return_for_correction')" type="button"
              class="button neutral" :disabled="changing" @click="confirm('return_for_correction')">退回补正</button>
            <button v-if="record.allowedActions.includes('approve')" type="button" class="button primary"
              :disabled="changing" @click="confirm('approve')">
              <Check :size="17" aria-hidden="true" />{{ decisionText }}
            </button>
          </div>
        </header>
        <p v-if="actionNotice" class="form-message success-message detail-notice" role="status">{{ actionNotice }}</p>
        <FilingWithdrawalPanel v-if="kind === 'filings' && record.initialReviewAudience"
          :path="path" :version="record.version" :disabled="changing" @changed="refresh" />
        <div v-if="returnReason" class="form-message warning-message detail-notice">
          <strong>最近退回意见</strong>
          <div class="return-reason">{{ returnReason }}</div>
        </div>
        <nav v-if="kind === 'reports'" class="v2-tabs v2-detail-tabs" aria-label="测评报告详情分区">
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'overview' }"
            :aria-pressed="reportTab === 'overview'" @click="reportTab = 'overview'">基本信息</button>
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'extraction' }"
            :aria-pressed="reportTab === 'extraction'" @click="reportTab = 'extraction'">AI 抽取</button>
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'compliance' }"
            :aria-pressed="reportTab === 'compliance'" @click="reportTab = 'compliance'">合规比对</button>
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'comparison' }"
            :aria-pressed="reportTab === 'comparison'" @click="reportTab = 'comparison'">历史对比</button>
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'result' }"
            :aria-pressed="reportTab === 'result'" @click="reportTab = 'result'">测评结论登记</button>
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'history' }"
            :aria-pressed="reportTab === 'history'" @click="reportTab = 'history'">审核留痕</button>
          <button class="v2-tab" type="button" :class="{ active: reportTab === 'attachments' }"
            :aria-pressed="reportTab === 'attachments'" @click="reportTab = 'attachments'">附件</button>
        </nav>
        <div class="detail-grid">
          <div v-show="kind === 'filings' || reportTab !== 'history'" class="detail-main">
            <section v-show="kind === 'filings' || reportTab === 'overview'" class="panel detail-panel"
              aria-labelledby="overview-title">
              <header class="panel-heading panel-compact"><h2 id="overview-title">基本信息</h2></header>
              <dl class="facts">
                <div><dt>{{ label.number }}</dt><dd class="code">{{ record.number }}</dd></div>
                <div><dt>所属单位</dt><dd>{{ record.directorySnapshot?.unit?.name || record.organizationName }}</dd></div>
                <div><dt>测评机构</dt><dd>{{ record.directorySnapshot?.institution?.name || record.assessmentOrg }}</dd></div>
                <template v-if="kind === 'filings'">
                  <div><dt>定级</dt><dd>{{ levels[record.level ?? ''] ?? '—' }}</dd></div>
                  <div v-if="record.cancelledAt"><dt>注销时间</dt><dd>{{ formatTime(record.cancelledAt) }}</dd></div>
                </template>
                <template v-else>
                  <div><dt>关联备案</dt><dd>
                    <RouterLink v-if="record.filingId" class="text-action" :to="'/filings/' + encodeURIComponent(record.filingId)">
                      {{ record.filingNumber }}<ChevronRight :size="14" aria-hidden="true" />
                    </RouterLink>
                    <span v-else>—</span>
                  </dd></div>
                  <div><dt>风险级别</dt><dd :class="'risk-' + (record.risk ?? '')">{{ record.risk ? risks[record.risk] : '—' }}</dd></div>
                  <div><dt>提交时间</dt><dd>{{ formatTime(record.submittedAt) }}</dd></div>
                  <div v-if="record.directorySnapshot?.person"><dt>测评人员</dt>
                    <dd>{{ record.directorySnapshot.person.name }}<template v-if="record.directorySnapshot.person.certificateNo">（{{ record.directorySnapshot.person.certificateNo }}）</template></dd>
                  </div>
                </template>
              </dl>
              <div v-if="kind === 'filings' && record.status === 'approved'" class="panel-footer page-actions">
                <RouterLink class="button neutral" :to="{ path: '/filing-applications', query: { filingId: record.id } }">
                  变更与注销记录
                </RouterLink>
                <template v-if="!record.cancelledAt">
                  <RouterLink class="button neutral" :to="{ path: '/filing-applications/new', query: { filingId: record.id, type: 'change' } }">申请变更</RouterLink>
                  <RouterLink class="button neutral" :to="{ path: '/filing-applications/new', query: { filingId: record.id, type: 'cancel' } }">申请注销</RouterLink>
                </template>
              </div>
            </section>
            <AttachmentPanel v-show="kind === 'filings' || reportTab === 'attachments'" :key="path"
              :path="path" :kind="kind" :attachments="attachments"
              :required="record.requiredMaterials"
              :editable="record.allowedActions.includes('edit')" :loading="attachmentsLoading" :error="attachmentError"
              @uploaded="refresh" @retry="loadAttachments" />
            <ClassificationSummary v-if="kind === 'filings' && record.classificationAssessment"
              :assessment="record.classificationAssessment" :declared="record.level" />
            <FilingPolicyMaterials v-if="kind === 'filings' && record.policyMaterials?.length"
              :rules="record.policyMaterials" :files="attachments" />
            <PrecheckPanel v-if="kind === 'filings'" :path="path" :revision="revision" />
            <div v-else v-show="reportTab === 'extraction'">
              <ReportExtractionPanel :key="path" :path="path" :attachments="attachments" />
            </div>
            <ReportHistory v-if="kind === 'reports' && reportTab === 'comparison'" :key="path" :path="path" />
            <AssessmentResultPanel v-if="kind === 'reports' && reportTab === 'result'" :key="path"
              :path="path" :report-version="record.version" :report-status="record.status" />
            <ReportCompliance v-if="kind === 'reports' && reportTab === 'compliance'" :key="path" :path="path"
              :attachments="attachments" :report-version="record.version" :report-status="record.status"
              @refresh="refresh" />
          </div>

          <section v-show="kind === 'filings' || reportTab === 'history'" class="panel detail-panel timeline-panel"
            aria-labelledby="history-title">
            <header class="panel-heading panel-compact">
              <div class="heading-icon"><History :size="20" aria-hidden="true" /><h2 id="history-title">审核记录</h2></div>
            </header>
            <AsyncState :loading="historyLoading" :error="historyError" :empty="!history.length"
              empty-text="暂无审核记录。" @retry="loadHistory">
              <ol class="timeline">
                <li v-for="(item, index) in history" :key="item.occurredAt + ':' + index">
                  <span class="timeline-dot" aria-hidden="true"></span>
                  <strong>{{ knownActions[item.action] ?? item.action }}</strong>
                  <span>{{ statuses[item.fromStatus] }} → {{ statuses[item.toStatus] }}</span>
                  <p v-if="item.comment">{{ item.comment }}</p>
                  <p v-if="directorySnapshotLabel(item.directorySnapshot)">{{ directorySnapshotLabel(item.directorySnapshot) }}</p>
                  <small>{{ item.operator }} · {{ formatTime(item.occurredAt) }}</small>
                </li>
              </ol>
            </AsyncState>
          </section>
          <ReportFeedbackReceipts v-if="kind === 'reports' && reportTab === 'history'"
            :path="path" :institution="record.assessmentOrg" />
        </div>
      </template>
    </AsyncState>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="decision-title" @cancel.prevent="!changing && dialog?.close()">
      <form @submit.prevent="act">
        <h2 id="decision-title">{{ dialogHeading }}</h2>
        <p v-if="command === 'submit'">{{ submitText }}后将进入待初审状态。</p>
        <label v-else class="field">
          <span>{{ command === 'return_for_correction' ? '退回原因' : '审核意见（选填）' }}</span>
          <textarea v-model="comment" rows="4" maxlength="1000" :required="command === 'return_for_correction'"
            :disabled="changing" placeholder="填写本次审核意见" />
        </label>
        <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="changing" @click="dialog?.close()">取消</button>
          <button v-if="stale" type="button" class="button neutral" @click="reloadForReview">重新加载记录</button>
          <button class="button primary" type="submit" :disabled="changing || stale">确认提交</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.return-reason { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

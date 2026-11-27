<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { ArrowLeft, Download, History } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import BusinessReceiptDialog from '../components/BusinessReceiptDialog.vue'
import FilingWithdrawalPanel from '../components/FilingWithdrawalPanel.vue'
import { filingReviewLabel } from '../lib/domain'
import StatusBadge from '../components/StatusBadge.vue'
import { api, ApiError, attachmentURL, errorText, isAbort } from '../lib/api'
import { refreshWorkbench } from '../lib/workbench'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { formatSize, formatTime, type Attachment, type Page } from '../lib/domain'
import { applicationMaterials, applicationTypes, displayFilingValue, filingApplicationActions, filingApplicationPath,
  filingApplicationsPath, filingApplicationStatuses, filingFields, type FilingApplicationDetail,
  type FilingApplicationMutation, type FilingApplicationReview, type FilingApplicationReviewAction } from '../lib/filingApplications'

const route = useRoute()
const path = computed(() => filingApplicationPath(String(route.params.id ?? '')))
const application = shallowRef<FilingApplicationDetail>()
const history = shallowRef<FilingApplicationReview[]>([])
const files = shallowRef<Attachment[]>([])
const filesError = ref('')
const loading = ref(true)
const historyLoading = ref(true)
const failure = ref('')
const historyError = ref('')
const notice = ref('')
const dialog = ref<HTMLDialogElement>()
const decision = ref<FilingApplicationReviewAction | 'submit'>('submit')
const comment = ref('')
const changing = ref(false)
const decisionError = ref('')
const stale = ref(false)
let detailRequest: AbortController | undefined
let historyRequest: AbortController | undefined
let decisionRequest: AbortController | undefined
const approvalText = computed(() => application.value?.status === 'pending_final' ? '终审通过' : '初审通过')
const decisionText = computed(() => decision.value === 'approve' ? approvalText.value : filingApplicationActions[decision.value])

/**
 * 用途 加载申请详情及当前允许的办理操作。
 * 参数 无，申请标识从路由取得。
 * 返回值 申请详情加载任务。
 */
async function load() {
  detailRequest?.abort()
  const request = new AbortController()
  detailRequest = request
  loading.value = true
  failure.value = ''
  try {
    const item = await api<FilingApplicationDetail>(path.value, { signal: request.signal })
    if (detailRequest !== request) return
    application.value = item
    if (item.requiredMaterials?.length) await loadFiles(request)
  } catch (error) {
    if (detailRequest === request && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (detailRequest === request) loading.value = false
  }
}

/**
 * 用途 读取运营者登记的申请材料，供监管审核时核对原件。
 * 参数 request 为和申请详情共用的取消控制器。
 * 返回值 申请材料的加载任务。
 */
async function loadFiles(request: AbortController) {
  filesError.value = ''
  try {
    const result = await api<Page<Attachment>>(path.value + '/attachments', { signal: request.signal })
    if (detailRequest === request) files.value = result.items
  } catch (error) {
    if (detailRequest === request && !isAbort(error)) filesError.value = errorText(error)
  }
}

/**
 * 用途 加载独立的审核轨迹，读取失败时保留主记录操作。
 * 参数 无，当前申请路径来自路由。
 * 返回值 审核记录加载任务。
 */
async function loadHistory() {
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  historyLoading.value = true
  historyError.value = ''
  try {
    const result = await api<{ items: FilingApplicationReview[] }>(path.value + '/review-history', { signal: request.signal })
    if (historyRequest === request) history.value = result.items
  } catch (error) {
    if (historyRequest === request && !isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === request) historyLoading.value = false
  }
}

/**
 * 用途 在路由切换时清除前一申请的状态并加载新页面。
 * 参数 无，监听当前申请路径。
 * 返回值 详情及审核历史的更新任务。
 */
function openApplication() {
  decisionRequest?.abort()
  dialog.value?.close()
  changing.value = false
  application.value = undefined
  history.value = []
  files.value = []
  filesError.value = ''
  notice.value = ''
  return Promise.all([load(), loadHistory()])
}

/**
 * 用途 打开办理确认框，在提交或审核前让用户核对当前阶段。
 * 参数 action 为当前申请可执行的业务动作。
 * 返回值 无。
 */
function confirm(action: FilingApplicationReviewAction | 'submit') {
  if (changing.value || !application.value?.allowedActions.includes(action)) return
  decision.value = action
  comment.value = ''
  decisionError.value = ''
  stale.value = false
  dialog.value?.showModal()
}

/**
 * 用途 遇到并发状态变化时刷新详情，并在无法操作时关闭确认框。
 * 参数 无。
 * 返回值 申请与审核历史的刷新任务。
 */
async function reloadDecision() {
  await Promise.all([load(), loadHistory()])
  if (failure.value || !application.value?.allowedActions.includes(decision.value)) {
    dialog.value?.close()
    return
  }
  dialog.value?.close()
  stale.value = false
  notice.value = '已刷新申请状态，请重新确认操作。'
}

/**
 * 用途 发送当前版本的办理动作，仅携带实际提交所需的字段。
 * 参数 无，动作、意见和状态版本来自当前确认框。
 * 返回值 办理任务，成功后刷新状态和审核历史。
 */
async function act() {
  if (!application.value || changing.value || stale.value) return
  const action = decision.value
  const content = comment.value.trim()
  if (action === 'return_for_correction' && !content) {
    decisionError.value = '请填写退回原因。'
    return
  }
  const expectedVersion = application.value.version
  const body = action === 'submit' ? { expectedVersion } : {
    decision: action, expectedVersion, ...(content ? { comment: content } : {}),
  }
  const request = new AbortController()
  decisionRequest = request
  changing.value = true
  decisionError.value = ''
  notice.value = ''
  try {
    const mutation = await api<FilingApplicationMutation>(path.value + (action === 'submit' ? '/submit' : '/review-actions'),
      { method: 'POST', body, signal: request.signal })
    void refreshWorkbench()
    if (request.signal.aborted) return
    application.value = { ...application.value, ...mutation, allowedActions: [] }
    dialog.value?.close()
    notice.value = mutation.status === 'approved' ? '申请终审通过，已生效。'
      : mutation.status === 'withdrawn' ? '申请已撤回，可以重新发起。' : '已更新申请审核状态。'
    await Promise.all([load(), loadHistory()])
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    decisionError.value = errorText(error)
    stale.value = error instanceof ApiError && error.status === 409 && error.code !== 'retest_required'
  } finally {
    if (decisionRequest === request) changing.value = false
  }
}

watch(path, openApplication, { immediate: true })
refreshBusinessOnFocus(() => {
  if (application.value && !changing.value && !loading.value && !dialog.value?.open) {
    void Promise.all([load(), loadHistory()])
  }
}, 10_000)
onScopeDispose(() => {
  detailRequest?.abort()
  historyRequest?.abort()
  decisionRequest?.abort()
})
</script>

<template>
  <section>
    <RouterLink class="back-link" :to="filingApplicationsPath"><ArrowLeft :size="16" aria-hidden="true" />返回申请列表</RouterLink>
    <AsyncState :loading="loading" :error="failure" @retry="load">
      <template v-if="application">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>备案{{ applicationTypes[application.type] }}申请</h1>
              <span v-if="application.status === 'withdrawn'" class="status-badge status-draft">
                <span aria-hidden="true"></span>{{ filingApplicationStatuses.withdrawn }}
              </span>
              <StatusBadge v-else :status="application.status" :label="filingReviewLabel(application.status, application.initialReviewAudience)" />
            </div>
            <p>{{ application.systemName }}</p>
          </div>
          <div class="page-actions">
            <BusinessReceiptDialog resource="filing-applications" :record-id="application.id" />
            <RouterLink v-if="application.allowedActions.includes('edit')" class="button neutral" :to="path + '/edit'">
              {{ application.status === 'returned' ? '编辑补正' : '编辑草稿' }}
            </RouterLink>
            <button v-if="application.allowedActions.includes('withdraw')" class="button neutral" type="button"
              @click="confirm('withdraw')">撤回申请</button>
            <button v-if="application.allowedActions.includes('submit')" class="button primary" type="button" @click="confirm('submit')">
              {{ application.status === 'returned' ? '重新提交' : '提交审核' }}
            </button>
            <button v-if="application.allowedActions.includes('return_for_correction')" class="button neutral" type="button"
              @click="confirm('return_for_correction')">退回补正</button>
            <button v-if="application.allowedActions.includes('approve')" class="button primary" type="button"
              @click="confirm('approve')">{{ approvalText }}</button>
          </div>
        </header>
        <p v-if="notice" class="form-message success-message detail-notice" role="status">{{ notice }}</p>
        <FilingWithdrawalPanel v-if="application.initialReviewAudience" :path="path" :version="application.version"
          :disabled="changing" @changed="reloadDecision" />
        <div class="detail-grid">
          <div class="detail-main">
            <section class="panel" aria-labelledby="application-details">
              <header class="panel-heading panel-compact"><h2 id="application-details">申请信息</h2></header>
              <dl class="facts">
                <div><dt>备案编号</dt><dd><RouterLink class="text-action code" :to="'/filings/' + encodeURIComponent(application.filingId)">
                  {{ application.filingNumber }}
                </RouterLink></dd></div>
                <div><dt>原所属单位</dt><dd>{{ application.organizationName }}</dd></div>
                <div><dt>创建时间</dt><dd>{{ formatTime(application.createdAt) }}</dd></div>
                <div v-if="application.appliedAt"><dt>生效时间</dt><dd>{{ formatTime(application.appliedAt) }}</dd></div>
                <div v-if="application.implementationId"><dt>复测报备</dt><dd>
                  <RouterLink class="text-action" :to="'/implementations/' + encodeURIComponent(application.implementationId)">
                    查看已通过的实施报备
                  </RouterLink>
                </dd></div>
                <div class="wide"><dt>申请理由</dt><dd class="application-reason">{{ application.reason }}</dd></div>
              </dl>
            </section>
            <section v-if="application.changes?.length" class="panel" aria-labelledby="application-changes">
              <header class="panel-heading panel-compact"><h2 id="application-changes">变更内容</h2></header>
              <div class="table-wrap">
                <table class="data-table application-changes">
                  <thead><tr><th scope="col">字段</th><th scope="col">原值</th><th scope="col">申请值</th></tr></thead>
                  <tbody>
                    <tr v-for="change in application.changes" :key="change.field">
                      <td>{{ filingFields[change.field] }}</td><td>{{ displayFilingValue(change.field, change.before) }}</td>
                      <td>{{ displayFilingValue(change.field, change.after) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>
            <section v-if="application.requiredMaterials?.length" class="panel detail-panel">
              <header class="panel-heading panel-compact"><h2>申请材料</h2></header>
              <AsyncState :error="filesError" :empty="!files.length" empty-text="尚无已登记材料。" @retry="load">
                <div class="table-wrap">
                  <table class="data-table">
                    <thead><tr><th>材料</th><th>类别</th><th>大小</th><th>操作</th></tr></thead>
                    <tbody><tr v-for="file in files" :key="file.id">
                      <td>{{ file.fileName }}</td>
                      <td>{{ (applicationMaterials as Record<string, string>)[file.kind] ?? file.kind }}</td>
                      <td>{{ formatSize(file.size) }}</td>
                      <td><a class="text-action" :href="attachmentURL(path, file.id)">
                        <Download :size="16" aria-hidden="true" />下载
                      </a></td>
                    </tr></tbody>
                  </table>
                </div>
              </AsyncState>
            </section>
          </div>
          <section class="panel timeline-panel" aria-labelledby="application-history">
            <header class="panel-heading panel-compact">
              <div class="heading-icon"><History :size="20" aria-hidden="true" /><h2 id="application-history">审核记录</h2></div>
            </header>
            <AsyncState :loading="historyLoading" :error="historyError" :empty="!history.length"
              empty-text="暂无审核记录。" @retry="loadHistory">
              <ol class="timeline">
                <li v-for="(item, index) in history" :key="item.occurredAt + ':' + index">
                  <span class="timeline-dot" aria-hidden="true"></span>
                  <strong>{{ filingApplicationActions[item.action] ?? item.action }}</strong>
                  <span>{{ filingApplicationStatuses[item.fromStatus] }} → {{ filingApplicationStatuses[item.toStatus] }}</span>
                  <p v-if="item.comment">{{ item.comment }}</p>
                  <small>{{ item.operator }} · {{ formatTime(item.occurredAt) }}</small>
                </li>
              </ol>
            </AsyncState>
          </section>
        </div>
      </template>
    </AsyncState>
    <dialog ref="dialog" class="decision-dialog" aria-labelledby="application-decision" @cancel.prevent="!changing && dialog?.close()">
      <form @submit.prevent="act">
        <h2 id="application-decision">{{ decisionText }}</h2>
        <p v-if="decision === 'submit'">提交后，申请将进入待初审状态。</p>
        <template v-else>
          <p v-if="decision === 'approve' && application?.status === 'pending_final'">终审通过后，申请将立即生效。</p>
          <p v-if="decision === 'withdraw'">撤回后将保留这笔申请和审核记录，可发起新的申请。</p>
          <label class="field">
            <span>{{ decision === 'return_for_correction' ? '退回原因'
              : decision === 'withdraw' ? '撤回说明（选填）' : '审核意见（选填）' }}</span>
            <textarea v-model="comment" rows="4" maxlength="1000" :required="decision === 'return_for_correction'"
              :disabled="changing || stale" />
          </label>
        </template>
        <p v-if="decisionError" class="form-message error-message" role="alert">{{ decisionError }}</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="changing" @click="dialog?.close()">取消</button>
          <button v-if="stale" class="button neutral" type="button" :disabled="loading" @click="reloadDecision">刷新申请</button>
          <button class="button primary" type="submit" :disabled="changing || stale">{{ changing ? '处理中…' : '确认' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.application-reason { white-space: pre-wrap; }
.application-changes td { overflow-wrap: anywhere; white-space: normal; }
</style>

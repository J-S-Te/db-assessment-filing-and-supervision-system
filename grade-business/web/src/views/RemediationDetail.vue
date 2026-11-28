<script setup lang="ts">
import { hasPermission } from '../lib/session'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { ArrowLeft, ClipboardCheck, Download, FileText, ListChecks, Upload } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import RemediationMaterialTemplates from '../components/RemediationMaterialTemplates.vue'
import BusinessReceiptDialog from '../components/BusinessReceiptDialog.vue'
import { api, ApiError, attachmentURL, errorText, isAbort } from '../lib/api'
import { refreshWorkbench } from '../lib/workbench'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { formatSize, formatTime, knownActions, risks, type Attachment } from '../lib/domain'
import {
  remediationBadge, remediationMaterials, remediationPath, remediationRecordPath, remediationRequiredMaterials,
  remediationStatuses,
  type RemediationDecision, type RemediationDetail, type RemediationMutation, type RemediationReview,
} from '../lib/remediations'

const route = useRoute()
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const path = computed(() => remediationRecordPath(id.value))
const record = ref<RemediationDetail>()
const attachments = ref<Attachment[]>([])
const history = ref<RemediationReview[]>([])
const loading = ref(false)
const refreshing = ref(false)
const detailError = ref('')
const filesError = ref('')
const historyError = ref('')
const busy = ref(false)
const actionError = ref('')
const actionNotice = ref('')
const stale = ref(false)
const category = ref('')
const fileInput = ref<HTMLInputElement>()
const dialog = ref<HTMLDialogElement>()
const decision = ref<RemediationDecision>('approve')
const confirmedDeadline = ref('')
const reviewComment = ref('')
const returnReason = ref('')
const reviewError = ref('')
let readRequest: AbortController | undefined

const editable = computed(() => record.value?.allowedActions.includes('edit') ?? false)
const canSubmit = computed(() => record.value?.allowedActions.includes('submit') ?? false)
const canReview = computed(() => record.value?.allowedActions.includes('approve') ?? false)
const missing = computed(() => remediationRequiredMaterials
  .filter(kind => !attachments.value.some(file => file.kind === kind)))
const submittable = computed(() => !busy.value && !refreshing.value && !stale.value
  && !filesError.value && !missing.value.length)

/**
 * 用途 清空前一条计划的局部状态并读取当前计划。
 * 参数 无，详情标识来自路由。
 * 返回值 无，读取在当前计划页面异步进行。
 */
function openRecord() {
  dialog.value?.close()
  record.value = undefined
  attachments.value = []
  history.value = []
  actionError.value = ''
  actionNotice.value = ''
  category.value = ''
  stale.value = false
  void refresh()
}

/**
 * 用途 同步详情、附件与审核留痕，使下一次操作使用当前版本。
 * 参数 无，计划标识由当前地址确定。
 * 返回值 完成三个资源读取后的任务。
 */
async function refresh() {
  readRequest?.abort()
  const request = new AbortController()
  readRequest = request
  loading.value = !record.value
  refreshing.value = true
  detailError.value = ''
  filesError.value = ''
  historyError.value = ''
  const base = path.value
  const options = { signal: request.signal }
  const [item, files, events] = await Promise.allSettled([
    api<RemediationDetail>(base, options),
    api<{ items: Attachment[] }>(base + '/attachments', options),
    api<{ items: RemediationReview[] }>(base + '/review-history', options),
  ])
  if (readRequest !== request || request.signal.aborted) return
  if (item.status === 'fulfilled') {
    record.value = item.value
    stale.value = false
  } else if (!isAbort(item.reason)) detailError.value = errorText(item.reason)
  if (files.status === 'fulfilled') attachments.value = files.value.items
  else if (!isAbort(files.reason)) filesError.value = errorText(files.reason)
  if (events.status === 'fulfilled') history.value = events.value.items
  else if (!isAbort(events.reason)) historyError.value = errorText(events.reason)
  refreshing.value = false
  loading.value = false
}

/**
 * 用途 开启审核表单，默认维持当前整改期限。
 * 参数 无，使用所选规划的期限和审核权限。
 * 返回值 无。
 */
function openReview() {
  if (!canReview.value || busy.value || refreshing.value || !record.value || stale.value) return
  decision.value = 'approve'
  confirmedDeadline.value = record.value.deadline
  reviewComment.value = ''
  returnReason.value = ''
  reviewError.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 保存审核结果和回传意见，并更新历史版本。
 * 参数 无，审核结论和原先读取版本来自当前详情表单。
 * 返回值 完成审核请求、结果刷新或显示错误的任务。
 */
async function review() {
  if (busy.value || !record.value || stale.value || !canReview.value) return
  const comment = reviewComment.value.trim()
  const reason = returnReason.value.trim()
  if (!comment || (decision.value === 'return_for_correction' && !reason)) {
    reviewError.value = '请填写审核意见和需要的退回原因。'
    return
  }
  const base = path.value
  const body = {
    decision: decision.value, deadline: confirmedDeadline.value, comment,
    ...(decision.value === 'return_for_correction' ? { reason } : {}),
    expectedVersion: record.value.version,
  }
  busy.value = true
  reviewError.value = ''
  actionError.value = ''
  try {
    const result = await api<RemediationMutation>(base + '/review-actions', { method: 'POST', body })
    void refreshWorkbench()
    if (path.value !== base) return
    dialog.value?.close()
    actionNotice.value = result.status === 'approved' ? '审核已通过，整改期限已确认。' : '已记录退回补正意见。'
    await refresh()
  } catch (error) {
    if (path.value !== base) return
    reviewError.value = errorText(error)
    if (error instanceof ApiError && error.status === 409) {
      stale.value = true
      actionError.value = reviewError.value
    }
  } finally {
    busy.value = false
  }
}

/**
 * 用途 提交当前整改规划，保留提交前的版本检查。
 * 参数 无，使用当前计划和附件状态。
 * 返回值 完成提交请求、结果刷新或显示错误的任务。
 */
async function submit() {
  if (!canSubmit.value || !submittable.value || !record.value) return
  const base = path.value
  busy.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    await api<RemediationMutation>(base + '/submit', {
      method: 'POST', body: { expectedVersion: record.value.version },
    })
    void refreshWorkbench()
    if (path.value !== base) return
    actionNotice.value = '整改规划已提交审核。'
    await refresh()
  } catch (error) {
    if (path.value !== base) return
    actionError.value = errorText(error)
    if (error instanceof ApiError && error.status === 409) stale.value = true
  } finally {
    busy.value = false
  }
}

/**
 * 用途 按所选类别上传一个安全校验的整改附件。
 * 参数 无，随文件提交页面当时所见的报告标识。
 * 返回值 完成上传并刷新材料、版本、留痕的任务。
 */
async function upload() {
  const current = record.value
  if (busy.value || refreshing.value || stale.value || !editable.value || !current) return
  const file = fileInput.value?.files?.[0]
  if (!file || !Object.hasOwn(remediationMaterials, category.value)
    || !file.size || !/\.(pdf|docx|xlsx|zip)$/i.test(file.name)) {
    actionError.value = '请选择类别与非空的 PDF、DOCX、XLSX 或 ZIP 文件。'
    return
  }
  const base = path.value
  const body = new FormData()
  body.append('kind', category.value)
  body.append('file', file)
  body.append('reportId', current.reportId)
  busy.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    await api<Attachment>(base + '/attachments', { method: 'POST', body })
    if (path.value !== base) return
    if (fileInput.value) fileInput.value.value = ''
    actionNotice.value = '整改材料已上传。'
    await refresh()
  } catch (error) {
    if (path.value !== base) return
    actionError.value = errorText(error)
    if (error instanceof ApiError && error.status === 409) {
      stale.value = true
      if (error.code === 'report_changed' && fileInput.value) fileInput.value.value = ''
    }
  } finally {
    busy.value = false
  }
}

/**
 * 用途 为此前通过的整改规划补齐台账，并显示已存在的条目数量。
 * 参数 无，规划标识来自当前详情。
 * 返回值 同步请求完成后的任务。
 */
async function syncLedger() {
  const current = record.value
  if (!hasPermission('admin.grade.case.edit') || busy.value || current?.status !== 'approved') return
  busy.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    const result = await api<{ count: number }>('/hazards/sync/' + encodeURIComponent(current.id), { method: 'POST' })
    void refreshWorkbench()
    if (id.value === current.id) actionNotice.value = `隐患台账已同步，共 ${result.count} 项。`
  } catch (error) {
    if (id.value === current.id) actionError.value = errorText(error)
  } finally {
    busy.value = false
  }
}

/**
 * 用途 处理审核对话框内的并发冲突并恢复计划视图。
 * 参数 无，借助当前详情路由刷新。
 * 返回值 刷新中的任务。
 */
function reloadAfterConflict() {
  dialog.value?.close()
  actionError.value = ''
  return refresh()
}

watch(id, openRecord, { immediate: true })
refreshBusinessOnFocus(() => {
  if (record.value && !busy.value && !refreshing.value && !dialog.value?.open) void refresh()
}, 10_000)
onScopeDispose(() => readRequest?.abort())
</script>

<template>
  <section>
    <RouterLink class="back-link" :to="remediationPath">
      <ArrowLeft :size="16" aria-hidden="true" />返回整改规划
    </RouterLink>

    <AsyncState :loading="loading" :error="detailError" @retry="refresh">
      <template v-if="record">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>整改规划材料详情</h1>
              <span class="status-badge" :class="remediationBadge(record.status)">
                <span aria-hidden="true"></span>{{ remediationStatuses[record.status] }}
              </span>
            </div>
            <p>{{ record.systemName }} · {{ record.organizationName }}</p>
          </div>
          <div class="page-actions">
            <BusinessReceiptDialog resource="remediations" :record-id="record.id" />
            <template v-if="record.status === 'approved'">
              <button v-if="hasPermission('admin.grade.case.edit')" class="button neutral" type="button"
                :disabled="busy" @click="syncLedger">同步隐患台账</button>
              <RouterLink class="button primary" :to="{ path: '/hazards', query: { planId: record.id } }">查看隐患台账</RouterLink>
            </template>
            <RouterLink v-if="editable" class="button neutral" :to="path + '/edit'">编辑规划</RouterLink>
            <button v-if="canSubmit" class="button primary" type="button" :disabled="!submittable" @click="submit">
              {{ record.submission ? '重新提交审核' : '提交审核' }}
            </button>
            <button v-if="canReview" class="button primary" type="button"
              :disabled="busy || refreshing || stale" @click="openReview">
              <ClipboardCheck :size="17" aria-hidden="true" />进入审核
            </button>
          </div>
        </header>

        <p v-if="actionNotice" class="form-message success-message" role="status">{{ actionNotice }}</p>
        <p v-if="actionError" class="form-message error-message" role="alert">
          {{ actionError }}
          <button v-if="stale" class="text-action" type="button" @click="reloadAfterConflict">刷新规划</button>
        </p>

        <div class="detail-grid">
          <div class="detail-main">
            <section class="panel">
              <header class="panel-heading panel-compact">
                <div class="heading-icon"><FileText :size="19" aria-hidden="true" /><h2>规划信息</h2></div>
              </header>
              <dl class="facts">
                <div><dt>备案编号</dt><dd class="code">{{ record.filingNumber }}</dd></div>
                <div><dt>上报单位</dt><dd>{{ record.organizationName }}</dd></div>
                <div><dt>关联测评报告</dt><dd>
                  <RouterLink class="text-action code" :to="'/reports/' + encodeURIComponent(record.reportId)">
                    {{ record.reportNumber }}
                  </RouterLink>
                </dd></div>
                <div><dt>整改期限</dt><dd>{{ record.deadline }}</dd></div>
                <div><dt>提交版本</dt><dd>{{ record.submission ? 'V' + record.submission + '.0' : '未提交' }}</dd></div>
                <div><dt>提交时间</dt><dd>{{ formatTime(record.submittedAt) }}</dd></div>
              </dl>
            </section>

            <section class="panel">
              <header class="panel-heading panel-compact">
                <div class="heading-icon"><ListChecks :size="19" aria-hidden="true" /><h2>风险项与整改措施</h2></div>
                <small class="muted">共 {{ record.risks.length }} 项</small>
              </header>
              <ol class="risk-items">
                <li v-for="(risk, index) in record.risks" :key="index">
                  <h3>
                    <span :class="'risk-' + risk.risk">{{ risk.risk ? risks[risk.risk] : '' }}</span>
                    {{ risk.description }}
                  </h3>
                  <p class="risk-measure">{{ risk.measure }}</p>
                  <p class="muted"><strong>责任</strong> {{ risk.responsibility }} · <strong>期限</strong> {{ risk.deadline }}</p>
                </li>
              </ol>
            </section>

            <section class="panel detail-panel">
              <header class="panel-heading panel-compact">
                <div class="heading-icon"><FileText :size="19" aria-hidden="true" /><h2>上报材料</h2></div>
                <small class="muted">已具备 {{ remediationRequiredMaterials.length - missing.length }} /
                  {{ remediationRequiredMaterials.length }} 类必传材料</small>
              </header>
              <div class="attachment-body">
                <RemediationMaterialTemplates :base-path="path" />
                <p v-if="editable && missing.length && !filesError" class="hint-row">
                  待上传 {{ missing.map(key => remediationMaterials[key]).join('、') }}
                </p>
                <form v-if="editable" class="attachment-form" @submit.prevent="upload">
                  <label class="field"><span>材料类别</span>
                    <select v-model="category" required :disabled="busy || stale">
                      <option value="" disabled>请选择类别</option>
                      <option v-for="(label, key) in remediationMaterials" :key="key" :value="key">{{ label }}</option>
                    </select>
                  </label>
                  <label class="field"><span>选择文件</span>
                    <input ref="fileInput" type="file" accept=".pdf,.docx,.xlsx,.zip" required :disabled="busy || stale">
                  </label>
                  <button class="button primary" type="submit" :disabled="busy || refreshing || stale">
                    <Upload :size="16" aria-hidden="true" />{{ busy ? '处理中…' : '上传材料' }}
                  </button>
                </form>
                <AsyncState :error="filesError" :empty="!attachments.length" empty-text="暂未上传整改材料。"
                  @retry="refresh">
                  <ul class="attachment-list">
                    <li v-for="file in attachments" :key="file.id">
                      <div class="file-icon"><FileText :size="17" aria-hidden="true" /></div>
                      <div class="file-info"><strong>{{ file.fileName }}</strong><small>
                        {{ remediationMaterials[file.kind] }} · {{ formatSize(file.size) }} · {{ formatTime(file.uploadedAt) }}
                      </small></div>
                      <a class="text-action" :href="attachmentURL(path, file.id)"
                        :aria-label="'下载 ' + file.fileName"><Download :size="16" aria-hidden="true" />下载</a>
                    </li>
                  </ul>
                </AsyncState>
              </div>
            </section>
          </div>

          <section class="panel detail-panel">
            <header class="panel-heading panel-compact">
              <div class="heading-icon"><ClipboardCheck :size="19" aria-hidden="true" /><h2>审核留痕</h2></div>
            </header>
            <AsyncState :error="historyError" :empty="!history.length" empty-text="尚无提交或审核记录。" @retry="refresh">
              <ol class="timeline">
                <li v-for="event in history" :key="event.submission + '-' + event.action">
                  <span class="timeline-dot" aria-hidden="true"></span>
                  <strong>{{ event.action === 'withdraw' ? '撤回申请' : knownActions[event.action] }}
                    · V{{ event.submission }}.0</strong>
                  <small>{{ event.operator }} · {{ formatTime(event.occurredAt) }}</small>
                  <p v-if="event.comment">{{ event.comment }}</p>
                  <p v-if="event.reason"><strong>退回原因</strong> {{ event.reason }}</p>
                  <small>整改期限 {{ event.deadline }}</small>
                </li>
              </ol>
            </AsyncState>
          </section>
        </div>
      </template>
    </AsyncState>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="remediation-review-title"
      @cancel="busy && $event.preventDefault()">
      <form @submit.prevent="review">
        <h2 id="remediation-review-title">审核整改规划</h2>
        <fieldset class="review-fields" :disabled="busy || stale">
          <label class="field"><span>审核结论</span>
            <select v-model="decision">
              <option value="approve">审核通过</option>
              <option value="return_for_correction">退回补正</option>
            </select>
          </label>
          <label class="field"><span>整改期限确认</span>
            <input v-model="confirmedDeadline" type="date" required>
          </label>
          <label class="field"><span>审核意见</span>
            <textarea v-model="reviewComment" rows="3" maxlength="1000" required></textarea>
          </label>
          <label v-if="decision === 'return_for_correction'" class="field"><span>退回原因</span>
            <textarea v-model="returnReason" rows="3" maxlength="1000" required></textarea>
          </label>
        </fieldset>
        <p v-if="reviewError" class="form-message error-message" role="alert">{{ reviewError }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="busy" @click="dialog?.close()">取消</button>
          <button v-if="stale" type="button" class="button neutral" @click="reloadAfterConflict">刷新规划</button>
          <button type="submit" class="button primary" :disabled="busy || stale">
            {{ busy ? '提交中…' : '提交审核结论' }}
          </button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.risk-items { margin: 0; padding: 0 16px; list-style: none; }
.risk-items li { padding: 13px 0; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
.risk-items li:last-child { border-bottom: 0; }
.risk-items h3 { font-size: .96rem; }
.risk-items h3 span { margin-right: 6px; }
.risk-measure { white-space: pre-wrap; margin: 5px 0; }
.review-fields { display: grid; gap: 10px; }
.decision-dialog { max-height: calc(100vh - 26px); overflow-y: auto; }
.decision-dialog textarea { min-height: 65px; }
</style>

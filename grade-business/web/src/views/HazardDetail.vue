<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ArrowLeft, Download, Upload } from '@lucide/vue'
import AsyncState from '../components/AsyncState.vue'
import BusinessReceiptDialog from '../components/BusinessReceiptDialog.vue'
import { api, ApiError, attachmentURL, errorText, isAbort } from '../lib/api'
import { refreshWorkbench } from '../lib/workbench'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { formatSize, formatTime, risks, type Attachment } from '../lib/domain'
import {
  hazardActions, hazardBadge, hazardEventLabel, hazardLedgers, hazardPath, hazardRequiresComment,
  hazardSources, hazardStatuses,
  type HazardAction, type HazardDetail, type HazardEvent, type HazardMutation,
} from '../lib/hazards'

const route = useRoute()
const path = computed(() => hazardPath(typeof route.params.id === 'string' ? route.params.id : ''))
const record = ref<HazardDetail>()
const major = computed(() => record.value?.ledger === 'major_risk')
const backQuery = computed(() => Object.keys(route.query).length ? route.query : {
  ledger: major.value ? 'major_risk' : undefined, planId: record.value?.planId,
})
const adminActions = computed(() => record.value?.allowedActions.filter((action): action is HazardAction =>
  action !== 'retest_pass' && action !== 'retest_return') ?? [])
const files = ref<Attachment[]>([])
const events = ref<HazardEvent[]>([])
const loading = ref(false)
const busy = ref(false)
const stale = ref(false)
const failure = ref('')
const fileError = ref('')
const historyError = ref('')
const actionError = ref('')
const notice = ref('')
const fileInput = ref<HTMLInputElement>()
const uploadVersion = ref<number>()
const dialog = ref<HTMLDialogElement>()
const action = ref<HazardAction>('submit')
const comment = ref('')
const confirmationError = ref('')
const commentLabels: Record<HazardAction, string> = {
  dispatch: '派发说明（选填）', supervise: '督办意见', submit: '整改说明',
  return_for_correction: '退回原因', close: '复核意见（选填）',
}
const actionResults: Record<HazardAction, string> = {
  dispatch: '已派发整改，运营者可查看整改单。',
  supervise: '已发送站内督办，办理记录已更新。',
  submit: '已提交复核。', return_for_correction: '隐患已退回补正。', close: '隐患已销项。',
}
const confirmLabel = computed(() => action.value === 'dispatch' ? '确认派发'
  : action.value === 'supervise' ? '确认督办' : '确认提交')
const supervisionRecipient = computed(() => record.value?.status === 'pending_retest'
  ? record.value.assessmentOrg || '负责复测的测评机构' : record.value?.organizationName)
let request: AbortController | undefined

/**
 * 用途 同步当前隐患、证据及办理历史，跳转时取消上一条的读取。
 * 参数 无，隐患标识以当前路由为准。
 * 返回值 当前读取完成后的任务。
 */
async function refresh() {
  request?.abort()
  const next = new AbortController()
  const base = path.value
  request = next
  const options = { signal: next.signal }
  loading.value = true
  failure.value = fileError.value = historyError.value = ''
  const [current, attachments, history] = await Promise.allSettled([
    api<HazardDetail>(base, options),
    api<{ items: Attachment[] }>(base + '/attachments', options),
    api<{ items: HazardEvent[] }>(base + '/history', options),
  ])
  if (request !== next || next.signal.aborted) return
  if (current.status === 'fulfilled') {
    record.value = current.value
    stale.value = false
  } else if (!isAbort(current.reason)) failure.value = errorText(current.reason)
  if (attachments.status === 'fulfilled') files.value = attachments.value.items
  else if (!isAbort(attachments.reason)) fileError.value = errorText(attachments.reason)
  if (history.status === 'fulfilled') events.value = history.value.items
  else if (!isAbort(history.reason)) historyError.value = errorText(history.reason)
  loading.value = false
}

/**
 * 用途 重置页面并读取选中的隐患。
 * 参数 无。
 * 返回值 详情加载任务。
 */
function openRecord() {
  dialog.value?.close()
  record.value = undefined
  files.value = []
  uploadVersion.value = undefined
  events.value = []
  actionError.value = notice.value = ''
  stale.value = false
  return refresh()
}

/**
 * 用途 上传原生文件输入中的整改证据并同步版本。
 * 参数 无，证据来自当前详情的文件控件。
 * 返回值 上传和刷新完成后的任务。
 */
async function upload() {
  const file = fileInput.value?.files?.[0]
  if (busy.value || loading.value || stale.value || !record.value?.allowedActions.includes('submit')) return
  if (!file || !file.size || !/\.(pdf|docx|xlsx|zip)$/i.test(file.name)) {
    actionError.value = '请选择非空的 PDF、DOCX、XLSX 或 ZIP 证据。'
    return
  }
  const base = path.value
  const body = new FormData()
  body.append('kind', 'evidence')
  body.append('file', file)
  busy.value = true
  actionError.value = notice.value = ''
  try {
    await api<Attachment>(base + '/attachments', { method: 'POST', body, expectedVersion: uploadVersion.value })
    if (path.value !== base) return
    if (fileInput.value) fileInput.value.value = ''
    notice.value = '整改证据已上传。'
    await refresh()
  } catch (error) {
    if (path.value !== base) return
    actionError.value = errorText(error)
    stale.value = error instanceof ApiError && error.status === 409
  } finally {
    busy.value = false
  }
}

/**
 * 用途 打开当前可执行动作的确认框。
 * 参数 value 为当前隐患允许执行的监管办理动作。
 * 返回值 无。
 */
function confirm(value: HazardAction) {
  if (busy.value || loading.value || stale.value || !record.value?.allowedActions.includes(value)) return
  action.value = value
  comment.value = ''
  confirmationError.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 按当前业务版本提交动作并显示服务端办理结果。
 * 参数 无，当前确认框提供动作和意见。
 * 返回值 办理结束后的任务。
 */
async function act() {
  const current = record.value
  const selected = action.value
  if (busy.value || stale.value || !current?.allowedActions.includes(selected)) return
  const note = comment.value.trim()
  if (hazardRequiresComment(selected) && !note) {
    confirmationError.value = '请填写办理说明。'
    return
  }
  const base = path.value
  busy.value = true
  confirmationError.value = actionError.value = notice.value = ''
  try {
    await api<HazardMutation>(base + '/actions', {
      method: 'POST',
      body: { action: selected, expectedVersion: current.version, ...(note ? { comment: note } : {}) },
    })
    void refreshWorkbench()
    if (path.value !== base) return
    dialog.value?.close()
    notice.value = actionResults[selected]
    await refresh()
  } catch (error) {
    if (path.value !== base) return
    await showActionError(error)
  } finally {
    busy.value = false
  }
}

/**
 * 用途 展示当前办理冲突，并在当天已经督办时同步实际留痕。
 * 参数 error 为提交动作时收到的业务或网络错误。
 * 返回值 错误反馈及必要的详情刷新任务。
 */
async function showActionError(error: unknown) {
  if (error instanceof ApiError && error.code === 'already_supervised') {
    dialog.value?.close()
    notice.value = '该重大风险今日已督办。'
    await refresh()
    return
  }
  confirmationError.value = errorText(error)
  stale.value = error instanceof ApiError && error.status === 409
}

/**
 * 用途 结束过期确认并重新读取隐患，以便检查新状态。
 * 参数 无。
 * 返回值 最新详情的读取任务。
 */
function reload() {
  dialog.value?.close()
  if (fileInput.value) fileInput.value.value = ''
  uploadVersion.value = undefined
  actionError.value = ''
  return refresh()
}

watch(path, openRecord, { immediate: true })
refreshBusinessOnFocus(() => {
  if (record.value && !busy.value && !loading.value && !dialog.value?.open) void refresh()
})
onScopeDispose(() => request?.abort())
</script>

<template>
  <section>
    <RouterLink class="back-link" :to="{ path: '/hazards', query: backQuery }">
      <ArrowLeft :size="16" aria-hidden="true" />返回隐患台账
    </RouterLink>
    <AsyncState :loading="loading && !record" :error="failure" @retry="refresh">
      <template v-if="record">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>{{ record.title || '隐患详情' }}</h1>
              <span class="status-badge" :class="hazardBadge(record.status)"><span aria-hidden="true"></span>{{ hazardStatuses[record.status] }}</span>
            </div>
            <p>{{ hazardLedgers[record.ledger] }} · {{ record.systemName }} · {{ record.organizationName }}</p>
          </div>
          <div class="page-actions">
            <BusinessReceiptDialog resource="hazards" :record-id="record.id" />
            <button v-for="choice in adminActions" :key="choice" type="button"
              class="button" :class="choice === 'return_for_correction' || choice === 'supervise' ? 'neutral' : 'primary'"
              :disabled="busy || loading || stale" @click="confirm(choice)">{{ hazardActions[choice] }}</button>
          </div>
        </header>
        <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
        <p v-if="actionError" class="form-message error-message" role="alert">
          {{ actionError }}
          <button v-if="stale" class="text-action" type="button" @click="reload">刷新隐患</button>
        </p>

        <div class="detail-grid">
          <div class="detail-main">
            <section class="panel detail-panel">
              <header class="panel-heading panel-compact"><h2>整改信息</h2></header>
              <dl class="facts">
                <div><dt>风险等级</dt><dd :class="'risk-' + record.severity">{{ risks[record.severity] }}</dd></div>
                <div><dt>整改期限</dt><dd>{{ record.deadline }}</dd></div>
                <div><dt>{{ major ? '处置人或部门' : '责任人或部门' }}</dt><dd>{{ record.responsibility }}</dd></div>
                <div v-if="record.assessmentOrg"><dt>测评机构</dt><dd>{{ record.assessmentOrg }}</dd></div>
                <div v-if="record.source"><dt>来源</dt><dd>{{ hazardSources[record.source] }}</dd></div>
                <div v-if="record.discoveredOn"><dt>发现日期</dt><dd>{{ record.discoveredOn }}</dd></div>
                <div v-if="record.dispatchedAt"><dt>派发时间</dt><dd>{{ formatTime(record.dispatchedAt) }}</dd></div>
                <div v-if="record.closedAt"><dt>销项时间</dt><dd>{{ formatTime(record.closedAt) }}</dd></div>
                <div><dt>隐患描述</dt><dd class="hazard-text">{{ record.description }}</dd></div>
                <div><dt>整改措施</dt><dd class="hazard-text">{{ record.measure }}</dd></div>
              </dl>
              <footer class="panel-footer page-actions">
                <RouterLink class="button neutral small" :to="'/filings/' + encodeURIComponent(record.filingId)">备案 {{ record.filingNumber }}</RouterLink>
                <RouterLink v-if="record.reportId" class="button neutral small"
                  :to="'/reports/' + encodeURIComponent(record.reportId)">报告 {{ record.reportNumber }}</RouterLink>
                <RouterLink v-if="record.planId" class="button neutral small"
                  :to="'/remediations/' + encodeURIComponent(record.planId)">来源整改规划</RouterLink>
              </footer>
            </section>

            <section class="panel detail-panel">
              <header class="panel-heading panel-compact"><h2>整改证据</h2></header>
              <form v-if="record.allowedActions.includes('submit')" class="hazard-files" @submit.prevent="upload">
                <label class="field"><span>选择证据文件</span>
                  <input ref="fileInput" @change="uploadVersion = record.version" type="file" accept=".pdf,.docx,.xlsx,.zip" required :disabled="busy || loading || stale">
                </label>
                <button class="button primary" type="submit" :disabled="busy || loading || stale">
                  <Upload :size="16" aria-hidden="true" />上传证据
                </button>
              </form>
              <AsyncState :error="fileError" :empty="!files.length" empty-text="暂无整改证据。" @retry="refresh">
                <ul class="hazard-evidence">
                  <li v-for="file in files" :key="file.id">
                    <span><strong>{{ file.fileName }}</strong><small>{{ formatSize(file.size) }} · {{ formatTime(file.uploadedAt) }}</small></span>
                    <a class="button neutral small" :href="attachmentURL(path, file.id)" :aria-label="'下载 ' + file.fileName">
                      <Download :size="15" aria-hidden="true" />下载
                    </a>
                  </li>
                </ul>
              </AsyncState>
            </section>
          </div>

          <section class="panel detail-panel timeline-panel">
            <header class="panel-heading panel-compact"><h2>整改办理记录</h2></header>
            <AsyncState :error="historyError" :empty="!events.length" empty-text="暂无整改办理记录。" @retry="refresh">
              <ol class="timeline">
                <li v-for="(event, index) in events" :key="event.occurredAt + ':' + index">
                  <span class="timeline-dot" aria-hidden="true"></span><strong>{{ hazardEventLabel(event) }}</strong>
                  <span v-if="event.fromStatus && event.fromStatus !== event.toStatus">
                    {{ hazardStatuses[event.fromStatus] }} → {{ hazardStatuses[event.toStatus] }}</span>
                  <span v-else-if="!event.fromStatus">{{ hazardStatuses[event.toStatus] }}</span>
                  <p v-if="event.comment" class="hazard-text">{{ event.comment }}</p>
                  <small>{{ event.operator }} · {{ formatTime(event.occurredAt) }}</small>
                </li>
              </ol>
            </AsyncState>
          </section>
        </div>
      </template>
    </AsyncState>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="hazard-decision"
      @cancel.prevent="!busy && dialog?.close()">
      <form @submit.prevent="act">
        <h2 id="hazard-decision">{{ hazardActions[action] }}</h2>
        <p v-if="action === 'submit' && record?.status === 'returned'">请先上传本轮补正的新证据。</p>
        <p v-else-if="action === 'dispatch'">派发后，{{ record?.organizationName }}将收到整改单和站内通知。</p>
        <p v-else-if="action === 'supervise'">督办意见将发送给{{ supervisionRecipient }}并写入办理记录。</p>
        <p v-else-if="action === 'close'">确认完成整改后销项，证据和办理记录将保留。</p>
        <label class="field">
          <span>{{ commentLabels[action] }}</span>
          <textarea v-model="comment" rows="4" maxlength="1000"
            :required="hazardRequiresComment(action)" :disabled="busy || stale" />
        </label>
        <p v-if="confirmationError" class="form-message error-message" role="alert">{{ confirmationError }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="busy" @click="dialog?.close()">取消</button>
          <button v-if="stale" type="button" class="button neutral" @click="reload">刷新隐患</button>
          <button v-else type="submit" class="button primary" :disabled="busy">{{ confirmLabel }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.hazard-text { white-space: pre-wrap; overflow-wrap: anywhere; }
.hazard-files { display: flex; align-items: end; gap: 0.8rem; flex-wrap: wrap; margin: 0.7rem 0; }
.hazard-evidence { list-style: none; padding: 0; margin: 0; }
.hazard-evidence li { display: flex; align-items: center; justify-content: space-between; gap: 0.9rem; padding: 0.65rem 0; }
.hazard-evidence small { display: block; }
</style>

<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { Download, Upload } from '@lucide/vue'
import AsyncState from './AsyncState.vue'
import { api, ApiError, attachmentURL, errorText, isAbort, params } from '../lib/api'
import { formatSize, formatTime, risks, type Attachment } from '../lib/domain'
import { hazardBadge, hazardEventLabel, hazardPath, hazardSources, hazardStatuses,
  type HazardDetail, type HazardEvent, type HazardMutation, type HazardRetestAction } from '../lib/hazards'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

const props = defineProps<{ id: string; institution: string }>()
const path = '/institution' + hazardPath(props.id)
const scope = params({ institution: props.institution })
const record = shallowRef<HazardDetail>()
const files = shallowRef<Attachment[]>([])
const history = shallowRef<HazardEvent[]>([])
const loading = ref(false)
const pending = ref<'upload' | HazardRetestAction>()
const stale = ref(false)
const failure = ref('')
const fileFailure = ref('')
const historyFailure = ref('')
const actionFailure = ref('')
const notice = ref('')
const fileInput = ref<HTMLInputElement>()
const uploadVersion = ref<number>()
const dialog = ref<HTMLDialogElement>()
const action = ref<HazardRetestAction>()
const comment = ref('')
const choices: HazardRetestAction[] = ['retest_return', 'retest_pass']
const labels = { retest_return: '退回复测', retest_pass: '复测通过' } as const
const locked = computed(() => Boolean(pending.value || loading.value || stale.value || failure.value))
const actions = computed(() => choices.filter(value => record.value?.allowedActions.includes(value)))
const needsReport = computed(() => actions.value.includes('retest_pass') && !record.value?.retestReportReady)
const fileGroups = computed(() => [
  { kind: 'evidence', title: '整改证据', items: files.value.filter(file => file.kind === 'evidence'), empty: '暂无整改证据。' },
  { kind: 'retest_report', title: '复测报告', items: files.value.filter(file => file.kind === 'retest_report'), empty: '暂无复测报告。' },
])
let readRequest: AbortController | undefined
let actionRequest: AbortController | undefined

/**
 * 用途 更新同一台账的真实状态、材料和办理历史。
 * 参数 quiet 表示可见页面后台刷新，保留已展示记录。
 * 返回值 详情、附件及历史读取完成的任务。
 */
async function load(quiet = false) {
  readRequest?.abort()
  const request = new AbortController()
  readRequest = request
  loading.value = !quiet
  const options = { signal: request.signal }
  const [detail, attachments, events] = await Promise.allSettled([
    api<HazardDetail>(path + scope, options),
    api<{ items: Attachment[] }>(path + '/attachments' + scope, options),
    api<{ items: HazardEvent[] }>(path + '/history' + scope, options),
  ])
  if (readRequest !== request || request.signal.aborted) return
  readRequest = undefined
  loading.value = false
  failure.value = detail.status === 'rejected' ? errorText(detail.reason) : ''
  if (detail.status === 'rejected') {
    if (detail.reason instanceof ApiError && [403, 404].includes(detail.reason.status)) record.value = undefined
    return
  }
  record.value = detail.value
  stale.value = false
  fileFailure.value = attachments.status === 'rejected' ? errorText(attachments.reason) : ''
  historyFailure.value = events.status === 'rejected' ? errorText(events.reason) : ''
  if (attachments.status === 'fulfilled') files.value = attachments.value.items
  if (events.status === 'fulfilled') history.value = events.value.items
}

/**
 * 用途 串行提交附件或复测结论，成功后重读服务端状态和版本。
 * 参数 operation 为当前办理动作，body 为附件表单或结论及预期版本。
 * 返回值 写入及同步任务。
 */
async function write(operation: NonNullable<typeof pending.value>, body: object) {
  readRequest?.abort()
  readRequest = undefined
  const request = new AbortController()
  actionRequest = request
  pending.value = operation
  actionFailure.value = notice.value = ''
  const options = { method: 'POST' as const, body, signal: request.signal,
    expectedVersion: operation === 'upload' ? uploadVersion.value : undefined }
  try {
    const message = await saveResult(operation, options)
    if (request.signal.aborted) return
    notice.value = message
    await load()
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    actionFailure.value = errorText(error)
    stale.value = error instanceof ApiError && error.status === 409
  } finally {
    if (actionRequest === request) {
      actionRequest = undefined
      pending.value = undefined
    }
  }
}

/**
 * 用途 保存单份复测报告或处理当前复测结论并更新即时反馈。
 * 参数 operation 为当前办理动作，options 携带请求体及取消信号。
 * 返回值 服务端确认后可展示的结果提示。
 */
async function saveResult(operation: NonNullable<typeof pending.value>, options: {
  method: 'POST'; body: object; signal: AbortSignal; expectedVersion?: number
}) {
  if (operation === 'upload') {
    await api<Attachment>(path + '/attachments' + scope, options)
    if (!options.signal.aborted && fileInput.value) fileInput.value.value = ''
    return '复测报告已上传。'
  }
  const result = await api<HazardMutation>(path + '/actions' + scope, options)
  if (!options.signal.aborted) {
    if (record.value) record.value = { ...record.value, ...result, allowedActions: [] }
    dialog.value?.close()
  }
  return operation === 'retest_pass' ? '复测已通过，等待监管复核。' : '已经退回运营者继续整改。'
}

/**
 * 用途 从原生文件选择框校验并上传本轮复测报告。
 * 参数 无，当前选择的报告及台账范围来自页面。
 * 返回值 校验通过后的上传任务。
 */
function upload() {
  if (locked.value || !actions.value.length) return
  const file = fileInput.value?.files?.[0]
  if (!file || !file.size || !/\.(pdf|docx|xlsx|zip)$/i.test(file.name)) {
    actionFailure.value = '请选择非空的 PDF、DOCX、XLSX 或 ZIP 报告。'
    return
  }
  const body = new FormData()
  body.append('kind', 'retest_report')
  body.append('file', file)
  return write('upload', body)
}

/**
 * 用途 根据当前台账允许的动作打开原生复测确认框。
 * 参数 value 为复测通过或退回复测动作。
 * 返回值 无。
 */
function confirm(value: HazardRetestAction) {
  if (locked.value || !actions.value.includes(value) || (value === 'retest_pass' && needsReport.value)) return
  readRequest?.abort()
  readRequest = undefined
  action.value = value
  comment.value = actionFailure.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 以详情的已读版本提交复测结论和意见。
 * 参数 无，确认动作、版本和意见来自当前页面。
 * 返回值 校验通过后的办理任务。
 */
function submit() {
  const current = record.value
  const value = action.value
  const note = comment.value.trim()
  if (!current || !value || locked.value || !actions.value.includes(value)) return
  if (value === 'retest_pass' ? !current.retestReportReady : !note) return
  return write(value, { action: value, expectedVersion: current.version, comment: note })
}

/**
 * 用途 关闭已失效的确认并读取当前隐患状态。
 * 参数 无。
 * 返回值 最新详情加载任务。
 */
function reload() {
  dialog.value?.close()
  if (fileInput.value) fileInput.value.value = ''
  uploadVersion.value = undefined
  actionFailure.value = ''
  return load()
}

void load()
refreshBusinessOnFocus(() => {
  if (!readRequest && !actionRequest && !dialog.value?.open && !stale.value) void load(true)
}, 10_000)
onScopeDispose(() => {
  readRequest?.abort()
  actionRequest?.abort()
})
</script>

<template>
  <AsyncState :loading="loading && !record" :error="failure" @retry="load()">
    <template v-if="record">
      <header class="page-header detail-header">
        <div>
          <div class="detail-title">
            <h1>{{ record.title || record.systemName }}</h1>
            <span v-if="record.ledger === 'major_risk'" class="level-tag">重大风险</span>
            <span class="status-badge" :class="hazardBadge(record.status)">
              <span aria-hidden="true"></span>{{ hazardStatuses[record.status] }}</span>
          </div>
          <p><template v-if="record.title">{{ record.systemName }} · </template>{{ record.organizationName }}
            · {{ record.assessmentOrg ?? institution }}</p>
        </div>
        <div class="page-actions">
          <button v-for="choice in actions" :key="choice" class="button"
            :class="choice === 'retest_pass' ? 'primary' : 'neutral'" type="button"
            :disabled="locked || (choice === 'retest_pass' && needsReport)" @click="confirm(choice)">{{ labels[choice] }}</button>
        </div>
      </header>
      <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
      <p v-if="actionFailure && !dialog?.open" class="form-message error-message" role="alert">
        {{ actionFailure }} <button v-if="stale" class="text-action" type="button" @click="reload">刷新隐患</button>
      </p>
      <div class="detail-grid">
        <div class="detail-main">
          <section class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>整改信息</h2></header>
            <dl class="facts">
              <div><dt>风险等级</dt><dd :class="'risk-' + record.severity">{{ risks[record.severity] }}</dd></div>
              <div><dt>整改期限</dt><dd>{{ record.deadline }}</dd></div>
              <div><dt>备案编号</dt><dd>{{ record.filingNumber }}</dd></div>
              <div v-if="record.reportNumber"><dt>来源测评报告</dt><dd>{{ record.reportNumber }}</dd></div>
              <div><dt>{{ record.ledger === 'major_risk' ? '处置人或部门' : '责任人或部门' }}</dt>
                <dd>{{ record.responsibility }}</dd></div>
              <div v-if="record.source"><dt>来源</dt><dd>{{ hazardSources[record.source] }}</dd></div>
              <div v-if="record.discoveredOn"><dt>发现日期</dt><dd>{{ record.discoveredOn }}</dd></div>
              <div v-if="record.closedAt"><dt>销项时间</dt><dd>{{ formatTime(record.closedAt) }}</dd></div>
              <div class="wide"><dt>隐患描述</dt><dd class="retest-text">{{ record.description }}</dd></div>
              <div class="wide"><dt>整改措施</dt><dd class="retest-text">{{ record.measure }}</dd></div>
            </dl>
          </section>
          <section class="panel detail-panel">
            <header class="panel-heading panel-compact"><h2>复测材料</h2></header>
            <form v-if="actions.length" class="retest-upload" @submit.prevent="upload">
              <p v-if="needsReport" class="form-message warning-message">本轮尚未上传复测报告。通过前须上传本轮报告。</p>
              <div class="input-action">
                <label class="field">
                  <span>选择复测报告</span>
                  <input ref="fileInput" @change="uploadVersion = record?.version" type="file" accept=".pdf,.docx,.xlsx,.zip" required :disabled="locked">
                </label>
                <button class="button primary" type="submit" :disabled="locked">
                  <Upload :size="16" aria-hidden="true" />{{ pending === 'upload' ? '上传中…' : '上传复测报告' }}
                </button>
              </div>
            </form>
            <AsyncState :error="fileFailure" @retry="load()">
              <section v-for="group in fileGroups" :key="group.kind" class="retest-materials-group">
                <h3>{{ group.title }}</h3>
                <p v-if="!group.items.length" class="muted">{{ group.empty }}</p>
                <ul v-else class="retest-files">
                  <li v-for="file in group.items" :key="file.id">
                    <span><strong>{{ file.fileName }}</strong>
                      <small class="muted">{{ formatSize(file.size) }} · {{ formatTime(file.uploadedAt) }}</small></span>
                    <a class="button neutral small" :href="attachmentURL(path, file.id) + scope"
                      :aria-label="'下载 ' + file.fileName"><Download :size="15" aria-hidden="true" />下载</a>
                  </li>
                </ul>
              </section>
            </AsyncState>
          </section>
        </div>
        <section class="panel detail-panel timeline-panel">
          <header class="panel-heading panel-compact"><h2>办理记录</h2></header>
          <AsyncState :error="historyFailure" :empty="!history.length" empty-text="暂无办理记录。" @retry="load()">
            <ol class="timeline">
              <li v-for="(event, index) in history" :key="event.occurredAt + ':' + index">
                <span class="timeline-dot" aria-hidden="true"></span><strong>{{ hazardEventLabel(event) }}</strong>
                <span v-if="event.fromStatus && event.fromStatus !== event.toStatus">
                  {{ hazardStatuses[event.fromStatus] }} → {{ hazardStatuses[event.toStatus] }}</span>
                <span v-else-if="!event.fromStatus">{{ hazardStatuses[event.toStatus] }}</span>
                <p v-if="event.comment" class="retest-text">{{ event.comment }}</p>
                <small>{{ event.operator }} · {{ formatTime(event.occurredAt) }}</small>
              </li>
            </ol>
          </AsyncState>
        </section>
      </div>
    </template>
  </AsyncState>
  <dialog ref="dialog" class="decision-dialog" aria-labelledby="institution-retest-decision"
    @cancel.prevent="!pending && dialog?.close()">
    <form v-if="action" @submit.prevent="submit">
      <h2 id="institution-retest-decision">{{ labels[action] }}</h2>
      <p v-if="action === 'retest_pass'">通过后将交由监管复核。</p>
      <label class="field">
        <span>{{ action === 'retest_return' ? '退回意见' : '复测意见（选填）' }}</span>
        <textarea v-model="comment" rows="4" maxlength="1000" :required="action === 'retest_return'" :disabled="locked" />
      </label>
      <p v-if="actionFailure" class="form-message error-message" role="alert">{{ actionFailure }}</p>
      <div class="form-actions">
        <button type="button" class="button neutral" :disabled="Boolean(pending)" @click="dialog?.close()">取消</button>
        <button v-if="stale" type="button" class="button neutral" @click="reload">刷新隐患</button>
        <button v-else type="submit" class="button primary"
          :disabled="locked || (action === 'retest_return' ? !comment.trim() : needsReport)">
          {{ pending ? '办理中…' : action === 'retest_return' ? '确认退回' : '确认通过' }}
        </button>
      </div>
    </form>
  </dialog>
</template>

<style scoped>
.retest-text { white-space: pre-wrap; overflow-wrap: anywhere; }
.retest-upload { padding: 10px 16px 12px; }
.retest-upload .input-action { flex-wrap: wrap; align-items: end; gap: 8px; }
.retest-materials-group { margin: 0 16px; padding: 10px 0; }
.retest-materials-group + .retest-materials-group { border-top: 1px solid var(--line); }
.retest-materials-group h3 { margin: 0; font-size: 14px; }
.retest-materials-group p { margin: 6px 0 0; }
.retest-files { list-style: none; margin: 0; padding: 0; }
.retest-files li { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 10px 0; }
.retest-files li + li { border-top: 1px solid var(--line); }
.retest-files small { display: block; }
.retest-files strong { overflow-wrap: anywhere; }
</style>

<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { nextBusinessDate, supervisionDownload } from '../lib/supervision'

type Action = 'schedule' | 'record' | 'follow_up' | 'submit_follow_up' | 'return_follow_up' | 'close' | 'cancel'
type SiteStatus = 'scheduled' | 'recorded' | 'following_up' | 'follow_up_submitted' | 'closed' | 'cancelled'
type EvidenceKind = 'inspection_evidence' | 'follow_up_evidence'
interface Evidence { id: string; kind: EvidenceKind; fileName: string }
interface Site {
  status?: SiteStatus
  version: number
  visitAt?: string
  location?: string
  inspectors?: string
  outcome?: 'pass' | 'follow_up_required'
  followUpDueOn?: string
  allowedActions: Array<Action | 'upload'>
  attachments: Evidence[]
  events: Array<{ version: number; action: Action; actor: string; comment?: string; evidenceId?: string; occurredAt: string }>
}

const props = defineProps<{ path: string; organization?: string; taskVersion: number; focusSection?: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const endpoint = computed(() => props.path + '/site-inspection')
const scope = computed(() => params({ organization: props.organization }))
const evidenceKind = computed<EvidenceKind>(() => props.organization ? 'follow_up_evidence' : 'inspection_evidence')
const view = shallowRef<Site>()
const error = ref('')
const section = ref<HTMLElement>()
const showSection = computed(() => Boolean(view.value?.version || view.value?.allowedActions.length || error.value))
const busy = ref(false)
const confirmation = shallowRef<{ action: Action; version: number }>()
const selected = computed(() => confirmation.value?.action)
const visit = ref('')
const location = ref('')
const inspectors = ref('')
const due = ref('')
const outcome = ref('')
const comment = ref('')
let currentRead: AbortController | undefined
let mutation: AbortController | undefined

const names: Record<Action, string> = {
  schedule: '安排现场检查', record: '登记现场结果', follow_up: '下发现场跟进',
  submit_follow_up: '提交跟进证据', return_follow_up: '退回跟进补正',
  close: '办结现场检查', cancel: '取消现场检查',
}
const states: Record<SiteStatus, string> = {
  scheduled: '已安排', recorded: '已登记结果', following_up: '待运营者跟进',
  follow_up_submitted: '待监管复核', closed: '已办结', cancelled: '已取消',
}
const evidenceNames: Record<EvidenceKind, string> = {
  inspection_evidence: '现场检查证据', follow_up_evidence: '运营者跟进证据',
}

/**
 * 用途 查询已加载监督任务中的现场安排及其收件范围。
 * 参数 无，使用父页传入的任务地址与原接收单位。
 * 返回值 可取消的读取请求。
 */
async function load() {
  currentRead?.abort()
  const request = new AbortController()
  currentRead = request
  try {
    const data = await api<Site>(endpoint.value + scope.value, { signal: request.signal })
    if (!request.signal.aborted) view.value = data
  } catch (reason) {
    if (!isAbort(reason)) error.value = errorText(reason)
  }
}

/**
 * 用途 打开当前版本对应的现场动作表单。
 * 参数 action 表示监管安排、现场核查或原收件单位提交。
 * 返回值 无。
 */
function start(action: Action) {
  if (!view.value) return
  confirmation.value = { action, version: view.value.version }
  comment.value = ''
  due.value = nextBusinessDate
  outcome.value = ''
  error.value = ''
  if (action === 'schedule') {
    visit.value = ''
    location.value = view.value?.location ?? ''
    inspectors.value = view.value?.inspectors ?? ''
  }
}

/**
 * 用途 生成当前动作最小字段，现场时间以页面标明的北京时间解释。
 * 参数 action 是正在保存的现场动作，version 是已读版本。
 * 返回值 仅包含对应表单字段的请求对象。
 */
function actionBody(action: Action, version: number) {
  const body = { action, expectedVersion: version, comment: comment.value.trim() || undefined }
  if (action === 'schedule') {
    return { ...body, visitAt: visit.value + ':00+08:00', location: location.value.trim(), inspectors: inspectors.value.trim() }
  }
  if (action === 'record') return { ...body, outcome: outcome.value }
  if (action === 'follow_up') return { ...body, followUpDueOn: due.value }
  return body
}

/**
 * 用途 提交现场动作，请求成功后重新读取材料与轨迹。
 * 参数 无，使用打开表单时核对的动作和版本。
 * 返回值 可取消的状态更新任务。
 */
async function save() {
  const current = confirmation.value
  if (!current || busy.value) return
  const body = actionBody(current.action, current.version)
  await write(endpoint.value + '/actions', body, () => {
    confirmation.value = undefined
    emit('changed')
  })
}

/**
 * 用途 选择一份现场证据，通过服务端实际内容验证后保存。
 * 参数 event 为原生附件输入事件。
 * 返回值 上传及重新读取任务。
 */
async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || !view.value || busy.value) return
  const body = new FormData()
  body.set('kind', evidenceKind.value)
  body.set('expectedVersion', String(view.value.version))
  body.set('file', file)
  await write(endpoint.value + '/attachments', body)
}

/**
 * 用途 串行提交现场材料或动作，版本变化后关闭已失效的确认。
 * 参数 address 与 body 是实际请求地址及业务字段，done 为可选成功操作。
 * 返回值 业务提交及页面刷新任务。
 */
async function write(address: string, body: object | FormData, done?: () => void) {
  const request = new AbortController()
  mutation = request
  busy.value = true
  error.value = ''
  try {
    await api(address + scope.value, { method: 'POST', body, signal: request.signal })
    if (!request.signal.aborted) {
      done?.()
      await load()
    }
  } catch (reason) {
    if (!request.signal.aborted && !isAbort(reason)) {
      if (reason instanceof ApiError && reason.status === 409) {
        confirmation.value = undefined
        error.value = '现场状态或办理范围已变化，请核对最新记录后重新发起操作。'
      } else {
        error.value = errorText(reason)
      }
      await load()
    }
  } finally {
    if (mutation === request) busy.value = false
  }
}

watch(() => [endpoint.value, scope.value], () => {
  mutation?.abort()
  confirmation.value = undefined
  busy.value = false
  error.value = ''
  view.value = undefined
  void load()
}, { immediate: true })
watch(() => props.taskVersion, () => { if (!busy.value && !selected.value) void load() })
watch([() => props.focusSection, section], ([focused, element]) => {
  if (!focused || !element) return
  element.scrollIntoView({ block: 'start' })
  element.focus({ preventScroll: true })
}, { flush: 'post' })
refreshBusinessOnFocus(() => { if (!busy.value && !selected.value) void load() }, 30000)
onScopeDispose(() => { currentRead?.abort(); mutation?.abort() })
</script>

<template>
  <section v-if="showSection" id="site-inspection" ref="section" tabindex="-1"
    class="panel site-panel" aria-label="现场检查">
    <header><h2>现场检查</h2><strong v-if="view?.status">{{ states[view.status] }}</strong></header>
    <dl v-if="view?.version" class="facts">
      <div><dt>现场时间</dt><dd>{{ formatTime(view.visitAt) }}</dd></div>
      <div><dt>检查地点</dt><dd>{{ view.location }}</dd></div>
      <div><dt>检查组</dt><dd>{{ view.inspectors }}</dd></div>
      <div v-if="view.outcome"><dt>现场结果</dt><dd>{{ view.outcome === 'pass' ? '通过' : '需要跟进' }}</dd></div>
      <div v-if="view.followUpDueOn"><dt>跟进截止日期</dt><dd>{{ view.followUpDueOn }}</dd></div>
    </dl>
    <p v-if="error" class="form-message error-message" role="alert">{{ error }}</p>
    <ul v-if="view?.attachments.length" class="site-files">
      <li v-for="item in view.attachments" :key="item.id">
        {{ evidenceNames[item.kind] }} · <a class="text-action" download
          :href="supervisionDownload(endpoint, item.id, organization)">{{ item.fileName }}</a>
      </li>
    </ul>
    <label v-if="view?.allowedActions.includes('upload') && !selected" class="field site-picker">
      <span>上传{{ evidenceNames[evidenceKind] }}</span>
      <input type="file" accept=".pdf,.docx" :name="evidenceKind" :disabled="busy" @change="upload">
    </label>
    <div v-if="view && !selected" class="site-actions">
      <template v-for="action in view.allowedActions" :key="action">
        <button v-if="action !== 'upload'" type="button" class="button neutral"
          :disabled="busy" @click="start(action)">
          {{ action === 'schedule' && view.version ? '调整现场安排' : names[action] }}
        </button>
      </template>
    </div>
    <form v-if="selected" class="site-form" @submit.prevent="save">
      <h3>{{ names[selected] }}</h3>
      <div v-if="selected === 'schedule'" class="site-actions">
        <label class="field"><span>现场时间（北京时间）</span><input v-model="visit" type="datetime-local" required></label>
        <label class="field"><span>检查地点</span><input v-model="location" maxlength="200" required></label>
        <label class="field"><span>检查组</span><input v-model="inspectors" maxlength="200" required></label>
      </div>
      <label v-if="selected === 'record'" class="field"><span>现场结果</span>
        <select v-model="outcome" required>
          <option value="">请选择</option><option value="pass">通过</option><option value="follow_up_required">需要跟进</option>
        </select>
      </label>
      <label v-if="selected === 'follow_up'" class="field"><span>跟进截止日期</span>
        <input v-model="due" type="date" :min="nextBusinessDate" required>
      </label>
      <label class="field"><span>办理说明</span>
        <textarea v-model="comment" maxlength="1000" rows="3"
          :required="selected !== 'schedule' && selected !== 'submit_follow_up'"></textarea>
      </label>
      <div class="site-actions">
        <button type="button" class="button neutral" :disabled="busy" @click="confirmation = undefined">返回</button>
        <button type="submit" class="button primary" :disabled="busy">确认{{ names[selected] }}</button>
      </div>
    </form>
    <details v-if="view?.events.length">
      <summary>现场检查留痕</summary>
      <ol class="site-history">
        <li v-for="event in view.events" :key="event.version">
          <strong>{{ names[event.action] }}</strong> · {{ event.actor }} · {{ formatTime(event.occurredAt) }}
          <p v-if="event.comment">{{ event.comment }}</p>
          <a v-if="event.evidenceId" class="text-action" download
            :href="supervisionDownload(endpoint, event.evidenceId, organization)">下载本轮证据</a>
        </li>
      </ol>
    </details>
  </section>
</template>

<style scoped>
.site-panel,.site-form { display:grid; gap:12px; }
.site-panel { padding:16px; scroll-margin-top:80px; }
.site-panel header,.site-actions { display:flex; align-items:center; flex-wrap:wrap; gap:10px; }
.site-panel header { justify-content:space-between; }
.site-panel h2,.site-panel h3,.site-panel p { margin:0; }
.site-form .site-actions .field { flex:1 1 160px; }
.site-picker { justify-self:start; }
.site-files,.site-history { margin:0; display:grid; gap:6px; padding-inline-start:1.5em; }
.site-history { margin-top:8px; }
</style>

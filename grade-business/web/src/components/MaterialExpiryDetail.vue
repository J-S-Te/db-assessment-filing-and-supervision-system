<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { api, attachmentURL, errorText, isAbort } from '../lib/api'
import { formatSize, formatTime } from '../lib/domain'

import { expiryStatuses, type ExpiryDetail } from '../lib/materialExpiry'

const props = defineProps<{ detail: ExpiryDetail; path: string; query: string }>()
const emit = defineEmits<{ changed: [] }>()
const selected = ref<'renew' | 'approve' | 'reject' | 'retire'>()
const expectedVersion = ref<number>()
const validUntil = ref('')
const evidence = ref('')
const file = ref<File>()
const failure = ref('')
const busy = ref(false)
const notice = ref('')
const statuses = { pending: '待监管审核', approved: '已通过', rejected: '未通过' }
const actions = { renew: '续传材料', approve: '通过续传', reject: '驳回续传', retire: '结束有效期登记' }
const eventLabels: Record<string, string> = { register: '登记', submit: '提交续传', ...actions }
const sourceURL = computed(() => props.detail.sourceAttachmentId
  ? attachmentURL('/' + props.detail.caseKind + 's/' + props.detail.caseId, props.detail.sourceAttachmentId) : '')
let request: AbortController | undefined

/**
 * 用途 打开选定办理动作并清空上一次操作的输入。
 * 参数 action 是后端已声明可用的动作。
 * 返回值 无。
 */
function choose(action: unknown) {
  if (action !== 'renew' && action !== 'approve' && action !== 'reject' && action !== 'retire') return
  selected.value = action
  expectedVersion.value = props.detail.version
  evidence.value = ''
  validUntil.value = ''
  file.value = undefined
  failure.value = notice.value = ''
}

/**
 * 用途 从原生控件读取一份拟续期的新材料，后台再次核对文件内容。
 * 参数 event 是文件选择事件。
 * 返回值 无。
 */
function selectFile(event: Event) {
  file.value = (event.target as HTMLInputElement).files?.[0]
}

/**
 * 用途 组装最少请求字段，提交明确续期或监管审核动作。
 * 参数 无，从当前台账和表单取得动作、日期和证据。
 * 返回值 当前操作任务，成功后要求刷新详情。
 */
async function save() {
  const action = selected.value
  const version = expectedVersion.value
  if (!action || !version || busy.value || !props.detail.allowedActions.includes(action)) return
  const note = evidence.value.trim()
  if ((!note && action !== 'approve') || (action === 'renew' && (!file.value || !validUntil.value))) return
  const active = new AbortController()
  request = active
  busy.value = true
  failure.value = notice.value = ''
  try {
    const reviewed = { action, expectedVersion: version, ...(note ? { comment: note } : {}) }
    let body: FormData | typeof reviewed = reviewed
    if (action === 'renew' && file.value) {
      body = new FormData()
      body.set('file', file.value)
      body.set('validUntil', validUntil.value)
      body.set('evidence', note)
      body.set('expectedVersion', String(version))
    }
    await api(props.path + (action === 'renew' ? '/renewals' : '/actions') + props.query, {
      method: 'POST', body, signal: active.signal,
    })
    if (!active.signal.aborted) {
      selected.value = undefined
      notice.value = action === 'renew' ? '续传已提交，待监管核验有效期。' : '办理结果已保存。'
      emit('changed')
    }
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === active) busy.value = false
  }
}

watch(() => props.detail.id, () => { selected.value = undefined; notice.value = ''; request?.abort() })
onScopeDispose(() => request?.abort())
</script>

<template>
  <div class="expiry-detail">
    <section class="panel expiry-panel" aria-label="材料有效期详情">
      <header><h2>{{ detail.systemName }}</h2><p class="muted">{{ detail.number }} · {{ detail.organizationName }}</p></header>
      <dl class="facts">
        <div><dt>原件</dt><dd>
          <a v-if="sourceURL" class="text-action" :href="sourceURL" download>{{ detail.sourceFileName }}</a>
          <span v-else>{{ detail.sourceFileName }}</span>
        </dd></div>
        <div><dt>有效期至</dt><dd>{{ detail.validUntil }}</dd></div>
        <div><dt>日期状态</dt><dd>{{ expiryStatuses[detail.dateStatus] }}</dd></div>
        <div><dt>续传审核</dt><dd>{{ detail.renewalPending ? '待监管审核' : '暂无待审核续传' }}</dd></div>
        <div><dt>当前日期依据</dt><dd class="expiry-text">{{ detail.evidence }}</dd></div>
      </dl>
      <div v-if="!selected" class="expiry-actions">
        <button v-for="action in detail.allowedActions" :key="action" class="button neutral" type="button"
          @click="choose(action)">{{ actions[action as keyof typeof actions] }}</button>
      </div>
      <form v-else class="expiry-action" @submit.prevent="save">
        <h3>{{ actions[selected] }}</h3>
        <template v-if="selected === 'renew'">
          <label class="field"><span>新的有效期至</span>
            <input v-model="validUntil" type="date" required :disabled="busy">
          </label>
          <label class="field"><span>新的 PDF 或 DOCX</span>
            <input type="file" accept=".pdf,.docx" required :disabled="busy" @change="selectFile">
          </label>
        </template>
        <label class="field"><span>{{ selected === 'renew' ? '新的日期依据' : '办理说明' }}</span>
          <textarea v-model="evidence" maxlength="1000" rows="3" :required="selected !== 'approve'" :disabled="busy"></textarea>
        </label>
        <div class="expiry-actions">
          <button type="button" class="button neutral" :disabled="busy" @click="selected = undefined">取消</button>
          <button type="submit" class="button primary" :disabled="busy">
            {{ busy ? '提交中…' : '确认' + actions[selected] }}
          </button>
        </div>
      </form>
      <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
      <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
    </section>
    <section class="panel expiry-panel" aria-label="续传版本">
      <h2>续传版本</h2>
      <p v-if="!detail.renewals.length" class="muted">暂无续传记录。</p>
      <ul v-else class="expiry-history">
        <li v-for="item in detail.renewals" :key="item.id">
          <strong>{{ statuses[item.status] }}</strong> · {{ item.audience === 'institution' ? '测评机构' : '运营单位' }}
          · 拟有效至 {{ item.validUntil }} · {{ formatTime(item.submittedAt) }}
          <p v-if="item.evidence" class="expiry-text">{{ item.evidence }}</p>
          <a v-if="item.download" class="text-action"
            :href="'/api/v1' + path + '/renewals/' + item.id + '/download' + query" download>{{ item.fileName }}</a>
          <small v-if="item.size" class="muted">{{ formatSize(item.size) }}</small>
        </li>
      </ul>
    </section>
    <section class="panel expiry-panel" aria-label="有效期历史">
      <h2>有效期历史</h2>
      <ol class="expiry-history">
        <li v-for="item in detail.history" :key="item.version">
          <strong>{{ eventLabels[item.action] || item.action }}</strong>
          · 周期 {{ item.cycle }} · 有效期至 {{ item.validUntil }} · {{ item.actor }} · {{ formatTime(item.createdAt) }}
          <p v-if="item.evidence" class="expiry-text">{{ item.evidence }}</p>
        </li>
      </ol>
    </section>
  </div>
</template>

<style scoped>
.expiry-detail,.expiry-panel,.expiry-action,.expiry-history { display: grid; gap: 12px; }
.expiry-panel { padding: 16px; }
.expiry-panel h2,.expiry-panel p { margin: 0; }
.expiry-action { max-width: 44rem; }
.expiry-actions { display: flex; flex-wrap: wrap; gap: 10px; }
.expiry-history { margin: 0; padding-inline-start: 1.4rem; }
.expiry-history small { margin-inline-start: .65em; }
.expiry-text { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

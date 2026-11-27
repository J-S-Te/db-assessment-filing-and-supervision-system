<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { refreshWorkbench } from '../lib/workbench'

const props = defineProps<{
  path: string; suffix?: string; version: number; disabled?: boolean
}>()
const emit = defineEmits<{ changed: [] }>()
type WithdrawalStatus = 'pending' | 'approved' | 'rejected'
type WithdrawalRecord = {
  id: string; status: WithdrawalStatus; caseVersion: number; reason: string
  requestedAt: string; comment?: string; decidedBy?: string; decidedAt?: string
}
type WithdrawalView = {
  request?: WithdrawalRecord; previous?: WithdrawalRecord[]; canRequest?: true; canDecide?: true
}
type Choice = 'request' | 'approve' | 'reject'
const labels = { pending: '待监管确认撤回', approved: '已确认撤回', rejected: '撤回申请已驳回' }
const view = ref<WithdrawalView>()
const failure = ref('')
const choice = ref<Choice>()
const note = ref('')
const changing = ref(false)
const endpoint = computed(() => props.path + '/withdrawal')
const sameVersion = computed(() => !view.value?.request || view.value.request.caseVersion === props.version)
let query: AbortController | undefined
let mutation: AbortController | undefined

/**
 * 用途 加载当前业务撤回及最近历史，忽略已变更范围的响应。
 * 参数 无，业务和席位来自父页面。
 * 返回值 当前读取任务。
 */
async function load() {
  if (changing.value) return
  query?.abort()
  const request = new AbortController()
  query = request
  failure.value = ''
  try {
    const result = await api<WithdrawalView>(endpoint.value + (props.suffix ?? ''), { signal: request.signal })
    if (query === request) view.value = result
  } catch (error) {
    if (query === request && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (query === request) query = undefined
  }
}

/**
 * 用途 为有权执行的撤回动作展示必填原因和确认。
 * 参数 action 为申请、同意或驳回。
 * 返回值 无。
 */
function choose(action: Choice) {
  choice.value = action
  note.value = ''
}

/**
 * 用途 按当前业务版本提交申请或监管决定并刷新父详情。
 * 参数 无，当前选择和意见来自表单。
 * 返回值 提交任务。
 */
async function save() {
  const action = choice.value
  const text = note.value.trim()
  const item = view.value?.request
  if (changing.value || props.disabled || !action || !text) return
  if (action !== 'request' && (!item || !sameVersion.value || !view.value?.canDecide)) return
  if (action === 'request' && !view.value?.canRequest) return
  const expectedVersion = props.version
  const data = action === 'request' ? { expectedVersion, reason: text }
    : { withdrawalId: item?.id, expectedVersion, decision: action, comment: text }
  const request = new AbortController()
  mutation = request
  query?.abort()
  changing.value = true
  failure.value = ''
  try {
    const url = endpoint.value + (action === 'request' ? props.suffix ?? '' : '/decision')
    const result = await api<WithdrawalView>(url, { method: 'POST', body: data, signal: request.signal })
    if (mutation !== request) return
    view.value = result
    choice.value = undefined
    emit('changed')
    void refreshWorkbench()
  } catch (error) {
    if (mutation !== request || isAbort(error)) return
    if (error instanceof ApiError && error.status === 409) {
      choice.value = undefined
      emit('changed')
    }
    failure.value = errorText(error)
  } finally {
    if (mutation === request) {
      mutation = undefined
      changing.value = false
    }
  }
}

watch(() => [props.path, props.suffix], () => {
  mutation?.abort()
  mutation = undefined
  changing.value = false
  choice.value = undefined
  view.value = undefined
  void load()
}, { immediate: true })
watch(() => props.version, () => void load())
refreshBusinessOnFocus(() => { if (!choice.value && !query) void load() })
onScopeDispose(() => { query?.abort(); mutation?.abort() })
</script>

<template>
  <section v-if="view?.request || view?.canRequest || failure" class="panel withdrawal-panel"
    aria-label="备案撤回">
    <header><h2>备案撤回</h2></header>
    <div v-if="view?.request" aria-live="polite">
      <strong>{{ labels[view.request.status] }}</strong>
      <p>申请于 {{ formatTime(view.request.requestedAt) }}</p>
      <p>申请原因 {{ view.request.reason }}</p>
      <p v-if="view.request.comment">监管意见 {{ view.request.comment }}</p>
      <p v-if="view.request.decidedAt" class="muted">
        {{ view.request.decidedBy }} · {{ formatTime(view.request.decidedAt) }}
      </p>
    </div>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}
      <button class="text-action" type="button" :disabled="changing" @click="load()">重试读取</button>
    </p>
    <div v-if="!choice" class="withdrawal-actions">
      <button v-if="view?.canRequest" class="button neutral" type="button"
        :disabled="disabled || changing" @click="choose('request')">申请在审撤回</button>
      <template v-if="view?.canDecide">
        <button class="button primary" type="button" :disabled="disabled || changing || !sameVersion"
          @click="choose('approve')">同意撤回</button>
        <button class="button neutral" type="button" :disabled="disabled || changing || !sameVersion"
          @click="choose('reject')">驳回撤回申请</button>
      </template>
    </div>
    <form v-else class="withdrawal-form" @submit.prevent="save">
      <label class="field"><span>{{ choice === 'request' ? '撤回原因' : '监管意见' }}</span>
        <textarea v-model="note" rows="3" maxlength="1000" required :disabled="changing" />
      </label>
      <div class="withdrawal-actions">
        <button type="button" class="button neutral" :disabled="changing" @click="choice = undefined">取消</button>
        <button type="submit" class="button primary" :disabled="disabled || changing || !note.trim()">
          {{ changing ? '正在提交…' : choice === 'request' ? '提交撤回申请' : choice === 'approve' ? '确认同意撤回' : '确认驳回撤回' }}
        </button>
      </div>
    </form>
    <details v-if="view?.previous?.length">
      <summary>查看历史撤回申请</summary>
      <ul><li v-for="item in view.previous" :key="item.id">
        {{ formatTime(item.requestedAt) }} · {{ labels[item.status] }} · {{ item.reason }}
        <p v-if="item.comment">监管意见 {{ item.comment }}</p>
      </li></ul>
    </details>
  </section>
</template>

<style scoped>
.withdrawal-panel { margin: 12px 0; padding: 16px; display: grid; gap: 8px; }
h2, p { margin: 0; overflow-wrap: anywhere; }
.withdrawal-form { display: grid; gap: 9px; }
.withdrawal-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.withdrawal-actions:empty { display: none; }
</style>

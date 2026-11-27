<script setup lang="ts">
import { onMounted, onScopeDispose, ref, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import { feedbackDecisions, feedbackPath, type FeedbackDelivery } from '../lib/reportFeedback'

const props = defineProps<{ path: string; institution: string }>()
const items = ref<FeedbackDelivery[]>([])
const loading = ref(false)
const failure = ref('')
let request: AbortController | undefined
let interval: ReturnType<typeof setInterval> | undefined

/**
 * 用途 查询一次当前测评报告已回传的审核意见和签收时间。
 * 参数 无，使用当前传入的监管记录路径。
 * 返回值 回执列表加载任务。
 */
async function load() {
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  try {
    const result = await api<{ items: FeedbackDelivery[] }>(props.path + '/feedback', { signal: current.signal })
    if (request === current) {
      items.value = result.items
      failure.value = ''
    }
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) loading.value = false
  }
}

/**
 * 用途 在留痕页签位于前台时刷新机构签收情况。
 * 参数 无。
 * 返回值 无。
 */
function refreshVisible() {
  if (!loading.value && document.visibilityState === 'visible') void load()
}

watch(() => props.path, () => {
  items.value = []
  failure.value = ''
  void load()
}, { immediate: true })
onMounted(() => {
  interval = setInterval(refreshVisible, 10_000)
  document.addEventListener('visibilitychange', refreshVisible)
})
onScopeDispose(() => {
  request?.abort()
  clearInterval(interval)
  document.removeEventListener('visibilitychange', refreshVisible)
})
</script>

<template>
  <section class="panel detail-panel" aria-label="机构回执">
    <header class="panel-heading panel-compact">
      <h2>机构回执</h2>
      <div class="page-actions">
        <button class="button neutral small" type="button" :disabled="loading" @click="load">刷新回执</button>
        <RouterLink class="text-action" :to="{ path: feedbackPath, query: { institution } }">打开机构收件箱</RouterLink>
      </div>
    </header>
    <p v-if="items.length && failure" class="form-message warning-message" role="status">{{ failure }}</p>
    <AsyncState :loading="loading && !items.length" :error="items.length ? '' : failure" :empty="!items.length"
      empty-text="暂无已回传的退回或终审意见。" @retry="load">
      <ul class="feedback-receipts">
        <li v-for="item in items" :key="item.decision + ':' + item.sentAt">
          <div><strong>{{ feedbackDecisions[item.decision] }}</strong>
            <small>{{ item.institution }} · {{ formatTime(item.sentAt) }}</small>
          </div>
          <div>{{ item.acknowledgedAt ? '已签收' : '待签收' }}
            <small v-if="item.acknowledgedAt">{{ formatTime(item.acknowledgedAt) }}</small>
          </div>
        </li>
      </ul>
    </AsyncState>
  </section>
</template>

<style scoped>
.feedback-receipts { margin: 0; padding: 6px 16px; list-style: none; }
.feedback-receipts li { display: flex; justify-content: space-between; gap: 12px; padding: 9px 0; }
.feedback-receipts li + li { border-top: 1px solid var(--line); }
.feedback-receipts small { display: block; color: var(--muted); }
</style>

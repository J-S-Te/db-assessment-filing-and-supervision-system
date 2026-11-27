<script setup lang="ts">
import { onScopeDispose, ref, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import { businessStatus, type BusinessResource } from '../lib/scopedBusiness'

const props = defineProps<{ resource: BusinessResource | 'assessment-dues'; recordId: string }>()
const dialog = ref<HTMLDialogElement>()
interface Receipt {
  id: string
  recipient: string
  state: string
  sentAt: string
  acknowledgedAt?: string
}
const items = ref<Receipt[]>([])
const loading = ref(false)
const failure = ref('')
let request: AbortController | undefined

/**
 * 用途 读取当前业务的送达对象和签收状态。
 * 参数 无，记录标识及类型由监管详情提供。
 * 返回值 回执列表的更新任务。
 */
async function load() {
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  failure.value = ''
  try {
    const path = '/review-feedback/' + props.resource + '/' + encodeURIComponent(props.recordId)
    const result = await api<{ items: Receipt[] }>(path, { signal: current.signal })
    if (request === current) items.value = result.items
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) loading.value = false
  }
}

/**
 * 用途 打开回执查看窗口并读取最新结果。
 * 参数 无。
 * 返回值 回执加载任务。
 */
function open() {
  dialog.value?.showModal()
  return load()
}

watch(() => props.recordId, () => {
  request?.abort()
  items.value = []
  dialog.value?.close()
})
onScopeDispose(() => request?.abort())
</script>

<template>
  <button class="button neutral" type="button" @click="open">站内回执</button>
  <dialog ref="dialog" class="decision-dialog" aria-label="站内回执">
    <h2>站内回执</h2>
    <AsyncState :loading="loading" :error="failure" :empty="!items.length"
      empty-text="这条业务暂无已送达的站内意见。" @retry="load">
      <ul class="notice-receipts">
        <li v-for="item in items" :key="item.id">
          <div>
            <strong>{{ businessStatus(resource, item.state) }}</strong>
            <small>{{ item.recipient }} · {{ formatTime(item.sentAt) }}</small>
          </div>
          <div>
            {{ item.acknowledgedAt ? '已签收' : '待签收' }}
            <small v-if="item.acknowledgedAt">{{ formatTime(item.acknowledgedAt) }}</small>
          </div>
        </li>
      </ul>
    </AsyncState>
    <div class="form-actions">
      <button class="button neutral" type="button" :disabled="loading" @click="load">刷新回执</button>
      <button class="button primary" type="button" @click="dialog?.close()">关闭</button>
    </div>
  </dialog>
</template>

<style scoped>
.notice-receipts { list-style: none; padding: 0; }
.notice-receipts li { display: flex; justify-content: space-between; gap: 16px; padding: 8px 0; }
.notice-receipts li + li { border-top: 1px solid var(--line); }
.notice-receipts small { display: block; color: var(--muted); }
</style>

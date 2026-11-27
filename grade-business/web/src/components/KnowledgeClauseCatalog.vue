<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { clausePage, type ClauseEntry, writeIdentity } from '../lib/clauses'

const props = defineProps<{ path: string; version: number }>()
const emit = defineEmits<{ refresh: [] }>()
const items = shallowRef<ClauseEntry[]>([])
const nextAfter = ref('')
const loading = ref(false)
const saving = ref(false)
const failure = ref('')
const notice = ref('')
const chosen = shallowRef<ClauseEntry>()
const editing = ref(false)
const number = ref('')
const text = ref('')
const active = ref(true)
const requestID = writeIdentity()
let readRequest: AbortController | undefined
let writeRequest: AbortController | undefined

/**
 * 用途 按页面已显示的知识版本读取条款，并按需追加分页。
 * 参数 after 为上一页最后的条款号，省略时从第一页读取。
 * 返回值 条款加载任务。
 */
async function load(after = '') {
  readRequest?.abort()
  const request = new AbortController()
  readRequest = request
  loading.value = true
  failure.value = ''
  try {
    const page = await clausePage(props.path, request.signal, after)
    if (request.signal.aborted) return
    if (page.knowledgeVersion !== props.version) {
      emit('refresh')
      return
    }
    items.value = after ? [...items.value, ...page.items] : page.items
    nextAfter.value = page.nextAfter ?? ''
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (readRequest === request) loading.value = false
  }
}

/**
 * 用途 用已有条款或空白字段显示编辑表单。
 * 参数 item 为需要修订的条款，省略时打开新增表单。
 * 返回值 无。
 */
function open(item?: ClauseEntry) {
  chosen.value = item
  number.value = item?.number ?? ''
  text.value = item?.text ?? ''
  active.value = item?.active ?? true
  failure.value = notice.value = ''
  editing.value = true
}

/**
 * 用途 保存条款原文和启停状态，防止重试产生额外版本。
 * 参数 无，使用当前知识版本及表单内容。
 * 返回值 提交与刷新任务。
 */
async function save() {
  if (saving.value || !number.value.trim() || !text.value.trim()) return
  const request = new AbortController()
  writeRequest = request
  const body = { knowledgeVersion: props.version, number: number.value.trim(),
    expectedVersion: chosen.value?.version ?? 0, text: text.value.trim(), active: active.value }
  failure.value = notice.value = ''
  saving.value = true
  try {
    await api<ClauseEntry>(props.path + '/clauses', { method: 'POST', signal: request.signal,
      body: { ...body, requestId: requestID({ path: props.path, ...body }) } })
    if (request.signal.aborted) return
    requestID()
    editing.value = false
    await load()
    if (!request.signal.aborted) notice.value = '条款已保存。'
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    if (error instanceof ApiError && error.code === 'source_conflict') await load()
    if (!request.signal.aborted) failure.value = errorText(error)
  } finally {
    if (writeRequest === request) saving.value = false
  }
}

watch(() => [props.path, props.version], () => {
  writeRequest?.abort()
  saving.value = editing.value = false
  items.value = []
  nextAfter.value = notice.value = ''
  void load()
}, { immediate: true })
onBeforeUnmount(() => {
  readRequest?.abort()
  writeRequest?.abort()
})
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="knowledge-clause-title">
    <header class="panel-heading panel-compact">
      <h2 id="knowledge-clause-title">条款目录</h2>
      <button class="button neutral small" type="button" :disabled="saving || editing"
        @click="open()">新增条款</button>
    </header>
    <p class="muted">录入当前法规或标准的正式原文。知识正文版本变更后，请核对并登记新版条款。</p>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
    <form v-if="editing" class="clause-edit" @submit.prevent="save">
      <label class="field"><span>条款编号</span>
        <input v-model="number" maxlength="48" placeholder="例如 3.1.2 或 十二" :disabled="saving || !!chosen" required>
      </label>
      <label class="field"><span>条款原文</span>
        <textarea v-model="text" rows="5" maxlength="4000" :disabled="saving" required />
      </label>
      <label><input v-model="active" type="checkbox" :disabled="saving">启用</label>
      <div class="form-actions">
        <button class="button neutral" type="button" :disabled="saving" @click="editing = false">取消</button>
        <button class="button primary" type="submit" :disabled="saving">保存条款</button>
      </div>
    </form>
    <p v-if="loading && !items.length" class="muted" role="status">正在读取条款…</p>
    <p v-else-if="!items.length && !failure" class="muted">当前知识版本暂无条款。</p>
    <ul v-if="items.length" class="clause-entries">
      <li v-for="item in items" :key="item.number">
        <details>
          <summary>{{ item.number }} · 条款版本 {{ item.version }} · {{ item.active ? '启用' : '已停用' }}</summary>
          <p>{{ item.text }}</p>
        </details>
        <button class="text-action" type="button" :disabled="saving || editing" @click="open(item)">修订条款</button>
      </li>
    </ul>
    <button v-if="nextAfter" class="button neutral small" type="button" :disabled="loading" @click="load(nextAfter)">
      加载更多条款
    </button>
  </section>
</template>

<style scoped>
.clause-edit { display: grid; max-width: 52rem; gap: 12px; padding-block: 12px; }
.clause-entries { display: grid; gap: 10px; padding-inline-start: 22px; }
.clause-entries p { white-space: pre-wrap; overflow-wrap: anywhere; }
summary { cursor: pointer; }
</style>

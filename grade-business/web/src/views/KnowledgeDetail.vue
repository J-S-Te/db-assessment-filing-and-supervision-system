<script setup lang="ts">
import { hasPermission } from '../lib/session'
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { ArrowLeft, BookOpen } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import KnowledgeClauseCatalog from '../components/KnowledgeClauseCatalog.vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { categories, formatTime } from '../lib/domain'
import { knowledgePath, knowledgeRecord, type KnowledgeEntry } from '../lib/knowledge'
import KnowledgeHistory from './KnowledgeHistory.vue'

const route = useRoute()
const path = computed(() => typeof route.params.id === 'string' ? knowledgeRecord(route.params.id) : '')
const item = shallowRef<KnowledgeEntry>()
const loading = ref(false)
const changing = ref(false)
const showingHistory = ref(false)
const failure = ref('')
const statusFailure = ref('')
const notice = ref('')
const statusDialog = ref<HTMLDialogElement>()
const statusAction = computed(() => item.value?.inactiveAt ? '启用' : '停用')
let request: AbortController | undefined
let mutation: AbortController | undefined

/**
 * 用途 重新读取当前知识条目的纯文本正文。
 * 参数 无，标识由路由提供。
 * 返回值 加载任务。
 */
async function load() {
  if (!path.value) return
  request?.abort()
  const next = new AbortController()
  request = next
  item.value = undefined
  failure.value = ''
  loading.value = true
  try {
    const result = await api<KnowledgeEntry>(path.value, { signal: next.signal })
    if (request === next) item.value = result
  } catch (error) {
    if (request === next && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === next) loading.value = false
  }
}

/**
 * 用途 确认后按当前条目版本切换启停状态，并重新读取结果。
 * 参数 无，状态和版本来自当前详情。
 * 返回值 更新及刷新任务。
 */
async function changeEntryStatus() {
  if (!item.value || changing.value) return
  const current = new AbortController()
  const active = Boolean(item.value.inactiveAt)
  const body = { active, expectedVersion: item.value.version }
  mutation = current
  changing.value = true
  statusFailure.value = notice.value = ''
  try {
    await api(path.value + '/status', { method: 'PATCH', body, signal: current.signal })
    if (current.signal.aborted) return
    notice.value = active ? '知识已启用。' : '知识已停用。'
    statusDialog.value?.close()
    await load()
  } catch (error) {
    if (current.signal.aborted || isAbort(error)) return
    statusDialog.value?.close()
    statusFailure.value = errorText(error)
    if (error instanceof ApiError && error.code === 'version_conflict') await load()
  } finally {
    if (mutation === current) {
      mutation = undefined
      changing.value = false
    }
  }
}

watch(path, () => {
  mutation?.abort()
  mutation = undefined
  changing.value = showingHistory.value = false
  statusFailure.value = notice.value = ''
  statusDialog.value?.close()
  void load()
}, { immediate: true })
onBeforeUnmount(() => {
  request?.abort()
  mutation?.abort()
})
</script>

<template>
  <section class="knowledge-detail">
    <RouterLink class="back-link" :to="{ path: knowledgePath, query: route.query }"><ArrowLeft :size="16" aria-hidden="true" />返回知识库</RouterLink>
    <AsyncState :loading="loading" :error="failure" @retry="load">
      <template v-if="item">
        <header class="page-header">
          <div><h1>{{ item.title }}</h1><p>{{ item.source }}</p></div>
          <div class="page-actions">
            <span class="level-tag">{{ categories[item.category] ?? item.category }}</span>
            <button class="button neutral small" type="button" aria-controls="knowledge-versions"
              :aria-expanded="showingHistory" @click="showingHistory = !showingHistory">版本历史</button>
            <RouterLink v-if="hasPermission('admin.agent.kb.edit')" class="button neutral small"
              :to="{ path: path + '/tags', query: route.query }">标签</RouterLink>
            <RouterLink v-if="hasPermission('admin.agent.kb.edit')" class="button neutral small"
              :to="{ path: path + '/edit', query: route.query }">编辑</RouterLink>
            <button v-if="hasPermission('admin.agent.kb.edit')" class="button neutral small"
              type="button" :disabled="changing"
              @click="statusDialog?.showModal()">{{ statusAction }}</button>
          </div>
        </header>
        <p v-if="statusFailure" class="form-message error-message" role="alert">{{ statusFailure }}</p>
        <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
        <p v-if="item.inactiveAt" class="form-message warning-message" role="status">
          已停用 · {{ formatTime(item.inactiveAt) }}
        </p>
        <div v-if="item.tags.length" class="knowledge-tags" aria-label="知识标签">
          <span v-for="tag in item.tags" :key="tag" class="level-tag knowledge-tag">{{ tag }}</span>
        </div>
        <article class="panel">
          <header class="panel-heading panel-compact">
            <div class="heading-icon"><BookOpen :size="19" aria-hidden="true" /><h2>正文</h2></div>
            <span class="muted">v{{ item.version }} · {{ formatTime(item.updatedAt) }}</span>
          </header>
          <div class="knowledge-body">{{ item.body }}</div>
        </article>
        <KnowledgeClauseCatalog v-if="!item.inactiveAt && ['regulation', 'standard'].includes(item.category)"
          :path="path" :version="item.version" @refresh="load" />
        <KnowledgeHistory v-if="showingHistory" :path="path" :current-version="item.version" />
      </template>
    </AsyncState>
    <dialog ref="statusDialog" class="decision-dialog" aria-labelledby="knowledge-status-title"
      @cancel.prevent="!changing && statusDialog?.close()">
      <form @submit.prevent="changeEntryStatus">
        <h2 id="knowledge-status-title">{{ statusAction }}知识</h2>
        <p>{{ item?.inactiveAt ? '启用后，条目将重新出现在常规检索结果中。' : '停用后，仍可在知识管理中查看正文和历史版本。' }}</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="changing" @click="statusDialog?.close()">取消</button>
          <button class="button primary" type="submit" :disabled="changing || loading || !item">
            {{ changing ? '保存中…' : '确认' + statusAction }}
          </button>
        </div>
      </form>
    </dialog>
  </section>
</template>

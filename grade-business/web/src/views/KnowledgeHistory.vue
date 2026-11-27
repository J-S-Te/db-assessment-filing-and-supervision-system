<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from '../components/AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { categories, formatTime, type Page } from '../lib/domain'
import { revisionActions, type KnowledgeRevision, type KnowledgeRevisionDetail } from '../lib/knowledge'

const props = defineProps<{ path: string; currentVersion: number }>()
const history = shallowRef<Page<KnowledgeRevision>>({ items: [] })
const snapshot = shallowRef<KnowledgeRevisionDetail>()
const loading = ref(false)
const snapshotLoading = ref(false)
const failure = ref('')
const snapshotFailure = ref('')
const selected = ref(0)
let historyRequest: AbortController | undefined
let detailRequest: AbortController | undefined

/**
 * 用途 读取知识版本列表，向更早的历史继续分页。
 * 参数 before 为上页末尾版本游标，省略时从当前版本开始。
 * 返回值 版本列表加载任务。
 */
async function loadVersions(before?: string) {
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  loading.value = true
  failure.value = ''
  try {
    const path = props.path + '/versions' + params({ before })
    const result = await api<Page<KnowledgeRevision>>(path, { signal: request.signal })
    if (request.signal.aborted) return
    history.value = before ? { ...result, items: [...history.value.items, ...result.items] } : result
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (historyRequest === request) loading.value = false
  }
}

/**
 * 用途 查看指定版本的正文与该时刻的条目状态。
 * 参数 version 为用户在历史列表中选中的版本号。
 * 返回值 对应版本的详情查询任务。
 */
async function openVersion(version: number) {
  detailRequest?.abort()
  const request = new AbortController()
  detailRequest = request
  selected.value = version
  snapshot.value = undefined
  snapshotLoading.value = true
  snapshotFailure.value = ''
  try {
    const result = await api<KnowledgeRevisionDetail>(props.path + '/versions/' + version, { signal: request.signal })
    if (!request.signal.aborted) snapshot.value = result
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) snapshotFailure.value = errorText(error)
  } finally {
    if (detailRequest === request) snapshotLoading.value = false
  }
}

watch(() => [props.path, props.currentVersion], () => {
  detailRequest?.abort()
  selected.value = 0
  history.value = { items: [] }
  void loadVersions()
}, { immediate: true })
onScopeDispose(() => {
  historyRequest?.abort()
  detailRequest?.abort()
})
</script>

<template>
  <section id="knowledge-versions" class="panel knowledge-versions" aria-labelledby="knowledge-versions-title">
    <header class="panel-heading panel-compact"><h2 id="knowledge-versions-title">版本历史</h2></header>
    <AsyncState :loading="loading && !history.items.length" :error="history.items.length ? '' : failure"
      :empty="!history.items.length" empty-text="暂无可查看的版本。" @retry="loadVersions()">
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>版本</th><th>操作</th><th>时间</th><th>内容</th></tr></thead>
          <tbody>
            <tr v-for="version in history.items" :key="version.version">
              <td>v{{ version.version }}<small v-if="version.version === currentVersion" class="muted"> · 当前</small></td>
              <td>{{ revisionActions[version.action] }}</td>
              <td>{{ formatTime(version.updatedAt) }}</td>
              <td>
                <button type="button" class="text-action" :aria-pressed="selected === version.version"
                  @click="openVersion(version.version)">查看快照</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <footer v-if="history.nextCursor" class="panel-footer">
      <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
      <button type="button" class="button neutral small" :disabled="loading" @click="loadVersions(history.nextCursor)">
        {{ loading ? '加载中…' : '加载更早版本' }}
      </button>
    </footer>
    <div v-if="selected" class="knowledge-snapshot">
      <AsyncState :loading="snapshotLoading" :error="snapshotFailure" @retry="openVersion(selected)">
        <template v-if="snapshot">
          <header class="panel-heading panel-compact">
            <h3>v{{ snapshot.version }} · {{ snapshot.title }}</h3>
            <span class="level-tag">{{ snapshot.inactiveAt ? '已停用' : '启用' }}</span>
          </header>
          <dl class="facts">
            <div><dt>分类</dt><dd>{{ categories[snapshot.category] }}</dd></div>
            <div><dt>来源</dt><dd>{{ snapshot.source }}</dd></div>
            <div><dt>标签</dt><dd>{{ snapshot.tags.join('、') || '—' }}</dd></div>
          </dl>
          <div class="knowledge-body">{{ snapshot.body }}</div>
        </template>
      </AsyncState>
    </div>
  </section>
</template>

<style scoped>
.knowledge-versions { margin-top: 16px; }
.knowledge-snapshot { border-top: 1px solid var(--line); }
.knowledge-snapshot h3 { overflow-wrap: anywhere; }
.knowledge-snapshot .knowledge-body { padding-top: 0; }
</style>

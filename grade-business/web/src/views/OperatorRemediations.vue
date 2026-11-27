<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Plus, RefreshCw } from '@lucide/vue'
import OperatorWorkflowTabs from '../components/OperatorWorkflowTabs.vue'
import AsyncState from '../components/AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime, type Page } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { operatorRemediationPath, remediationBadge, remediationRecordPath, remediationStatuses,
  type RemediationSummary } from '../lib/remediations'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

const route = useRoute()
const router = useRouter()
const unitName = ref('')
const keyword = ref('')
const status = ref('')
const page = shallowRef<Page<RemediationSummary>>({ items: [] })
const loading = ref(false)
const failure = ref('')
const refreshFailure = ref('')
const cursors = ref<string[]>([])
const currentScope = computed(() => queryValue(route.query.organization).trim())
const scope = computed(() => ({ organization: currentScope.value }))
const criteria = computed(() => ({ ...scope.value, q: queryValue(route.query.q),
  status: queryValue(route.query.status), cursor: queryValue(route.query.cursor) }))
let request: AbortController | undefined

/**
 * 用途 读取当前单位和筛选对应的一页整改规划。
 * 参数 quiet 表示页面切回后的后台更新，保留上次成功数据。
 * 返回值 列表读取任务。
 */
async function load(quiet = false) {
  request?.abort()
  if (!currentScope.value) {
    page.value = { items: [] }
    loading.value = false
    return
  }
  const active = new AbortController()
  request = active
  if (!quiet) {
    loading.value = true
    failure.value = ''
  }
  refreshFailure.value = ''
  try {
    const result = await api<Page<RemediationSummary>>(operatorRemediationPath + params(criteria.value), {
      signal: active.signal,
    })
    if (active.signal.aborted) return
    page.value = result
    failure.value = ''
  } catch (error) {
    if (isAbort(error)) return
    if (quiet && page.value.items.length) refreshFailure.value = errorText(error)
    else failure.value = errorText(error)
  } finally {
    if (request === active) loading.value = false
  }
}

/**
 * 用途 应用备案单位和用户选择的列表筛选。
 * 参数 无，使用表单中的单位全称、关键词和审核状态。
 * 返回值 列表导航或刷新任务。
 */
function search() {
  const organization = unitName.value.trim()
  const q = keyword.value.trim()
  if (organization === currentScope.value && q === criteria.value.q && status.value === criteria.value.status
    && !criteria.value.cursor) return load()
  cursors.value = []
  return router.push({ path: operatorRemediationPath, query: {
    organization: organization || undefined, q: q || undefined, status: status.value || undefined,
  } })
}

/**
 * 用途 使用最近一次查询结果中的游标浏览下一页。
 * 参数 无。
 * 返回值 下一页导航任务或空。
 */
function nextPage() {
  if (!page.value.nextCursor || loading.value) return
  cursors.value.push(criteria.value.cursor)
  return router.push({ query: { ...route.query, cursor: page.value.nextCursor } })
}

/**
 * 用途 返回已浏览的上一页，直接打开旧游标时回到第一页。
 * 参数 无。
 * 返回值 上一页导航任务。
 */
function previousPage() {
  const cursor = cursors.value.pop()
  return router.push({ query: { ...route.query, cursor: cursor || undefined } })
}

watch(criteria, (value, previous) => {
  if (value.organization !== previous?.organization || value.q !== previous?.q || value.status !== previous?.status) {
    cursors.value = []
  }
  unitName.value = value.organization
  keyword.value = value.q
  status.value = value.status
  void load()
}, { immediate: true })
refreshBusinessOnFocus(() => { if (currentScope.value && !loading.value) void load(true) }, 10_000)
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="v2-list" aria-label="我的整改规划">
    <header class="v2-page-head"><h1>我的整改规划</h1></header>
    <OperatorWorkflowTabs group="rectification" />
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <RouterLink v-if="currentScope" class="button primary" :to="{ path: operatorRemediationPath + '/new', query: scope }">
          <Plus :size="16" aria-hidden="true" />新建整改规划
        </RouterLink>
        <label class="field scope-field"><span>运营单位全称</span><input v-model="unitName" maxlength="200" required></label>
        <label class="field"><span>审核状态</span><select v-model="status">
          <option value="">全部状态</option>
          <option v-for="(label, value) in remediationStatuses" :key="value" :value="value">{{ label }}</option>
        </select></label>
        <label class="field filter-keyword"><span>关键词</span><input v-model="keyword" maxlength="120" type="search"
          placeholder="系统、备案编号或测评报告"></label>
        <button class="button primary" type="submit">查询</button>
        <button v-if="currentScope" class="button neutral" type="button" :disabled="loading" @click="load()">
          <RefreshCw :size="16" aria-hidden="true" />刷新
        </button>
      </div>
    </form>
    <p v-if="!currentScope" class="muted">输入备案登记的运营单位全称，可编制规划并查询审核进度。</p>
    <p v-if="refreshFailure" class="form-message error-message" role="alert">{{ refreshFailure }}</p>
    <div v-if="currentScope" class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="该单位暂无符合条件的整改规划。" @retry="load()">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th scope="col">备案编号</th><th scope="col">关联系统 / 报告</th><th scope="col">提交版本</th>
              <th scope="col">整改期限</th><th scope="col">审核状态</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="item in page.items" :key="item.id">
              <td class="code">{{ item.filingNumber }}</td>
              <td class="emphasis">{{ item.systemName }}<small class="muted cell-note">{{ item.reportNumber }}</small></td>
              <td>{{ item.submission ? 'V' + item.submission + '.0' : '尚未提交' }}
                <small v-if="item.submittedAt" class="muted cell-note">{{ formatTime(item.submittedAt) }}</small></td>
              <td>{{ item.deadline }}</td>
              <td><span class="status-badge" :class="remediationBadge(item.status)">{{ remediationStatuses[item.status] }}</span></td>
              <td><RouterLink class="text-action" :to="{ path: remediationRecordPath(item.id, operatorRemediationPath), query: scope }">
                {{ item.status === 'draft' ? '继续办理' : item.status === 'returned' ? '查看意见 / 补正' : '查看详情' }}
              </RouterLink></td>
            </tr></tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="!loading && !failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button class="button neutral small" type="button" :disabled="!criteria.cursor" @click="previousPage">上一页</button>
          <button class="button neutral small" type="button" :disabled="!page.nextCursor" @click="nextPage">下一页</button>
        </div>
      </footer>
    </div>
  </section>
</template>

<style scoped>
.v2-toolbar-row .scope-field { flex-basis: 210px; }
.cell-note { display: block; font-weight: 400; margin-top: 3px; }
</style>

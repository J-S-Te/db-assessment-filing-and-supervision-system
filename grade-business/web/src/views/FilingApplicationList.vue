<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { ChevronLeft, ChevronRight, RefreshCw } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { filingReviewLabel } from '../lib/domain'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime, type Page } from '../lib/domain'
import { applicationTypes, filingApplicationPath, filingApplicationsPath, filingApplicationStatuses,
  type FilingApplicationSummary } from '../lib/filingApplications'
import { queryValue } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

const route = useRoute()
const router = useRouter()
const keyword = ref('')
const type = ref('')
const status = ref('')
const page = shallowRef<Page<FilingApplicationSummary>>({ items: [] })
const loading = ref(true)
const failure = ref('')
const backCursors: string[] = []
let request: AbortController | undefined
let lastFilter = ''

/**
 * 用途 根据路由筛选和游标加载一页备案申请。
 * 参数 quiet 表示后台更新时保留正在编辑的筛选。
 * 返回值 加载任务，并在当前请求完成时更新页面。
 */
async function fetchApplications(quiet: boolean) {
  request?.abort()
  const current = new AbortController()
  request = current
  const filters = {
    q: queryValue(route.query.q), status: queryValue(route.query.status),
    type: queryValue(route.query.type), filingId: queryValue(route.query.filingId),
  }
  const nextFilter = params(filters)
  const cursor = queryValue(route.query.cursor)
  if (lastFilter !== nextFilter || !cursor) backCursors.length = 0
  lastFilter = nextFilter
  if (!quiet) {
    keyword.value = filters.q
    status.value = filters.status
    type.value = filters.type
    loading.value = true
    failure.value = ''
  }
  try {
    const result = await api<Page<FilingApplicationSummary>>(
      filingApplicationsPath + params({ ...filters, cursor, limit: 20 }), { signal: current.signal },
    )
    if (request === current) {
      page.value = result
      failure.value = ''
    }
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) {
      request = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 从链接加载申请页并同步已应用的筛选条件。
 * 参数 无。
 * 返回值 请求及界面更新任务。
 */
function load() {
  return fetchApplications(false)
}

/**
 * 用途 应用申请类别、审核状态和关键词并回到第一页。
 * 参数 无，筛选来自表单和来源备案。
 * 返回值 导航或当前页面的重新加载任务。
 */
function search() {
  const query = {
    q: keyword.value.trim() || undefined, type: type.value || undefined,
    status: status.value || undefined, filingId: queryValue(route.query.filingId) || undefined,
  }
  const destination = { path: filingApplicationsPath, query }
  if (router.resolve(destination).fullPath === route.fullPath) return load()
  return router.push(destination)
}

/**
 * 用途 重置筛选表单，保留打开列表的来源备案。
 * 参数 无。
 * 返回值 重置后的查询任务。
 */
function reset() {
  keyword.value = ''
  type.value = queryValue(route.query.type)
  status.value = ''
  return search()
}

/**
 * 用途 使用服务端游标查询下一页，并保留返回位置。
 * 参数 无，使用已完成的当前页响应。
 * 返回值 下一页导航任务，无后续记录时为空。
 */
function nextPage() {
  if (!page.value.nextCursor || loading.value) return
  backCursors.push(queryValue(route.query.cursor))
  return router.push({ query: { ...route.query, cursor: page.value.nextCursor } })
}

/**
 * 用途 回到浏览过的上一页，直接打开游标链接时回第一页。
 * 参数 无。
 * 返回值 上一页或第一页导航任务。
 */
function previousPage() {
  if (loading.value) return
  return router.push({ query: { ...route.query, cursor: backCursors.pop() || undefined } })
}

watch(() => route.fullPath, load, { immediate: true })
refreshBusinessOnFocus(() => {
  if (!request && route.path === filingApplicationsPath) void fetchApplications(true)
}, 10_000)
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="v2-list" aria-label="备案变更与注销申请">
    <nav class="v2-subviews" aria-label="备案视图">
      <RouterLink class="button neutral small" to="/filings">全部</RouterLink>
      <RouterLink v-for="(label, key) in applicationTypes" :key="key" class="button neutral small"
        :class="{ active: route.query.type === key }" :aria-current="route.query.type === key ? 'page' : undefined"
        :to="{ path: filingApplicationsPath, query: { type: key, filingId: route.query.filingId } }">{{ label }}</RouterLink>
    </nav>
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field">
          <span>申请类型</span>
          <select v-model="type" @change="search">
            <option value="">全部类型</option>
            <option v-for="(label, key) in applicationTypes" :key="key" :value="key">{{ label }}</option>
          </select>
        </label>
        <label class="field">
          <span>审核状态</span>
          <select v-model="status" @change="search">
            <option value="">全部状态</option>
            <option v-for="(label, key) in filingApplicationStatuses" :key="key" :value="key">{{ label }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="搜索备案编号、原系统名称或所属单位">
        </label>
        <button class="button primary" type="submit">查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
      </div>
      <div class="v2-toolbar-row">
        <RouterLink v-if="queryValue(route.query.filingId)" class="button neutral" :to="filingApplicationsPath">
          查看全部申请
        </RouterLink>
        <RouterLink v-if="queryValue(route.query.filingId)" class="button primary"
          :to="'/filings/' + encodeURIComponent(queryValue(route.query.filingId))">查看备案</RouterLink>
        <RouterLink v-else class="button primary"
          :to="{ path: '/filings', query: { status: 'approved', lifecycle: 'active' } }">选择备案发起申请</RouterLink>
        <span class="v2-toolbar-spacer"></span>
        <button class="button neutral small" type="button" :disabled="loading" @click="load">
          <RefreshCw :size="14" aria-hidden="true" />刷新
        </button>
      </div>
    </form>
    <div class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="未找到符合条件的申请。" @retry="load">
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th scope="col">备案编号</th><th scope="col">原系统名称</th><th scope="col">原所属单位</th>
                <th scope="col">类型</th><th scope="col">审核状态</th><th scope="col">创建时间</th><th scope="col">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td class="code">{{ item.filingNumber }}</td><td>{{ item.systemName }}</td><td>{{ item.organizationName }}</td>
                <td>{{ applicationTypes[item.type] }}</td>
                <td>
                  <span v-if="item.status === 'withdrawn'" class="status-badge status-draft">
                    <span aria-hidden="true"></span>{{ filingApplicationStatuses.withdrawn }}
                  </span>
                  <StatusBadge v-else :status="item.status" :label="filingReviewLabel(item.status, item.initialReviewAudience)" />
                </td>
                <td>{{ formatTime(item.createdAt) }}</td>
                <td><RouterLink class="text-action"
                  :to="{ path: filingApplicationPath(item.id), query: route.query }"
                  :aria-label="'查看 ' + item.filingNumber + ' ' + applicationTypes[item.type] + '申请'">查看</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="!loading && !failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button class="button neutral small" type="button" :disabled="!route.query.cursor" @click="previousPage">
            <ChevronLeft :size="16" aria-hidden="true" />上一页
          </button>
          <button class="button neutral small" type="button" :disabled="!page.nextCursor" @click="nextPage">
            下一页<ChevronRight :size="16" aria-hidden="true" />
          </button>
        </div>
      </footer>
    </div>
  </section>
</template>

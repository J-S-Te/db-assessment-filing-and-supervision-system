<script setup lang="ts">
import { ChevronLeft, ChevronRight, Download, Search } from '@lucide/vue'
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { queryValue, usePagedList } from '../lib/paging'

const resources = {
  filings: '备案',
  'filing-applications': '备案变更与注销',
  implementations: '实施报备',
  reports: '测评报告',
  remediations: '整改规划',
  hazards: '隐患台账',
  alerts: '站内预警',
  publications: '信息发布',
  'workday-calendar': '工作日历',
} as const

interface AuditItem {
  id: string
  resource: keyof typeof resources
  resourceId: string
  title: string
  reference?: string
  action: string
  actionLabel: string
  operator: string
  occurredAt: string
  href: string
}

const route = useRoute()
const { keyword, filter, page, loading, failure, load, search, reset, nextPage, previousPage }
  = usePagedList<AuditItem>(() => '/audit', 'resource')
const exportURL = computed(() => '/api/v1/audit/export' + params({
  resource: queryValue(route.query.resource), q: queryValue(route.query.q),
}))
const exporting = ref(false)
const exportFailure = ref('')
const exportUnavailable = '暂时无法导出操作日志，请稍后重试。'
const exportErrors: Partial<Record<number, string>> = {
  400: '请核对业务类型和关键词。',
  401: '登录状态已失效，请重新登录后再导出。',
  403: '当前账号无权导出操作日志。',
  422: '当前筛选结果超过导出上限，请缩小筛选范围后重试。',
}
let exportRequest: AbortController | undefined

/**
 * 用途 在筛选条件变化或离开页面时取消旧下载并清除提示。
 * 参数 无。
 * 返回值 无。
 */
function resetExport() {
  exportRequest?.abort()
  exportRequest = undefined
  exporting.value = false
  exportFailure.value = ''
}

/**
 * 用途 导出已应用筛选的操作日志，在当前页面反馈下载错误。
 * 参数 无，使用列表当前路由中的筛选条件。
 * 返回值 文件下载或错误处理完成的任务。
 */
async function downloadAudit() {
  if (exporting.value) return
  const controller = new AbortController()
  exportRequest = controller
  exporting.value = true
  exportFailure.value = ''
  try {
    const response = await fetch(exportURL.value, { signal: controller.signal, redirect: 'error' })
    if (exportRequest !== controller) return
    if (!response.ok || response.headers.get('Content-Type') !== 'text/csv; charset=utf-8') {
      exportFailure.value = exportErrors[response.status] ?? exportUnavailable
      if (response.status === 401) window.dispatchEvent(new Event('grade-business-session-expired'))
      return
    }
    const contents = await response.blob()
    if (exportRequest !== controller) return
    const objectURL = URL.createObjectURL(contents)
    const link = document.createElement('a')
    link.href = objectURL
    link.download = '业务操作日志.csv'
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(objectURL), 30000)
  } catch {
    if (exportRequest === controller) exportFailure.value = exportUnavailable
  } finally {
    if (exportRequest === controller) {
      exportRequest = undefined
      exporting.value = false
    }
  }
}

watch(exportURL, resetExport, { flush: 'sync' })
onScopeDispose(resetExport)
</script>

<template>
  <section class="v2-list" aria-label="业务操作审计">
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field">
          <span>业务类型</span>
          <select v-model="filter" @change="search">
            <option value="">全部业务</option>
            <option v-for="(label, resource) in resources" :key="resource" :value="resource">{{ label }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="操作人、对象、编号或办理说明">
        </label>
        <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
        <button class="button neutral" type="button" title="导出已应用筛选的操作日志"
          :disabled="loading || !!failure || exporting" @click="downloadAudit">
          <Download :size="16" aria-hidden="true" />{{ exporting ? '导出中…' : '导出日志' }}
        </button>
      </div>
      <p v-if="exportFailure" class="danger-text" role="alert">{{ exportFailure }}</p>
    </form>

    <div class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="暂无符合条件的业务操作记录。" @retry="load">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>操作人</th><th>操作</th><th>业务对象</th><th>发生时间</th></tr></thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td>{{ item.operator }}</td>
                <td>{{ item.actionLabel }}</td>
                <td class="audit-object">
                  <span class="level-tag">{{ resources[item.resource] }}</span>
                  <RouterLink class="text-action" :to="item.href">{{ item.title }}</RouterLink>
                  <small v-if="item.reference !== item.title" class="muted">{{ item.reference || item.resourceId }}</small>
                </td>
                <td>{{ formatTime(item.occurredAt) }}</td>
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

<style scoped>
.audit-object { min-width: 13rem; max-width: 38rem; white-space: normal; overflow-wrap: anywhere; }
.audit-object .level-tag { margin-right: 8px; }
.audit-object small { display: block; margin-top: 4px; }
</style>

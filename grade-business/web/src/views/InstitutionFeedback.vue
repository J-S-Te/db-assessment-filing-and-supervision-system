<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime, type Page } from '../lib/domain'
import { queryValue } from '../lib/paging'
import {
  feedbackDecisions, feedbackPath, institutionReportPath, type FeedbackMessage, type FeedbackReceipt,
} from '../lib/reportFeedback'

const route = useRoute()
const router = useRouter()
const scope = computed(() => queryValue(route.query.institution).trim())
const before = computed(() => queryValue(route.query.before))
const term = ref('')
const page = ref<Page<FeedbackMessage>>({ items: [] })
const loading = ref(false)
const failure = ref('')
const actionFailure = ref('')
const acknowledging = ref('')
let request: AbortController | undefined
let interval: ReturnType<typeof setInterval> | undefined
let active = true

/**
 * 用途 读取当前机构最近的测评意见和签收状态。
 * 参数 quiet 表示后台检查，保留页面上的现有内容。
 * 返回值 当前收件箱的加载任务。
 */
async function load(quiet = false) {
  request?.abort()
  if (!active || !scope.value) {
    request = undefined
    loading.value = false
    return
  }
  const current = new AbortController()
  request = current
  if (!quiet) loading.value = true
  try {
    const query = params({ institution: scope.value, before: before.value })
    const result = await api<Page<FeedbackMessage>>(feedbackPath + query, { signal: current.signal })
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
 * 用途 以输入的机构名称在收件箱中查询业务意见。
 * 参数 无，机构名称来自用户输入。
 * 返回值 查询导航或重新读取任务。
 */
function search() {
  const institution = term.value.trim()
  if (!institution) return
  if (scope.value === institution && !before.value) return load()
  return router.push({ path: feedbackPath, query: { institution } })
}

/**
 * 用途 确认收到指定审核意见并呈现服务端的签收时间。
 * 参数 item 为当前机构收件箱中真实存在的消息。
 * 返回值 签收并更新列表的任务。
 */
async function acknowledge(item: FeedbackMessage) {
  if (acknowledging.value || item.acknowledgedAt) return
  const institution = scope.value
  acknowledging.value = item.id
  actionFailure.value = ''
  try {
    const path = feedbackPath + '/' + encodeURIComponent(item.id) + '/acknowledge'
    const result = await api<FeedbackReceipt>(path + params({ institution }), { method: 'POST' })
    if (active && scope.value === institution) {
      page.value.items = page.value.items.map(current => current.id === item.id
        ? { ...current, acknowledgedAt: result.acknowledgedAt }
        : current)
      await load(true)
    }
  } catch (error) {
    if (active && scope.value === institution) actionFailure.value = errorText(error)
  } finally {
    acknowledging.value = ''
  }
}

/**
 * 用途 在前台更新收件箱，切回页面时立即检查。
 * 参数 无。
 * 返回值 无。
 */
function refreshVisible() {
  if (document.visibilityState === 'visible' && !request && !acknowledging.value) void load(true)
}

watch([scope, before], () => {
  term.value = scope.value
  page.value = { items: [] }
  failure.value = actionFailure.value = ''
  void load()
}, { immediate: true })
onMounted(() => {
  interval = setInterval(refreshVisible, 10_000)
  document.addEventListener('visibilitychange', refreshVisible)
})
onScopeDispose(() => {
  active = false
  request?.abort()
  clearInterval(interval)
  document.removeEventListener('visibilitychange', refreshVisible)
})
</script>

<template>
  <section class="v2-list" aria-labelledby="institution-inbox">
    <header class="v2-page-head">
      <div><h1 id="institution-inbox">审核意见查收</h1><p>测评报告 · 退回补正与终审结果</p></div>
    </header>
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword">
          <span>测评机构名称</span>
          <input v-model="term" required maxlength="200" placeholder="输入报告登记的测评机构名称">
        </label>
        <button class="button primary" type="submit">查询意见</button>
        <button v-if="scope" class="button neutral" type="button" :disabled="loading" @click="load()">刷新</button>
      </div>
    </form>
    <p v-if="!scope" class="muted">请选择本地测试所用的测评机构。</p>
    <template v-else>
      <p v-if="actionFailure" class="form-message error-message" role="alert">{{ actionFailure }}</p>
      <p v-if="failure && page.items.length" class="form-message warning-message" role="status">{{ failure }}</p>
      <AsyncState :loading="loading && !page.items.length" :error="page.items.length ? '' : failure"
        :empty="!page.items.length" empty-text="该机构暂无测评审核意见。" @retry="load()">
        <div class="table-wrap v2-list-table">
          <table class="data-table">
            <thead><tr>
              <th>测评报告</th><th>审核意见</th><th>报告当前状态</th><th>送达时间</th><th>签收</th><th>操作</th>
            </tr></thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td>{{ item.systemName }}<small class="muted feedback-number">{{ item.number }}</small></td>
                <td class="feedback-opinion">
                  <strong>{{ feedbackDecisions[item.decision] }}</strong>
                  <span v-if="item.comment">{{ item.comment }}</span>
                </td>
                <td><StatusBadge v-if="item.status" :status="item.status" /><span v-else class="muted">已变更归属</span></td>
                <td>{{ formatTime(item.sentAt) }}</td>
                <td v-if="item.acknowledgedAt">已签收 <small class="feedback-number muted">{{ formatTime(item.acknowledgedAt) }}</small></td>
                <td v-else><button type="button" class="button neutral small" :disabled="Boolean(acknowledging)"
                  @click="acknowledge(item)">{{ acknowledging === item.id ? '签收中' : '确认签收' }}</button></td>
                <td><RouterLink v-if="item.status" class="text-action"
                  :to="{ path: institutionReportPath(item.reportId), query: { institution: scope } }">
                  {{ item.status === 'returned' ? '办理补正' : '查看报告' }}
                </RouterLink><span v-else class="muted">—</span></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="before || page.nextCursor" class="panel-footer pager">
        <div>
          <RouterLink v-if="before" class="button neutral small" :to="{ path: feedbackPath, query: { institution: scope } }">
            最新消息
          </RouterLink>
          <RouterLink v-if="page.nextCursor" class="button neutral small"
            :to="{ path: feedbackPath, query: { institution: scope, before: page.nextCursor } }">下一页</RouterLink>
        </div>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.feedback-number, .feedback-opinion span { display: block; }
.feedback-opinion { white-space: normal; min-width: 160px; max-width: 350px; overflow-wrap: anywhere; }
.feedback-opinion span { margin-top: 4px; white-space: pre-wrap; }
</style>

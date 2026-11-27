<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import OperatorWorkflowTabs from '../components/OperatorWorkflowTabs.vue'
import AsyncState from '../components/AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime, type Page } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { feedbackPath as reportFeedbackPath, institutionReportPath, type FeedbackReceipt } from '../lib/reportFeedback'
import {
  acceptsResource, businessBadge, businessResources, businessScopes, businessStatus, scopedBusinessPath,
  type BusinessAudience, type BusinessFeedbackMessage,
} from '../lib/scopedBusiness'

const props = defineProps<{ audience: BusinessAudience }>()
const route = useRoute()
const router = useRouter()
const settings = computed(() => businessScopes[props.audience])
const feedbackPath = computed(() => '/' + props.audience + '/feedback')
const scope = computed(() => queryValue(route.query[settings.value.key]).trim())
const before = computed(() => queryValue(route.query.before))
const criteria = computed(() => ({ [settings.value.key]: scope.value || undefined }))
const term = ref('')
const page = shallowRef<Page<BusinessFeedbackMessage>>({ items: [] })
const loading = ref(false)
const failure = ref('')
const actionFailure = ref('')
const acknowledging = ref('')
let readRequest: AbortController | undefined
let receiptRequest: AbortController | undefined
let interval: ReturnType<typeof setInterval> | undefined

/**
 * 用途 读取当前机构或运营单位的审核意见及签收时间。
 * 参数 quiet 表示后台刷新，保留页面当前内容。
 * 返回值 当前页的加载任务。
 */
async function load(quiet = false) {
  readRequest?.abort()
  if (!scope.value) {
    readRequest = undefined
    loading.value = false
    return
  }
  const request = new AbortController()
  readRequest = request
  loading.value = !quiet
  try {
    const query = params({ ...criteria.value, before: before.value })
    const result = await api<Page<BusinessFeedbackMessage>>(feedbackPath.value + query, {
      signal: request.signal,
    })
    if (readRequest === request && !request.signal.aborted) {
      page.value = result
      failure.value = ''
    }
  } catch (error) {
    if (readRequest === request && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (readRequest === request) {
      readRequest = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 使用输入的机构或单位名称查询最新审核意见。
 * 参数 无，名称来自范围筛选框。
 * 返回值 导航或刷新任务。
 */
function search() {
  const name = term.value.trim()
  if (!name) {
    term.value = scope.value
    return
  }
  if (name === scope.value && !before.value) return load()
  return router.push({ path: feedbackPath.value, query: { [settings.value.key]: name } })
}

/**
 * 用途 签收当前收件范围中的一条真实审核意见。
 * 参数 item 为用户选择的送达消息。
 * 返回值 以服务端时间更新签收结果的任务。
 */
async function acknowledge(item: BusinessFeedbackMessage) {
  if (receiptRequest || !scope.value || item.acknowledgedAt) return
  const request = new AbortController()
  receiptRequest = request
  acknowledging.value = item.id
  actionFailure.value = ''
  try {
    const path = feedbackPath.value + '/' + encodeURIComponent(item.id) + '/acknowledge'
    const result = await api<FeedbackReceipt>(path + params(criteria.value), {
      method: 'POST', signal: request.signal,
    })
    if (request.signal.aborted) return
    page.value = { ...page.value, items: page.value.items.map(row => row.id === item.id
      ? { ...row, acknowledgedAt: result.acknowledgedAt } : row) }
    await load(true)
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) actionFailure.value = errorText(error)
  } finally {
    if (receiptRequest === request) {
      receiptRequest = undefined
      acknowledging.value = ''
    }
  }
}

/**
 * 用途 前台定期刷新意见，切回页面时补读当前页。
 * 参数 无。
 * 返回值 无。
 */
function refreshVisible() {
  if (document.visibilityState === 'visible' && !readRequest && !receiptRequest) void load(true)
}

watch([feedbackPath, scope], () => {
  term.value = scope.value
  receiptRequest?.abort()
  receiptRequest = undefined
  acknowledging.value = ''
}, { immediate: true })
watch([feedbackPath, scope, before], () => {
  page.value = { items: [] }
  failure.value = actionFailure.value = ''
  void load()
}, { immediate: true })

onMounted(() => {
  interval = setInterval(refreshVisible, 10_000)
  document.addEventListener('visibilitychange', refreshVisible)
})
onScopeDispose(() => {
  readRequest?.abort()
  receiptRequest?.abort()
  clearInterval(interval)
  document.removeEventListener('visibilitychange', refreshVisible)
})
</script>

<template>
  <section class="v2-list" aria-labelledby="business-inbox">
    <header class="v2-page-head">
      <div>
        <h1 id="business-inbox">{{ settings.title }}</h1>
        <p>{{ audience === 'institution' ? '备案、实施、测评与隐患复测' : '备案、整改与预警处置' }}</p>
      </div>
      <RouterLink v-if="audience === 'institution'" class="button neutral"
        :to="{ path: reportFeedbackPath, query: criteria }">测评报告审核意见</RouterLink>
    </header>

    <OperatorWorkflowTabs group="messages" :audience="audience" />
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword">
          <span>{{ settings.label }}</span>
          <input v-model="term" required maxlength="200" :placeholder="'输入登记的' + settings.label">
        </label>
        <button class="button primary" type="submit">查询意见</button>
        <button v-if="scope" class="button neutral" type="button" :disabled="loading || Boolean(acknowledging)"
          @click="load()">刷新</button>
      </div>
    </form>

    <p v-if="!scope" class="muted">输入{{ settings.label }}后查询。</p>
    <template v-else>
      <p v-if="actionFailure" class="form-message error-message" role="alert">{{ actionFailure }}</p>
      <p v-if="failure && page.items.length" class="form-message warning-message" role="status">{{ failure }}</p>
      <AsyncState :loading="loading && !page.items.length" :error="page.items.length ? '' : failure"
        :empty="!page.items.length" empty-text="当前范围内暂无审核意见。" @retry="load()">
        <div class="table-wrap v2-list-table">
          <table class="data-table">
            <thead>
              <tr><th>业务事项</th><th>审核意见</th><th>当前状态</th><th>送达时间</th><th>签收</th><th>操作</th></tr>
            </thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td>
                  {{ item.subject }}
                  <small class="muted feedback-line">
                    {{ item.resource === 'material-issues' ? '材料异常' : item.resource === 'alerts' ? '预警处置'
                      : item.resource === 'reports' ? '测评报告' : item.resource === 'supervision-site'
                        ? '现场检查' : item.resource === 'supervisions' ? '监督检查'
                          : item.resource === 'material-expiries' ? '材料有效期' : item.resource === 'assessment-dues'
                            ? '测评到期' : businessResources[item.resource]?.label }}
                    {{ item.reference }}
                  </small>
                </td>
                <td class="feedback-opinion">
                  <strong>{{ businessStatus(item.resource, item.state) }}</strong>
                  <span v-if="item.comment" class="feedback-line">{{ item.comment }}</span>
                </td>
                <td>
                  <span v-if="item.status" class="status-badge" :class="businessBadge(item.status)">
                    <span aria-hidden="true"></span>{{ businessStatus(item.resource, item.status) }}
                  </span>
                  <span v-else class="muted">已变更归属</span>
                </td>
                <td>{{ formatTime(item.sentAt) }}</td>
                <td v-if="item.acknowledgedAt">
                  已签收 <small class="muted feedback-line">{{ formatTime(item.acknowledgedAt) }}</small>
                </td>
                <td v-else>
                  <button type="button" class="button neutral small" :disabled="Boolean(acknowledging)"
                    @click="acknowledge(item)">{{ acknowledging === item.id ? '签收中…' : '确认签收' }}</button>
                </td>
                <td>
                  <RouterLink v-if="item.resource === 'material-issues'" class="text-action"
                    :to="{ path: '/' + audience + '/alerts', query: { ...criteria, view: 'materials', issue: item.resourceId } }">
                    查看材料异常
                  </RouterLink>
                  <RouterLink v-else-if="item.status && item.resource === 'alerts'"
                    class="text-action" :to="{ path: '/' + audience + '/alerts', query: { ...criteria, alert: item.resourceId } }">
                    查看预警
                  </RouterLink>
                  <RouterLink v-else-if="item.status && item.resource === 'reports' && audience === 'institution'"
                    class="text-action" :to="{ path: institutionReportPath(item.resourceId), query: criteria }">
                    {{ item.status === 'returned' ? '办理补正' : '查看报告' }}
                  </RouterLink>
                  <RouterLink v-else-if="item.status && audience === 'operator'
                    && (item.resource === 'supervisions' || item.resource === 'supervision-site')"
                    class="text-action" :to="{ path: '/operator/supervisions/' + item.resourceId, query: {
                      ...criteria,
                      section: item.resource === 'supervision-site' || item.state.startsWith('site_') ? 'site' : undefined,
                    } }">
                    {{ item.resource === 'supervision-site' || item.state.startsWith('site_') ? '查看现场检查'
                      : item.status === 'returned' ? '办理补正' : '查看自查任务' }}
                  </RouterLink>
                  <RouterLink v-else-if="item.status && item.resource === 'material-expiries'"
                    class="text-action" :to="{ path: '/' + audience + '/material-expiries/' + item.resourceId, query: criteria }">
                    查看材料有效期
                  </RouterLink>
                  <RouterLink v-else-if="item.status && item.resource === 'assessment-dues'"
                    class="text-action" :to="{ path: '/' + audience + '/assessment-dues/' + item.resourceId, query: criteria }">
                    查看测评到期
                  </RouterLink>
                  <RouterLink v-else-if="item.status && acceptsResource(item.resource, audience)" class="text-action"
                    :to="{ path: scopedBusinessPath(audience, item.resource, item.resourceId), query: {
                      ...criteria, before: before || undefined,
                    } }">{{ item.status === 'returned' ? '办理补正' : '查看详情' }}</RouterLink>
                  <span v-else class="muted">—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>

      <footer v-if="before || page.nextCursor" class="panel-footer pager">
        <div>
          <RouterLink v-if="before" class="button neutral small"
            :to="{ path: feedbackPath, query: criteria }">最新消息</RouterLink>
          <RouterLink v-if="page.nextCursor" class="button neutral small"
            :to="{ path: feedbackPath, query: { ...criteria, before: page.nextCursor } }">下一页</RouterLink>
        </div>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.feedback-line { display: block; }
.feedback-opinion { min-width: 160px; max-width: 350px; white-space: normal; overflow-wrap: anywhere; }
.feedback-opinion .feedback-line { margin-top: 4px; white-space: pre-wrap; }
</style>

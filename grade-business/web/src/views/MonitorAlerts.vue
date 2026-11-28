<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { RefreshCw, Search } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import OperatorWorkflowTabs from '../components/OperatorWorkflowTabs.vue'
import AsyncState from '../components/AsyncState.vue'
import AlertDeadlineQueue from '../components/AlertDeadlineQueue.vue'
import AlertRulePanel from '../components/AlertRulePanel.vue'
import AlertSupervisionQueue from '../components/AlertSupervisionQueue.vue'
import MaterialIssueList from '../components/MaterialIssueList.vue'
import MonitorAlertDetail from '../components/MonitorAlertDetail.vue'
import {
  alertBadgeStyles, alertPath, alertStatuses, alertTypes, institutionAlertPath, operatorAlertPath,
  type AlertAction, type AlertCounts, type AlertItem,
} from '../lib/alerts'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime, risks } from '../lib/domain'
import { queryValue, usePagedList } from '../lib/paging'
import { businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'
import { hasPermission, session } from '../lib/session'
import { refreshWorkbench } from '../lib/workbench'

const props = defineProps<{ audience?: BusinessAudience }>()
const route = useRoute()
const router = useRouter()
const path = computed(() => props.audience === 'institution' ? institutionAlertPath
  : props.audience === 'operator' ? operatorAlertPath : alertPath)
const settings = computed(() => props.audience ? businessScopes[props.audience] : undefined)
const scope = computed(() => settings.value ? queryValue(route.query[settings.value.key]).trim() : '')
const scopeDraft = ref('')
const available = computed(() => !props.audience || Boolean(scope.value))
const showOtherSources = computed(() => session.value?.roleCode !== 'ROLE_REGULATOR')
const canConfigureRules = computed(() => !props.audience && hasPermission('admin.monitor.rule.config'))
const showMaterials = computed(() => showOtherSources.value && queryValue(route.query.view) === 'materials')
const showDeadlines = computed(() => showOtherSources.value && queryValue(route.query.view) === 'deadlines')
const showSupervision = computed(() => !props.audience && queryValue(route.query.view) === 'supervision')
const showRules = computed(() => canConfigureRules.value && queryValue(route.query.view) === 'rules')
const showAlerts = computed(() => !showMaterials.value && !showDeadlines.value && !showSupervision.value && !showRules.value)
const validityPath = computed(() => (props.audience ? '/' + props.audience : '') + '/material-expiries')
const assessmentDuePath = computed(() => (props.audience ? '/' + props.audience : '') + '/assessment-dues')
const selectedIssue = computed(() => queryValue(route.query.issue))
const materialStatus = computed(() => route.query.materialStatus === 'closed' ? 'closed' : 'open')
const scopeQuery = computed(() => settings.value ? { [settings.value.key]: scope.value } : {})
const alertType = computed(() => props.audience ? queryValue(route.query.type) : '')
const pendingReview = computed(() => !props.audience && queryValue(route.query.review) === 'pending')
const fixedFilters = computed((): Record<string, string> => {
  if (settings.value) return { [settings.value.key]: scope.value,
    ...(alertType.value ? { type: alertType.value } : {}) }
  return pendingReview.value ? { review: 'pending' } : {}
})
const list = usePagedList<AlertItem>(() => available.value && showAlerts.value ? path.value : '', 'status', () => 'severity',
  () => fixedFilters.value)
const { keyword, filter, extraFilter, page, loading, failure, load, search, reset, nextPage, previousPage } = list
const selected = computed(() => queryValue(route.query.alert) || queryValue(route.query.alertId))
const initialAction = ref<AlertAction>()
const counts = shallowRef<AlertCounts>()
const countFailure = ref('')
const syncing = ref(false)
const notice = ref('')
const syncFailure = ref('')
const from = ref('')
const to = ref('')
const exportURL = computed(() => props.audience === 'operator' && scope.value &&
  from.value && to.value && from.value <= to.value
  ? '/api/v1/operator/alerts/export' + params({ organization: scope.value, from: from.value, to: to.value })
  : '')
let countsRequest: AbortController | undefined
let syncRequest: AbortController | undefined

/**
 * 用途 读取预警中心的全部记录及各处置状态计数。
 * 参数 无。
 * 返回值 计数刷新或错误反馈任务。
 */
async function loadCounts() {
  countsRequest?.abort()
  if (props.audience) {
    counts.value = undefined
    countFailure.value = ''
    return
  }
  const request = new AbortController()
  countsRequest = request
  countFailure.value = ''
  try {
    const result = await api<AlertCounts>(alertPath + '/summary', { signal: request.signal })
    if (!request.signal.aborted) counts.value = result
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) countFailure.value = errorText(error)
  } finally {
    if (countsRequest === request) countsRequest = undefined
  }
}

/**
 * 用途 更新当前筛选列表与预警中心计数。
 * 参数 无。
 * 返回值 列表与计数的刷新任务。
 */
function refresh() {
  return Promise.all([load(), loadCounts()])
}

/**
 * 用途 预警操作落库后更新当前队列以及监管工作台的待审提醒。
 * 参数 无。
 * 返回值 当前预警列表与计数的刷新任务。
 */
function refreshAfterAction() {
  if (!props.audience) void refreshWorkbench()
  return refresh()
}

/**
 * 用途 切换当前办理端的收件范围并重新查询其预警。
 * 参数 无，范围取自当前单位或机构输入框。
 * 返回值 重新查询或切换范围的导航任务。
 */
function chooseScope() {
  const name = scopeDraft.value.trim()
  if (!settings.value || !name) return
  if (showMaterials.value) return router.push({ path: path.value, query: {
    [settings.value.key]: name, view: 'materials', materialStatus: route.query.materialStatus, issue: scope.value && scope.value !== name
      ? undefined : selectedIssue.value || undefined,
  } })
  if (showDeadlines.value) return router.push({
    path: path.value, query: { [settings.value.key]: name, view: 'deadlines' },
  })
  if (name === scope.value) return refresh()
  return router.push({ path: path.value, query: { [settings.value.key]: name,
    alert: scope.value ? undefined : selected.value || undefined } })
}

/**
 * 用途 返回本端材料异常队列并保留当前收件范围。
 * 参数 无。
 * 返回值 清除单条异常筛选的导航任务。
 */
function clearIssueSelection() {
  return router.replace({ path: path.value, query: { ...scopeQuery.value, view: 'materials' } })
}

/**
 * 用途 切换预警申请待审筛选并从第一页读取结果。
 * 参数 无，使用当前 URL 中的已应用筛选条件。
 * 返回值 更新待审筛选的导航任务。
 */
function togglePendingReview() {
  return router.push({ path: alertPath, query: {
    ...route.query, review: pendingReview.value ? undefined : 'pending', cursor: undefined,
  } })
}

/**
 * 用途 按收件端选中的预警类型重新查询第一页。
 * 参数 event 为原生筛选框变更事件。
 * 返回值 当前筛选的导航任务。
 */
function chooseAlertType(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  return router.push({ query: { ...route.query, type: value || undefined, cursor: undefined } })
}

/**
 * 用途 清空收件端已选类型和通用预警筛选。
 * 参数 无，保留当前办理端的收件范围。
 * 返回值 导航或现有筛选重置任务。
 */
function resetAlertFilters() {
  if (settings.value && alertType.value) {
    return router.push({ path: path.value, query: { [settings.value.key]: scope.value } })
  }
  return reset()
}

/**
 * 用途 从已登记的本地隐患同步预警，显示实际新增数量。
 * 参数 无，服务端使用当前台账与业务日期。
 * 返回值 同步结果及刷新任务。
 */
async function sync() {
  if (!canConfigureRules.value || syncing.value) return
  const request = new AbortController()
  syncRequest = request
  syncing.value = true
  notice.value = syncFailure.value = ''
  try {
    const result = await api<{ created: number }>(alertPath + '/sync', { method: 'POST', signal: request.signal })
    if (request.signal.aborted) return
    notice.value = result.created ? '本次新增 ' + result.created + ' 条站内预警。' : '本次同步没有新增预警。'
    await refreshAfterAction()
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) syncFailure.value = errorText(error)
  } finally {
    if (syncRequest === request) syncing.value = false
  }
}

/**
 * 用途 打开指定预警的详情窗口并保留当前筛选。
 * 参数 id 为所选预警标识，action 为列表选定的初始办理动作。
 * 返回值 带详情标识的导航任务。
 */
function openAlert(id: string, action?: AlertAction) {
  initialAction.value = action
  return router.push({ path: path.value, query: { ...route.query, alert: id, alertId: undefined } })
}

/**
 * 用途 关闭当前预警详情并保留原列表查询。
 * 参数 无。
 * 返回值 清除详情标识的导航任务。
 */
function closeAlert() {
  initialAction.value = undefined
  return router.replace({ path: path.value, query: { ...route.query, alert: undefined, alertId: undefined } })
}

watch(scope, value => { scopeDraft.value = value }, { immediate: true })
watch(() => props.audience, loadCounts, { immediate: true })
refreshBusinessOnFocus(() => {
  if (!showAlerts.value) return
  if (available.value) void list.refresh()
  if (!props.audience && !countsRequest) void loadCounts()
}, 10_000)
onScopeDispose(() => {
  countsRequest?.abort()
  syncRequest?.abort()
})
</script>

<template>
  <section class="v2-list" :aria-label="audience ? '我的预警' : '预警中心'">
    <header v-if="audience" class="v2-page-head">
      <h1>消息中心</h1>
      <RouterLink v-if="audience === 'institution' && scope" class="button neutral"
        :to="{ path: '/institution/workbench', query: { institution: scope } }">查看任务时限提醒</RouterLink>
    </header>
    <OperatorWorkflowTabs v-if="audience" group="messages" :audience="audience" />
    <form v-if="settings" class="v2-list-toolbar" @submit.prevent="chooseScope">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword">
          <span>{{ audience === 'operator' ? '运营单位' : '测评机构' }}</span>
          <input v-model="scopeDraft" maxlength="200" required :placeholder="'输入备案登记的' + settings.label">
        </label>
        <button class="button primary" type="submit">
          {{ audience === 'institution' ? '查看本机构预警' : '查看本单位预警' }}
        </button>
      </div>
    </form>
    <nav class="v2-tabs" aria-label="预警来源">
      <RouterLink class="v2-tab" :class="{ active: showAlerts }"
        :aria-current="showAlerts ? 'page' : undefined" :to="{ path, query: scopeQuery }">隐患预警</RouterLink>
      <RouterLink v-if="showOtherSources" class="v2-tab" :class="{ active: showMaterials }" :aria-current="showMaterials ? 'page' : undefined"
        :to="{ path, query: { ...scopeQuery, view: 'materials' } }">材料异常</RouterLink>
      <RouterLink v-if="showOtherSources" class="v2-tab" :to="{ path: validityPath, query: scopeQuery }">材料有效期</RouterLink>
      <RouterLink v-if="showOtherSources" class="v2-tab" :to="{ path: assessmentDuePath, query: scopeQuery }">测评到期</RouterLink>
      <RouterLink v-if="showOtherSources" class="v2-tab" :class="{ active: showDeadlines }" :aria-current="showDeadlines ? 'page' : undefined"
        :to="{ path, query: { ...scopeQuery, view: 'deadlines' } }">业务时限</RouterLink>
      <RouterLink v-if="!audience" class="v2-tab" :class="{ active: showSupervision }"
        :aria-current="showSupervision ? 'page' : undefined"
        :to="{ path, query: { view: 'supervision' } }">监督检查线索</RouterLink>
      <RouterLink v-if="canConfigureRules" class="v2-tab" :class="{ active: showRules }"
        :aria-current="showRules ? 'page' : undefined"
        :to="{ path, query: { view: 'rules' } }">预警规则</RouterLink>
    </nav>
    <MaterialIssueList v-if="showMaterials && available" :scope="scope" :audience="audience"
      :selected="selectedIssue" :initial-status="materialStatus" @clear-selection="clearIssueSelection" />
    <AlertDeadlineQueue v-if="showDeadlines && available" :scope="scope" :audience="audience" />
    <AlertSupervisionQueue v-if="showSupervision" />
    <AlertRulePanel v-if="showRules" />
    <template v-if="showAlerts">
      <div v-if="audience === 'operator' && available" class="v2-toolbar-row" aria-label="预警处置台账导出">
        <label class="field"><span>关闭日期起</span><input v-model="from" type="date"></label>
        <label class="field"><span>关闭日期止</span><input v-model="to" type="date" :min="from"></label>
        <a v-if="exportURL" class="button neutral" :href="exportURL" download>导出处置台账</a>
        <button v-else class="button neutral" type="button" disabled>导出处置台账</button>
      </div>
      <form v-if="available" class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <button v-if="canConfigureRules" class="button primary" type="button" :disabled="syncing" @click="sync">
            <RefreshCw :size="16" aria-hidden="true" />{{ syncing ? '同步中…' : '同步本地业务预警' }}
          </button>
          <button v-if="!audience" class="button" :class="pendingReview ? 'primary' : 'neutral'"
            type="button" :aria-pressed="pendingReview"
            @click="togglePendingReview">
            待审核申请
            <template v-if="counts?.pendingDispositions !== undefined">
              （{{ counts.pendingDispositions }}）
            </template>
          </button>
          <span v-if="!audience" class="v2-toolbar-spacer"></span>
          <label v-if="audience" class="field">
            <span>预警类型</span>
            <select :value="alertType" @change="chooseAlertType">
              <option value="">全部预警类型</option>
              <option v-for="(label, value) in alertTypes" :key="value" :value="value">{{ label }}</option>
            </select>
          </label>
          <label class="field">
            <span>预警级别</span>
            <select v-model="extraFilter">
              <option value="">全部级别</option>
              <option v-for="(label, severity) in risks" :key="severity" :value="severity">{{ label }}</option>
            </select>
          </label>
          <label class="field">
            <span>处置状态</span>
            <select v-model="filter">
              <option value="">全部状态</option>
              <option v-for="(label, status) in alertStatuses" :key="status" :value="status">{{ label }}</option>
            </select>
          </label>
          <label class="field filter-keyword">
            <span>关键词</span>
            <input v-model="keyword" type="search" maxlength="120" placeholder="系统名称或所属单位">
          </label>
          <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
          <button class="button neutral" type="button" @click="resetAlertFilters">重置</button>
        </div>
      </form>
      <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
      <p v-if="syncFailure" class="form-message error-message" role="alert">{{ syncFailure }}</p>
      <p v-if="countFailure" class="form-message warning-message" role="status">{{ countFailure }}</p>
      <p v-if="pendingReview" class="muted alert-filter-note">当前显示含待审核申请的预警。</p>

      <div v-if="available" class="panel v2-list-table">
        <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
          :empty-text="pendingReview ? '暂无符合条件的待审核申请。' : '暂无符合条件的本地预警。'" @retry="load">
          <ul v-if="audience" class="operator-alert-items" :aria-label="audience === 'institution' ? '本机构预警' : '本单位预警'">
            <li v-for="item in page.items" :key="item.id">
              <div class="alert-object">
                <strong>{{ alertTypes[item.type] }} · {{ item.systemName }}</strong>
                <small class="muted">触发 {{ formatTime(item.triggeredAt) }} · 整改期限 {{ item.deadline }}
                  <template v-if="item.type === 'overdue' && item.currentlyOverdue === false"> · 当前未逾期</template>
                </small>
              </div>
              <div class="operator-alert-actions">
                <span class="status-badge" :class="'alert-risk-' + item.severity">
                  <span aria-hidden="true"></span>{{ risks[item.severity] }}
                </span>
                <span class="status-badge" :class="alertBadgeStyles[item.status]">
                  <span aria-hidden="true"></span>{{ alertStatuses[item.status] }}
                </span>
                <button class="text-action" type="button" @click="openAlert(item.id)">
                  {{ item.status === 'closed' ? '查看结果' : '处置' }}
                </button>
              </div>
            </li>
          </ul>
          <div v-else class="table-wrap">
            <table class="data-table">
              <thead><tr><th>级别</th><th>类型</th><th>对象</th><th>触发时间</th><th>状态</th><th>操作</th></tr></thead>
              <tbody>
                <tr v-for="item in page.items" :key="item.id">
                  <td>
                    <span class="status-badge" :class="'alert-risk-' + item.severity">
                      <span aria-hidden="true"></span>{{ risks[item.severity] }}
                    </span>
                  </td>
                  <td>{{ alertTypes[item.type] }}
                    <small v-if="item.type === 'overdue' && item.currentlyOverdue === false" class="muted alert-cell-note">
                      当前未逾期
                    </small>
                  </td>
                  <td class="alert-object">{{ item.systemName }}<small class="muted">{{ item.organizationName }}</small></td>
                  <td>{{ formatTime(item.triggeredAt) }}</td>
                  <td>
                    <span class="status-badge" :class="alertBadgeStyles[item.status]">
                      <span aria-hidden="true"></span>{{ alertStatuses[item.status] }}
                    </span>
                  </td>
                  <td class="alert-row-actions">
                    <button class="text-action" type="button"
                      @click="openAlert(item.id)">{{ pendingReview ? '审核' : item.status === 'closed' ? '详情' : '处置' }}</button>
                    <button v-if="!pendingReview && item.status !== 'closed'" class="text-action" type="button"
                      @click="openAlert(item.id, 'close')">关闭</button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </AsyncState>
        <footer class="panel-footer pager">
          <span v-if="counts && !pendingReview">共 {{ counts.total }} 条 · 待处理 {{ counts.pending }} · 处理中 {{ counts.processing }} · 已关闭 {{ counts.closed }}</span>
          <span v-else>本页 {{ page.items.length }} 条</span>
          <div>
            <button class="button neutral small" type="button" :disabled="loading" @click="refresh">刷新</button>
            <button class="button neutral small" type="button" :disabled="!route.query.cursor || loading"
              @click="previousPage">上一页</button>
            <button class="button neutral small" type="button" :disabled="!page.nextCursor || loading"
              @click="nextPage">下一页</button>
          </div>
        </footer>
      </div>
      <MonitorAlertDetail v-if="available && selected" :key="path + ':' + scope + ':' + selected"
        :id="selected" :audience="audience" :scope="scope" :initial-action="initialAction"
        @close="closeAlert" @changed="refreshAfterAction" />
    </template>
  </section>
</template>

<style scoped>
.alert-object { overflow-wrap: anywhere; max-width: 20rem; }
.alert-object small, .alert-cell-note { display: block; }
.alert-filter-note { margin: 0 0 12px; }
.alert-row-actions { white-space: nowrap; }
.alert-row-actions button + button { margin-left: 10px; }
.alert-risk-high { color: var(--c-danger); background: var(--c-danger-bg); }
.alert-risk-medium { color: var(--c-warn); background: var(--c-warn-bg); }
.alert-risk-low { color: var(--c-info); background: var(--c-info-bg); }
.operator-alert-items { margin: 0; padding: 0; list-style: none; }
.operator-alert-items li { display: flex; align-items: center; justify-content: space-between; gap: 12px;
  padding: 12px 16px; border-bottom: 1px solid var(--line); flex-wrap: wrap; }
.operator-alert-items li:last-child { border-bottom: 0; }
.operator-alert-actions { display: flex; align-items: center; gap: 10px; }
</style>

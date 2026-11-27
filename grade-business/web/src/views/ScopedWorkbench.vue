<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import {
  ArrowUpRight, BellRing, Building2, CalendarClock, ClipboardList, ClipboardPenLine, Clock3, FileChartColumn,
  FileClock, FilePlus2, FileUp, ListTodo, MessageSquareText, RefreshCw, ScanSearch, Wrench,
} from '@lucide/vue'
import { RouterLink, useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import V2RouteDialog from '../components/V2RouteDialog.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { queryValue } from '../lib/paging'
import { businessBadge, businessScopes, businessStatus, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'
import { sendRetestReminder } from '../lib/workbench'
import { describeDeadline, workbenchTaskLink, type WorkbenchCounts, type WorkbenchTask } from '../lib/workbenchItems'

interface ScopedWorkbenchData {
  tasks: WorkbenchTask[]
  counts: WorkbenchCounts
  recentSystems: Array<{ id: string; number: string; systemName: string; status: string }>
  rectification: { open: number; pendingRetest: number; pendingReview: number; closed: number }
}

const props = defineProps<{ audience: BusinessAudience }>()
const route = useRoute()
const operator = computed(() => props.audience === 'operator')
const scopeSettings = computed(() => businessScopes[props.audience])
const scope = computed(() => queryValue(route.query[scopeSettings.value.key]).trim())
const basePath = computed(() => '/' + props.audience)
const workbenchPath = computed(() => basePath.value + '/workbench')
const query = computed(() => ({ [scopeSettings.value.key]: scope.value }))
const context = computed(() => ({ audience: props.audience, query: query.value }))
const data = shallowRef<ScopedWorkbenchData>()
const loading = ref(false)
const failure = ref('')
const reminder = shallowRef<WorkbenchTask>()
const reminderBusy = ref(false)
const reminderFailure = ref('')
const notice = ref<{ text: string; error?: boolean }>()
let request: AbortController | undefined
let reminderRequest: AbortController | undefined

const formatNumber = new Intl.NumberFormat('zh-CN')
const figures = computed(() => [
  { label: operator.value ? '待我处理' : '待处理任务', count: data.value?.counts.pending, tone: 'blue', icon: ListTodo },
  { label: '即将到期', count: data.value?.counts.near, tone: 'warn', icon: Clock3 },
  { label: '已逾期', count: data.value?.counts.overdue, tone: 'red', icon: CalendarClock },
  { label: '待签收消息', count: data.value?.counts.unread, tone: 'teal', icon: MessageSquareText, path: '/feedback' },
])
const cards = computed(() => (data.value?.tasks ?? []).map(task => {
  const deadline = describeDeadline(task.deadline)
  const tracking = operator.value && task.resource === 'hazards' && task.status === 'pending_retest'
  const status = task.status && businessStatus(task.resource, task.status)
  return { ...task, deadlineInfo: deadline, tone: task.urgency ?? deadline.urgency, tracking, statusLabel: status,
    link: workbenchTaskLink(task, context.value) }
}))
const recentSystems = computed(() => (data.value?.recentSystems ?? []).map(system => ({ ...system,
  label: businessStatus('filings', system.status), link: workbenchTaskLink({ ...system, resource: 'filings' }, context.value),
})))
const rectification = computed(() => {
  const counts = data.value?.rectification
  return [
    { label: '待整改或补正', count: counts?.open },
    { label: '待机构复测', count: counts?.pendingRetest, status: 'pending_retest' },
    { label: '待监管复核', count: counts?.pendingReview, status: 'pending_review' },
    { label: '已销项', count: counts?.closed, status: 'closed' },
  ]
})
const deadlines = computed(() => cards.value.filter(task => task.tone === 'overdue' || task.tone === 'near').slice(0, 4))
const mainActions = computed(() => operator.value ? [
  { label: '新报备案', path: '/filings/new', icon: FilePlus2, detail: '为新的系统提交备案' },
  { label: '隐患整改', path: '/hazards', icon: Wrench, detail: '处理整改单和提交整改证据' },
] : [
  { label: '报送备案材料', path: '/filings/new', icon: FileUp, detail: '登记系统并提交备案材料' },
  { label: '新建测评报告', path: '/reports/new', icon: FileChartColumn, detail: '关联备案并提交测评报告' },
  { label: '隐患复测', path: '/hazards', icon: ScanSearch, detail: '办理复测并提交复测报告',
    filters: { status: 'pending_retest' } },
])
const shortcuts = computed(() => operator.value ? [
  { label: '备案进度', path: '/filings', icon: FileClock },
  { label: '整改规划', path: '/remediations', icon: ClipboardList },
  { label: '审核意见', path: '/feedback', icon: MessageSquareText },
  { label: '预警处置', path: '/alerts', icon: BellRing },
] : [
  { label: '实施管理', path: '/implementations', icon: ClipboardPenLine },
  { label: '测评报告台账', path: '/reports', icon: FileChartColumn },
  { label: '审核意见', path: '/feedback', icon: MessageSquareText },
])

/**
 * 用途 加载已选端与业务范围的工作台，刷新时保留最后一次有效结果。
 * 参数 无，请求范围取自当前 URL。
 * 返回值 本次查询任务，同一范围正在加载时直接返回。
 */
async function load() {
  if (!scope.value || request) return
  const current = new AbortController()
  request = current
  loading.value = true
  failure.value = ''
  try {
    const result = await api<ScopedWorkbenchData>(workbenchPath.value + params(query.value), {
      signal: current.signal,
    })
    if (!current.signal.aborted) data.value = result
  } catch (error) {
    if (!current.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) {
      request = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 打开或关闭复测催办确认，提交进行中时保留反馈面板。
 * 参数 task 为当前可催办的整改单，关闭时省略。
 * 返回值 无。
 */
function selectReminder(task?: WorkbenchTask) {
  if (reminderBusy.value) return
  reminderFailure.value = ''
  reminder.value = task
}

/**
 * 用途 向负责当前整改单的测评机构发送站内复测催办。
 * 参数 无，目标取自已确认事项和当前范围。
 * 返回值 服务端确认的发送反馈及工作台刷新任务。
 */
async function remindRetest() {
  const task = reminder.value
  if (!operator.value || !scope.value || !task?.canRemind || reminderRequest) return
  const current = new AbortController()
  reminderRequest = current
  reminderBusy.value = true
  reminderFailure.value = ''
  notice.value = undefined
  try {
    const message = await sendRetestReminder(scope.value, task.id, current.signal)
    if (current.signal.aborted) return
    notice.value = { text: message }
    reminder.value = undefined
    await load()
  } catch (error) {
    if (current.signal.aborted || isAbort(error)) return
    reminderFailure.value = errorText(error)
    if (error instanceof ApiError && (error.status === 409 || error.status === 404)) {
      notice.value = { text: reminderFailure.value, error: true }
      reminder.value = undefined
      request?.abort()
      request = undefined
      await load()
    }
  } finally {
    if (reminderRequest === current) {
      reminderRequest = undefined
      reminderBusy.value = false
    }
  }
}

watch([workbenchPath, scope], () => {
  request?.abort()
  request = undefined
  reminderRequest?.abort()
  reminderRequest = undefined
  data.value = reminder.value = undefined
  loading.value = reminderBusy.value = false
  failure.value = reminderFailure.value = ''
  notice.value = undefined
  void load()
}, { immediate: true })
refreshBusinessOnFocus(() => { if (!reminderRequest) void load() }, 10_000)
onScopeDispose(() => {
  request?.abort()
  reminderRequest?.abort()
})
</script>

<template>
  <section class="scoped-workbench" :class="operator ? 'operator-workbench' : 'assessment-workbench'"
    aria-labelledby="scoped-workbench-title">
    <header class="v2-page-head workbench-header">
      <span class="workbench-heading-icon" aria-hidden="true">
        <component :is="operator ? Building2 : FileChartColumn" :size="26" />
      </span>
      <div class="workbench-heading-copy">
        <h1 id="scoped-workbench-title">{{ operator ? '我的工作台' : '测评工作台' }}</h1>
        <p>{{ operator ? '查备案进度、看待办、办整改' : '材料报送、报告交付与隐患复测' }}</p>
      </div>
      <button v-if="scope" class="button neutral" type="button" :disabled="loading" @click="load">
        <RefreshCw :size="15" aria-hidden="true" /> 刷新工作台
      </button>
    </header>

    <section class="panel workbench-panel business-actions" aria-labelledby="business-action-title">
      <header class="section-heading">
        <h2 id="business-action-title">{{ operator ? '常用办理' : '测评交付' }}</h2>
        <span v-if="!scope" class="muted">先填写{{ scopeSettings.label }}再办理</span>
      </header>
      <nav class="business-action-grid" aria-labelledby="business-action-title">
        <component :is="scope ? RouterLink : 'a'" v-for="(item, index) in mainActions" :key="item.path"
          class="business-action" :class="{ 'business-action-primary': operator && index === 0 }"
          v-bind="scope ? { to: { path: basePath + item.path, query: { ...query, ...('filters' in item ? item.filters : {}) } } }
            : { href: '#workbench-scope' }">
          <span class="business-action-icon"><component :is="item.icon" :size="21" aria-hidden="true" /></span>
          <span><strong>{{ item.label }}</strong><small>{{ item.detail }}</small></span>
          <ArrowUpRight class="business-action-arrow" :size="16" aria-hidden="true" />
        </component>
      </nav>
      <nav class="business-shortcuts" aria-label="更多业务入口">
        <component :is="scope ? RouterLink : 'a'" v-for="item in shortcuts" :key="item.path" class="business-shortcut"
          v-bind="scope ? { to: { path: basePath + item.path, query } }
            : { href: '#workbench-scope' }">
          <component :is="item.icon" :size="16" aria-hidden="true" />{{ item.label }}
        </component>
      </nav>
    </section>

    <AsyncState v-if="scope" :loading="loading && !data" :error="data ? '' : failure" @retry="load">
      <template v-if="data">
        <p v-if="failure" class="form-message warning-message" role="status">刷新失败，显示上次读取的数据。{{ failure }}</p>
        <p v-if="notice" class="form-message" :class="notice.error ? 'error-message' : 'success-message'"
          :role="notice.error ? 'alert' : 'status'">{{ notice.text }}</p>

        <div class="summary-grid" aria-label="工作台概览">
          <article v-for="figure in figures" :key="figure.label"
            :data-tone="figure.tone" class="panel summary-card">
            <RouterLink v-if="figure.path" class="summary-link"
              :to="{ path: basePath + figure.path, query }">
              <span class="summary-label"><component :is="figure.icon" :size="16" aria-hidden="true" />{{ figure.label }}</span>
              <strong>{{ figure.count === undefined ? '—' : formatNumber.format(figure.count) }}</strong>
            </RouterLink>
            <template v-else>
              <span class="summary-label"><component :is="figure.icon" :size="16" aria-hidden="true" />{{ figure.label }}</span>
              <strong>{{ figure.count === undefined ? '—' : formatNumber.format(figure.count) }}</strong>
            </template>
          </article>
        </div>

        <div class="workbench-columns">
          <section class="panel workbench-panel" aria-labelledby="tasks-title">
            <header class="section-heading">
              <h2 id="tasks-title">{{ operator ? '待办事项' : '任务看板' }}</h2>
              <span v-if="cards.length" class="muted">待办优先，按期限排序</span>
            </header>
            <AsyncState :empty="!cards.length" empty-text="当前范围内暂无待办事项。">
              <ul class="task-list">
                <li v-for="task in cards" :key="task.resource + ':' + task.id" class="task-card" :data-tone="task.tone">
                  <div class="task-body">
                    <strong>{{ task.label }}</strong>
                    <div class="task-context">{{ task.systemName }}<template v-if="task.number"> · {{ task.number }}</template></div>
                    <span v-if="task.status && task.statusLabel" class="status-badge" :class="businessBadge(task.status)">
                      <span aria-hidden="true"></span>{{ task.statusLabel }}
                    </span>
                  </div>
                  <div class="task-meta">
                    <time v-if="task.deadline" :datetime="task.deadline" class="deadline-time">{{ task.deadlineInfo.text }}</time>
                    <small v-if="task.deadline" class="muted">{{ task.deadline }}</small>
                    <span v-else class="muted">{{ task.deadlineInfo.text }}</span>
                    <div class="task-actions">
                      <RouterLink class="text-action" :to="task.link">{{ task.tracking ? '查看进度' : '直达处理' }}</RouterLink>
                      <button v-if="operator && task.canRemind" class="button neutral small" type="button"
                        :disabled="reminderBusy" @click="selectReminder(task)">催办复测</button>
                    </div>
                  </div>
                </li>
              </ul>
            </AsyncState>
          </section>

          <aside class="panel workbench-panel" aria-labelledby="rectification-title">
            <header class="section-heading">
              <h2 id="rectification-title">{{ operator ? '整改提醒' : '整改进度' }}</h2>
              <RouterLink class="text-action" :to="{ path: basePath + '/hazards', query }">查看整改单</RouterLink>
            </header>
            <div class="rectification-grid">
              <template v-for="item in rectification" :key="item.label">
                <RouterLink v-if="item.status" class="rectification-stat"
                  :to="{ path: basePath + '/hazards', query: { ...query, status: item.status } }">
                  <strong>{{ item.count === undefined ? '—' : formatNumber.format(item.count) }}</strong>{{ item.label }}
                </RouterLink>
                <div v-else class="rectification-stat">
                  <strong>{{ item.count === undefined ? '—' : formatNumber.format(item.count) }}</strong>{{ item.label }}
                </div>
              </template>
            </div>
            <header class="section-heading"><h2>临期与逾期</h2></header>
            <ul v-if="deadlines.length" class="task-list deadline-list">
              <li v-for="task in deadlines" :key="task.resource + ':' + task.id">
                <RouterLink class="task-card deadline-card" :data-tone="task.tone" :to="task.link">
                  <span><strong>{{ task.label }}</strong><small>{{ task.systemName }}</small></span>
                  <time v-if="task.deadline" :datetime="task.deadline" class="deadline-time">{{ task.deadlineInfo.text }}</time>
                </RouterLink>
              </li>
            </ul>
            <p v-else class="muted">当前看板没有临期或逾期事项。</p>
          </aside>
        </div>

        <section v-if="operator" class="panel workbench-panel systems-panel" aria-labelledby="systems-title">
          <header class="section-heading">
            <h2 id="systems-title">我的备案系统</h2>
            <RouterLink class="text-action" :to="{ path: basePath + '/filings', query }">查看全部备案</RouterLink>
          </header>
          <AsyncState :empty="!recentSystems.length" empty-text="该运营单位暂无备案系统。">
            <div class="table-wrap">
              <table class="data-table systems-table">
                <thead><tr><th scope="col">系统名称</th><th scope="col">备案编号</th><th scope="col">备案状态</th><th scope="col">操作</th></tr></thead>
                <tbody>
                  <tr v-for="system in recentSystems" :key="system.id">
                    <td>{{ system.systemName }}</td><td class="muted">{{ system.number || '—' }}</td>
                    <td><span class="status-badge" :class="businessBadge(system.status)">
                      <span aria-hidden="true"></span>{{ system.label }}
                    </span></td>
                    <td><RouterLink class="text-action" :to="system.link">查进度</RouterLink></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </AsyncState>
        </section>
      </template>
    </AsyncState>

    <V2RouteDialog v-if="reminder" label="确认复测催办" :width="460" @close="selectReminder()">
      <form class="reminder-form" @submit.prevent="remindRetest">
        <h2>确认复测催办</h2>
        <p>向负责「{{ reminder.systemName }}」本轮复测的测评机构发送站内催办。</p>
        <p v-if="reminderFailure" class="form-message error-message" role="alert">{{ reminderFailure }}</p>
        <div class="page-actions">
          <button class="button neutral" type="button" :disabled="reminderBusy" @click="selectReminder()">取消</button>
          <button class="button primary" type="submit" :disabled="reminderBusy">
            {{ reminderBusy ? '正在发送…' : '发送催办' }}
          </button>
        </div>
      </form>
    </V2RouteDialog>
  </section>
</template>

<style scoped>
.workbench-header {
  position: relative;
  isolation: isolate;
  overflow: hidden;
  align-items: center;
  justify-content: flex-start;
  gap: 14px;
  padding: 20px 22px;
  color: var(--c-primary-700);
  background: linear-gradient(105deg, var(--role-hero-start), var(--role-hero-end));
  border: 1px solid var(--role-edge);
  border-radius: var(--radius);
  box-shadow: var(--role-elevation);
}
.workbench-header::after {
  content: '';
  position: absolute;
  inset: 0 0 0 50%;
  z-index: -1;
  background:
    repeating-linear-gradient(0deg, var(--role-grid) 0 1px, transparent 1px 22px),
    repeating-linear-gradient(90deg, var(--role-grid) 0 1px, transparent 1px 22px);
  mask-image: linear-gradient(90deg, transparent, #000);
  pointer-events: none;
}
.workbench-heading-icon {
  display: grid; place-items: center; flex: none; width: 49px; height: 49px;
  color: var(--primary); background: #ffffffa1; border: 1px solid var(--role-edge); border-radius: 10px;
}
.workbench-heading-copy { flex: 1; min-width: 0; }
.workbench-header h1 { font-size: clamp(1.35rem, 2.4vw, 1.9rem); font-weight: 690; }
.workbench-header p { color: var(--role-text); }
.scope-form {
  display: flex; align-items: flex-end; flex-wrap: wrap; gap: 10px; margin-bottom: 16px; padding: 12px 16px;
}
.scope-input { flex: 0 1 340px; font-size: 13px; }
#workbench-scope { scroll-margin-top: calc(var(--topbar-h) + 28px); }
.assessment-workbench .section-heading { padding-bottom: 8px; border-bottom: 1px solid var(--line); }
.business-actions { margin-block-end: 16px; }
.business-action-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(210px, 100%), 1fr)); gap: 10px; }
.business-action {
  min-width: 0;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  padding: 13px 12px;
  color: var(--c-primary-700);
  background: linear-gradient(145deg, #ffffffc7, var(--role-highlight));
  border: 1px solid var(--role-line);
  border-radius: 9px;
  box-shadow: inset 0 1px 0 #fff, 0 3px 8px #233f5410;
}
.business-action-icon {
  display: grid; place-items: center; width: 38px; height: 38px; border-radius: 8px;
  background: #ffffffac; color: var(--primary); border: 1px solid var(--role-line);
}
.business-action strong, .business-action small { display: block; }
.business-action strong { font-size: 15px; font-weight: 690; }
.business-action small { margin-top: 2px; color: var(--role-muted); font-size: 12px; }
.business-action-arrow { color: var(--primary); }
.business-action-primary {
  color: #fff;
  background: linear-gradient(118deg, #175b50, #237266);
  border-color: #2b8272;
  box-shadow: inset 0 1px 0 #aee0d941, 0 5px 14px #245e5735;
}
.business-action-primary small { color: #e5f7f1; }
.business-action-primary :is(.business-action-icon, .business-action-arrow) { color: #e5fbf4; }
.business-action-primary .business-action-icon { background: #ffffff26; border-color: #ccfff35c; }
.business-action:hover { border-color: var(--primary); }
.business-action:hover strong, .business-shortcut:hover { text-decoration: underline; }
.business-shortcuts { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 11px; }
.business-shortcut {
  display: inline-flex; align-items: center; gap: 6px; padding: 6px 10px;
  color: var(--c-primary-700); background: #ffffff81; border: 1px solid var(--role-line); border-radius: 7px;
}
@media (prefers-reduced-motion: no-preference) {
  .business-action { transition: transform .16s ease, border-color .16s ease; }
  .business-action:hover { transform: translateY(-2px); }
}
.summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin: 0 0 16px; }
.summary-card {
  --role-panel-edge: var(--role-edge) var(--role-edge) var(--role-edge) var(--tone, var(--primary));
  border-left-width: 3px; padding: 14px 16px; overflow: hidden;
}
.summary-label { display: flex; align-items: center; gap: 7px; color: var(--muted); font-size: 13px; }
.summary-label svg { color: var(--tone, var(--primary)); }
.summary-card strong { display: block; color: var(--tone, var(--c-primary-700)); font-size: 26px; line-height: 1.25; }
.summary-link { display: block; }
.summary-link:hover span { color: var(--primary); text-decoration: underline; }
[data-tone="teal"] { --tone: var(--c-teal); }
[data-tone="warn"], [data-tone="near"] { --tone: var(--c-warn); }
[data-tone="red"], [data-tone="overdue"] { --tone: var(--c-danger); }
[data-tone="upcoming"] { --tone: var(--c-teal); }
.workbench-panel { min-width: 0; padding: 16px; }
.systems-panel { margin-top: 16px; }
.systems-table { min-width: 480px; }
.systems-table :is(td, th) { padding: 8px 10px; }
.workbench-columns { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 1fr); align-items: start; gap: 16px; }
.section-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.section-heading h2 { display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; }
.section-heading h2::before { content: ""; width: 3px; height: 14px; border-radius: 2px; background: var(--primary); }
.section-heading :is(a, span) { font-size: 12px; }
.task-list { display: grid; gap: 8px; padding: 0; margin: 0; list-style: none; }
.task-card {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 10px; align-items: start;
  padding: 10px 12px; border: 1px solid var(--line); border-left: 3px solid var(--tone, var(--primary));
  border-radius: var(--radius-sm); background: var(--c-surface-2);
}
.task-body { min-width: 0; }
.task-body strong, .deadline-card strong { color: var(--text); font-size: 14px; font-weight: 600; }
.task-context { margin: 2px 0 5px; color: var(--muted); font-size: 12px; overflow-wrap: anywhere; }
.task-meta { display: grid; gap: 2px; justify-items: end; color: var(--text); white-space: nowrap; font-size: 12px; }
.deadline-time { color: var(--tone, var(--text)); font-size: 12px; font-weight: 600; }
.task-actions { display: flex; flex-wrap: wrap; justify-content: end; align-items: center; gap: 7px; margin-top: 4px; }
.rectification-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; margin: 0 0 16px; }
.rectification-stat {
  padding: 8px 10px; border-radius: var(--radius-sm); background: var(--c-primary-50);
  color: var(--muted); font-size: 12px;
}
.rectification-stat strong { display: block; color: var(--c-primary-700); font-size: 18px; line-height: 1.2; }
a.rectification-stat:hover { color: var(--primary); text-decoration: underline; }
.deadline-card { align-items: center; }
.deadline-card:hover { border-color: var(--tone, var(--primary)); }
.deadline-card small { display: block; color: var(--muted); overflow-wrap: anywhere; }
.reminder-form { display: grid; gap: 12px; }
.reminder-form h2 { font-size: 16px; }
.reminder-form p { overflow-wrap: anywhere; }
@media (max-width: 1050px) { .workbench-columns { grid-template-columns: 1fr; } }
@media (max-width: 620px) {
  .scope-input { flex-basis: 100%; }
  .summary-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .workbench-header { padding: 13px; flex-wrap: wrap; }
}
@media (max-width: 400px) {
  .workbench-header { align-items: start; flex-wrap: wrap; }
  .task-card:not(.deadline-card) { grid-template-columns: 1fr; }
  .task-meta { justify-items: start; }
  .task-actions { justify-content: start; }
}
</style>

<script setup lang="ts">
import { computed } from 'vue'
import {
  ArrowUpRight, BellRing, BookOpenText, ClipboardCheck, ClipboardPenLine, FileSearch, Files,
  FolderArchive, Radar, Settings, ShieldCheck,
} from '@lucide/vue'
import AsyncState from '../components/AsyncState.vue'
import { dashboardTabLocation } from '../lib/dashboard'
import { hazardStatuses } from '../lib/hazards'
import { businessBadge } from '../lib/scopedBusiness'
import { refreshWorkbench, workbench } from '../lib/workbench'
import { describeDeadline, workbenchTaskLink, type WorkbenchTask } from '../lib/workbenchItems'

const shortcuts = [
  { to: '/filings', name: '等级保护审核', icon: ClipboardCheck },
  { to: '/archives/units', name: '全息档案', icon: FolderArchive },
  { to: '/alerts', name: '监测预警', icon: BellRing },
  { to: '/knowledge', name: '知识库', icon: BookOpenText },
  { to: dashboardTabLocation, name: '驾驶舱', icon: Radar },
  { to: '/audit', name: '系统管理', icon: Settings },
]
const formatNumber = new Intl.NumberFormat('zh-CN')
const { overview: data, loading, failure } = workbench
const remediation = computed(() => data.value?.remediation)
const figures = computed(() => {
  const current = data.value
  return [
    { number: current?.filingTotal, label: '备案总数', icon: Files, to: '/filings',
      help: current && `本年新增 ${current.filingsThisYear}`, color: 'blue' },
    { number: current?.implementationOpen, label: '实施报备中', icon: ClipboardPenLine, to: '/implementations',
      help: current && `待监管复核 ${current.implementationInReview}`, color: 'teal' },
    { number: current && current.reportInitial + current.reportFinal, label: '测评待审核',
      icon: FileSearch, to: '/reports',
      help: current && `初审 ${current.reportInitial} / 终审 ${current.reportFinal}`, color: 'warn' },
    { number: current?.activeAlerts, label: '活跃预警', icon: BellRing, to: '/alerts',
      help: '待处理及处理中预警', color: current?.activeAlerts ? 'danger' : 'blue' },
  ]
})
const completionRate = computed(() => data.value?.hazardTotal
  ? `${Math.round(data.value.hazardClosed * 100 / data.value.hazardTotal)}%` : '—')
const tasks = computed(() => {
  const entries = data.value?.tasks ?? []
  const queue: readonly WorkbenchTask[] = data.value?.workflow ? entries : [
    ...entries,
    ...(remediation.value?.pendingPlans ?? []).map(plan => ({
      ...plan, resource: 'remediations' as const, label: '整改规划审核', number: plan.reportNumber,
      status: 'pending_review',
    })),
    ...(remediation.value?.items ?? []).map(item => ({
      ...item, resource: 'hazards' as const, label: hazardStatuses[item.status], number: item.description,
    })),
  ]
  return [...queue].sort((a, b) => (a.deadline ?? '9999-12-31').localeCompare(b.deadline ?? '9999-12-31'))
    .map(task => ({ ...task, to: workbenchTaskLink(task), deadlineInfo: describeDeadline(task.deadline),
      badge: businessBadge(task.status ?? (task.resource === 'filing-applications' ? 'pending_final' : 'pending_initial')),
    }))
})
const queueCount = computed(() => data.value?.workflow?.pending ?? tasks.value.length)

</script>

<template>
  <section class="regulatory-workbench">
    <header class="workbench-hero">
      <div class="workbench-radar" aria-hidden="true">
        <span class="workbench-radar-sweep"></span>
        <ShieldCheck :size="52" :stroke-width="1.25" />
      </div>
      <div class="workbench-hero-copy">
        <h1>网络安全监管工作台</h1>
        <p class="workbench-status" :data-state="failure ? 'error' : data ? 'ready' : 'loading'" role="status">
          <span aria-hidden="true"></span>{{ failure ? (data ? '刷新失败，显示上次数据' : '监管数据加载失败')
            : data ? '监管数据已同步' : '正在加载监管数据' }}
        </p>
        <RouterLink class="button workbench-launch" :to="dashboardTabLocation" target="_blank" rel="noopener">
          <Radar :size="16" aria-hidden="true" />打开指挥驾驶舱<ArrowUpRight :size="15" aria-hidden="true" />
        </RouterLink>
      </div>
    </header>

    <div class="v2-stat-grid" aria-label="态势总览">
      <RouterLink v-for="figure in figures" :key="figure.label" class="v2-stat-card" :data-color="figure.color"
        :to="figure.to">
        <span class="workbench-metric-title">
          <span class="workbench-metric-icon" aria-hidden="true"><component :is="figure.icon" :size="18" /></span>
          {{ figure.label }}<ArrowUpRight :size="15" class="workbench-metric-arrow" aria-hidden="true" />
        </span>
        <strong>{{ figure.number === undefined ? '—' : formatNumber.format(figure.number) }}</strong>
        <small>{{ figure.help || '—' }}</small>
      </RouterLink>
    </div>

    <div class="v2-kpi-grid">
      <div class="workbench-primary">
        <section class="panel v2-card-pad" aria-labelledby="workbench-queue">
          <header class="v2-section-header">
            <h2 id="workbench-queue">待我审核<span v-if="data">（{{ formatNumber.format(queueCount) }} 项）</span></h2>
            <button class="button neutral small" type="button" :disabled="loading" @click="refreshWorkbench">刷新待办</button>
          </header>
          <AsyncState :loading="loading && !data" :error="failure && !tasks.length ? failure : ''"
            :empty="!tasks.length" empty-text="当前没有待办理事项。" @retry="refreshWorkbench">
            <div class="table-wrap v2-workbench-table">
              <table class="data-table">
                <thead><tr><th>待办事项</th><th>上下文说明</th><th>剩余时限</th><th>操作</th></tr></thead>
                <tbody>
                  <tr v-for="item in tasks" :key="item.resource + ':' + item.id">
                    <td><span class="status-badge" :class="item.badge"><span aria-hidden="true"></span>{{ item.label }}</span></td>
                    <td>{{ item.systemName }}<template v-if="item.number"> · {{ item.number }}</template></td>
                    <td class="deadline-cell" :data-urgency="item.urgency ?? item.deadlineInfo.urgency">
                      <time v-if="item.deadline" :datetime="item.deadline">{{ item.deadlineInfo.text }}</time>
                      <small v-if="item.deadline" class="muted">{{ item.deadline }}</small>
                      <span v-else class="muted">{{ item.deadlineInfo.text }}</span>
                    </td>
                    <td><RouterLink class="text-action" :to="item.to">直达处理</RouterLink></td>
                  </tr>
                </tbody>
              </table>
            </div>
          </AsyncState>
          <p v-if="queueCount > tasks.length" class="muted queue-note">当前显示前 {{ tasks.length }} 项，完整清单可在各业务模块查看。</p>
          <p v-if="failure && tasks.length" class="form-message error-message" role="status">{{ failure }}</p>
        </section>

        <section class="panel v2-card-pad" aria-labelledby="quick-title">
          <header class="v2-section-header"><h2 id="quick-title">快捷入口</h2></header>
          <nav class="v2-quick-grid" aria-label="快捷入口">
            <RouterLink v-for="item in shortcuts" :key="item.name" class="v2-quick" :to="item.to"
              :target="item.to === dashboardTabLocation ? '_blank' : undefined"
              :rel="item.to === dashboardTabLocation ? 'noopener' : undefined">
              <span class="v2-quick-icon" aria-hidden="true"><component :is="item.icon" :size="19" /></span>{{ item.name }}
            </RouterLink>
          </nav>
        </section>
      </div>

      <aside class="panel v2-card-pad" aria-label="关键指标和临期提醒">
        <header class="v2-section-header"><h2>关键指标</h2></header>
        <div class="v2-rate-grid">
          <div><strong>{{ completionRate }}</strong><small>整改完成率</small></div>
          <div><strong>—</strong><small>等保覆盖率</small></div>
        </div>
        <header class="v2-section-header"><h2>临期提醒</h2></header>
        <div v-if="data?.workflow" class="v2-task-list" aria-label="工作流期限">
          <div class="v2-task-card" data-color="danger">
            <strong>工作流已逾期 {{ data.workflow.overdue }} 项</strong>
          </div>
          <div class="v2-task-card" data-color="warn">
            <strong>工作流即将到期 {{ data.workflow.near }} 项</strong>
          </div>
        </div>
        <div v-if="data?.reviewDeadlines" class="v2-task-list" aria-label="备案与测评审核时限">
          <div class="v2-task-card" data-color="warn">
            <strong>审核已逾期 {{ data.reviewDeadlines.overdue }} 项</strong><small>备案与测评审核</small>
          </div>
          <div class="v2-task-card" data-color="blue">
            <strong>10 日内到期 {{ data.reviewDeadlines.near }} 项</strong><small>备案与测评审核</small>
          </div>
          <div class="v2-task-card" data-color="teal">
            <strong>11–30 日内到期 {{ data.reviewDeadlines.upcoming }} 项</strong><small>备案与测评审核</small>
          </div>
        </div>
        <div v-if="remediation" class="v2-task-list">
          <RouterLink class="v2-task-card" data-color="warn" :to="{ path: '/hazards', query: { due: 'overdue' } }">
            <strong>已逾期 {{ remediation.overdue }} 项</strong><small>已逾期隐患</small>
          </RouterLink>
          <RouterLink class="v2-task-card" data-color="blue" :to="{ path: '/hazards', query: { due: 'within_10_days' } }">
            <strong>10 日内到期 {{ remediation.dueWithin10Days }} 项</strong><small>临期隐患</small>
          </RouterLink>
          <div v-if="data" class="v2-task-card" data-color="teal">
            <strong>30 日内到期 {{ data.hazardDueWithin30Days }} 项</strong><small>未销项隐患</small>
          </div>
        </div>
        <nav v-if="remediation" aria-label="整改待办筛选" class="v2-metric-links">
          <RouterLink class="text-action" :to="{ path: '/remediations', query: { status: 'pending_review' } }">待审核整改规划 {{ remediation.pendingPlanReview }}</RouterLink>
          <RouterLink class="text-action" :to="{ path: '/hazards', query: { status: 'pending_review' } }">待复核隐患 {{ remediation.pendingReview }}</RouterLink>
        </nav>
      </aside>
    </div>
  </section>
</template>

<style scoped>
.workbench-hero {
  position: relative;
  isolation: isolate;
  display: flex;
  align-items: center;
  overflow: hidden;
  min-height: 228px;
  padding: clamp(20px, 3.2vw, 48px);
  margin-bottom: 20px;
  color: #f0f8ff;
  background:
    radial-gradient(ellipse at 73% 40%, #5286ab8c, transparent 58%),
    linear-gradient(109deg, #0c213aef 20%, #123e60de 100%);
  border: 1px solid #6a9cbb;
  border-radius: var(--radius);
  box-shadow: inset 0 1px 0 #aadbf15c, 0 12px 28px #132f473a;
}

.workbench-hero::before {
  content: '';
  position: absolute;
  z-index: -1;
  inset: 0 0 0 24%;
  background:
    repeating-linear-gradient(90deg, #7fbad92b 0 1px, transparent 1px 32px),
    repeating-linear-gradient(0deg, #7fbad92b 0 1px, transparent 1px 32px);
  mask-image: linear-gradient(90deg, transparent 5%, #000 75%);
  pointer-events: none;
}

.workbench-hero-copy { min-width: 0; }
.workbench-hero h1 { font-size: clamp(1.55rem, 3vw, 2.65rem); letter-spacing: .035em; }
.workbench-status {
  display: flex;
  align-items: center;
  gap: 9px;
  min-height: 20px;
  margin: 10px 0 20px;
  color: #bad3e8;
  font-size: 13px;
}

.workbench-status span {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 100%;
  background: #96b1c7;
}
.workbench-status[data-state='ready'] span { background: #76dcbd; box-shadow: 0 0 8px #64d4b06b; }
.workbench-status[data-state='error'] span { background: #ffbe89; }

.workbench-launch {
  min-height: 39px;
  padding-inline: 13px;
  background: #b0e5fc;
  color: #102c43;
  border: 1px solid #c8eeff;
  font-weight: 650;
}
.workbench-launch:hover { background: #dcf5ff; }
.workbench-launch:focus-visible { outline-color: #b0e5fc; }

.workbench-radar {
  display: grid;
  place-items: center;
  position: absolute;
  z-index: -1;
  right: clamp(32px, 10vw, 180px);
  top: 50%;
  width: clamp(192px, 24vw, 340px);
  aspect-ratio: 1;
  overflow: hidden;
  transform: translateY(-50%);
  border-radius: 50%;
  background:
    radial-gradient(circle, transparent 0 20%, #70bed654 20.3% 20.7%,
      transparent 21% 35%, #70bed65c 35.3% 35.7%,
      transparent 36% 47%, #8ccfe37f 47.3% 47.7%, transparent 48%),
    linear-gradient(90deg, transparent 49.8%, #83cbe34d 50%, transparent 50.3%),
    linear-gradient(0deg, transparent 49.8%, #83cbe34d 50%, transparent 50.3%);
  pointer-events: none;
}
.workbench-radar::after {
  content: '';
  position: absolute;
  inset: 0;
  background:
    radial-gradient(circle at 70% 21%, #9eeaff 0 2px, #67bbec44 3px, transparent 6px),
    radial-gradient(circle at 72% 62%, #85dffa 0 2px, #6aaee654 3px, transparent 6px),
    radial-gradient(circle at 23% 66%, #9ae8ff 0 2px, #67bbec44 3px, transparent 6px);
}
.workbench-radar-sweep {
  position: absolute;
  inset: 0;
  background: conic-gradient(from 238deg, transparent 0deg 282deg, #6fd9ff20 323deg,
    #6fd9ff61 359deg, transparent 360deg);
  mask-image: radial-gradient(circle, #000 0 64%, transparent 66%);
}
.workbench-radar svg {
  position: relative;
  z-index: 1;
  color: #ace8ff;
  filter: drop-shadow(0 0 9px #8edaff8a);
}

.v2-stat-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin-bottom: 18px; }
.v2-stat-card {
  position: relative; overflow: hidden; background: var(--regulator-glass);
  border: 1px solid var(--regulator-glass-edge);
  border-radius: var(--radius); padding: 14px 16px 15px; box-shadow: var(--regulator-glass-shadow);
}
.v2-stat-card::before {
  content: ''; position: absolute; inset: 0 0 auto; height: 2px;
  background: linear-gradient(90deg, var(--marker, var(--c-primary)), transparent 70%);
}
[data-color="teal"] { --marker: var(--c-teal); }
[data-color="warn"] { --marker: var(--c-warn); }
[data-color="danger"] { --marker: var(--c-danger); }
.v2-stat-card:hover, .v2-stat-card:focus-visible { border-color: #729eba; }
.workbench-metric-title { display: flex; align-items: center; gap: 8px; color: var(--muted); }
.workbench-metric-icon {
  display: grid; place-items: center; flex: none; width: 33px; height: 33px; border-radius: 7px;
  color: var(--marker, var(--c-primary));
  background: color-mix(in srgb, var(--marker, var(--c-primary)) 9%, white);
}
.workbench-metric-arrow { margin-left: auto; color: #4e718d; }
.v2-stat-card strong, .v2-stat-card small { display: block; }
.v2-stat-card strong {
  margin-top: 9px; color: var(--c-primary-700); font-size: 30px; line-height: 1.1;
  font-variant-numeric: tabular-nums;
}
.v2-stat-card[data-color='danger'] strong { color: var(--c-danger); }
.v2-stat-card small { margin-top: 6px; font-size: 12px; color: var(--c-text-3); }
.v2-kpi-grid { display: grid; grid-template-columns: minmax(0, 2fr) minmax(0, 1fr); align-items: start; gap: 16px; }
.workbench-primary { min-width: 0; display: grid; gap: 16px; }
.v2-card-pad { padding: 16px; }
.v2-section-header {
  display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px;
}
.v2-section-header h2 {
  display: flex; align-items: center; gap: 8px; font-size: 14px; font-weight: 600; letter-spacing: normal;
}
.v2-section-header h2::before { content: ""; width: 3px; height: 14px; border-radius: 2px; background: var(--primary); }
.v2-workbench-table .data-table { min-width: 470px; }
.v2-workbench-table .data-table :is(td, th) { padding: 8px 10px; }
.deadline-cell time { font-weight: 600; }
.deadline-cell small { display: block; margin-top: 2px; }
.deadline-cell[data-urgency="overdue"] time { color: var(--c-danger); }
.deadline-cell[data-urgency="near"] time { color: var(--c-warn); }
.queue-note { margin-top: 8px; font-size: 12px; }
.v2-rate-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; margin-bottom: 14px; }
.v2-rate-grid strong { display: block; font-size: 22px; color: var(--c-primary-700); }
.v2-rate-grid div:first-child strong { color: var(--c-success); }
.v2-rate-grid small { font-size: 12px; color: var(--muted); }
.v2-task-list { display: grid; gap: 10px; }
.v2-task-list + .v2-task-list { margin-top: 10px; }
.v2-task-card {
  position: relative; background: var(--c-surface-2); border: 1px solid var(--line);
  border-radius: 8px; padding: 10px 12px 10px 32px;
}
.v2-task-card::before {
  content: ""; position: absolute; inset: 11px auto 11px 14px; width: 6px;
  border-radius: 3px; background: var(--marker, var(--c-info-bg));
}
.v2-task-card strong { display: block; color: var(--text); font-weight: 500; }
.v2-task-card small { color: var(--muted); font-size: 12px; }
.v2-metric-links { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 10px; font-size: 12px; }
.v2-quick-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100px, 100%), 1fr)); gap: 12px; }
.v2-quick {
  display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 16px 8px;
  border: 1px solid var(--line); border-radius: var(--radius); color: var(--muted); font-size: 13px;
}
a.v2-quick:hover { border-color: var(--primary); }
.v2-quick-icon {
  display: grid; place-items: center; width: 38px; height: 38px; border-radius: 10px;
  background: var(--c-info-bg); color: var(--primary); font-size: 18px;
}

@media (prefers-reduced-motion: no-preference) {
  .workbench-radar-sweep { animation: workbench-scan 27s linear infinite; }
  .v2-stat-card { transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease; }
  .v2-stat-card:hover { transform: translateY(-2px); box-shadow: 0 7px 18px #24507818; }
}
@keyframes workbench-scan { to { transform: rotate(1turn); } }

@media (max-width: 1080px) {
  .v2-kpi-grid { grid-template-columns: 1fr; }
}
@media (max-width: 760px) {
  .v2-stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .v2-quick-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .workbench-hero { min-height: 180px; }
  .workbench-radar { right: -20px; opacity: .37; }
}
@media (max-width: 460px) {
  .v2-stat-grid { gap: 8px; }
  .v2-stat-card { padding: 10px; }
  .workbench-metric-title { gap: 5px; font-size: 12px; }
  .workbench-metric-icon { width: 25px; height: 25px; }
  .workbench-metric-icon svg { width: 15px; }
  .workbench-metric-arrow { display: none; }
}
</style>

<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import {
  Activity, ArrowLeft, ArrowRight, BellRing, Boxes, CalendarClock, ChartNoAxesCombined, CircleCheck,
  ClipboardList, ClipboardPenLine, FileChartColumn, FileSearch, Files, MapPinned, Maximize, Minimize, RefreshCw,
  Server, ShieldAlert, ShieldCheck,
} from '@lucide/vue'
import { RouterLink, useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import DashboardAlerts from '../components/DashboardAlerts.vue'
import DashboardAnalytics from '../components/DashboardAnalytics.vue'
import DashboardClassificationEditor from '../components/DashboardClassificationEditor.vue'
import DashboardDistribution from '../components/DashboardDistribution.vue'
import DashboardDistrictResponse from '../components/DashboardDistrictResponse.vue'
import DashboardDistrictRisk from '../components/DashboardDistrictRisk.vue'
import DashboardRanking from '../components/DashboardRanking.vue'
import DashboardRisk from '../components/DashboardRisk.vue'
import DashboardTrend from '../components/DashboardTrend.vue'
import { alertPath, alertStatuses, type AlertStatus } from '../lib/alerts'
import { dashboardAccessEvent, sessionChangeKey } from '../lib/api'
import { archivePaths } from '../lib/archives'
import {
  dashboardNumber, dashboardPath, dashboardPercent, dashboardTabFlag, dashboardTime, useDashboard,
} from '../lib/dashboard'
import type { ClassificationKind } from '../lib/dashboardAnalytics'
import { vDashboardScroll } from '../lib/dashboardAutoScroll'
import { useDistrictDetail, useDistrictSummary } from '../lib/districtResponse'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { refreshSession, session } from '../lib/session'
import {
  dashboardColors, type DashboardTone, type DashboardTrendRange, type MetricSlice,
} from '../lib/dashboardMetrics'

const tabs = {
  overview: { label: '态势总览', icon: Activity },
  risk: { label: '风险分析', icon: ShieldAlert },
  alerts: { label: '实时预警', icon: BellRing },
  statistics: { label: '统计分析', icon: ChartNoAxesCombined },
}
const route = useRoute()
const router = useRouter()
const tabSessionKey = 'grade-business.dashboard-standalone'
const standalone = ref(route.query[dashboardTabFlag] === '1')
const returnLabel = computed(() => standalone.value ? '返回原页面' : '返回工作台')
let returnTimer: number | undefined
const tab = computed(() => {
  const selected = route.query.tab
  return typeof selected === 'string' && Object.hasOwn(tabs, selected) ? selected as keyof typeof tabs : 'overview'
})
const openedSession = session.value
const area = openedSession?.dashboardArea
const sessionAvailable = ref(true)
const sessionCheckPending = ref(false)
const fixedDistrict = area?.districts.length === 1 ? area.name : undefined
const { statistics: globalStatistics, analytics: globalAnalytics, overview: globalOverview, alerts,
  alertCounts: globalAlertCounts, alertsUpdatedAt, failure, loading, refresh } = useDashboard(!area)
const selectedDistrict = computed(() => {
  if (fixedDistrict) return fixedDistrict
  const district = route.query.district
  return typeof district === 'string' && (!area || area.districts.includes(district)) ? district : null
})
const overviewVisible = computed(() => tab.value === 'overview')
const districts = useDistrictSummary(overviewVisible)
const showScopedDetail = computed(() => Boolean(area) || overviewVisible.value || selectedDistrict.value !== null)
const districtDetail = useDistrictDetail(selectedDistrict, showScopedDetail)
const snapshot = districtDetail.snapshot
// 区县按钮立即反映选择，整屏内容和范围说明随成功响应一起更新。
const displayedDistrict = computed(() => snapshot.value ? snapshot.value.district : selectedDistrict.value)
const districtScoped = computed(() => Boolean(area) || displayedDistrict.value !== null)
const displayedDistrictName = computed(() => displayedDistrict.value === null
  ? area?.name ?? '' : displayedDistrict.value || '未归区')
const targetDistrictName = computed(() => selectedDistrict.value === null
  ? area?.name ?? '全域' : selectedDistrict.value || '未归区')
const scopeChanging = computed(() => displayedDistrict.value !== selectedDistrict.value)
const statistics = computed(() => districtScoped.value ? snapshot.value?.statistics : globalStatistics.value)
const overview = computed(() => districtScoped.value ? snapshot.value?.overview : globalOverview.value)
const analytics = computed(() => districtScoped.value ? snapshot.value?.analytics : globalAnalytics.value)
const alertCounts = computed(() => districtScoped.value ? snapshot.value?.overview?.alertCounts : globalAlertCounts.value)
const hasSnapshot = computed(() => !!(statistics.value || analytics.value || overview.value || alerts.value || alertCounts.value))
const reportPending = computed(() => overview.value && overview.value.reportInitial + overview.value.reportFinal)
const activeAlerts = computed(() => alertCounts.value && alertCounts.value.pending + alertCounts.value.processing)
const completion = computed(() => dashboardPercent(overview.value?.hazardClosed, overview.value?.hazardTotal))
const openRiskTotal = computed(() => {
  const risk = statistics.value?.openHazards
  return risk && risk.high + risk.medium + risk.low
})
const range = ref<DashboardTrendRange>(7)
const riskByDistrict = computed(() => route.query.view === 'district')
const classificationEditor = ref<ClassificationKind>()
const districtRiskLocation = { path: dashboardPath, query: { tab: 'risk', view: 'district' } }
const systemRiskLocation = { path: dashboardPath, query: { tab: 'risk' } }
const overviewMapLocation = computed(() => ({
  ...districtRiskLocation, query: { ...districtRiskLocation.query, district: selectedDistrict.value ?? undefined },
}))
const scopedRankingLocation = computed(() => ({
  ...systemRiskLocation, query: { ...systemRiskLocation.query, district: displayedDistrict.value ?? undefined },
}))
const scopedAlertsLocation = computed(() => ({
  path: dashboardPath, query: { tab: 'alerts', district: displayedDistrict.value ?? undefined },
}))
const visibleRiskSystems = computed(() => showScopedDetail.value
  ? snapshot.value?.highRiskSystems : globalStatistics.value?.highRiskSystems)
const visibleAlerts = computed(() => showScopedDetail.value ? snapshot.value?.alerts : alerts.value)
const scopedLoading = computed(() => showScopedDetail.value ? districtDetail.loading.value : loading.value)
const metricsLoading = computed(() => districtScoped.value ? districtDetail.loading.value : loading.value)
const screenRefreshing = computed(() => loading.value || districts.loading.value || districtDetail.loading.value)
const screenFailed = computed(() => (!districtScoped.value && failure.value) || (overviewVisible.value && districts.failed.value)
  || (showScopedDetail.value && districtDetail.failed.value))

const figures = computed(() => [
  { label: '备案总数', value: dashboardNumber(overview.value?.filingTotal), tone: dashboardColors.blue,
    path: '/filings', icon: Files, note: overview.value && `本年新增 ${dashboardNumber(overview.value.filingsThisYear)}` },
  { label: '有效备案系统', value: dashboardNumber(statistics.value?.filings.active), tone: dashboardColors.teal,
    path: { path: archivePaths.systems, query: { status: 'active' } }, icon: Server,
    note: statistics.value && `涉及 ${dashboardNumber(statistics.value.filings.activeOrganizations)} 个备案单位` },
  { label: '测评待审', value: dashboardNumber(reportPending.value), tone: dashboardColors.amber,
    path: '/reports', icon: FileSearch,
    note: overview.value && `初审 ${dashboardNumber(overview.value.reportInitial)} · 终审 ${dashboardNumber(overview.value.reportFinal)}` },
  { label: '活跃预警', value: dashboardNumber(activeAlerts.value), tone: dashboardColors.red,
    path: districtScoped.value ? scopedAlertsLocation.value : alertPath, icon: BellRing,
    note: alertCounts.value && `待处理 ${dashboardNumber(alertCounts.value.pending)} · 处理中 ${dashboardNumber(alertCounts.value.processing)}` },
  { label: '未销项隐患', value: dashboardNumber(openRiskTotal.value), tone: dashboardColors.red,
    path: scopedRankingLocation.value, icon: ShieldAlert,
    note: statistics.value && `高风险 ${dashboardNumber(statistics.value.openHazards.high)} 项` },
  { label: '整改完成率', value: completion.value, tone: dashboardColors.teal, path: '/hazards', icon: CircleCheck,
    note: overview.value && `已销项 ${dashboardNumber(overview.value.hazardClosed)} / ${dashboardNumber(overview.value.hazardTotal)} 项` },
])

const filingSlices = computed<MetricSlice[]>(() => {
  const rows = statistics.value?.filings
  return rows ? [
    { label: '有效备案', count: rows.active, tone: 'blue' },
    { label: '审核中', count: rows.underReview, tone: 'teal' },
    { label: '待补正', count: rows.returned, tone: 'amber' },
    { label: '草稿', count: rows.draft, tone: 'slate' },
    { label: '已注销', count: rows.cancelled, tone: 'purple' },
  ] : []
})
const hazardSlices = computed<MetricSlice[]>(() => {
  const rows = statistics.value?.hazards
  return rows ? [
    { label: '高风险', count: rows.high, tone: 'red' },
    { label: '中风险', count: rows.medium, tone: 'amber' },
    { label: '低风险', count: rows.low, tone: 'blue' },
  ] : []
})
const openRiskSlices = computed<MetricSlice[]>(() => {
  const rows = statistics.value?.openHazards
  return rows ? [
    { label: '高风险', count: rows.high, tone: 'red' },
    { label: '中风险', count: rows.medium, tone: 'amber' },
    { label: '低风险', count: rows.low, tone: 'blue' },
  ] : []
})
const categories = computed(() => [
  { label: '备案记录', value: overview.value?.filingTotal, tone: 'blue', path: '/filings' },
  { label: '在办实施报备', value: overview.value?.implementationOpen, tone: 'teal', path: '/implementations' },
  { label: '已登记隐患', value: overview.value?.hazardTotal, tone: 'amber', path: '/hazards' },
  { label: '站内预警', value: alertCounts.value?.total, tone: 'red', path: alertPath },
] as const satisfies readonly { label: string; value?: number; tone: DashboardTone; path: string }[])
const categoryMax = computed(() => Math.max(1, ...categories.value.map(item => item.value ?? 0)))
const coverage = computed(() => statistics.value?.filingCoverage)
const coveragePath = { path: archivePaths.systems, query: { tab: 'coverage' } }
const coverageSlices = computed<MetricSlice[]>(() => {
  const current = coverage.value
  return current ? [
    { label: '已覆盖', count: current.covered, tone: 'amber' },
    { label: '未覆盖', count: current.eligible - current.covered, tone: 'slate' },
  ] : []
})
const assetCategories = computed(() => {
  const assets = statistics.value?.registeredAssets
  return [
    { label: 'IP 地址', value: assets?.ip }, { label: '域名', value: assets?.domain },
    { label: '拓扑连接', value: assets?.topology }, { label: '供应商关联', value: assets?.supplier },
    { label: '安全事件', value: assets?.securityEvent }, { label: '云平台资产', value: assets?.cloud },
    { label: '大数据资产', value: assets?.bigData },
  ]
})
const reportReview = computed(() => statistics.value?.reportFirstReview)
const statisticsLocation = computed(() => tabLocation('statistics'))
const footerFigures = computed(() => [
  { label: '在办实施报备', value: dashboardNumber(overview.value?.implementationOpen),
    path: '/implementations', icon: ClipboardPenLine },
  { label: '测评首轮通过率', value: dashboardPercent(reportReview.value?.approved, reportReview.value?.decided),
    path: statisticsLocation.value, icon: FileChartColumn },
  { label: '预警关闭率', value: dashboardPercent(alertCounts.value?.closed, alertCounts.value?.total),
    path: { path: alertPath, query: { status: 'closed' } }, icon: BellRing },
  { label: '审核已逾期', value: dashboardNumber(overview.value?.reviewDeadlines?.overdue),
    path: statisticsLocation.value, icon: CalendarClock },
  { label: '已销项隐患', value: dashboardNumber(overview.value?.hazardClosed),
    path: { path: '/hazards', query: { status: 'closed' } }, icon: CircleCheck },
  { label: '资产登记总量', value: dashboardNumber(statistics.value
    ? assetCategories.value.reduce((sum, item) => sum + (item.value ?? 0), 0) : undefined),
    path: archivePaths.systems, icon: Boxes },
])
const dutyCounts = computed(() => districtScoped.value ? snapshot.value?.counts : {
  overdue: globalOverview.value?.remediation.overdue,
  pendingReview: globalOverview.value?.remediation.pendingReview,
  pendingAlerts: alertCounts.value?.pending,
  pendingPlanReview: globalOverview.value?.remediation.pendingPlanReview,
})
const workItems = computed(() => [
  { label: '已逾期隐患', value: dutyCounts.value?.overdue, icon: CalendarClock, urgent: true,
    to: { path: '/hazards', query: { due: 'overdue' } } },
  { label: '待监管复核隐患', value: dutyCounts.value?.pendingReview, icon: ShieldCheck,
    to: { path: '/hazards', query: { status: 'pending_review' } } },
  { label: '待处理预警', value: dutyCounts.value?.pendingAlerts, icon: BellRing, urgent: true,
    to: { path: alertPath, query: { status: 'pending' } } },
  { label: '待审核整改规划', value: dutyCounts.value?.pendingPlanReview, icon: ClipboardList,
    to: { path: '/remediations', query: { status: 'pending_review' } } },
])
const flow = [
  { label: '定级备案', path: '/filings' }, { label: '实施报备', path: '/implementations' },
  { label: '测评审核', path: '/reports' }, { label: '整改规划', path: '/remediations' },
  { label: '整改闭环', path: '/hazards' }, { label: '持续监测', path: alertPath },
]
const screen = ref<HTMLElement>()
const districtRisk = ref<InstanceType<typeof DashboardDistrictRisk>>()
const fullscreenAvailable = ref(false)
const fullscreen = ref(false)
const fullscreenMessage = ref('')
const screenState = computed(() => {
  if (fullscreenMessage.value) return fullscreenMessage.value
  if (scopeChanging.value) {
    return districtDetail.failed.value ? `${targetDistrictName.value}读取失败 · 仍显示当前数据`
      : `正在切换至${targetDistrictName.value}`
  }
  if (screenRefreshing.value) return '正在更新'
  if (screenFailed.value) return hasSnapshot.value ? '部分数据刷新失败' : '暂时无法读取数据'
  return '每 30 秒更新'
})

/**
 * 用途 在驾驶舱内切换视图时携带当前区县范围。
 * 参数 key 为目标驾驶舱分区。
 * 返回值 同源驾驶舱路由。
 */
function tabLocation(key: keyof typeof tabs) {
  return { path: dashboardPath,
    query: { tab: key === 'overview' ? undefined : key, district: selectedDistrict.value ?? undefined } }
}

/**
 * 用途 在当前驾驶舱视图设置范围，切换区县时统一更新整屏数据。
 * 参数 district 为空字符串时选择未归区，null 时恢复全域。
 * 返回值 范围改变时返回路由切换任务。
 */
function selectDistrict(district: string | null) {
  if (fixedDistrict || district === selectedDistrict.value) return
  if (area && district !== null && !area.districts.includes(district)) return
  return router.replace({ query: { ...route.query, district: district ?? undefined } })
}

/**
 * 用途 判断指标可否在当前范围内安全地跳转。
 * 参数 to 为指标原来的目标路由。
 * 返回值 目标能保持区县范围时为真。
 */
function canOpenMetric(to: RouteLocationRaw) {
  return !districtScoped.value || (typeof to === 'object' && 'path' in to && to.path === dashboardPath)
}

/**
 * 用途 刷新驾驶舱当前的数据源及正在查看的区县风险。
 * 参数 无。
 * 返回值 本次刷新任务。
 */
async function refreshScreen() {
  await Promise.all([refresh(), districtRisk.value?.refresh(), districts.refresh(), districtDetail.refresh()])
}

/**
 * 用途 根据浏览器全屏事件同步大屏按钮及退出状态。
 * 参数 无。
 * 返回值 无。
 */
function syncFullscreen() {
  fullscreen.value = !!screen.value && document.fullscreenElement === screen.value
}

/**
 * 用途 在用户触发时进入或退出当前驾驶舱的浏览器全屏。
 * 参数 无。
 * 返回值 浏览器全屏请求任务。
 */
async function toggleFullscreen() {
  if (!screen.value || !fullscreenAvailable.value) return
  fullscreenMessage.value = ''
  try {
    if (document.fullscreenElement === screen.value) await document.exitFullscreen()
    else await screen.value.requestFullscreen()
  } catch {
    fullscreenMessage.value = '暂时无法切换全屏，请检查浏览器设置。'
  }
}

/**
 * 用途 在现有驾驶舱全屏时响应 Esc，保留打开的业务弹窗对按键的处理。
 * 参数 event 为当前页的键盘事件。
 * 返回值 无。
 */
function onFullscreenEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.defaultPrevented || !screen.value
    || document.fullscreenElement !== screen.value || screen.value.querySelector('dialog[open]')) return
  event.preventDefault()
  void toggleFullscreen()
}

/**
 * 用途 关闭从业务页打开的驾驶舱，直接访问或浏览器拦截时返回工作台。
 * 参数 无，依据当前标签页的打开方式。
 * 返回值 无。
 */
function returnFromDashboard() {
  if (!standalone.value) {
    void router.replace(session.value?.home ?? '/')
    return
  }
  window.close()
  if (!window.closed && returnTimer === undefined) {
    returnTimer = window.setTimeout(() => { void router.replace(session.value?.home ?? '/') }, 150)
  }
}

/**
 * 用途 立即隐藏原会话的驾驶舱，再由路由按当前会话重新进入。
 * 参数 无。
 * 返回值 无。
 */
function reloadDashboardSession() {
  if (!sessionAvailable.value) return
  sessionAvailable.value = false
  window.location.reload()
}

watch(session, actor => {
  if (actor?.id !== openedSession?.id || actor?.areaCode !== openedSession?.areaCode ||
    actor?.roleCode !== openedSession?.roleCode || actor?.mustChangePassword !== openedSession?.mustChangePassword) {
    reloadDashboardSession()
  }
}, { flush: 'sync' })

/**
 * 用途 核对独立标签页原账号是否仍有权展示驾驶舱。
 * 参数 无。
 * 返回值 本次会话校验完成的任务。
 */
async function verifyDashboardSession() {
  if (!sessionAvailable.value || sessionCheckPending.value) return
  sessionCheckPending.value = true
  try {
    await refreshSession()
  } catch {
    // 临时网络故障由驾驶舱现有的刷新提示显示。
  } finally {
    sessionCheckPending.value = false
  }
}

/**
 * 用途 接收同源标签页登录、注销或改密后的会话变更信号。
 * 参数 event 为浏览器共享存储的键变更。
 * 返回值 无。
 */
function onSessionStorage(event: StorageEvent) {
  if (event.key === sessionChangeKey) reloadDashboardSession()
}

refreshBusinessOnFocus(() => void verifyDashboardSession(), 30_000)
onMounted(() => {
  window.addEventListener('storage', onSessionStorage)
  window.addEventListener(dashboardAccessEvent, verifyDashboardSession)
  try {
    if (standalone.value) window.sessionStorage.setItem(tabSessionKey, '1')
    else standalone.value = window.sessionStorage.getItem(tabSessionKey) === '1'
  } catch {
    // 受限存储环境仍可按入口地址识别当前标签页。
  }
  fullscreenAvailable.value = document.fullscreenEnabled && typeof screen.value?.requestFullscreen === 'function'
  document.addEventListener('fullscreenchange', syncFullscreen)
  document.addEventListener('keydown', onFullscreenEscape)
})
onScopeDispose(() => {
  window.removeEventListener('storage', onSessionStorage)
  window.removeEventListener(dashboardAccessEvent, verifyDashboardSession)
  window.clearTimeout(returnTimer)
  document.removeEventListener('fullscreenchange', syncFullscreen)
  document.removeEventListener('keydown', onFullscreenEscape)
})
</script>

<template>
  <section v-if="sessionAvailable" ref="screen" class="command-screen" aria-label="指挥驾驶舱">
    <div class="screen-circuit-rail" aria-hidden="true"></div>
    <div class="screen-circuit-rail screen-circuit-rail-end" aria-hidden="true"></div>
    <main class="command-stage">
      <header class="screen-header">
        <div class="screen-title">
          <span class="screen-title-mark" aria-hidden="true"><ShieldCheck class="title-icon" /></span>
          <div>
            <h1>网络安全等级保护<span>指挥驾驶舱</span></h1>
            <p>浙江省衢州市 · {{ displayedDistrict === null && area ? '市域' : displayedDistrictName || '全域' }}</p>
          </div>
        </div>
        <nav class="screen-tabs" aria-label="驾驶舱分区">
          <RouterLink v-for="(item, key) in tabs" :key="key"
            :to="tabLocation(key)"
            :class="{ selected: tab === key }" :aria-current="tab === key ? 'page' : undefined">
            <component :is="item.icon" aria-hidden="true" />{{ item.label }}
          </RouterLink>
        </nav>
        <div class="screen-meta">
          <div class="screen-status">
            <p class="screen-state" role="status" :class="{ 'has-warning': screenFailed || fullscreenMessage,
              'is-switching': scopeChanging && !districtDetail.failed.value }">
              {{ screenState }}
            </p>
            <p v-if="statistics?.updatedAt" class="screen-snapshot">
              更新于 <time :datetime="statistics.updatedAt">{{ dashboardTime(statistics.updatedAt) }}</time>
            </p>
          </div>
          <div class="screen-actions" aria-label="驾驶舱控制">
            <button class="screen-button" type="button" :disabled="screenRefreshing" title="刷新" aria-label="刷新"
              @click="refreshScreen">
              <RefreshCw :class="{ spinning: screenRefreshing }" aria-hidden="true" />
            </button>
            <button class="screen-button fullscreen-button" type="button" :disabled="!fullscreenAvailable"
              :title="!fullscreenAvailable ? '当前浏览器不可用全屏' : fullscreen ? '退出全屏' : '全屏'"
              :aria-label="fullscreen ? '退出全屏' : '全屏'" @click="toggleFullscreen">
              <Minimize v-if="fullscreen" aria-hidden="true" /><Maximize v-else aria-hidden="true" />
            </button>
            <button class="screen-button back-button" type="button" :title="returnLabel" :aria-label="returnLabel"
              @click="returnFromDashboard">
              <ArrowLeft aria-hidden="true" />
            </button>
          </div>
        </div>
      </header>

      <div v-if="tab === 'overview'" class="screen-pane pane-overview">
        <div class="screen-kpis" aria-label="关键指标">
          <component :is="canOpenMetric(figure.path) ? RouterLink : 'article'" v-for="figure in figures"
            :key="figure.label" :to="canOpenMetric(figure.path) ? figure.path : undefined" class="screen-kpi"
            :style="{ '--kpi-tone': figure.tone }">
            <span class="kpi-icon"><component :is="figure.icon" aria-hidden="true" /></span>
            <div class="kpi-reading">
              <span class="kpi-label">{{ figure.label }}</span>
              <strong>{{ figure.value }}</strong>
              <small v-if="figure.note">{{ figure.note }}</small>
            </div>
          </component>
        </div>

        <div class="overview-grid">
          <div class="screen-column layout-left">
            <section class="screen-card coverage-card" aria-labelledby="overview-coverage">
              <header class="card-heading">
                <h2 id="overview-coverage">备案覆盖</h2>
                <RouterLink v-if="!districtScoped" class="screen-link" :to="coveragePath">
                  覆盖清册<ArrowRight aria-hidden="true" />
                </RouterLink>
              </header>
              <DashboardDistribution :slices="coverageSlices" label="覆盖率" :loading="metricsLoading"
                :center="dashboardPercent(coverage?.covered, coverage?.eligible)" />
              <p v-if="coverage && !coverage.eligible" class="screen-caption">暂无应备案对象登记</p>
            </section>
            <section class="screen-card trend-card" aria-label="业务趋势">
              <DashboardTrend v-model="range" :history="statistics?.trend" :loading="metricsLoading" />
            </section>
            <section class="screen-card" aria-labelledby="dashboard-counts">
              <header class="card-heading"><h2 id="dashboard-counts">业务分类统计</h2></header>
              <div class="category-list">
                <component :is="districtScoped ? 'div' : RouterLink" v-for="item in categories" :key="item.label"
                  :to="districtScoped ? undefined : item.path" class="category-row">
                  <span>{{ item.label }}</span><strong>{{ dashboardNumber(item.value) }}</strong>
                  <span class="category-track" aria-hidden="true"><span :style="{
                    width: `${(item.value ?? 0) * 100 / categoryMax}%`, background: dashboardColors[item.tone],
                  }"></span></span>
                </component>
              </div>
            </section>
          </div>

          <div class="screen-column layout-center">
            <section class="screen-card district-feature" aria-labelledby="dashboard-risk">
              <header class="card-heading">
                <h2 id="dashboard-risk"><MapPinned aria-hidden="true" />区县处置态势</h2>
                <span class="screen-caption">有效备案系统</span>
              </header>
              <DashboardDistrictResponse :summary="districts.summary.value" :selected="selectedDistrict"
                :detail="snapshot" :loading-summary="districts.loading.value"
                :failed-summary="districts.failed.value" :loading-detail="districtDetail.loading.value"
                :failed-detail="districtDetail.failed.value" :map-settings="overviewMapLocation"
                :area="area" :locked="Boolean(fixedDistrict)" @select="selectDistrict"
                @retry-summary="districts.refresh" @retry-detail="districtDetail.refresh" />
            </section>
            <div class="overview-lists">
              <section class="screen-card" aria-labelledby="dashboard-systems">
                <header class="card-heading">
                  <h2 id="dashboard-systems">高风险系统</h2>
                  <RouterLink class="screen-link" :to="scopedRankingLocation">查看排行<ArrowRight aria-hidden="true" /></RouterLink>
                </header>
                <p v-if="displayedDistrictName" class="screen-caption">当前范围 · {{ displayedDistrictName }}</p>
                <DashboardRanking :key="JSON.stringify(displayedDistrict)" :systems="visibleRiskSystems"
                  :loading="scopedLoading" :filing-links="Boolean(area)" compact />
              </section>
              <section class="screen-card" aria-labelledby="dashboard-feed">
                <header class="card-heading">
                  <h2 id="dashboard-feed">最新预警</h2>
                  <RouterLink class="screen-link" :to="scopedAlertsLocation">
                    查看全部<ArrowRight aria-hidden="true" />
                  </RouterLink>
                </header>
                <p v-if="displayedDistrictName" class="screen-caption">当前范围 · {{ displayedDistrictName }}</p>
                <DashboardAlerts :key="JSON.stringify(displayedDistrict)" :items="visibleAlerts"
                  :loading="scopedLoading" :read-only="Boolean(area)" compact highlight-new />
              </section>
            </div>
          </div>

          <div class="screen-column layout-right">
            <section class="screen-card duty-card" aria-labelledby="dashboard-deadlines"
              :aria-busy="districtScoped && districtDetail.loading.value">
              <header class="card-heading">
                <h2 id="dashboard-deadlines">监管待办</h2>
                <span class="screen-caption">{{ displayedDistrictName || '全域' }}</span>
              </header>
              <div class="count-list">
                <component :is="districtScoped ? 'div' : RouterLink" v-for="task in workItems" :key="task.label"
                  class="duty-item" :to="districtScoped ? undefined : task.to"
                  :class="{ 'duty-urgent': task.urgent && (task.value ?? 0) > 0 }">
                  <component :is="task.icon" aria-hidden="true" />
                  <span>{{ task.label }}</span><strong>{{ dashboardNumber(task.value) }}</strong>
                  <ArrowRight v-if="!districtScoped" class="duty-arrow" aria-hidden="true" />
                </component>
              </div>
            </section>
            <section class="screen-card" aria-labelledby="filing-distribution">
              <header class="card-heading">
                <h2 id="filing-distribution">备案状态分布</h2>
                <RouterLink v-if="!districtScoped" class="screen-link" to="/filings">
                  备案台账<ArrowRight aria-hidden="true" />
                </RouterLink>
              </header>
              <DashboardDistribution :slices="filingSlices" label="备案记录" :loading="metricsLoading" />
            </section>
            <section class="screen-card" aria-labelledby="overview-open">
              <header class="card-heading">
                <h2 id="overview-open">未销项风险分布</h2>
                <RouterLink class="screen-link" :to="scopedRankingLocation">
                  风险分析<ArrowRight aria-hidden="true" />
                </RouterLink>
              </header>
              <DashboardDistribution :slices="openRiskSlices" label="未销项隐患" :loading="metricsLoading" />
            </section>
          </div>
        </div>

        <div class="overview-footer" aria-label="监管业务指标">
          <component :is="canOpenMetric(item.path) ? RouterLink : 'div'" v-for="item in footerFigures"
            :key="item.label" :to="canOpenMetric(item.path) ? item.path : undefined" class="footer-kpi">
            <component :is="item.icon" aria-hidden="true" />
            <div><span>{{ item.label }}</span><strong>{{ item.value }}</strong></div>
          </component>
        </div>
      </div>

      <div v-else-if="tab === 'risk'" class="screen-pane detail-grid">
        <section class="screen-card" aria-labelledby="high-risk-systems">
          <header class="card-heading">
            <h2 id="high-risk-systems">{{ riskByDistrict ? '区县风险热力' : '高风险隐患系统' }}</h2>
            <RouterLink class="screen-link district-switch"
              :to="riskByDistrict ? scopedRankingLocation : overviewMapLocation">
              {{ riskByDistrict ? '按系统查看' : '按区县查看' }}
            </RouterLink>
          </header>
          <div class="scoped-panel-heading">
            <span class="screen-caption">{{ riskByDistrict ? '有效备案系统未销项隐患'
              : (displayedDistrictName ? displayedDistrictName + ' · ' : '') + '未销项高风险隐患数 · 前 10' }}</span>
            <RouterLink v-if="displayedDistrict !== null && !fixedDistrict" class="screen-link"
              :to="systemRiskLocation">{{ area ? '查看市域' : '查看全域' }}</RouterLink>
          </div>
          <DashboardDistrictRisk v-if="riskByDistrict" ref="districtRisk"
            :area="area" :locked="Boolean(fixedDistrict)" />
          <DashboardRanking v-else :key="JSON.stringify(displayedDistrict)" :systems="visibleRiskSystems"
            :loading="scopedLoading" :filing-links="Boolean(area)" />
        </section>
        <div class="screen-column layout-detail">
          <section class="screen-card risk-feature" aria-labelledby="open-hazards">
            <header class="card-heading">
              <h2 id="open-hazards">未销项风险分布</h2>
              <RouterLink v-if="!districtScoped" class="screen-link" to="/hazards">
                隐患台账<ArrowRight aria-hidden="true" />
              </RouterLink>
            </header>
            <DashboardRisk :risks="statistics?.openHazards" :loading="metricsLoading" />
          </section>
          <section class="screen-card" aria-labelledby="hazard-distribution">
            <header class="card-heading"><h2 id="hazard-distribution">全部隐患风险等级</h2></header>
            <DashboardDistribution :slices="hazardSlices" label="隐患记录" :loading="metricsLoading" />
          </section>
          <section class="screen-card" aria-labelledby="common-issues">
            <header class="card-heading">
              <h2 id="common-issues">常见问题 TOP 10</h2>
              <button v-if="!districtScoped" class="screen-link district-switch" type="button"
                @click="classificationEditor = 'issue'">维护分类</button>
            </header>
            <DashboardAnalytics :issues="analytics?.commonIssues" :loading="metricsLoading" />
          </section>
        </div>
      </div>

      <div v-else-if="tab === 'alerts'" class="screen-pane stacked-pane">
        <div class="screen-stats" aria-label="预警状态">
          <component :is="districtScoped ? 'article' : RouterLink" v-for="(label, status) in alertStatuses"
            :key="status" class="screen-card screen-stat"
            :to="districtScoped ? undefined : { path: alertPath, query: { status } }">
            <span>{{ label }}预警</span><strong>{{ dashboardNumber(alertCounts?.[status as AlertStatus]) }}</strong>
          </component>
        </div>
        <section class="screen-card" aria-labelledby="live-alerts">
          <header class="card-heading">
            <h2 id="live-alerts">实时预警</h2>
            <span v-if="alertsUpdatedAt && !displayedDistrictName" class="screen-caption">
              更新于 <time :datetime="alertsUpdatedAt">{{ dashboardTime(alertsUpdatedAt) }}</time>
            </span>
            <RouterLink v-if="!districtScoped" class="screen-link" :to="alertPath">
              预警中心<ArrowRight aria-hidden="true" />
            </RouterLink>
            <button v-else-if="displayedDistrict !== null && !fixedDistrict" class="screen-link district-switch"
              type="button" @click="selectDistrict(null)">{{ area ? '查看市域' : '查看全域' }}</button>
          </header>
          <DashboardAlerts :key="JSON.stringify(displayedDistrict)" :items="visibleAlerts"
            :loading="scopedLoading" :read-only="Boolean(area)" />
        </section>
      </div>

      <div v-else class="screen-pane stacked-pane statistics-pane">
        <div class="screen-stats" aria-label="安全状况分析">
          <article class="screen-card screen-stat">
            <span>测评首轮通过率</span><strong>{{ dashboardPercent(reportReview?.approved, reportReview?.decided) }}</strong>
            <small v-if="reportReview">首轮通过 {{ dashboardNumber(reportReview.approved) }} / 已有首轮结论 {{ dashboardNumber(reportReview.decided) }}</small>
          </article>
          <article class="screen-card screen-stat">
            <span>预警关闭率</span><strong>{{ dashboardPercent(alertCounts?.closed, alertCounts?.total) }}</strong>
            <small v-if="alertCounts">已关闭 {{ dashboardNumber(alertCounts.closed) }} / {{ dashboardNumber(alertCounts.total) }} 条</small>
          </article>
          <article class="screen-card screen-stat">
            <span><CalendarClock aria-hidden="true" />审核已逾期</span><strong>{{ dashboardNumber(overview?.reviewDeadlines?.overdue) }}</strong>
            <small v-if="overview">{{ overview.reviewDeadlines ? '按备案和测评审核期限统计' : '未配置审核时限' }}</small>
          </article>
        </div>
        <div class="coverage-detail-grid">
          <section class="screen-card" aria-labelledby="dashboard-industry">
            <header class="card-heading">
              <h2 id="dashboard-industry">行业分析</h2>
              <button v-if="!districtScoped" class="screen-link district-switch" type="button"
                @click="classificationEditor = 'industry'">维护行业</button>
            </header>
            <DashboardAnalytics :key="JSON.stringify(displayedDistrict)" :industry="analytics?.industries"
              :loading="metricsLoading" />
          </section>
          <section class="screen-card" aria-labelledby="district-coverage">
            <header class="card-heading">
              <h2 id="district-coverage">区县备案覆盖</h2>
              <RouterLink v-if="!districtScoped" class="screen-link" :to="coveragePath">
                查看覆盖清册<ArrowRight aria-hidden="true" />
              </RouterLink>
            </header>
            <p class="screen-caption">{{ displayedDistrictName || '按已登记应备案对象数展示前 20 个区县' }}</p>
            <div v-if="statistics?.filingCoverageByDistrict?.length" :key="JSON.stringify(displayedDistrict)"
              v-dashboard-scroll class="coverage-district-list" tabindex="0">
              <ul class="coverage-district-rows" data-dashboard-loop-original aria-label="区县备案覆盖明细">
                <li v-for="item in statistics.filingCoverageByDistrict" :key="item.district">
                  <span>{{ item.district }}</span>
                  <span>{{ dashboardNumber(item.covered) }} / {{ dashboardNumber(item.eligible) }}</span>
                  <strong>{{ dashboardPercent(item.covered, item.eligible) }}</strong>
                </li>
              </ul>
              <ul class="coverage-district-rows" data-dashboard-loop-copy aria-hidden="true" hidden></ul>
            </div>
            <p v-else class="screen-caption">{{ metricsLoading ? '正在读取覆盖数据…'
              : statistics ? '暂无覆盖范围登记' : '暂无可用数据' }}</p>
          </section>
          <section class="screen-card" aria-labelledby="dashboard-inventory">
            <header class="card-heading">
              <h2 id="dashboard-inventory">有效备案系统资产登记</h2>
              <RouterLink v-if="!districtScoped" class="screen-link" :to="archivePaths.systems">
                系统档案<ArrowRight aria-hidden="true" />
              </RouterLink>
            </header>
            <dl class="dashboard-asset-types">
              <div v-for="item in assetCategories" :key="item.label">
                <dt>{{ item.label }}</dt><dd>{{ dashboardNumber(item.value) }}</dd>
              </div>
            </dl>
          </section>
          <section class="screen-card trend-card" aria-label="业务趋势">
            <DashboardTrend v-model="range" :history="statistics?.trend" :loading="metricsLoading" />
          </section>
        </div>
        <section v-if="!districtScoped" class="screen-card" aria-labelledby="dashboard-flow">
          <header class="card-heading"><h2 id="dashboard-flow">监管业务流程</h2></header>
          <ol class="screen-flow">
            <li v-for="(step, index) in flow" :key="step.path">
              <RouterLink :to="step.path">{{ step.label }}</RouterLink>
              <ArrowRight v-if="index < flow.length - 1" aria-hidden="true" />
            </li>
          </ol>
        </section>
      </div>
      <CopyrightFooter class="dashboard-copyright" />
      <DashboardClassificationEditor v-if="classificationEditor" :key="classificationEditor" :kind="classificationEditor"
        @close="classificationEditor = undefined" @updated="refresh" />
    </main>
  </section>
</template>

<style scoped src="./commandDashboard.css"></style>

<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import {
  Activity, ArrowLeft, ArrowRight, BellRing, Boxes, CalendarClock, ChartNoAxesCombined, CircleCheck,
  ClipboardList, ClipboardPenLine, FileChartColumn, FileSearch, Files, Maximize, Minimize, RefreshCw,
  Server, ShieldAlert, ShieldCheck,
} from '@lucide/vue'
import { RouterLink, useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import DashboardAlerts from '../components/DashboardAlerts.vue'
import DashboardDistribution from '../components/DashboardDistribution.vue'
import DashboardDistrictResponse from '../components/DashboardDistrictResponse.vue'
import DashboardScene from '../components/DashboardScene.vue'
import DashboardTrend from '../components/DashboardTrend.vue'
import { alertPath, alertStatuses, type AlertStatus } from '../lib/alerts'
import { dashboardAccessEvent, sessionChangeKey } from '../lib/api'
import { archivePaths } from '../lib/archives'
import {
  dashboardNumber, dashboardPath, dashboardPercent, dashboardTabFlag, dashboardTime, useDashboard,
  type DashboardComparison, type DashboardMetricDelta,
} from '../lib/dashboard'
import {
  completionChip, completionYearChip, periodChip, withWindowRange, yearChip,
  type DeltaChip,
} from '../lib/dashboardComparison'
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
const districtScopeName = computed(() => {
  if (fixedDistrict) return fixedDistrict
  if (selectedDistrict.value !== null) return selectedDistrict.value || '未归区'
  return area?.name ?? '全域'
})
const statistics = computed(() => snapshot.value?.statistics ?? globalStatistics.value)
const overview = computed(() => snapshot.value?.overview ?? globalOverview.value)
const analytics = computed(() => snapshot.value?.analytics ?? globalAnalytics.value)
const alertCounts = computed(() => snapshot.value?.overview?.alertCounts ?? globalAlertCounts.value)
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
  ? snapshot.value?.highRiskSystems ?? globalStatistics.value?.highRiskSystems
  : globalStatistics.value?.highRiskSystems)
const visibleAlerts = computed(() => showScopedDetail.value ? snapshot.value?.alerts : alerts.value)
const scopedLoading = computed(() => showScopedDetail.value ? districtDetail.loading.value : loading.value)
const metricsLoading = computed(() => snapshot.value?.statistics ? districtDetail.loading.value : loading.value)
const screenRefreshing = computed(() => loading.value || districts.loading.value || districtDetail.loading.value)
const screenFailed = computed(() => (!snapshot.value?.statistics && failure.value) || (overviewVisible.value && districts.failed.value)
  || (showScopedDetail.value && districtDetail.failed.value))

const comparison = computed<DashboardComparison | undefined>(() => snapshot.value?.comparison)

/** 当前范围的综合风险指数。 */
const riskIndex = computed(() => snapshot.value?.risk)
const riskLevel = computed(() => riskIndex.value?.level ?? 'none')
/** 当前范围的待办汇总。 */
const dutySummary = computed(() => snapshot.value?.duty)
const displayedDistrict = computed(() => snapshot.value?.district ?? selectedDistrict.value)

const metrics = computed<readonly {
  label: string; value: string; tone: DashboardTone; note: string
  period: DeltaChip; year: DeltaChip
}[]>(() => {
  const windowDays = comparison.value?.windowDays ?? 30
  const hasYearAgo = comparison.value?.hasYearAgo ?? false
  return [
    {
      label: '综合风险指数', value: riskIndex.value ? String(riskIndex.value.index) : '—',
      tone: riskLevel.value === 'high' ? 'red' : riskLevel.value === 'mid' ? 'amber' : 'blue',
      note: riskIndex.value?.weights ?? '逾期 40% / 高风险 40% / 待复测 20%',
      period: withWindowRange(periodChip(comparison.value?.riskIndex), windowDays),
      year: yearChip(comparison.value?.riskIndex, hasYearAgo),
    },
    {
      label: '未销项隐患', value: dashboardNumber(openRiskTotal.value), tone: 'red',
      note: statistics.value ? `高 ${dashboardNumber(statistics.value.openHazards.high)} · 中 ${dashboardNumber(statistics.value.openHazards.medium)} · 低 ${dashboardNumber(statistics.value.openHazards.low)}` : '',
      period: withWindowRange(periodChip(comparison.value?.openHazards), windowDays),
      year: yearChip(comparison.value?.openHazards, hasYearAgo),
    },
    {
      label: '整改完成率', value: completion.value, tone: 'teal',
      note: overview.value ? `已销项 ${dashboardNumber(overview.value.hazardClosed)} / ${dashboardNumber(overview.value.hazardTotal)}` : '',
      period: withWindowRange(completionChip(comparison.value?.completionRate), windowDays),
      year: completionYearChip(comparison.value?.completionRate, hasYearAgo),
    },
    {
      label: '活跃预警', value: dashboardNumber(activeAlerts.value), tone: 'amber',
      note: alertCounts.value ? `待处理 ${dashboardNumber(alertCounts.value.pending)} · 处理中 ${dashboardNumber(alertCounts.value.processing)}` : '',
      period: withWindowRange(periodChip(comparison.value?.activeAlerts), windowDays),
      year: yearChip(comparison.value?.activeAlerts, hasYearAgo),
    },
  ]
})

/** 业务量的同比环比，展示在趋势卡上方。 */
const trendComparison = computed<readonly { label: string; period: DeltaChip; year: DeltaChip }[]>(() => {
  const windowDays = comparison.value?.windowDays ?? 30
  const hasYearAgo = comparison.value?.hasYearAgo ?? false
  const build = (label: string, delta: DashboardMetricDelta | undefined) => ({
    label,
    period: withWindowRange(periodChip(delta, true), windowDays),
    year: yearChip(delta, hasYearAgo),
  })
  return [
    build('新增备案', comparison.value?.filingsCreated),
    build('触发预警', comparison.value?.alertsTriggered),
    build('隐患销项', comparison.value?.hazardsClosed),
  ]
})

const categories = computed<readonly { label: string; value?: number; tone: DashboardTone; path: string }[]>(() => [
  { label: '备案记录', value: overview.value?.filingTotal, tone: 'blue', path: '/filings' },
  { label: '在办实施报备', value: overview.value?.implementationOpen, tone: 'teal', path: '/implementations' },
  { label: '已登记隐患', value: overview.value?.hazardTotal, tone: 'amber', path: '/hazards' },
  { label: '站内预警', value: alertCounts.value?.total, tone: 'red', path: alertPath },
])
const categoryMax = computed(() => Math.max(1, ...categories.value.map(item => item.value ?? 0)))

const filingSlices = computed<MetricSlice[]>(() => {
  const rows = statistics.value?.filings
  return rows ? [
    { label: '有效备案', count: rows.active, tone: 'blue', color: '#58afff' },
    { label: '审核中', count: rows.underReview, tone: 'blue', color: '#3d8fc9' },
    { label: '待补正', count: rows.returned, tone: 'blue', color: '#2f6fa3' },
    { label: '草稿', count: rows.draft, tone: 'blue', color: '#27567f' },
    { label: '已注销', count: rows.cancelled, tone: 'slate', color: '#4a5f78' },
  ] : []
})
const openRiskSlices = computed<MetricSlice[]>(() => {
  const rows = statistics.value?.openHazards
  return rows ? [
    { label: '高风险', count: rows.high, tone: 'red', color: dashboardColors.red },
    { label: '中风险', count: rows.medium, tone: 'amber', color: dashboardColors.amber },
    { label: '低风险', count: rows.low, tone: 'blue', color: dashboardColors.blue },
  ] : []
})
const coverage = computed(() => statistics.value?.filingCoverage)
const coveragePath = { path: archivePaths.systems, query: { tab: 'coverage' } }
const coverageSlices = computed<MetricSlice[]>(() => {
  const current = coverage.value
  return current ? [
    { label: '已覆盖', count: current.covered, tone: 'blue', color: dashboardColors.blue },
    { label: '未覆盖', count: current.eligible - current.covered, tone: 'slate', color: '#33546f' },
  ] : []
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
    ? statistics.value.registeredAssets.ip + statistics.value.registeredAssets.domain
      + statistics.value.registeredAssets.topology + statistics.value.registeredAssets.supplier
      + statistics.value.registeredAssets.securityEvent + statistics.value.registeredAssets.cloud
      + statistics.value.registeredAssets.bigData
    : undefined),
    path: archivePaths.systems, icon: Boxes },
])
const dutyCounts = computed(() => snapshot.value?.counts ? {
  overdue: snapshot.value.counts.overdue,
  pendingReview: snapshot.value.counts.pendingReview,
  pendingAlerts: snapshot.value.counts.pendingAlerts,
  pendingPlanReview: snapshot.value.counts.pendingPlanReview,
} : {
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
  { label: '待处理预警', value: dutyCounts.value?.pendingAlerts, icon: BellRing,
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
const districtRisk = ref<InstanceType<typeof DashboardDistrictResponse>>()
const fullscreenAvailable = ref(false)
const fullscreen = ref(false)
const fullscreenMessage = ref('')
const screenState = computed(() => {
  if (fullscreenMessage.value) return fullscreenMessage.value
  if (districtDetail.loading.value) return `正在切换至${districtScopeName.value}`
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
 * 用途 刷新驾驶舱当前的数据源及正在查看的区县风险。
 * 参数 无。
 * 返回值 本次刷新任务。
 */
async function refreshScreen() {
  await Promise.all([refresh(), districts.refresh(), districtDetail.refresh()])
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
    <DashboardScene />

    <div class="command-stage">
      <header class="screen-header">
        <div class="brand">
          <span class="brand-mark" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z"/><path d="m9 12 2 2 4-4"/></svg>
          </span>
          <span class="brand-text">
            <strong>网络安全等级保护平台</strong>
            <span>CYBERSECURITY LEVEL PROTECTION PLATFORM</span>
          </span>
        </div>

        <div class="screen-title">
          <div class="title-main">
            <span class="title-ring" aria-hidden="true">
              <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z"/><path d="m9 12 2 2 4-4"/></svg>
            </span>
            <h1>网络安全等级保护<span>指挥驾驶舱</span></h1>
          </div>
          <p class="title-sub">浙江省衢州市 · {{ districtScopeName }}</p>
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
            <p class="screen-state" role="status" :class="{ 'has-warning': screenFailed || fullscreenMessage }">
              {{ screenState }}
            </p>
            <p v-if="statistics?.updatedAt" class="screen-snapshot">
              统计截至 <time :datetime="statistics.updatedAt">{{ dashboardTime(statistics.updatedAt) }}</time>
              · 全市 6 区县
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

      <section v-if="tab === 'overview'" class="screen-pane pane-overview">
        <section class="conclusion-bar" aria-label="当前范围态势结论">
          <span class="conclusion-flag">
            <ShieldAlert aria-hidden="true" />
            {{ districtScopeName }}待办 {{ dashboardNumber(dutySummary?.total ?? 0) }} 项
          </span>
          <p v-if="dutySummary">
            其中 <em>{{ dashboardNumber(dutySummary.overdueHazards) }} 项已逾期</em>、
            <em>{{ dashboardNumber(dutySummary.pendingAlerts) }} 项活跃预警</em>待处置；
            <b>{{ dutySummary.topDistrict || '当前范围' }}</b> 风险指数最高（{{ dashboardNumber(dutySummary.topDistrictIndex) }}）。
          </p>
          <p v-else>正在读取当前范围的待办汇总…</p>
          <div class="conclusion-worst">
            <span>综合风险指数</span><b>{{ dashboardNumber(riskIndex?.index) }}</b>
            <span class="conclusion-level">{{ riskIndex?.level === 'high' ? '高' : riskIndex?.level === 'mid' ? '中' : riskIndex?.level === 'low' ? '低' : '无' }}</span>
          </div>
        </section>

        <div class="overview-grid">
          <div class="screen-column layout-left">
            <section class="screen-card metrics-card">
              <header class="card-heading"><h2>核心指标</h2>
                <span class="screen-caption">{{ districtScopeName }} · 对比窗口 {{ comparison?.windowDays ?? 30 }} 日</span>
              </header>
              <div class="metric-grid">
                <div v-for="item in metrics" :key="item.label" class="metric-card"
                  :style="{ '--metric-tone': dashboardColors[item.tone] }">
                  <span class="metric-head">
                    <span class="metric-label">{{ item.label }}</span>
                    <span class="delta-chip" :class="item.year.tone" :title="item.year.title">{{ item.year.text }}</span>
                  </span>
                  <span class="metric-row">
                    <strong class="metric-value">{{ item.value }}</strong>
                    <span class="delta-chip" :class="item.period.tone" :title="item.period.title">{{ item.period.text }}</span>
                  </span>
                  <small v-if="item.note">{{ item.note }}</small>
                </div>
              </div>
            </section>

            <section class="screen-card category-card">
              <header class="card-heading"><h2>业务分类统计</h2><span class="screen-caption">{{ districtScopeName }}</span></header>
              <div class="category-list">
                <div v-for="item in categories" :key="item.label" class="category-row">
                  <span class="category-label">{{ item.label }}</span>
                  <span class="category-value">{{ dashboardNumber(item.value) }}</span>
                  <span class="category-track" aria-hidden="true">
                    <i :style="{ width: `${Math.max(4, (item.value ?? 0) * 100 / categoryMax)}%`, background: dashboardColors[item.tone] }" />
                  </span>
                </div>
              </div>
            </section>

            <section class="screen-card trend-card">
              <header class="card-heading"><h2>业务趋势</h2>
                <span class="screen-caption">按日统计实际业务量</span>
              </header>
              <div class="trend-compare">
                <span v-for="item in trendComparison" :key="item.label" class="trend-compare-item">
                  <span class="trend-compare-label">{{ item.label }}</span>
                  <span class="delta-chip" :class="item.period.tone" :title="item.period.title">{{ item.period.text }}</span>
                  <span class="delta-chip" :class="item.year.tone" :title="item.year.title">{{ item.year.text }}</span>
                </span>
              </div>
              <DashboardTrend v-model="range" :history="statistics?.trend" :loading="metricsLoading" />
            </section>
          </div>

          <div class="screen-column layout-center">
            <section class="screen-card map-card">
              <header class="card-heading">
                <h2>区县处置态势</h2>
                <span class="screen-caption">综合风险指数 · 逾期 40% / 高风险 40% / 待复测 20%</span>
              </header>
              <DashboardDistrictResponse :summary="districts.summary.value" :selected="selectedDistrict"
                :detail="snapshot" :loading-summary="districts.loading.value"
                :failed-summary="districts.failed.value" :loading-detail="districtDetail.loading.value"
                :failed-detail="districtDetail.failed.value" :map-settings="overviewMapLocation"
                :area="area" :locked="Boolean(fixedDistrict)" @select="selectDistrict"
                @retry-summary="districts.refresh" @retry-detail="districtDetail.refresh" />
            </section>

            <div class="overview-lists">
              <section class="screen-card">
                <header class="card-heading"><h2>高风险系统</h2>
                  <RouterLink class="screen-link" :to="scopedRankingLocation">查看排行<ArrowRight aria-hidden="true" /></RouterLink>
                </header>
                <ul class="system-list">
                  <li v-for="item in (visibleRiskSystems ?? []).slice(0, 3)" :key="item.filingId" class="system-row">
                    <span class="system-name">{{ item.systemName }}</span>
                    <span class="system-count">{{ dashboardNumber(item.count) }}<small>项隐患</small></span>
                    <span class="system-org">{{ item.organizationName }}</span>
                  </li>
                  <li v-if="!(visibleRiskSystems ?? []).length" class="system-empty">当前范围暂无高风险系统。</li>
                </ul>
              </section>
              <section class="screen-card">
                <header class="card-heading"><h2>最新预警</h2>
                  <RouterLink class="screen-link" :to="scopedAlertsLocation">查看全部<ArrowRight aria-hidden="true" /></RouterLink>
                </header>
                <div class="marquee" v-if="(visibleAlerts ?? []).length">
                  <ul class="marquee-track" v-dashboard-scroll>
                    <li v-for="item in visibleAlerts ?? []" :key="item.id" class="alert-row">
                      <span class="alert-title">{{ item.systemName || item.type }}</span>
                      <span class="alert-meta">
                        <span class="chip" :class="item.severity === 'high' ? 'high' : item.severity === 'medium' ? 'mid' : 'low'">
                          {{ item.severity === 'high' ? '高风险' : item.severity === 'medium' ? '中风险' : '低风险' }}</span>
                        <span class="chip state">{{ alertStatuses[item.status as AlertStatus] }}</span>
                        <span class="alert-org">{{ item.organizationName }}</span>
                      </span>
                    </li>
                  </ul>
                </div>
                <p v-else class="system-empty">当前范围暂无预警。</p>
              </section>
            </div>
          </div>

          <div class="screen-column layout-right">
            <section class="screen-card duty-card">
              <header class="card-heading"><h2>监管待办</h2><span class="screen-caption">{{ districtScopeName }}</span></header>
              <div class="duty-group">
                <p class="duty-group-title">风险处置</p>
                <div v-for="task in workItems.slice(0, 2)" :key="task.label" class="duty-item"
                  :class="{ urgent: task.urgent && (task.value ?? 0) > 0 }">
                  <component :is="task.icon" aria-hidden="true" />
                  <span>{{ task.label }}</span><b>{{ dashboardNumber(task.value) }}</b>
                </div>
              </div>
              <div class="duty-group">
                <p class="duty-group-title">审核流程</p>
                <div v-for="task in workItems.slice(2)" :key="task.label" class="duty-item">
                  <component :is="task.icon" aria-hidden="true" />
                  <span>{{ task.label }}</span><b>{{ dashboardNumber(task.value) }}</b>
                </div>
              </div>
            </section>

            <section class="screen-card">
              <header class="card-heading"><h2>备案状态分布</h2>
                <RouterLink class="screen-link" to="/filings">备案台账<ArrowRight aria-hidden="true" /></RouterLink>
              </header>
              <DashboardDistribution :slices="filingSlices" label="备案记录" :loading="metricsLoading" />
            </section>

            <section class="screen-card">
              <header class="card-heading"><h2>备案覆盖</h2>
                <RouterLink class="screen-link" :to="coveragePath">覆盖清册<ArrowRight aria-hidden="true" /></RouterLink>
              </header>
              <div class="bench-list">
                <div v-for="item in coverageSlices" :key="item.label" class="bench-row">
                  <span class="bench-label">{{ item.label }}</span>
                  <span class="bench-track"><i :style="{ width: `${coverage?.eligible ? item.count * 100 / coverage.eligible : 0}%`, background: item.color }" /></span>
                  <span class="bench-value">{{ dashboardNumber(item.count) }}<em v-if="coverage"> /{{ dashboardNumber(coverage.eligible) }}</em></span>
                </div>
                <p class="bench-note">覆盖率 {{ dashboardPercent(coverage?.covered, coverage?.eligible) }} · 未覆盖单位已发督办</p>
              </div>
            </section>

            <section class="screen-card">
              <header class="card-heading"><h2>未销项风险分布</h2>
                <RouterLink class="screen-link" :to="scopedRankingLocation">风险分析<ArrowRight aria-hidden="true" /></RouterLink>
              </header>
              <div class="bench-list">
                <div v-for="item in openRiskSlices" :key="item.label" class="bench-row">
                  <span class="bench-label">{{ item.label }}</span>
                  <span class="bench-track"><i :style="{ width: `${openRiskTotal ? item.count * 100 / openRiskTotal : 0}%`, background: item.color }" /></span>
                  <span class="bench-value">{{ dashboardNumber(item.count) }}</span>
                </div>
                <p class="bench-note">合计 {{ dashboardNumber(openRiskTotal) }} 项</p>
              </div>
            </section>
          </div>
        </div>

        <footer class="overview-footer" aria-label="监管业务指标">
          <RouterLink v-for="item in footerFigures" :key="item.label" class="footer-kpi" :to="item.path">
            <component :is="item.icon" aria-hidden="true" />
            <span>{{ item.label }}</span><b>{{ item.value }}</b>
          </RouterLink>
        </footer>
      </section>

      <div v-else-if="tab === 'risk'" class="screen-pane detail-grid">
        <section class="screen-card" aria-labelledby="high-risk-systems">
          <header class="card-heading">
            <h2 id="high-risk-systems">{{ riskByDistrict ? '区县风险热力' : '高风险隐患系统' }}</h2>
            <RouterLink class="screen-link district-switch" :to="riskByDistrict ? scopedRankingLocation : overviewMapLocation">
              {{ riskByDistrict ? '按系统查看' : '按区县查看' }}
            </RouterLink>
          </header>
          <div class="scoped-panel-heading">
            <span class="screen-caption">{{ districtScopeName }} · 未销项高风险隐患数 · 前 10</span>
          </div>
          <ul class="system-list">
            <li v-for="item in (visibleRiskSystems ?? [])" :key="item.filingId" class="system-row">
              <span class="system-name">{{ item.systemName }}</span>
              <span class="system-count">{{ dashboardNumber(item.count) }}<small>项隐患</small></span>
              <span class="system-org">{{ item.organizationName }}</span>
            </li>
            <li v-if="!(visibleRiskSystems ?? []).length" class="system-empty">当前范围暂无高风险系统。</li>
          </ul>
        </section>
        <section class="screen-card" aria-labelledby="dashboard-alerts">
          <header class="card-heading"><h2 id="dashboard-alerts">实时预警</h2>
            <RouterLink class="screen-link" :to="scopedAlertsLocation">查看全部<ArrowRight aria-hidden="true" /></RouterLink>
          </header>
          <DashboardAlerts :items="visibleAlerts ?? []" :loading="scopedLoading" :read-only="Boolean(area)"
            highlight-new />
        </section>
      </div>

      <div v-else-if="tab === 'alerts'" class="screen-pane detail-grid">
        <section class="screen-card" aria-labelledby="dashboard-alerts-tab">
          <header class="card-heading"><h2 id="dashboard-alerts-tab">实时预警</h2></header>
          <DashboardAlerts :items="visibleAlerts ?? []" :loading="scopedLoading" :read-only="Boolean(area)"
            highlight-new />
        </section>
      </div>

      <div v-else class="screen-pane detail-grid">
        <section class="screen-card" aria-labelledby="dashboard-statistics">
          <header class="card-heading"><h2 id="dashboard-statistics">统计分析</h2></header>
          <DashboardTrend v-model="range" :history="statistics?.trend" :loading="metricsLoading" />
        </section>
      </div>

      <CopyrightFooter />
    </div>
  </section>
</template>

<style scoped src="./commandDashboard.css"></style>

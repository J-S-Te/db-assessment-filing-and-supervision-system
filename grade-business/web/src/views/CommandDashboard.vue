<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onScopeDispose, ref, watch } from 'vue'
import {
  Activity, ArrowLeft, ArrowRight, BellRing, Boxes, CalendarClock, ChartNoAxesCombined, CircleCheck,
  ClipboardList, ClipboardPenLine, FileChartColumn, FileSearch, Files, Maximize, Minimize, RefreshCw,
  Server, ShieldAlert, ShieldCheck,
} from '@lucide/vue'
import { RouterLink, useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import DashboardAlerts from '../components/DashboardAlerts.vue'
import DashboardDistribution from '../components/DashboardDistribution.vue'
import DashboardRiskMap from '../components/DashboardRiskMap.vue'
import DashboardOverviewTrend from '../components/DashboardOverviewTrend.vue'
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
import { vDashboardScroll } from '../lib/dashboardAutoScroll'
import { useDistrictDetail, useDistrictSummary } from '../lib/districtResponse'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { canVisit, refreshSession, session } from '../lib/session'
import {
  dashboardColors, type DashboardTone, type DashboardTrendRange, type MetricSlice,
} from '../lib/dashboardMetrics'

const tabs = {
  overview: { label: '态势总览', icon: Activity },
  risk: { label: '风险分析', icon: ShieldAlert },
  alerts: { label: '实时预警', icon: BellRing },
  statistics: { label: '统计分析', icon: ChartNoAxesCombined },
}
const DashboardRiskAnalysis = defineAsyncComponent(() => import('../components/DashboardRiskAnalysis.vue'))
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
const areaDistrictCount = area?.districts.filter(name => name !== area.name).length
const { statistics: globalStatistics, analytics: globalAnalytics, overview: globalOverview, alerts,
  alertCounts: globalAlertCounts, failure, loading, refresh } = useDashboard(!area)
const selectedDistrict = computed(() => {
  if (fixedDistrict) return fixedDistrict
  const district = route.query.district
  return typeof district === 'string' && (!area || area.districts.includes(district)) ? district : null
})
const overviewVisible = computed(() => tab.value === 'overview')
const districtViewVisible = computed(() => overviewVisible.value || tab.value === 'risk')
const districts = useDistrictSummary(districtViewVisible)
const showScopedDetail = computed(() =>
  Boolean(area) || districtViewVisible.value || selectedDistrict.value !== null)
const districtDetail = useDistrictDetail(selectedDistrict, showScopedDetail)
const snapshot = districtDetail.snapshot
const districtScopeName = computed(() => {
  if (fixedDistrict) return fixedDistrict
  if (selectedDistrict.value !== null) return selectedDistrict.value || '未归区'
  return area?.name ?? '全域'
})
const wholePlatform = computed(() => !area && selectedDistrict.value === null)
const statistics = computed(() => snapshot.value?.statistics ??
  (wholePlatform.value ? globalStatistics.value : undefined))
const overview = computed(() => snapshot.value?.overview ??
  (wholePlatform.value ? globalOverview.value : undefined))
const riskAnalytics = computed(() => snapshot.value?.analytics ?? (wholePlatform.value ? globalAnalytics.value : undefined))
const alertCounts = computed(() => snapshot.value?.overview?.alertCounts ??
  (wholePlatform.value ? globalAlertCounts.value : undefined))
const metricsLoading = computed(() => wholePlatform.value ? loading.value : districtDetail.loading.value)
const metricsFailed = computed(() => wholePlatform.value ? failure.value : districtDetail.failed.value)
const hasSnapshot = computed(() => !!(statistics.value || overview.value || visibleAlerts.value || alertCounts.value))
const activeAlerts = computed(() => alertCounts.value && alertCounts.value.pending + alertCounts.value.processing)
const completion = computed(() => dashboardPercent(overview.value?.hazardClosed, overview.value?.hazardTotal))
const openRiskTotal = computed(() => {
  const risk = statistics.value?.openHazards
  return risk && risk.high + risk.medium + risk.low
})
const range = ref<DashboardTrendRange>(7)
const scopedRankingLocation = computed(() => ({
  path: dashboardPath, query: { tab: 'risk', district: selectedDistrict.value ?? undefined },
}))
const scopedAlertsLocation = computed(() => ({
  path: dashboardPath, query: { tab: 'alerts', district: selectedDistrict.value ?? undefined },
}))
const visibleRiskSystems = computed(() => showScopedDetail.value
  ? snapshot.value?.highRiskSystems : globalStatistics.value?.highRiskSystems)
const visibleAlerts = computed(() => wholePlatform.value ? alerts.value : snapshot.value?.alerts)
const scopedLoading = computed(() => showScopedDetail.value ? districtDetail.loading.value : loading.value)
const scopedFailed = computed(() => showScopedDetail.value ? districtDetail.failed.value : failure.value)
const screenRefreshing = computed(() =>
  metricsLoading.value || scopedLoading.value || (districtViewVisible.value && districts.loading.value))
const screenFailed = computed(() =>
  metricsFailed.value || scopedFailed.value || (districtViewVisible.value && districts.failed.value))

const comparison = computed<DashboardComparison | undefined>(() => snapshot.value?.comparison)

/** 当前范围的综合风险指数。 */
const riskIndex = computed(() => snapshot.value?.risk)
const riskLevel = computed(() => riskIndex.value?.level ?? 'none')
/** 当前范围的待办汇总。 */
const dutySummary = computed(() => snapshot.value?.duty)
const riskHasRecords = computed(() => (snapshot.value?.counts.systems ?? 0) > 0 || Boolean(openRiskTotal.value))
const riskValue = computed(() => dashboardNumber(riskHasRecords.value ? riskIndex.value?.index : undefined))
const riskLevelLabel = computed(() => ({ high: '高', mid: '中', low: '低', none: '无' })[riskLevel.value])
const alertTimestamp = new Intl.DateTimeFormat('zh-CN', {
  timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false,
})

/**
 * 用途 检查当前驾驶舱账号是否可以下钻到详情。
 * 参数 to 为原有业务路由。
 * 返回值 会话允许访问该页面时为真。
 */
function canOpen(to: RouteLocationRaw) {
  return session.value ? canVisit(session.value, router.resolve(to).path) : false
}

/**
 * 用途 将实际预警时间整理为态势总览列表显示的本地时分。
 * 参数 value 为接口返回的带时区时间。
 * 返回值 以北京时间展示的时分，时间无效时为空字符串。
 */
function alertTime(value: string) {
  const time = Date.parse(value)
  return Number.isFinite(time) ? alertTimestamp.format(time) : ''
}

/**
 * 用途 以原型的短标签展示真实环比值，比较窗口由卡片标题说明。
 * 参数 chip 为使用原有业务口径计算的比较结果。
 * 返回值 含变化方向的短标签。
 */
function overviewPeriod(chip: DeltaChip) {
  const short = chip.text.replace(/^较上期\s*\d+\s*日\s*/, '')
  const change = /^(↑|↓)(.+)$/.exec(short)
  if (!change) return `环比 ${short}`
  return `环比 ${change[1] === '↑' ? '+' : '−'}${change[2]} ${change[1]}`
}

const metrics = computed<readonly {
  label: string; value: string; tone: DashboardTone; note: string
  period: DeltaChip; year: DeltaChip; path: RouteLocationRaw
}[]>(() => {
  const windowDays = comparison.value?.windowDays ?? 30
  const hasYearAgo = comparison.value?.hasYearAgo ?? false
  return [
    {
      label: '综合风险指数', value: riskValue.value,
      tone: riskLevel.value === 'high' ? 'red' : riskLevel.value === 'mid' ? 'amber' : 'blue',
      note: riskIndex.value?.weights ?? '逾期 40% / 高风险 40% / 待复测 20%',
      period: withWindowRange(periodChip(comparison.value?.riskIndex), windowDays),
      year: yearChip(comparison.value?.riskIndex, hasYearAgo),
      path: scopedRankingLocation.value,
    },
    {
      label: '未销项隐患', value: dashboardNumber(overview.value?.hazardTotal ? openRiskTotal.value : undefined), tone: 'red',
      note: overview.value?.hazardTotal && statistics.value ? `高 ${dashboardNumber(statistics.value.openHazards.high)} · 中 ${dashboardNumber(statistics.value.openHazards.medium)} · 低 ${dashboardNumber(statistics.value.openHazards.low)}` : '',
      period: withWindowRange(periodChip(comparison.value?.openHazards), windowDays),
      year: yearChip(comparison.value?.openHazards, hasYearAgo),
      path: { path: '/hazards' },
    },
    {
      label: '整改完成率', value: completion.value, tone: 'teal',
      note: overview.value?.hazardTotal ? `已销项 ${dashboardNumber(overview.value.hazardClosed)} / ${dashboardNumber(overview.value.hazardTotal)}` : '',
      period: withWindowRange(completionChip(comparison.value?.completionRate), windowDays),
      year: completionYearChip(comparison.value?.completionRate),
      path: { path: '/hazards', query: { status: 'closed' } },
    },
    {
      label: '活跃预警', value: dashboardNumber(alertCounts.value?.total ? activeAlerts.value : undefined), tone: 'amber',
      note: alertCounts.value?.total ? `待处理 ${dashboardNumber(alertCounts.value.pending)} · 处理中 ${dashboardNumber(alertCounts.value.processing)}` : '',
      period: withWindowRange(periodChip(comparison.value?.activeAlerts), windowDays),
      year: yearChip(comparison.value?.activeAlerts, hasYearAgo),
      path: scopedAlertsLocation.value,
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
  { label: '活跃预警', value: activeAlerts.value, tone: 'red', path: alertPath },
])
const categoryMax = computed(() => Math.max(1, ...categories.value.map(item => item.value ?? 0)))

const filingSlices = computed<MetricSlice[]>(() => {
  const rows = statistics.value?.filings
  return rows ? [
    { label: '有效备案', count: rows.active, tone: 'blue', color: '#35d6ff' },
    { label: '审核中', count: rows.underReview, tone: 'blue', color: '#6ea8ff' },
    { label: '待补正', count: rows.returned, tone: 'amber', color: '#ffb84d' },
    { label: '草稿', count: rows.draft, tone: 'red', color: '#ff5d6c' },
    { label: '已注销', count: rows.cancelled, tone: 'slate', color: '#5b7396' },
  ] : []
})
const openRiskSlices = computed<readonly (MetricSlice & { path: RouteLocationRaw })[]>(() => {
  const rows = statistics.value?.openHazards
  return rows ? [
    { label: '高风险', count: rows.high, tone: 'red', color: dashboardColors.red,
      path: { path: '/hazards', query: { severity: 'high' } } },
    { label: '中风险', count: rows.medium, tone: 'amber', color: dashboardColors.amber,
      path: { path: '/hazards', query: { severity: 'medium' } } },
    { label: '低风险', count: rows.low, tone: 'blue', color: dashboardColors.blue,
      path: { path: '/hazards', query: { severity: 'low' } } },
  ] : []
})
const coverage = computed(() => {
  const current = statistics.value?.filingCoverage
  return current?.eligible ? current : undefined
})
const coveragePath = { path: archivePaths.systems, query: { tab: 'coverage' } }
const coverageSlices = computed<readonly (MetricSlice & { path: RouteLocationRaw })[]>(() => {
  const current = coverage.value
  return current ? [
    { label: '已覆盖', count: current.covered, tone: 'blue', color: dashboardColors.blue, path: coveragePath },
    { label: '未覆盖', count: current.eligible - current.covered, tone: 'slate', color: '#33546f', path: coveragePath },
  ] : []
})
const reportReview = computed(() => statistics.value?.reportFirstReview)
const statisticsLocation = computed(() => tabLocation('statistics'))
const footerFigures = computed(() => [
  { label: '在办实施报备', value: dashboardNumber(overview.value?.implementationOpen || undefined),
    path: '/implementations', icon: ClipboardPenLine },
  { label: '测评首轮通过率', value: dashboardPercent(reportReview.value?.approved, reportReview.value?.decided),
    path: statisticsLocation.value, icon: FileChartColumn },
  { label: '预警关闭率', value: dashboardPercent(alertCounts.value?.closed, alertCounts.value?.total),
    path: { path: alertPath, query: { status: 'closed' } }, icon: BellRing },
  { label: '审核已逾期', value: dashboardNumber(overview.value?.reviewDeadlines?.overdue || undefined),
    path: statisticsLocation.value, icon: CalendarClock },
  { label: '已销项隐患', value: dashboardNumber(overview.value?.hazardTotal ? overview.value.hazardClosed : undefined),
    path: { path: '/hazards', query: { status: 'closed' } }, icon: CircleCheck },
  { label: '资产登记总量', value: dashboardNumber(statistics.value
    ? (statistics.value.registeredAssets.ip + statistics.value.registeredAssets.domain
      + statistics.value.registeredAssets.topology + statistics.value.registeredAssets.supplier
      + statistics.value.registeredAssets.securityEvent + statistics.value.registeredAssets.cloud
      + statistics.value.registeredAssets.bigData) || undefined
    : undefined),
    path: archivePaths.systems, icon: Boxes },
])
const dutyCounts = computed(() => {
  const counts = snapshot.value?.counts
  const remediation = wholePlatform.value ? globalOverview.value?.remediation : undefined
  return {
    overdue: counts?.overdue ?? remediation?.overdue,
    pendingReview: counts?.pendingReview ?? remediation?.pendingReview,
    pendingAlerts: counts?.pendingAlerts ?? alertCounts.value?.pending,
    pendingPlanReview: counts?.pendingPlanReview ?? remediation?.pendingPlanReview,
  }
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
const overviewRiskTasks = computed(() => [{
  label: '重点隐患', value: statistics.value?.openHazards.high, icon: ShieldAlert,
  to: { path: '/hazards', query: { severity: 'high' } }, urgent: false,
}, workItems.value[0]!, {
  label: '待监管复测隐患', value: snapshot.value?.counts.pendingRetest, icon: RefreshCw,
  to: { path: '/hazards', query: { status: 'pending_retest' } }, urgent: false,
}])
const screen = ref<HTMLElement>()
const fullscreenAvailable = ref(false)
const fullscreen = ref(false)
const fullscreenMessage = ref('')
const screenState = computed(() => {
  if (fullscreenMessage.value) return fullscreenMessage.value
  if (districtDetail.loading.value && !snapshot.value) return `正在切换至${districtScopeName.value}`
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
  <section v-if="sessionAvailable" ref="screen" class="command-screen"
    :class="{ 'risk-screen': tab === 'risk', 'overview-screen': overviewVisible }" aria-label="指挥驾驶舱">
    <DashboardScene v-if="tab !== 'risk' && !overviewVisible" />

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
          <p class="title-sub">{{ overviewVisible && !fixedDistrict && selectedDistrict !== null && area ? `${area.name} · ` : '' }}{{ districtScopeName }}</p>
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
              <template v-if="area && selectedDistrict === null"> · {{ areaDistrictCount }} 个区县</template>
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
            {{ districtScopeName }}待办<template v-if="dutySummary?.total"> {{ dashboardNumber(dutySummary.total) }} 项</template>
          </span>
          <p v-if="dutySummary?.total" :title="dutySummary.conclusion">
            其中 <b>{{ dashboardNumber(dutySummary.overdueHazards) }}</b> 项已逾期、<b>{{ dashboardNumber(dutySummary.pendingAlerts) }}</b> 项预警待处理。
            <template v-if="dutySummary.topDistrict">当前范围风险指数最高（<b>{{ dutySummary.topDistrict }}</b>）。</template>
          </p>
          <p v-else>{{ scopedLoading ? '正在读取待办汇总…' : scopedFailed ? '暂时无法读取待办汇总' : '暂无待办事项' }}</p>
          <div class="conclusion-worst">
            <span>综合风险指数</span><b>{{ riskValue }}</b>
            <span v-if="riskHasRecords" class="conclusion-level lv-pill" :class="riskLevel">{{ riskLevelLabel }}</span>
          </div>
        </section>

        <div class="overview-grid">
          <div class="screen-column layout-left">
            <section class="screen-card metrics-card">
              <header class="card-heading"><h2>核心指标</h2>
                <span class="screen-caption">{{ districtScopeName }} · 对比窗口 {{ comparison?.windowDays ?? 30 }} 日</span>
              </header>
              <div class="metric-grid">
                <RouterLink v-for="item in metrics" :key="item.label" class="metric-card" :to="item.path"
                  :style="{ '--metric-tone': dashboardColors[item.tone] }">
                  <span class="metric-head">
                    <span class="metric-label">{{ item.label }}</span>
                    <span class="delta-chip" :class="item.period.tone"
                      :title="`${item.period.text} · ${item.period.title}。${item.year.text} · ${item.year.title}`"
                      :aria-label="`${item.period.text}，${item.year.text}`">{{ overviewPeriod(item.period) }}</span>
                  </span>
                  <span class="metric-row">
                    <strong class="metric-value">{{ item.value }}</strong>
                  </span>
                  <small v-if="item.note">{{ item.note }}</small>
                </RouterLink>
              </div>
            </section>

            <section class="screen-card category-card">
              <header class="card-heading"><h2>业务分类统计</h2><span class="screen-caption">{{ districtScopeName }}</span></header>
              <div class="category-list">
                <RouterLink v-for="item in categories" :key="item.label" class="category-row" :to="item.path">
                  <span class="category-label">{{ item.label }}</span>
                  <span class="category-track" aria-hidden="true">
                    <i :class="`category-${item.tone}`"
                      :style="{ width: `${item.value ? Math.max(3, item.value * 100 / categoryMax) : 0}%` }" />
                  </span>
                  <span class="category-value">{{ dashboardNumber(item.value) }}</span>
                </RouterLink>
              </div>
            </section>

            <section class="screen-card trend-card">
              <DashboardOverviewTrend v-model="range" :history="statistics?.trend" :comparison="trendComparison"
                :loading="metricsLoading" :failed="metricsFailed" />
            </section>
          </div>

          <div class="screen-column layout-center">
            <DashboardRiskMap overview :summary="districts.summary.value" :detail="snapshot"
              :scope="districtScopeName" :selected="selectedDistrict" :area="area" :locked="Boolean(fixedDistrict)"
              :starting-rank="false" :summary-loading="districts.loading.value" :summary-failed="districts.failed.value"
              :loading="districtDetail.loading.value" :failed="districtDetail.failed.value"
              :open-total="openRiskTotal" :closed-total="overview?.hazardClosed" :active-alerts="activeAlerts" :can-open="canOpen"
              :scoped-alerts-location="scopedAlertsLocation" @select="selectDistrict"
              @retry-summary="districts.refresh" @retry-detail="districtDetail.refresh" />

            <div class="overview-lists">
              <section class="screen-card">
                <header class="card-heading"><h2>高风险系统</h2>
                  <RouterLink class="screen-link" :to="scopedRankingLocation">查看排行<ArrowRight aria-hidden="true" /></RouterLink>
                </header>
                <div v-if="visibleRiskSystems?.length" class="overview-roll" v-dashboard-scroll
                  tabindex="0" aria-label="高风险系统列表">
                  <ul class="system-list" data-dashboard-loop-original>
                    <li v-for="item in visibleRiskSystems" :key="item.filingId">
                      <component :is="canOpen('/hazards') ? RouterLink : 'span'" class="system-row"
                        :to="{ path: '/hazards', query: { filingId: item.filingId } }"
                        :title="item.systemName + ' · ' + item.organizationName">
                        <span class="tag tag-high">高</span><span class="system-name">{{ item.systemName }}</span>
                        <span class="system-count">{{ dashboardNumber(item.count) }} <small>项隐患</small></span>
                      </component>
                    </li>
                  </ul>
                  <ul class="system-list" data-dashboard-loop-copy aria-hidden="true" hidden></ul>
                </div>
                <p v-else class="system-empty">{{ !visibleRiskSystems && scopedLoading ? '正在读取高风险系统…' : !visibleRiskSystems && scopedFailed ? '暂时无法读取高风险系统' : '当前范围暂无高风险系统。' }}</p>
              </section>
              <section class="screen-card">
                <header class="card-heading"><h2>最新预警</h2>
                  <RouterLink class="screen-link" :to="scopedAlertsLocation">查看全部<ArrowRight aria-hidden="true" /></RouterLink>
                </header>
                <div v-if="visibleAlerts?.length" class="overview-roll" v-dashboard-scroll
                  tabindex="0" aria-label="最新预警列表">
                  <ul class="system-list" data-dashboard-loop-original>
                    <li v-for="item in visibleAlerts" :key="item.id">
                      <component :is="canOpen(alertPath) ? RouterLink : 'span'" class="alert-row"
                        :to="{ path: alertPath, query: { alert: item.id } }"
                        :title="[item.systemName, item.organizationName, alertStatuses[item.status as AlertStatus]].filter(Boolean).join(' · ')">
                        <time :datetime="item.triggeredAt">{{ alertTime(item.triggeredAt) }}</time>
                        <span class="alert-title">{{ item.systemName || item.type }}</span>
                      </component>
                    </li>
                  </ul>
                  <ul class="system-list" data-dashboard-loop-copy aria-hidden="true" hidden></ul>
                </div>
                <p v-else class="system-empty">{{ !visibleAlerts && metricsLoading ? '正在读取预警…' : !visibleAlerts && metricsFailed ? '暂时无法读取预警' : '当前范围暂无预警。' }}</p>
              </section>
            </div>
          </div>

          <div class="screen-column layout-right">
            <section class="screen-card duty-card">
              <header class="card-heading"><h2>监管待办</h2><span class="screen-caption">{{ districtScopeName }}</span></header>
              <div class="duty-group">
                <p class="duty-group-title">风险处置</p>
                <component :is="canOpen(task.to) ? RouterLink : 'span'" v-for="task in overviewRiskTasks"
                  :key="task.label" class="duty-item" :to="task.to" :class="{ urgent: task.urgent && (task.value ?? 0) > 0 }">
                  <component :is="task.icon" aria-hidden="true" />
                  <span>{{ task.label }}</span><b>{{ dashboardNumber(task.value) }}</b>
                </component>
              </div>
              <div class="duty-group">
                <p class="duty-group-title">审核流程</p>
                <component :is="canOpen(task.to) ? RouterLink : 'span'" v-for="task in workItems.slice(2)"
                  :key="task.label" class="duty-item" :to="task.to">
                  <component :is="task.icon" aria-hidden="true" />
                  <span>{{ task.label }}</span><b>{{ dashboardNumber(task.value) }}</b>
                </component>
              </div>
            </section>

            <section class="screen-card">
              <header class="card-heading"><h2>备案状态分布</h2></header>
              <DashboardDistribution :slices="filingSlices" label="备案记录" :loading="metricsLoading" :failed="metricsFailed" />
            </section>

            <section class="screen-card">
              <header class="card-heading"><h2>备案覆盖</h2>
                <RouterLink class="screen-link" :to="coveragePath">覆盖清册<ArrowRight aria-hidden="true" /></RouterLink>
              </header>
              <div v-if="coverage" class="bench-list">
                <RouterLink v-for="item in coverageSlices" :key="item.label" class="bench-row" :to="item.path">
                  <span class="bench-label">{{ item.label }}</span>
                  <span class="bench-track"><i :style="{ width: `${coverage?.eligible ? item.count * 100 / coverage.eligible : 0}%`, background: item.color }" /></span>
                  <span class="bench-value">{{ dashboardNumber(item.count) }}<em v-if="coverage"> /{{ dashboardNumber(coverage.eligible) }}</em></span>
                </RouterLink>
                <p class="bench-note">覆盖率 {{ dashboardPercent(coverage.covered, coverage.eligible) }}</p>
              </div>
              <p v-else class="bench-note">{{ metricsLoading && !statistics ? '正在读取覆盖清册…' : metricsFailed && !statistics ? '暂时无法读取覆盖清册' : '暂无覆盖清册' }}</p>
            </section>

            <section class="screen-card">
              <header class="card-heading"><h2>未销项风险分布</h2>
                <span v-if="openRiskTotal !== undefined" class="screen-caption">合计 {{ dashboardNumber(openRiskTotal) }} 项</span>
              </header>
              <div v-if="openRiskSlices.length" class="bench-list">
                <RouterLink v-for="item in openRiskSlices" :key="item.label" class="bench-row" :to="item.path">
                  <span class="bench-label">{{ item.label }}</span>
                  <span class="bench-track"><i :style="{ width: `${openRiskTotal ? item.count * 100 / openRiskTotal : 0}%`, background: item.color }" /></span>
                  <span class="bench-value">{{ dashboardNumber(item.count) }}</span>
                </RouterLink>
              </div>
              <p v-else class="bench-note">{{ metricsLoading && !statistics ? '正在读取风险分布…' : metricsFailed && !statistics ? '暂时无法读取风险分布' : '暂无未销项隐患' }}</p>
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

      <DashboardRiskAnalysis v-else-if="tab === 'risk'"
        :scope="districtScopeName" :selected="selectedDistrict" :area="area" :locked="Boolean(fixedDistrict)"
        :starting-rank="route.query.view === 'district'" :summary="districts.summary.value" :detail="snapshot"
        :statistics="statistics" :analytics="riskAnalytics" :systems="visibleRiskSystems" :alerts="visibleAlerts"
        :alert-counts="alertCounts" :tasks="workItems" :indicators="footerFigures" :loading="metricsLoading"
        :failed="metricsFailed" :detail-loading="scopedLoading" :detail-failed="scopedFailed"
        :summary-loading="districts.loading.value" :summary-failed="districts.failed.value"
        :scoped-alerts-location="scopedAlertsLocation" @select="selectDistrict"
        @retry-summary="districts.refresh" @retry-detail="districtDetail.refresh" />

      <div v-else-if="tab === 'alerts'" class="screen-pane detail-grid">
        <section class="screen-card" aria-labelledby="dashboard-alerts-tab">
          <header class="card-heading"><h2 id="dashboard-alerts-tab">实时预警</h2></header>
          <DashboardAlerts :items="visibleAlerts" :loading="metricsLoading" :failed="metricsFailed" :read-only="Boolean(area)"
            highlight-new />
        </section>
      </div>

      <div v-else class="screen-pane detail-grid">
        <section class="screen-card" aria-labelledby="dashboard-statistics">
          <header class="card-heading"><h2 id="dashboard-statistics">统计分析</h2></header>
          <DashboardTrend v-model="range" :history="statistics?.trend" :loading="metricsLoading" :failed="metricsFailed" />
        </section>
      </div>

      <CopyrightFooter />
    </div>
  </section>
</template>

<style scoped src="./commandDashboard.css"></style>
<style scoped src="./dashboardOverview.css"></style>

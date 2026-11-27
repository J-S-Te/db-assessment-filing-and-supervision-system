<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, type Component } from 'vue'
import { RouterLink, useRouter, type RouteLocationRaw } from 'vue-router'
import DashboardRiskMap from './DashboardRiskMap.vue'
import { alertPath, alertStatuses, alertTypes, type AlertCounts } from '../lib/alerts'
import {
  dashboardNumber, dashboardTime, type DashboardAlert, type DashboardStatistics, type DashboardSystemRisk,
} from '../lib/dashboard'
import type { DashboardAnalytics } from '../lib/dashboardAnalytics'
import { vDashboardScroll } from '../lib/dashboardAutoScroll'
import { periodChip, withWindowRange, yearChip } from '../lib/dashboardComparison'
import type { DashboardDay, DashboardTrendRange } from '../lib/dashboardMetrics'
import {
  activeRiskAlertCount, rankRiskIndustries, riskAlertSegments,
  riskColor, riskGaugeArc, riskLevels, riskParts,
} from '../lib/dashboardRiskAnalysis'
import type { DistrictResponse, ResponseSummary } from '../lib/districtResponse'
import { canVisit, session, type DashboardArea } from '../lib/session'

type Indicator = { label: string; value: string | number | undefined; path: RouteLocationRaw; icon: Component }
type Task = { label: string; value?: number; to: RouteLocationRaw; urgent?: boolean; icon: Component }
const props = defineProps<{
  scope: string; selected: string | null; area?: DashboardArea; locked: boolean; startingRank: boolean
  summary?: ResponseSummary; detail?: DistrictResponse; statistics?: Omit<DashboardStatistics, 'highRiskSystems'>
  analytics?: DashboardAnalytics; alertCounts?: AlertCounts; alerts?: readonly DashboardAlert[]
  systems?: readonly DashboardSystemRisk[]; tasks: readonly Task[]; indicators: readonly Indicator[]
  loading: boolean; failed: boolean; detailLoading: boolean; detailFailed: boolean
  summaryLoading: boolean; summaryFailed: boolean; scopedAlertsLocation: RouteLocationRaw
}>()
const emit = defineEmits<{ select: [district: string | null]; retrySummary: []; retryDetail: [] }>()
const router = useRouter()
const trendRange = ref<DashboardTrendRange>(7)
const trendRanges = [7, 14, 30] as const
const series = [
  { key: 'alertsTriggered', label: '触发预警', color: '#ffb84d' },
  { key: 'hazardsClosed', label: '隐患销项', color: '#3ddc97' },
  { key: 'filingsCreated', label: '新增备案', color: '#35d6ff' },
] as const

const counts = computed(() => props.detail?.counts)
const risk = computed(() => counts.value?.systems ? props.detail?.risk : undefined)
const duty = computed(() => props.detail?.duty)
const comparison = computed(() => props.detail?.comparison)
const parts = computed(() => riskParts(counts.value?.systems ? counts.value : undefined))
const maxPart = computed(() => Math.max(1, ...parts.value.map(part => part.count ?? 0)))
const riskPeriod = computed(() => withWindowRange(periodChip(comparison.value?.riskIndex), comparison.value?.windowDays ?? 30))
const riskYear = computed(() => yearChip(comparison.value?.riskIndex, Boolean(comparison.value?.hasYearAgo)))
const openPeriod = computed(() => withWindowRange(periodChip(comparison.value?.openHazards), comparison.value?.windowDays ?? 30))
const openRows = computed(() => {
  const values = props.statistics?.openHazards
  return ([
    ['high', '高风险', values?.high],
    ['mid', '中风险', values?.medium],
    ['low', '低风险', values?.low],
  ] as const).map(([level, label, count]) => ({ level, label, count }))
})
const openTotal = computed(() => props.statistics
  ? openRows.value.reduce((total, row) => total + (row.count ?? 0), 0) : undefined)
const maxOpen = computed(() => Math.max(1, ...openRows.value.map(row => row.count ?? 0)))
const daysInTrend = computed(() => props.statistics?.trend.slice(-trendRange.value) ?? [])
const peak = computed(() => Math.max(0, ...daysInTrend.value.flatMap(day => series.map(item => day[item.key]))))
const activeAlerts = computed(() => activeRiskAlertCount(props.alertCounts))
const segments = computed(() => riskAlertSegments(props.alertCounts))
const industries = computed(() => rankRiskIndustries(props.analytics))
const issues = computed(() => props.analytics?.recentCommonIssues?.items.slice(0, 5) ?? [])
const lists = computed(() => [
  { key: 'industry', title: '高风险行业 TOP', caption: '按未销项隐患', rows: industries.value.map(row => ({ ...row, count: row.total })) },
  { key: 'issues', title: '高频共性问题', caption: '近 30 日', rows: issues.value.map(row => ({ ...row, high: undefined })) },
] as const)
const paneStatus = computed(() => props.loading ? '正在读取当前范围…' : props.failed ? '暂时无法读取数据' : '暂无数据')

/**
 * 用途 检查业务跳转是否在当前会话允许的页面范围内。
 * 参数 to 为系统原有路由位置。
 * 返回值 当前账号可进入时为真。
 */
function canOpen(to: RouteLocationRaw) {
  return session.value ? canVisit(session.value, router.resolve(to).path) : false
}

/**
 * 用途 将日度计数生成与原型同尺寸的折线。
 * 参数 key 为预警、销项或备案的日统计字段。
 * 返回值 可供 SVG 绘图使用的坐标串。
 */
function linePoints(key: keyof Pick<DashboardDay, 'alertsTriggered' | 'hazardsClosed' | 'filingsCreated'>) {
  const days = daysInTrend.value
  return days.map((day, index) => {
    const x = 4 + 312 * index / Math.max(1, days.length - 1)
    const y = 92 - 88 * day[key] / Math.max(1, peak.value)
    return `${x.toFixed(1)},${y.toFixed(1)}`
  }).join(' ')
}

/**
 * 用途 打开高风险隐患系统对应的备案记录。
 * 参数 filingId 为风险统计关联的备案标识。
 * 返回值 备案详情路由。
 */
function systemLocation(filingId: string) {
  return '/filings/' + encodeURIComponent(filingId)
}

/**
 * 用途 返回预警记录在原有列表里的详情定位。
 * 参数 alertId 为真实的预警记录标识。
 * 返回值 预警详情链接。
 */
function alertLocation(alertId: string) {
  return { path: alertPath, query: { alert: alertId } }
}

/**
 * 用途 Esc 关闭区县浮层并恢复页面辖区。
 * 参数 event 为浏览器键盘事件。
 * 返回值 无。
 */
function escapeDistrict(event: KeyboardEvent) {
  if (event.key === 'Escape' && !event.defaultPrevented && props.selected !== null && !props.locked) {
    emit('select', null)
  }
}
onMounted(() => window.addEventListener('keydown', escapeDistrict))
onScopeDispose(() => window.removeEventListener('keydown', escapeDistrict))
</script>

<template>
  <section class="risk-analysis" aria-label="风险分析">
    <div class="risk-conclusion">
      <div class="concl-left">
        <span class="concl-badge">◉ {{ scope }}待办 <b>{{ dashboardNumber(duty?.total || undefined) }}</b><template v-if="duty?.total"> 项</template></span>
        <span class="concl-sep" :title="duty?.conclusion">{{ duty?.conclusion ?? paneStatus }}</span>
      </div>
      <div class="concl-right">综合风险指数 <b>{{ dashboardNumber(risk?.index) }}</b>
        <span v-if="risk" class="lv-pill" :class="risk.level">{{ riskLevels[risk.level] }}</span>
      </div>
    </div>

    <div class="risk-grid">
      <aside class="risk-left" aria-label="综合风险指标">
        <section class="card glass">
          <div class="card-head"><h3>综合风险指数</h3><span class="cap">{{ scope }} · 实时</span></div>
          <div class="gauge-wrap">
            <div class="gauge">
              <svg width="150" height="92" viewBox="0 0 150 92" aria-hidden="true">
                <path :d="riskGaugeArc(100)" fill="none" stroke="rgba(72,140,220,.2)" stroke-width="11" stroke-linecap="round" />
                <path v-if="risk?.index" :d="riskGaugeArc(risk.index)" fill="none" :stroke="riskColor(risk.level)"
                  stroke-width="11" stroke-linecap="round" :style="{ filter: 'drop-shadow(0 0 6px ' + riskColor(risk.level) + '66)' }" />
                <path d="M37,34 L45,46 M113,34 L105,46" stroke="rgba(72,140,220,.4)" />
                <text x="13" y="92" fill="#7e93b6" font-size="9">0</text><text x="124" y="92" fill="#7e93b6" font-size="9">100</text>
              </svg>
              <div class="gauge-value" :style="{ color: riskColor(risk?.level) }">{{ dashboardNumber(risk?.index) }}</div>
              <div class="gauge-cap">风险指数 / 100</div>
            </div>
            <div class="gauge-side">
              <div class="gauge-pill-row">
                <span v-if="risk" class="lv-pill" :class="risk.level">{{ riskLevels[risk.level] }}</span>
                <span class="cmp" :class="riskPeriod.tone" :title="riskPeriod.title">{{ riskPeriod.text }}</span>
                <span class="cmp" :class="riskYear.tone" :title="riskYear.title">{{ riskYear.text }}</span>
              </div>
              <div class="gauge-note">逾期 40% / 高风险 40% / 待复测 20%，封顶 100</div>
            </div>
          </div>
          <div class="contrib">
            <div v-for="part in parts" :key="part.key" class="row">
              <span class="label">{{ part.name }} <i class="w">×{{ part.weight }}%</i></span>
              <span class="bar" :class="part.key"><i :style="{ width: (part.count ?? 0) * 100 / maxPart + '%' }"></i></span>
              <span class="pct">{{ dashboardNumber(part.count) }}<template v-if="part.points !== undefined"> → {{ part.points }} 分</template></span>
            </div>
          </div>
        </section>

        <section class="card glass">
          <div class="card-head"><h3>未销项风险构成</h3>
            <RouterLink v-if="canOpen('/hazards')" class="link" to="/hazards">隐患台账 →</RouterLink>
          </div>
          <div class="stack-total">
            <div class="stack-bar" aria-hidden="true">
              <i v-for="row in openRows" :key="row.level" :class="'seg-' + row.level"
                :style="{ width: (openTotal ? (row.count ?? 0) / openTotal * 100 : 0) + '%' }"></i>
            </div>
            <div class="stack-caption"><b>{{ dashboardNumber(openTotal || undefined) }}</b>
              <span v-if="openTotal">项未销项隐患</span>
              <span class="cmp" :class="openPeriod.tone" :title="openPeriod.title">{{ openPeriod.text }}</span>
            </div>
          </div>
          <ul class="risk-rows">
            <li v-for="row in openRows" :key="row.level">
              <span class="risk-row">
                <span class="label">{{ row.label }}</span>
                <span class="bar" :class="'r-' + row.level"><i :style="{ width: (row.count ?? 0) * 100 / maxOpen + '%' }"></i></span>
                <span class="val">{{ dashboardNumber(openTotal ? row.count : undefined) }}
                  <small v-if="openTotal">项 · {{ Math.round((row.count ?? 0) * 100 / openTotal) }}%</small>
                </span>
              </span>
            </li>
          </ul>
        </section>

        <section class="card glass grow">
          <div class="card-head"><h3>风险趋势</h3>
            <div class="seg" role="group" aria-label="趋势日期范围">
              <button v-for="days in trendRanges" :key="days" class="chip mini" type="button"
                :class="{ active: trendRange === days }" :aria-pressed="trendRange === days"
                @click="trendRange = days">{{ days }} 日</button>
            </div>
          </div>
          <div class="trend-legend">
            <span v-for="item in series" :key="item.key"><i class="dot" :style="{ background: item.color }"></i>{{ item.label }}</span>
          </div>
          <div class="trend-body">
            <template v-if="peak">
              <span class="trend-max">峰值 {{ peak }}</span>
              <svg viewBox="0 0 320 96" preserveAspectRatio="none" role="img" :aria-label="'近 ' + trendRange + ' 风险趋势'">
                <defs><linearGradient id="risk-trend-shade" x2="0" y2="1">
                  <stop stop-color="#ffb84d" stop-opacity=".5" /><stop offset="100%" stop-color="#ffb84d" stop-opacity="0" />
                </linearGradient></defs>
                <path d="M0 24H320 M0 48H320 M0 72H320" stroke="rgba(72,140,220,.14)" />
                <polygon :points="'4,92 ' + linePoints('alertsTriggered') + ' 316,92'" fill="url(#risk-trend-shade)" opacity=".35" />
                <polyline v-for="item in series" :key="item.key" :points="linePoints(item.key)" fill="none"
                  :stroke="item.color" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round"
                  vector-effect="non-scaling-stroke"><title>{{ item.label }}</title></polyline>
              </svg>
              <div class="trend-x"><span>{{ daysInTrend[0]?.date.slice(5) }}</span><span>{{ daysInTrend.at(-1)?.date.slice(5) }}</span></div>
            </template>
            <p v-else class="panel-status" role="status">{{ daysInTrend.length ? '近 ' + trendRange + ' 日暂无业务记录' : paneStatus }}</p>
          </div>
        </section>
      </aside>

      <section class="risk-center" aria-label="区县热力和实时风险">
        <DashboardRiskMap :summary="summary" :detail="detail" :selected="selected" :scope="scope" :area="area"
          :locked="locked" :starting-rank="startingRank" :summary-loading="summaryLoading" :summary-failed="summaryFailed"
          :loading="detailLoading" :failed="detailFailed" :open-total="openTotal || undefined" :active-alerts="activeAlerts"
          :can-open="canOpen" :scoped-alerts-location="scopedAlertsLocation" @select="emit('select', $event)" @retry-summary="emit('retrySummary')" @retry-detail="emit('retryDetail')" />
        <div class="map-bottom">
          <section class="card glass">
            <div class="card-head"><h3>高风险隐患系统</h3><span class="cap">{{ scope }} · 未销项高危 · 前 10</span>
              <RouterLink v-if="canOpen('/hazards')" class="link" to="/hazards">隐患台账 →</RouterLink>
            </div>
            <div v-if="systems?.length" v-dashboard-scroll class="roll-viewport" tabindex="0" aria-label="高风险隐患系统列表">
              <ul class="mini-list" data-dashboard-loop-original>
                <li v-for="item in systems" :key="item.filingId">
                  <component :is="canOpen(systemLocation(item.filingId)) ? RouterLink : 'span'" :to="systemLocation(item.filingId)">
                    <b>{{ item.systemName }}</b><span class="sys-count">{{ item.count }} 项</span>
                    <span class="sys-org">{{ item.organizationName }}</span>
                  </component>
                </li>
              </ul>
              <ul class="mini-list roll-copy" data-dashboard-loop-copy aria-hidden="true" hidden></ul>
            </div>
            <p v-else class="panel-status">{{ systems ? '当前范围暂无高风险隐患系统' : paneStatus }}</p>
          </section>
          <section class="card glass">
            <div class="card-head"><h3>实时预警</h3>
              <RouterLink class="link" :to="scopedAlertsLocation">查看全部 →</RouterLink>
            </div>
            <div v-if="alerts?.length" v-dashboard-scroll class="roll-viewport" tabindex="0" aria-label="实时预警列表">
              <ul class="mini-list" data-dashboard-loop-original>
                <li v-for="item in alerts" :key="item.id">
                  <component :is="canOpen(alertLocation(item.id)) ? RouterLink : 'span'" :to="alertLocation(item.id)">
                    <span class="alert-time">{{ dashboardTime(item.triggeredAt) }}</span>
                    <span class="tag" :class="item.severity === 'medium' ? 'tag-mid' : 'tag-' + item.severity">
                      {{ item.severity === 'high' ? '高' : item.severity === 'medium' ? '中' : '低' }}
                    </span>{{ item.systemName }}<span class="when">{{ alertStatuses[item.status] }}</span>
                    <span class="sys-org">{{ alertTypes[item.type] }} · {{ item.organizationName }}</span>
                  </component>
                </li>
              </ul>
              <ul class="mini-list roll-copy" data-dashboard-loop-copy aria-hidden="true" hidden></ul>
            </div>
            <p v-else class="panel-status">{{ alerts ? '当前范围暂无预警' : paneStatus }}</p>
          </section>
        </div>
      </section>

      <aside class="risk-right" aria-label="风险待办与分类">
        <section class="card glass">
          <div class="card-head"><h3>风险待办</h3><span class="cap">{{ scope }}</span></div>
          <component v-for="task in tasks" :key="task.label"
            :is="selected === null && canOpen(task.to) ? RouterLink : 'div'" :to="task.to" class="dg-row">
            <component :is="task.icon" class="ico" aria-hidden="true" />
            <span class="label">{{ task.label }}</span>
            <span class="val" :class="{ hot: task.urgent && task.value }">{{ dashboardNumber(task.value || undefined) }}</span>
          </component>
        </section>

        <section class="card glass">
          <div class="card-head"><h3>预警处置分布</h3><span class="cap">活跃 {{ dashboardNumber(activeAlerts) }}</span></div>
          <div class="donut-wrap">
            <div class="alert-donut">
              <svg viewBox="0 0 96 96" width="96" height="96" aria-hidden="true">
                <circle r="38" cx="48" cy="48" fill="none" stroke="rgba(18,36,70,.7)" stroke-width="12" />
                <circle v-for="item in segments.filter(segment => segment.count)" :key="item.status"
                  r="38" cx="48" cy="48" fill="none" :stroke="item.color" stroke-width="12" pathLength="100"
                  :stroke-dasharray="item.percent + ' ' + (100 - item.percent)" :stroke-dashoffset="-item.start"
                  transform="rotate(-90 48 48)" />
              </svg>
              <div class="donut-center"><b>{{ dashboardNumber(alertCounts?.total || undefined) }}</b><span>预警总量</span></div>
            </div>
            <ul v-if="segments.length" class="fs-legend">
              <li v-for="item in segments" :key="item.status">
                <i class="dot" :style="{ background: item.color }"></i><span class="label">{{ item.label }}</span>
                <span class="val">{{ item.count }} · {{ Math.round(item.percent) }}%</span>
              </li>
            </ul>
            <p v-else class="panel-status">{{ alertCounts ? '暂无预警处置记录' : paneStatus }}</p>
          </div>
        </section>

        <section v-for="chart in lists" :key="chart.key" class="card glass">
          <div class="card-head"><h3>{{ chart.title }}</h3><span class="cap">{{ chart.caption }}</span></div>
          <ul v-if="chart.rows.length" class="top-list">
            <li v-for="(item, index) in chart.rows" :key="item.code">
              <div class="top-head"><span class="no">{{ index + 1 }}</span><span class="label">{{ item.label }}</span>
                <span class="val">{{ item.count }}<small> 项<template v-if="item.high"> · 高危 {{ item.high }}</template></small></span>
              </div>
              <div class="bar" :class="{ amber: chart.key === 'industry' && index === 0 }">
                <i :style="{ width: item.count * 100 / (chart.rows[0]?.count || 1) + '%' }"></i>
              </div>
            </li>
          </ul>
          <p v-else class="panel-status">{{ analytics ? '当前范围暂无分类记录' : paneStatus }}</p>
        </section>
      </aside>
    </div>

    <footer class="risk-bottom">
      <div class="strip-inner">
        <component v-for="item in indicators" :key="item.label" :is="canOpen(item.path) ? RouterLink : 'div'" :to="item.path" class="strip-item">
          <component :is="item.icon" class="ico" aria-hidden="true" />
          <span class="label">{{ item.label }}</span><span class="val">{{ item.value ?? '暂无' }}</span>
        </component>
      </div>
    </footer>
  </section>
</template>

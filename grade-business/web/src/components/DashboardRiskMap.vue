<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { RouterLink, type RouteLocationRaw } from 'vue-router'
import DashboardSandboxMap from './DashboardSandboxMap.vue'
import { alertPath, alertTypes } from '../lib/alerts'
import { districtAsset, loadPublishedDistrictBoundaries } from '../lib/dashboardBoundary'
import { dashboardNumber, dashboardTime, type DashboardAlert } from '../lib/dashboard'
import { projectRiskSandbox, type DistrictOutline } from '../lib/dashboardGeo'
import type { DistrictResponse, ResponseSummary } from '../lib/districtResponse'
import { hazardPath } from '../lib/hazards'
import { riskLevels, riskColor, riskParts } from '../lib/dashboardSandboxRisk'
import type { DashboardArea } from '../lib/session'

const props = defineProps<{
  summary?: ResponseSummary; detail?: DistrictResponse; selected: string | null; scope: string
  area?: DashboardArea; locked: boolean; startingRank: boolean
  summaryLoading: boolean; summaryFailed: boolean; loading: boolean; failed: boolean
  openTotal?: number; closedTotal?: number; activeAlerts?: number; scopedAlertsLocation: RouteLocationRaw
  canOpen: (to: RouteLocationRaw) => boolean
  overview?: boolean
}>()
const emit = defineEmits<{ select: [district: string | null]; retrySummary: []; retryDetail: [] }>()

const mode = ref<'heat' | 'rank'>(props.startingRank ? 'rank' : 'heat')
const map = ref<InstanceType<typeof DashboardSandboxMap>>()
const heatShown = ref(mode.value === 'heat')
watch(mode, value => { if (value === 'heat') heatShown.value = true })
const outlines = shallowRef<DistrictOutline[]>([])
const codes = shallowRef(new Map<string, string>())
const mapError = ref('')
const metersPerUnit = ref<number>()
const districts = computed(() => new Map(props.summary?.items.map(item => [item.district, item])))
const rows = computed(() => outlines.value.map(outline => {
  const item = districts.value.get(outline.district)
  return { ...outline, code: codes.value.get(outline.district), value: item?.riskIndex, level: item?.riskLevel ?? 'none' as const, stats: item }
}))
const ranking = computed(() => {
  const known = props.area?.districts.filter(name => props.locked || name !== props.area?.name)
    ?? outlines.value.map(outline => outline.district)
  const names = new Set([...districts.value.keys(), ...known])
  const items = [...names].map(name => ({ name, record: districts.value.get(name) }))
  return props.overview ? items.sort((a, b) => (b.record?.riskIndex ?? -1) - (a.record?.riskIndex ?? -1)) : items
})
const detailsOpen = computed(() => props.selected !== null && !props.locked && mode.value === 'heat')
const detailCount = computed(() => props.detail?.counts)
const status = computed(() => props.loading ? '正在读取区县统计…'
  : props.failed ? '暂时无法读取区县统计' : '当前区县暂无记录')
const legend = [
  { level: 'high', label: '高', range: '66-100' }, { level: 'mid', label: '中', range: '34-65' },
  { level: 'low', label: '低', range: '1-33' }, { level: 'none', label: '无', range: '0' },
]

/**
 * 用途 从实际发布的同源边界计算监管会话可见的三维形状和比例尺。
 * 参数 无。
 * 返回值 本轮地图加载任务。
 */
async function loadMap() {
  if (!districtAsset) {
    mapError.value = '暂无辖区地图'
    return
  }
  mapError.value = ''
  try {
    const boundary = await loadPublishedDistrictBoundaries()
    const allowed = props.area ? boundary.filter(item => props.area?.districts.includes(item.district)) : boundary
    const { outlines: projected, metersPerUnit: scale } = projectRiskSandbox(boundary, props.area?.districts)
    metersPerUnit.value = scale
    outlines.value = projected
    codes.value = new Map(allowed.flatMap(item => item.code ? [[item.district, item.code] as const] : []))
    if (!projected.length) mapError.value = '暂无辖区地图'
  } catch {
    mapError.value = '暂时无法加载区县边界'
  }
}

/**
 * 用途 在热力图展示所选范围，重复选择同一辖区时恢复完整可见范围。
 * 参数 name 为地图或排行中的实际区县标准名称。
 * 返回值 无。
 */
function choose(name: string) {
  if (props.locked) return
  mode.value = 'heat'
  emit('select', name === props.selected ? null : name)
}

/**
 * 用途 关闭区县浮层并复位沙盘和整页统计。
 * 参数 无。
 * 返回值 无。
 */
function reset() {
  if (props.locked) return
  if (props.overview) mode.value = 'heat'
  if (props.selected === null) map.value?.resetView()
  else emit('select', null)
}

/**
 * 用途 在态势总览的地图或排名内用 Esc 返回完整可见范围。
 * 参数 event 是当前页面的键盘事件。
 * 返回值 无。
 */
function leaveSelection(event: KeyboardEvent) {
  if (event.key !== 'Escape' || event.defaultPrevented || props.locked ||
    (mode.value !== 'rank' && props.selected === null)) return
  event.preventDefault()
  mode.value = 'heat'
  reset()
}

/**
 * 用途 返回与预警记录关联的详情入口。
 * 参数 item 为当前区县中的预警记录。
 * 返回值 站内预警详情定位。
 */
function alertLocation(item: DashboardAlert) {
  return { path: alertPath, query: { alert: item.id } }
}
onMounted(() => {
  void loadMap()
  if (props.overview) document.addEventListener('keydown', leaveSelection)
})
onScopeDispose(() => document.removeEventListener('keydown', leaveSelection))
</script>

<template>
  <section class="map-card glass" :aria-label="overview ? '区县处置态势' : undefined">
    <header class="map-head">
      <h3>{{ overview ? '区县处置态势' : '区县风险热力' }}</h3>
      <div class="tabs" role="group" aria-label="区县展示方式">
        <button class="tab-btn" type="button" :class="{ active: mode === 'rank' }"
          :aria-pressed="mode === 'rank'" @click="mode = 'rank'">排名</button>
        <button class="tab-btn" type="button" :class="{ active: mode === 'heat' }"
          :aria-pressed="mode === 'heat'" @click="mode = 'heat'">{{ overview ? '地图' : '热力' }}</button>
      </div>
      <span class="cap grow">综合风险指数 · 逾期 40% / 高风险 40% / 待复测 20%</span>
      <button v-if="overview && !area && (summary?.withoutDistrict.systems ?? 0) > 0"
        class="chip mini" type="button" :aria-pressed="selected === ''" @click="choose('')">未归区</button>
      <button v-if="!locked" class="chip mini" type="button" @click="reset">{{ area ? '市域' : '全域' }}</button>
    </header>

    <div class="map-wrap" :class="{ 'map-stage': overview }">
      <DashboardSandboxMap v-if="rows.length && heatShown && !mapError" ref="map" v-show="mode === 'heat'"
        :rows="rows" :meters-per-unit="metersPerUnit" :selected="selected" :locked="locked"
        :active="mode === 'heat'" :overview-presentation="overview" risk-presentation @select="choose"
        @unavailable="mapError = '当前浏览器无法加载三维地图'" />
      <div v-if="mode === 'heat' && mapError" class="map-empty" role="status">
        {{ mapError }}
        <button class="chip mini" type="button" @click="mode = 'rank'">查看排名</button>
      </div>
      <div v-else-if="mode === 'heat' && !rows.length" class="map-empty" role="status">正在加载区县边界…</div>

      <template v-if="mode === 'heat' && rows.length && !mapError">
        <div class="map-legend">
          <div class="legend-title">综合风险指数</div>
          <div v-for="item in legend" :key="item.level" class="legend-row">
            <i class="swatch" :class="item.level"></i>{{ item.label }}<span>{{ item.range }}</span>
          </div>
        </div>
        <div class="map-stats">
          <div class="stats-head">{{ selected === null && !overview ? scope + '概览' : scope }}</div>
          <div v-if="selected === null" class="stats-row"><span>{{ overview ? '涉及区县' : '区县' }}</span><b>{{ rows.length }}</b></div>
          <div v-else class="stats-row"><span>{{ overview ? '综合风险指数' : '风险指数' }}</span>
            <b>{{ dashboardNumber(detailCount?.systems ? detail?.risk.index : undefined) }}</b>
          </div>
          <div class="stats-row"><span>未销项隐患</span><b :class="{ red: !overview }">{{ dashboardNumber(openTotal) }}</b></div>
          <div v-if="overview" class="stats-row"><span>已办结</span><b class="red">{{ dashboardNumber(closedTotal) }}</b></div>
          <div v-else-if="selected === null" class="stats-row"><span>其中高风险</span>
            <b class="red">{{ dashboardNumber(detailCount?.high) }}</b>
          </div>
          <div class="stats-row"><span>活跃预警</span><b :class="{ red: overview }">{{ dashboardNumber(activeAlerts) }}</b></div>
        </div>
        <div class="map-caption">风险指数 = 逾期 40% / 高风险 40% / 待复测 20%，满分 100。<br>区域隆起高度即该区县的综合风险指数<template v-if="!overview">，点击可下钻</template>。</div>
        <div class="rotate-hint">北向上 · 拖动旋转 · 滚轮缩放<template v-if="!overview"> · 点击区县查看详情</template></div>
      </template>

      <div v-if="mode === 'rank'" class="rank-overlay">
        <div class="rank-head">{{ area ? area.name : '全域' }}风险排行</div>
        <p v-if="summaryFailed" class="panel-status" role="status">
          暂时无法更新区县排名 <button class="chip mini" type="button" @click="emit('retrySummary')">重试</button>
        </p>
        <p v-else-if="summaryLoading && !summary" class="panel-status" role="status">正在读取区县排名…</p>
        <ul class="rank-list">
          <li v-for="(row, index) in ranking" :key="row.name">
            <button class="rank-row" type="button" :disabled="locked"
              :aria-pressed="selected === row.name" @click="choose(row.name)">
              <span class="no">{{ row.record ? index + 1 : '—' }}</span>
              <span class="nm">{{ row.name }}<span v-if="row.record && !overview" class="sys">
                · {{ row.record.systems }} 个系统 · 未销项 {{ row.record.high + row.record.medium + row.record.low }}
              </span></span>
              <span class="idx" :style="{ color: riskColor(row.record?.riskLevel) }">
                {{ dashboardNumber(row.record?.riskIndex) }}
              </span>
              <span v-if="row.record" class="lv-pill" :class="row.record.riskLevel">{{ riskLevels[row.record.riskLevel] }}</span>
            </button>
          </li>
          <li v-if="!ranking.length && !summaryLoading && !summaryFailed" class="panel-status">暂无区县风险数据</li>
        </ul>
      </div>

      <aside v-if="detailsOpen" class="detail-panel" :aria-label="scope + '详情'">
        <div class="detail-head">
          <span class="dname">{{ scope }}</span>
          <span v-if="detailCount?.systems && detail" class="lv-pill" :class="detail.risk.level">{{ riskLevels[detail.risk.level] }}</span>
          <button type="button" class="close" aria-label="关闭区县详情" @click="reset">×</button>
        </div>
        <p v-if="failed || !detail" class="panel-status" role="status">{{ status }}
          <button v-if="failed" class="chip mini" type="button" @click="emit('retryDetail')">重试</button>
        </p>
        <template v-if="detail">
          <div class="idx-line">
            <span class="big" :style="{ color: riskColor(detail.risk.level) }">
              {{ dashboardNumber(detailCount?.systems ? detail.risk.index : undefined) }}
            </span>
            <span class="cap">综合风险指数<br>涉及系统 {{ dashboardNumber(detailCount?.systems || undefined) }}</span>
          </div>
          <div class="comp">
            <div v-for="part in riskParts(detailCount?.systems ? detailCount : undefined)" :key="part.key" class="row">
              <span class="label">{{ part.name }} <i class="w">×{{ part.weight }}%</i></span>
              <span class="bar" :class="part.key"><i :style="{ width: Math.min(100, part.points ?? 0) + '%' }"></i></span>
              <span class="pct">{{ dashboardNumber(part.count) }}</span>
            </div>
          </div>

          <div class="sec-title">重点隐患（{{ detail.tasks.length }}）</div>
          <ul class="mini-list">
            <li v-for="item in detail.tasks" :key="item.id" :class="{ clickable: canOpen(hazardPath(item.id)) }">
              <component :is="canOpen(hazardPath(item.id)) ? RouterLink : 'span'" :to="hazardPath(item.id)">
                <span class="tag" :class="item.severity === 'medium' ? 'tag-mid' : 'tag-' + item.severity">
                  {{ item.severity === 'high' ? '高' : item.severity === 'medium' ? '中' : '低' }}
                </span>{{ item.title }}<span class="when">截止 {{ item.deadline }}</span>
              </component>
            </li>
            <li v-if="!detail.tasks.length" class="panel-status">暂无重点隐患</li>
          </ul>
          <div class="sec-title">最新预警（{{ detail.alerts.length }}）</div>
          <ul class="mini-list">
            <li v-for="item in detail.alerts" :key="item.id" :class="{ clickable: canOpen(alertLocation(item)) }">
              <component :is="canOpen(alertLocation(item)) ? RouterLink : 'span'" :to="alertLocation(item)">
                {{ alertTypes[item.type] ?? item.type }} · {{ item.systemName }}
                <span class="when">{{ dashboardTime(item.triggeredAt) }}</span>
              </component>
            </li>
            <li v-if="!detail.alerts.length" class="panel-status">暂无预警</li>
          </ul>
          <div class="sec-title">处置建议</div>
          <ul class="mini-list">
            <li v-if="detail.duty.total">{{ detail.duty.conclusion }}</li>
            <li v-else class="panel-status">当前范围暂无待办事项</li>
            <li v-if="detail.alerts.length" class="clickable">
              <RouterLink :to="scopedAlertsLocation">查看{{ scope }}预警 →</RouterLink>
            </li>
          </ul>
        </template>
      </aside>
    </div>
  </section>
</template>

<style src="./dashboardOverviewMap.css"></style>

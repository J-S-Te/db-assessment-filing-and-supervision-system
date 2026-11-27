<script setup lang="ts">
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { dashboardNumber } from '../lib/dashboard'
import {
  dashboardColors, dashboardTrend, dashboardTrendOption, dashboardTrendRanges, dashboardTrendSeries,
  type DashboardDay, type DashboardSeriesKey, type DashboardTrendRange,
} from '../lib/dashboardMetrics'

const DashboardChart = defineAsyncComponent(() => import('./DashboardChart.vue'))
const props = defineProps<{ history?: DashboardDay[]; loading: boolean; failed?: boolean }>()
const range = defineModel<DashboardTrendRange>({ required: true })
const active = ref(0)
const pinned = ref('')
const hidden = ref(new Set<DashboardSeriesKey>())
const chart = computed(() => dashboardTrend(props.history ?? [], range.value))
const option = computed(() => dashboardTrendOption(chart.value, hidden.value))
const firstDate = computed(() => chart.value.days[0]?.date)
const lastDate = computed(() => chart.value.days.at(-1)?.date)
const selected = computed(() => chart.value.days.find(day => day.date === pinned.value) ?? chart.value.days[active.value])
const hasActivity = computed(() => Object.values(chart.value.totals).some(value => value > 0))
watch(() => chart.value.days.map(day => day.date).join(), () => {
  active.value = Math.max(0, chart.value.days.length - 1)
  if (!chart.value.days.some(day => day.date === pinned.value)) pinned.value = ''
}, { immediate: true })

/**
 * 用途 使用键盘或图表指针查看所选日期的业务记录。
 * 参数 index 为拟查看日期在所选窗口中的索引。
 * 返回值 无。
 */
function selectDay(index: number) {
  if (pinned.value) return
  active.value = Math.max(0, Math.min(index, chart.value.days.length - 1))
}

/**
 * 用途 固定趋势读数，或再次点击同一日期恢复随指针变化。
 * 参数 day 为点击的真实业务日期。
 * 返回值 无。
 */
function toggleDay(day?: DashboardDay) {
  if (day) pinned.value = pinned.value === day.date ? '' : day.date
}

/**
 * 用途 切换一条业务线的显示状态。
 * 参数 key 为已有的趋势统计序列标识。
 * 返回值 无。
 */
function toggleSeries(key: DashboardSeriesKey) {
  if (!hidden.value.delete(key)) hidden.value.add(key)
}
</script>

<template>
  <div class="trend-heading">
    <h2>业务趋势</h2>
    <div class="range-pills" role="group" aria-label="趋势日期范围">
      <button v-for="size in dashboardTrendRanges" :key="size" type="button" :aria-pressed="range === size"
        @click="range = size">{{ size }} 日</button>
    </div>
  </div>
  <p v-if="!history?.length" class="trend-empty">
    {{ failed ? '暂时无法读取业务趋势。' : loading ? '正在读取业务趋势…' : '暂无业务趋势。' }}
  </p>
  <div v-else class="trend-body">
    <p v-if="firstDate && lastDate" class="trend-period" role="status">
      <time :datetime="firstDate">{{ firstDate }}</time>
      至
      <time :datetime="lastDate">{{ lastDate }}</time>
    </p>
    <div tabindex="0" class="trend-plot" role="group" aria-label="每日业务趋势，左右键查看日期，回车固定"
      @keydown.left.prevent="selectDay(active - 1)" @keydown.right.prevent="selectDay(active + 1)"
      @keydown.home.prevent="selectDay(0)" @keydown.end.prevent="selectDay(chart.days.length - 1)"
      @keydown.enter.prevent="toggleDay(selected)" @keydown.esc="pinned = ''">
      <DashboardChart :option="option" @point="selectDay" @point-click="toggleDay(chart.days[$event])" />
      <p v-if="!hasActivity" class="trend-empty">近 {{ range }} 日暂无业务记录。</p>
      <p v-else-if="!option.series.length" class="trend-empty">已隐藏全部曲线</p>
    </div>
    <div v-if="selected && hasActivity" class="trend-reading" role="group" aria-label="趋势图例和日期">
      <button type="button" class="trend-date" :aria-pressed="!!pinned"
        :title="pinned ? '取消固定日期' : '固定当前日期'" @click="toggleDay(selected)">
        <time :datetime="selected.date">{{ selected.date.slice(5) }}</time>
        <span v-if="pinned" class="trend-date-state">已固定</span>
      </button>
      <button v-for="series in dashboardTrendSeries" :key="series.key" type="button"
        :aria-pressed="!hidden.has(series.key)" @click="toggleSeries(series.key)">
        <i :style="{ background: dashboardColors[series.tone] }" aria-hidden="true"></i>
        {{ series.label }} <strong>{{ dashboardNumber(selected[series.key]) }}</strong>
      </button>
    </div>
  </div>
</template>

<style scoped>
.trend-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: .35em; }
.range-pills { display: inline-flex; gap: .1em; border: 1px solid #29455f; border-radius: 4px; padding: .12em; }
.range-pills button {
  padding: .22em .43em; border: 1px solid transparent; border-radius: 3px;
  color: var(--dashboard-muted); background: none; font-size: .77em; white-space: nowrap;
}
.range-pills button[aria-pressed="true"] { color: #c8edff; background: #183d60; border-color: #3d7192; }
.range-pills button:hover { color: #f0faff; }
.trend-body { display: flex; flex: 1; flex-direction: column; gap: .35em; min-height: 0; padding-top: .35em; }
.trend-period { margin: 0; color: var(--dashboard-muted); font-size: .72em; font-variant-numeric: tabular-nums; }
.trend-plot { position: relative; flex: 1; min-height: 5.5em; }
.trend-empty { color: var(--dashboard-muted); font-size: .83em; }
.trend-plot .trend-empty { position: absolute; inset: 37% 0 auto; text-align: center; pointer-events: none; }
.trend-reading { display: flex; align-items: center; flex-wrap: wrap; gap: .28em .65em; color: var(--dashboard-muted); font-size: .72em; }
.trend-reading time { color: #e7f6ff; font-weight: 650; }
.trend-reading button {
  display: inline-flex; align-items: center; gap: .28em; padding: .08em .16em;
  background: transparent; border: 1px solid transparent; border-radius: 3px; color: inherit;
}
.trend-reading button:hover { background: #1a3753; color: #effaff; }
.trend-reading button[aria-pressed='false']:not(.trend-date) { opacity: .64; text-decoration: line-through; }
.trend-date-state { color: #8ed6fc; }
.trend-reading i { width: .52em; aspect-ratio: 1; border-radius: 50%; flex: none; }
.trend-reading strong { color: #e7f6ff; }
</style>

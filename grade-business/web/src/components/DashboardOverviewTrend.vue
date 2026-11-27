<script setup lang="ts">
import { computed } from 'vue'
import { dashboardTrendRanges, type DashboardDay, type DashboardTrendRange } from '../lib/dashboardMetrics'

const props = defineProps<{
  history?: DashboardDay[]; loading: boolean; failed: boolean
  comparison: readonly { label: string; period: { title: string }; year: { title: string } }[]
}>()
const range = defineModel<DashboardTrendRange>({ required: true })
const series = [
  { key: 'filingsCreated', label: '新增备案', color: '#35d6ff' },
  { key: 'alertsTriggered', label: '触发预警', color: '#ffb84d' },
  { key: 'hazardsClosed', label: '隐患销项', color: '#3ddc97' },
] as const
const days = computed(() => props.history?.slice(-range.value) ?? [])
const peak = computed(() => Math.max(0, ...days.value.flatMap(day => series.map(item => day[item.key]))))
const ticks = computed(() => [...new Set([0, .25, .5, .75, 1].map(fraction =>
  days.value[Math.round(fraction * (days.value.length - 1))]?.date).filter(Boolean))])
const plot = computed(() => series.map(item => {
  const points = days.value.map((day, index) => ({
    x: 4 + 312 * index / Math.max(1, days.value.length - 1),
    y: 92 - 88 * day[item.key] / Math.max(1, peak.value),
  }))
  const comparison = props.comparison.find(candidate => candidate.label === item.label)
  return { ...item, end: points.at(-1), coordinates: points.map(point => `${point.x},${point.y}`).join(' '),
    caption: [comparison?.period.title, comparison?.year.title].filter(Boolean).join(' · ') }
}))
</script>

<template>
  <header class="overview-trend-head">
    <h2>业务趋势</h2>
    <div class="trend-periods" role="group" aria-label="趋势日期范围">
      <button v-for="daysInRange in dashboardTrendRanges" :key="daysInRange" type="button"
        :aria-pressed="range === daysInRange" @click="range = daysInRange">{{ daysInRange }} 日</button>
    </div>
  </header>
  <div class="overview-trend-legend">
    <span v-for="item in plot" :key="item.key" :title="item.caption">
      <i :style="{ background: item.color }" aria-hidden="true"></i>{{ item.label }}
    </span>
  </div>
  <div v-if="days.length" class="overview-trend-chart">
    <span class="trend-peak">峰值 {{ peak }}</span>
    <svg viewBox="0 0 320 96" preserveAspectRatio="none" role="img"
      :aria-label="`${days[0]?.date} 至 ${days.at(-1)?.date} 的业务趋势`">
      <path d="M0 24H320 M0 48H320 M0 72H320" stroke="#488cdc24" />
      <template v-for="item in plot" :key="item.key">
        <polyline :points="item.coordinates" fill="none" :stroke="item.color" stroke-width="1.8"
          vector-effect="non-scaling-stroke"><title>{{ item.label }}</title></polyline>
        <circle v-if="item.end" :cx="item.end.x" :cy="item.end.y" r="2.2" :fill="item.color" />
      </template>
    </svg>
    <div class="trend-ticks"><time v-for="tick in ticks" :key="tick" :datetime="tick">{{ tick?.slice(5) }}</time></div>
  </div>
  <p v-else class="overview-trend-state" role="status">
    {{ failed ? '暂时无法读取业务趋势。' : loading ? '正在读取业务趋势…' : '暂无业务趋势。' }}
  </p>
</template>

<style scoped>
.overview-trend-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 8px; }
.overview-trend-head h2 { display: flex; align-items: center; gap: 7px; margin: 0; font-size: 13px; letter-spacing: .05em; }
.overview-trend-head h2::before { content: ''; width: 3px; height: 13px; border-radius: 2px;
  background: linear-gradient(180deg, var(--accent), transparent); }
.trend-periods { display: flex; gap: 4px; }
.trend-periods button { padding: 2px 10px; border: 1px solid var(--line); border-radius: 999px;
  color: var(--text-dim); background: var(--panel-2); font-size: 11px; white-space: nowrap; cursor: pointer; }
.trend-periods button[aria-pressed='true'] { border-color: #35d6ff99; background: #35d6ff29; color: #c9f2ff; }
.overview-trend-legend { display: flex; flex-wrap: wrap; gap: 4px 12px; margin-bottom: 4px; color: var(--text-dim); font-size: 11px; }
.overview-trend-legend i { display: inline-block; width: 7px; height: 7px; margin-right: 4px; border-radius: 50%; }
.overview-trend-chart { position: relative; flex: 1; min-height: 90px; }
.overview-trend-chart svg { display: block; width: 100%; height: calc(100% - 18px); }
.trend-peak, .trend-ticks { color: var(--text-dim); font-size: 9.5px; }
.trend-peak { position: absolute; left: 4px; top: 2px; }
.trend-ticks { display: flex; justify-content: space-between; padding: 0 2px; line-height: 16px; }
.overview-trend-state { margin: 0; color: var(--text-dim); font-size: 11.5px; }
@media (max-height: 820px) { .overview-trend-chart { min-height: 60px; } }
</style>

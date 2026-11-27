<script setup lang="ts">
import { computed, defineAsyncComponent, ref } from 'vue'
import { dashboardNumber } from '../lib/dashboard'
import { dashboardColors, dashboardRingOption, metricPercent, type MetricSlice } from '../lib/dashboardMetrics'

const DashboardChart = defineAsyncComponent(() => import('./DashboardChart.vue'))
const props = defineProps<{ slices: MetricSlice[]; label: string; loading: boolean; center?: string }>()
const total = computed(() => props.slices.reduce((sum, current) => sum + current.count, 0))
const option = computed(() => dashboardRingOption(props.slices))
const picked = ref('')
const hoverSlice = ref('')
const focused = ref('')
const active = computed(() => props.slices.find(slice => slice.label ===
  (focused.value || hoverSlice.value || picked.value)))
const highlight = computed(() => option.value.series[0]?.data.findIndex(item => item.name ===
  (focused.value || picked.value)) ?? -1)
const percentage = computed(() => metricPercent(active.value?.count, total.value))

/**
 * 用途 切换环图当前固定显示的分类。
 * 参数 name 为真实统计分类名称，再次选择同一分类恢复总数。
 * 返回值 无。
 */
function selectSlice(name: string) {
  picked.value = picked.value === name ? '' : name
  hoverSlice.value = ''
  focused.value = ''
}
</script>

<template>
  <div v-if="slices.length" class="distribution">
    <div class="ring">
      <DashboardChart :option="option" :highlight="highlight"
        @slice-hover="hoverSlice = $event" @slice-click="selectSlice" />
      <div class="ring-value" aria-live="polite" aria-atomic="true">
        <strong>{{ active ? dashboardNumber(active.count) : center ?? dashboardNumber(total) }}</strong>
        <span>{{ active?.label ?? label }}<template v-if="percentage !== undefined"> · {{ percentage }}%</template></span>
      </div>
    </div>
    <ul class="legend" :aria-label="label + '分类'">
      <li v-for="slice in slices" :key="slice.label">
        <button type="button" :aria-pressed="picked === slice.label" @click="selectSlice(slice.label)"
          @focus="focused = slice.label" @blur="focused = ''"
          @pointerenter="focused = slice.label" @pointerleave="focused = ''">
          <span class="legend-name"><i :style="{ background: dashboardColors[slice.tone] }" aria-hidden="true"></i>
            {{ slice.label }}</span><strong>{{ dashboardNumber(slice.count) }}</strong>
        </button>
      </li>
    </ul>
  </div>
  <p v-else class="dashboard-empty">{{ loading ? '正在读取统计…' : '暂时无法读取统计。' }}</p>
</template>

<style scoped>
.distribution { display: flex; align-items: center; gap: .65em 1em; flex: 1; min-height: 0; }
.ring { position: relative; width: min(43%, 9.2em); aspect-ratio: 1; flex: none; }
.ring-value { position: absolute; inset: 0; display: grid; place-content: center; text-align: center; pointer-events: none; }
.ring strong { color: #f3f8ff; font-size: 1.5em; line-height: 1.15; font-variant-numeric: tabular-nums; }
.ring span { color: var(--dashboard-muted); font-size: .7em; margin-top: .24em; }
.legend { display: grid; gap: .18em; flex: 1; margin: 0; padding: 0; min-width: 0; font-size: .85em; list-style: none; }
.legend button { display: flex; align-items: center; justify-content: space-between; gap: .45em; width: 100%;
  padding: .15em .28em; border: 1px solid transparent; border-radius: 3px;
  background: transparent; color: inherit; text-align: left; }
.legend button:hover, .legend button[aria-pressed='true'] { background: #17344c; border-color: #32607e; }
.legend-name { display: inline-flex; align-items: center; gap: .45em; }
.legend-name i { width: .52em; aspect-ratio: 1; border-radius: 2px; flex: none; }
.legend strong { font-variant-numeric: tabular-nums; font-weight: 650; }
.dashboard-empty { color: var(--dashboard-muted); font-size: .85em; }
</style>

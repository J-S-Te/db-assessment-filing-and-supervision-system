<script setup lang="ts">
import { computed } from 'vue'
import { dashboardNumber, dashboardPercent, type DashboardRisks } from '../lib/dashboard'
import { dashboardColors } from '../lib/dashboardMetrics'

const props = defineProps<{ risks?: DashboardRisks; loading: boolean }>()
const levels = [
  { key: 'high', label: '高风险', color: dashboardColors.red },
  { key: 'medium', label: '中风险', color: dashboardColors.amber },
  { key: 'low', label: '低风险', color: dashboardColors.blue },
] as const
const total = computed(() => props.risks && props.risks.high + props.risks.medium + props.risks.low)
</script>

<template>
  <div class="risk-body">
    <div class="risk-total">
      <span>未销项隐患</span>
      <strong>{{ dashboardNumber(total) }}</strong>
    </div>
    <div class="risk-track" aria-hidden="true">
      <span v-for="level in levels" :key="level.key"
        :style="{ background: level.color, flexGrow: risks?.[level.key] ?? 0 }"></span>
    </div>
    <dl class="risk-levels" aria-label="未销项隐患风险等级">
      <div v-for="level in levels" :key="level.key" :style="{ '--level-color': level.color }">
        <dt><i aria-hidden="true"></i>{{ level.label }}</dt>
        <dd>{{ dashboardNumber(risks?.[level.key]) }}<small v-if="total">{{ dashboardPercent(risks?.[level.key], total) }}</small></dd>
      </div>
    </dl>
    <p v-if="!total" class="dashboard-empty">
      {{ total === 0 ? '当前没有未销项隐患。' : loading ? '正在读取风险数据…' : '暂时无法读取风险数据。' }}
    </p>
  </div>
</template>

<style scoped>
.risk-body {
  flex: 1; display: flex; flex-direction: column; justify-content: center; gap: .8em;
  padding: .75em 0;
}
.risk-total { display: flex; flex-wrap: wrap; align-items: baseline; gap: .35em 1em; }
.risk-total span { color: #c0e2f8; font-size: 1em; }
.risk-total strong {
  color: #f0faff; font-size: 3.1em; letter-spacing: -.04em;
  line-height: 1.05; font-variant-numeric: tabular-nums;
}
.risk-track { display: flex; min-height: .5em; overflow: hidden; background: #20384d; border-radius: 4px; }
.risk-track span { flex-basis: 0; }
.risk-levels { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .55em; }
.risk-levels > div {
  padding: .55em .6em .4em; border-top: 1px solid #34506a;
  background: linear-gradient(180deg, #142a44c0, #10213990);
}
.risk-levels dt {
  display: flex; align-items: center; gap: .45em; white-space: nowrap;
  color: var(--dashboard-muted); font-size: .85em;
}
.risk-levels i { width: .52em; aspect-ratio: 1; flex: none; border-radius: 50%; background: var(--level-color); }
.risk-levels dd { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0 .5em; color: #edf7ff; font-size: 1.65em; font-weight: 720; }
.risk-levels small { color: var(--dashboard-muted); font-size: .5em; }
.dashboard-empty { color: var(--dashboard-muted); font-size: .82em; }
</style>

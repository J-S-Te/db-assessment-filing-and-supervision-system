<script setup lang="ts">
import type { districtHeatMap } from '../lib/dashboardGeo'
import { dashboardNumber } from '../lib/dashboard'

defineProps<{
  heatmap: ReturnType<typeof districtHeatMap>
  selected: string | null
  metricLabel: string
  showLabels?: boolean
  relief?: boolean
}>()
const emit = defineEmits<{ select: [district: string] }>()
</script>

<template>
  <svg :viewBox="heatmap.viewBox" class="geo-map" :class="{ 'geo-map-relief': relief }"
    role="group" aria-label="区县风险地图">
    <g class="map-districts">
      <path v-for="shape in heatmap.shapes" :key="shape.district" :d="shape.path"
        :class="[shape.intensity, { selected: shape.district === selected }]" fill-rule="evenodd"
        role="button" tabindex="0" :aria-label="shape.district" :aria-pressed="shape.district === selected"
        @click="emit('select', shape.district)" @keydown.enter.prevent="emit('select', shape.district)"
        @keydown.space.prevent="emit('select', shape.district)">
        <title>{{ shape.district }} · {{ shape.value === undefined ? '无匹配统计'
          : metricLabel + ' ' + dashboardNumber(shape.value) + ' 项' }}</title>
      </path>
    </g>
    <g v-if="showLabels" class="map-labels" aria-hidden="true">
      <text v-for="shape in heatmap.shapes" :key="shape.district"
        :x="shape.label[0]" :y="shape.label[1]" :class="{ selected: shape.district === selected }"
        @click="emit('select', shape.district)">
        {{ shape.district }}
      </text>
    </g>
  </svg>
</template>

<style scoped>
.geo-map-relief .map-districts {
  filter: drop-shadow(0 1px 0 #5894be) drop-shadow(0 2px 0 #2d5d85)
    drop-shadow(0 3px 0 #183a5c) drop-shadow(0 5px 4px #030d1cbf);
}
.geo-map path {
  fill: var(--geo-fill); stroke: #6a91b3; stroke-width: 1;
  vector-effect: non-scaling-stroke; cursor: pointer;
}
/* 区县边界的描边提供键盘焦点反馈，避免用外接矩形覆盖相邻区县。 */
.geo-map path:focus { outline: none; }
.geo-map path:is(.selected, :hover, :focus-visible) { stroke: #e9f8ff; stroke-width: 2.3; }
.geo-map-relief path { stroke: #83b6d8; }
.geo-map-relief .unmatched { --geo-fill: #294a69; }
.geo-map-relief .zero { --geo-fill: #2b5a76; }
.geo-map-relief .light { --geo-fill: #397ea9; }
.geo-map-relief .medium { --geo-fill: #a46b41; }
.geo-map-relief .strong { --geo-fill: #d47467; }
.map-labels { pointer-events: none; }
.map-labels text {
  text-anchor: middle; dominant-baseline: central; font-size: 16px; font-weight: 650;
  paint-order: stroke; stroke: #11263c; stroke-width: 2.6px; stroke-linejoin: round; fill: #dceeff;
  pointer-events: all; cursor: pointer; user-select: none;
}
.map-labels text:hover { fill: white; }
.map-labels text.selected { fill: white; stroke: #052043; }
@media (prefers-reduced-motion: no-preference) {
  .geo-map path { transition: stroke .15s ease, stroke-width .15s ease; }
}
.unmatched { --geo-fill: #243a52; }
.zero { --geo-fill: #30506c; }
.light { --geo-fill: #496ea5; }
.medium { --geo-fill: #9d6747; }
.strong { --geo-fill: #f27962; }
</style>

<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MapPin } from '@lucide/vue'
import DashboardCityScene from './DashboardCityScene.vue'
import DashboardDistrictMap from './DashboardDistrictMap.vue'
import { api, errorText, isAbort } from '../lib/api'
import { dashboardNumber, dashboardPath } from '../lib/dashboard'
import { loadPublishedDistrictBoundaries } from '../lib/dashboardBoundary'
import {
  districtBoundaryMaxBytes, districtHeatMap, projectDistrictBoundaries, readDistrictBoundaries,
  type DistrictMetric, type DistrictRiskSummary,
} from '../lib/dashboardGeo'
import { loadDistrictBoundaryFile, saveDistrictBoundaryFile } from '../lib/dashboardGeoStorage'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import type { DashboardArea } from '../lib/session'

const props = defineProps<{ compact?: boolean; area?: DashboardArea; locked?: boolean }>()
const route = useRoute()
const router = useRouter()
const summary = shallowRef<DistrictRiskSummary>()
const boundaries = shallowRef<ReturnType<typeof readDistrictBoundaries>>([])
const compactSelection = ref('')
const allDistricts = '\u0000'
const metric = ref<DistrictMetric>('all')
const error = ref('')
const boundaryWarning = ref('')
const importing = ref(false)
let request: AbortController | undefined

const metrics = {
  all: '全部未销项', high: '高风险', medium: '中风险', low: '低风险',
}
const paths = computed(() => projectDistrictBoundaries(boundaries.value, props.area?.districts))
const map = computed(() => paths.value.length && summary.value
  ? districtHeatMap(paths.value, summary.value.items, metric.value) : undefined)
const districts = computed(() => {
  const current = summary.value
  if (!current) return []
  return current.withoutDistrict.systems
    ? [...current.items, { ...current.withoutDistrict, district: '' }]
    : current.items
})
const highlightedDistricts = computed(() => summary.value?.items.slice(0, 4) ?? [])
const districtScope = computed(() => {
  if (props.locked) return props.area?.name ?? null
  if (props.compact) return compactSelection.value || null
  const district = route.query.district
  return typeof district === 'string' && (!props.area || props.area.districts.includes(district)) ? district : null
})
const districtOptions = computed(() => [...new Set([
  ...(summary.value?.items.map(item => item.district) ?? []),
  ...paths.value.map(path => path.district),
  ...(districtScope.value ? [districtScope.value] : []),
])])
const selected = computed(() => districtScope.value ?? '')
const selectedShape = computed(() => map.value?.shapes.find(shape => shape.district === selected.value))
const selectedDistrict = computed(() => summary.value?.items.find(item => item.district === selected.value))
const shownDistricts = computed(() => districtScope.value === null ? districts.value
  : districts.value.filter(item => item.district === districtScope.value))

/**
 * 用途 选择有效的区县，并让明细入口可通过地址重开。
 * 参数 district 为区县，空字符串表示未归区，allDistricts 表示全域。
 * 返回值 无。
 */
function selectDistrict(district: string) {
  if (props.locked) return
  if (props.area && district !== allDistricts && !props.area.districts.includes(district)) return
  if (props.compact) {
    compactSelection.value = district === allDistricts ? '' : district
  } else {
    void router.replace({ query: { ...route.query, district: district === allDistricts ? undefined : district } })
  }
}

/**
 * 用途 定期读取真实区县风险快照并保留上次成功结果。
 * 参数 无。
 * 返回值 本轮查询结束后的任务。
 */
async function load() {
  if (request) return
  const active = new AbortController()
  request = active
  const timeout = window.setTimeout(() => active.abort(), 12_000)
  try {
    summary.value = await api<DistrictRiskSummary>('/dashboard/risk-by-district?limit=200', { signal: active.signal })
    error.value = ''
  } catch (reason) {
    if (request === active) error.value = isAbort(reason) ? '读取区县风险超时。' : errorText(reason)
  } finally {
    window.clearTimeout(timeout)
    if (request === active) request = undefined
  }
}

defineExpose({ refresh: load })

/**
 * 用途 校验浏览器内的区县边界文件并在同源本地保存。
 * 参数 event 为本地文件选择事件。
 * 返回值 本次导入处理结束后的任务。
 */
async function importBoundary(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  boundaryWarning.value = ''
  if (file.size > districtBoundaryMaxBytes) {
    boundaryWarning.value = '区县边界文件不得超过 2 MiB。'
    return
  }
  importing.value = true
  try {
    const source = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
    boundaries.value = readDistrictBoundaries(source)
    if (!saveDistrictBoundaryFile(source)) boundaryWarning.value = '浏览器无法保存边界，当前页面会话仍可使用。'
  } catch (reason) {
    boundaryWarning.value = reason instanceof Error && !(reason instanceof TypeError)
      ? reason.message : '无法读取文件，请使用 UTF-8 编码的 GeoJSON。'
  } finally {
    importing.value = false
  }
}

/**
 * 用途 清除当前浏览器的边界并返回真实区县统计表。
 * 参数 无。
 * 返回值 无。
 */
function clearBoundary() {
  boundaries.value = []
  boundaryWarning.value = saveDistrictBoundaryFile(null) ? '' : '当前会话已移除边界，浏览器无法清除原有存储。'
}

/**
 * 用途 恢复同源浏览器曾导入的区县边界。
 * 参数 无。
 * 返回值 无，缓存无效时保留列表并提示重新导入。
 */
function restoreBoundary() {
  const { source, stored } = loadDistrictBoundaryFile()
  if (!source) return
  try {
    boundaries.value = readDistrictBoundaries(source)
    if (!stored) boundaryWarning.value = '浏览器无法保存边界，当前页面会话仍可使用。'
  } catch {
    boundaryWarning.value = '当前浏览器的边界文件无法读取，请重新导入。'
  }
}

onMounted(() => {
  if (props.area) {
    void loadPublishedDistrictBoundaries().then(result => {
      boundaries.value = result
    }).catch(() => { boundaryWarning.value = '辖区地图暂时无法读取。' })
  } else {
    restoreBoundary()
  }
  void load()
})
refreshBusinessOnFocus(() => void load(), 30_000)
onScopeDispose(() => {
  const active = request
  request = undefined
  active?.abort()
})
</script>

<template>
  <div class="district-risk" :class="{ compact }">
    <DashboardCityScene v-if="compact && !map" />
    <div v-if="!compact && !area" class="geo-toolbar">
      <label class="geo-upload">
        <input type="file" accept=".geojson,.json" :disabled="importing" @change="importBoundary" />
        <span>{{ importing ? '正在读取边界…' : '导入区县 GeoJSON' }}</span>
      </label>
      <button v-if="boundaries.length || boundaryWarning" type="button" :disabled="importing" @click="clearBoundary()">
        移除本地边界
      </button>
      <small>本浏览器使用，WGS84 区县面须在 properties.name 中填写名录区县全称。最多 2 MiB。</small>
    </div>
    <p v-if="boundaryWarning" class="geo-notice" role="status">{{ boundaryWarning }}</p>
    <p v-if="error" class="geo-notice" role="alert">{{ error }}
      <button type="button" @click="load()">重试</button>
    </p>
    <p v-else-if="!summary" role="status">正在读取区县风险…</p>
    <p v-if="!boundaries.length && !compact && !area" class="geo-notice">当前浏览器未导入区县边界，以下按名录区县展示实际风险。</p>
    <div v-if="!compact && (districtOptions.length || summary?.withoutDistrict.systems || districtScope === '')"
      class="geo-filters">
      <label v-if="map">热力指标
        <select v-model="metric">
          <option v-for="(label, value) in metrics" :key="value" :value="value">{{ label }}</option>
        </select>
      </label>
      <label v-if="!locked">区县
        <select :value="districtScope ?? allDistricts"
          @change="selectDistrict(($event.target as HTMLSelectElement).value)">
          <option :value="allDistricts">{{ area ? '市域' : '全部区县' }}</option>
          <option v-if="!area && (summary?.withoutDistrict.systems || districtScope === '')" value="">未归区</option>
          <option v-for="name in districtOptions" :key="name" :value="name">{{ name }}</option>
        </select>
      </label>
    </div>
    <template v-if="map">
      <DashboardDistrictMap :heatmap="map" :selected="selected" :metric-label="metrics[metric]" :show-labels="Boolean(area)"
        @select="selectDistrict($event)" />
      <div v-if="!compact" class="geo-legend">
        <span><i class="unmatched"></i>无匹配统计</span>
        <span><i class="zero"></i>0 项</span>
        <span><i class="light"></i><i class="medium"></i><i class="strong"></i>热度递增 · 最大 {{ dashboardNumber(map.max) }} 项</span>
      </div>
      <p v-if="!compact && selectedShape" class="district-selection" role="status">
        {{ selectedShape.district }} · {{ selectedShape.value === undefined ? '无匹配统计'
          : metrics[metric] + ' ' + dashboardNumber(selectedShape.value) + ' 项' }}
      </p>
      <p v-if="!compact && map.unmatched.length" class="geo-notice">
        {{ dashboardNumber(map.unmatched.length) }} 个区县统计未匹配边界，可在下方明细查看。
      </p>
    </template>
    <p v-if="!compact && summary?.omittedDistricts" class="geo-notice">
      另有 {{ dashboardNumber(summary.omittedDistricts) }} 个有风险的区县超出展示上限。
    </p>
    <div v-if="compact && summary" class="city-summary">
      <section v-if="selected" class="city-selection" aria-label="所选区县风险明细">
        <strong>{{ selected }}</strong>
        <div v-if="selectedDistrict" class="city-risk-counts" aria-live="polite">
          <span>高风险 <b>{{ dashboardNumber(selectedDistrict.high) }}</b></span>
          <span>中风险 <b>{{ dashboardNumber(selectedDistrict.medium) }}</b></span>
          <span>低风险 <b>{{ dashboardNumber(selectedDistrict.low) }}</b></span>
          <span>涉及系统 <b>{{ dashboardNumber(selectedDistrict.systems) }}</b></span>
        </div>
        <small v-else>该区县暂无未销项隐患统计。</small>
        <RouterLink :to="{ path: dashboardPath, query: { tab: 'risk', view: 'district', district: selected } }"
          class="city-detail-link">查看所选区县明细</RouterLink>
      </section>
      <ul v-if="highlightedDistricts.length" aria-label="区县未销项风险概览">
        <li v-for="item in highlightedDistricts" :key="item.district">
          <button type="button" class="city-district-card" :aria-pressed="item.district === selected"
            @click="selectDistrict(item.district === selected ? allDistricts : item.district)">
            <span class="city-district"><MapPin aria-hidden="true" /><b>{{ item.district }}</b></span>
            <span class="city-count">
              <strong>{{ dashboardNumber(item.high + item.medium + item.low) }}</strong> 项未销项
            </span>
            <span class="city-severity">高风险 {{ dashboardNumber(item.high) }}</span>
          </button>
        </li>
      </ul>
      <p v-else-if="!summary.withoutDistrict.systems">暂无有效备案系统的未销项隐患。</p>
      <p v-if="summary.withoutDistrict.systems" class="city-unassigned">
        未维护区县 {{ dashboardNumber(summary.withoutDistrict.systems) }} 个涉及系统
      </p>
    </div>
    <table v-else-if="!compact && shownDistricts.length" aria-label="区县未销项风险">
      <thead><tr><th scope="col">名录区县</th><th scope="col">涉及系统</th><th scope="col">高风险</th>
        <th scope="col">中风险</th><th scope="col">低风险</th></tr></thead>
      <tbody><tr v-for="item in shownDistricts" :key="item.district" :class="{ active: selected && item.district === selected }">
        <th scope="row">
          {{ item.district || '未维护区县' }}
          <small v-if="item.district && map?.unmatched.includes(item.district)">无匹配边界</small>
        </th>
        <td>{{ dashboardNumber(item.systems) }}</td><td>{{ dashboardNumber(item.high) }}</td>
        <td>{{ dashboardNumber(item.medium) }}</td><td>{{ dashboardNumber(item.low) }}</td>
      </tr></tbody>
    </table>
    <p v-else-if="!compact && summary">{{ districtScope !== null
      ? '当前范围暂无未销项隐患统计。' : '暂无有效备案系统的未销项隐患。' }}</p>
  </div>
</template>

<style scoped>
.district-risk { min-height: 0; overflow: auto; scrollbar-color: #416481 transparent; }
.district-risk table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
.district-risk th, .district-risk td {
  padding: .5em .38em; border-bottom: 1px solid #294662; text-align: right; white-space: nowrap;
}
.district-risk th:first-child { text-align: left; white-space: normal; overflow-wrap: anywhere; }
.district-risk thead, .district-risk p, .district-risk tr small { color: var(--dashboard-muted); }
.district-risk td:nth-child(3) { color: #ffad98; }
.district-risk td:nth-child(4) { color: #ffd18a; }
.district-risk tr small { display: block; font-weight: 400; }
.district-risk tr.active { background: #214564; }
.geo-toolbar, .geo-filters, .geo-legend { display: flex; align-items: center; flex-wrap: wrap; gap: .38em .65em; }
.geo-toolbar { margin-bottom: .52em; }
.geo-toolbar small { flex-basis: 100%; color: var(--dashboard-muted); }
.geo-upload { position: relative; display: inline-flex; cursor: pointer; }
.geo-upload input { position: absolute; width: 1px; height: 1px; opacity: 0; }
.geo-upload:focus-within { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: 3px; }
.geo-upload span, .district-risk button, .geo-filters select {
  padding: .28em .48em; color: #d0eafa; background: #142e47; border: 1px solid #496c89; border-radius: 4px;
}
.district-risk button:not(:disabled):hover, .geo-upload:hover span { background: #214763; }
.geo-notice, .district-selection { margin: .35em 0; font-size: .82em; }
.geo-filters, .geo-legend { font-size: .81em; }
.geo-filters label { display: flex; align-items: center; gap: .38em; min-width: 0; }
.geo-filters select { min-width: 0; max-width: 11em; }
.geo-map {
  display: block; width: 100%; max-height: 58vh; min-height: 8em; margin: .42em 0;
  border-radius: 5px; background: #0c1d31;
}
.compact { position: relative; isolation: isolate; display: flex; flex: 1; flex-direction: column; overflow: hidden; }
.compact .geo-map { flex: 1; max-height: none; min-height: 6em; margin: 0; background: #061a2a; }
.city-summary, .compact > p { position: relative; z-index: 1; }
.city-summary { margin-block-start: auto; padding: .55em .75em .68em; pointer-events: none; }
.city-summary :is(button, a) { pointer-events: auto; }
.city-summary ul { display: grid; grid-template-columns: repeat(auto-fit, minmax(7.8em, 1fr)); gap: .5em; padding: 0; margin: 0; list-style: none; }
.city-summary li { min-width: 0; }
.district-risk .city-district-card {
  display: grid; gap: .18em; width: 100%; height: 100%; padding: .51em .58em; text-align: left;
  border: 1px solid #397398; border-radius: 4px; background: #061c32ed;
}
.district-risk .city-district-card[aria-pressed='true'] {
  border-color: #72c1e8; background: #15334c; box-shadow: inset 2px 0 #72c1e8;
}
.city-selection {
  width: fit-content; max-width: 100%; padding: .48em .65em; margin-bottom: .48em;
  border: 1px solid #4f84a7; border-radius: 4px; background: #081c2ef5;
  display: grid; gap: .24em; font-size: .86em; overflow-wrap: anywhere;
}
.city-risk-counts { display: flex; flex-wrap: wrap; gap: .35em .85em; color: #bcd8eb; }
.city-risk-counts b { color: #edf8ff; }
.city-detail-link { color: #a9ddfb; text-decoration: underline; width: fit-content; }
.city-district { display: flex; align-items: center; gap: .28em; color: #d6edff; font-size: .86em; overflow-wrap: anywhere; }
.city-district svg { width: .95em; height: .95em; flex: none; color: #67bef3; }
.city-count { color: #bed6e8; font-size: .76em; }
.city-count strong { color: #fff; font-size: 1.5rem; line-height: 1.05; font-variant-numeric: tabular-nums; }
.city-severity { color: #ffab91; font-size: .72em; }
.city-summary > p, .compact > p { width: fit-content; background: #08192aeb; padding: .38em .58em; border-radius: 3px; font-size: .83em; }
.city-unassigned { margin-top: .35em; }
@media (max-width: 650px) {
  .compact { overflow: auto; }
}
.geo-legend span { display: inline-flex; align-items: center; gap: .22em; }
.geo-legend i { width: .7em; aspect-ratio: 1; border-radius: 2px; border: 1px solid #6a91b3; background: var(--geo-fill); }
.unmatched { --geo-fill: #243a52; }
.zero { --geo-fill: #30506c; }
.light { --geo-fill: #496ea5; }
.medium { --geo-fill: #9d6747; }
.strong { --geo-fill: #f27962; }
</style>

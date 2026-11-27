<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import { ArrowUpRight, ListOrdered, Map } from '@lucide/vue'
import type { RouteLocationRaw } from 'vue-router'
import DashboardDistrictMap from './DashboardDistrictMap.vue'
import { dashboardNumber } from '../lib/dashboard'
import { vDashboardScroll } from '../lib/dashboardAutoScroll'
import { districtHeatMap, projectDistrictBoundaries, readDistrictBoundaries } from '../lib/dashboardGeo'
import { loadDistrictBoundaryFile } from '../lib/dashboardGeoStorage'
import type { DistrictResponse, ResponseSummary } from '../lib/districtResponse'
import { risks } from '../lib/domain'
import { hazardPath, hazardStatuses } from '../lib/hazards'

const props = defineProps<{
  summary?: ResponseSummary; selected: string | null; detail?: DistrictResponse
  loadingSummary: boolean; failedSummary: boolean; loadingDetail: boolean; failedDetail: boolean
  mapSettings: RouteLocationRaw
}>()
const emit = defineEmits<{ select: [district: string | null]; retrySummary: []; retryDetail: [] }>()
const districtAssets = import.meta.glob<string>('../assets/district-boundaries.geojson', {
  eager: true, import: 'default', query: '?url&no-inline',
})
const districtAsset = districtAssets['../assets/district-boundaries.geojson']
const mapVisible = ref(Boolean(districtAsset))
const boundaryPaths = shallowRef<ReturnType<typeof projectDistrictBoundaries>>([])
const boundaryLoading = ref(false)
const boundaryIssue = ref(false)
const heatmap = computed(() => boundaryPaths.value.length
  ? districtHeatMap(boundaryPaths.value, props.summary?.items ?? [], 'high') : undefined)
const detailDistrict = computed(() => props.detail ? props.detail.district : props.selected)
const measures = [
  { key: 'high', label: '高风险', tone: 'high' },
  { key: 'overdue', label: '已逾期', tone: 'overdue' },
  { key: 'pendingRetest', label: '待机构复测', tone: 'normal' },
  { key: 'pendingReview', label: '待监管复核', tone: 'normal' },
] as const

/**
 * 用途 读取同源部署的辖区边界，未配置时兼容已保存在本机的区县边界。
 * 参数 无。
 * 返回值 本次读取任务，边界读取失败时保留排名入口。
 */
async function showMap() {
  mapVisible.value = true
  if (boundaryPaths.value.length || boundaryLoading.value) return
  boundaryLoading.value = true
  boundaryIssue.value = false
  try {
    let source: string | null
    if (districtAsset) {
      const response = await fetch(districtAsset, {
        signal: AbortSignal.timeout(8_000), redirect: 'error', mode: 'same-origin',
      })
      if (!response.ok) throw new Error('辖区地图读取失败')
      source = await response.text()
    } else {
      source = loadDistrictBoundaryFile().source
    }
    boundaryPaths.value = source ? projectDistrictBoundaries(readDistrictBoundaries(source)) : []
  } catch {
    boundaryPaths.value = []
    boundaryIssue.value = true
  } finally {
    boundaryLoading.value = false
  }
}

onMounted(() => { if (districtAsset) void showMap() })
</script>

<template>
  <div class="response-layout">
    <section class="district-board" aria-label="区县选择">
      <header class="response-heading">
        <div class="response-switch" role="group" aria-label="区县展示方式">
          <button class="response-button" type="button" :aria-pressed="!mapVisible" @click="mapVisible = false">
            <ListOrdered aria-hidden="true" />排名
          </button>
          <button class="response-button" type="button" :aria-pressed="mapVisible" @click="showMap">
            <Map aria-hidden="true" />地图
          </button>
        </div>
        <button class="response-button" type="button" :aria-pressed="selected === null"
          @click="emit('select', null)">全域</button>
      </header>
      <p v-if="failedSummary" class="response-status" role="status">
        {{ summary ? '区县排序更新失败' : '暂时无法读取区县排序' }}
        <button class="response-button" type="button" @click="emit('retrySummary')">重试</button>
      </p>
      <p v-else-if="loadingSummary && !summary" class="response-note" role="status">正在读取区县排序…</p>
      <template v-if="mapVisible">
        <template v-if="heatmap">
          <DashboardDistrictMap class="response-map" :heatmap="heatmap" :selected="selected"
            :show-labels="Boolean(districtAsset)" :relief="Boolean(districtAsset)"
            metric-label="高风险" @select="emit('select', $event)" />
          <p class="response-note">{{ summary ? '按未销项高风险隐患数着色' : '暂未取得区县风险统计' }}<template v-if="heatmap.unmatched.length">
            · {{ dashboardNumber(heatmap.unmatched.length) }} 个区县无匹配边界
          </template></p>
          <p v-if="districtAsset" class="response-note">浙江省衢州市 · 底图 CTAmap v2.0</p>
        </template>
        <div v-else class="map-empty">
          <Map aria-hidden="true" />
          <p class="response-note" role="status">{{ boundaryLoading ? '正在加载辖区地图…'
            : boundaryIssue ? '辖区地图读取失败' : '尚未配置辖区地图' }}</p>
          <button v-if="boundaryIssue" class="response-button" type="button" @click="showMap">重试</button>
          <RouterLink v-if="!districtAsset && !boundaryLoading" :to="mapSettings" class="screen-link">
            导入区县边界<ArrowUpRight aria-hidden="true" />
          </RouterLink>
        </div>
      </template>
      <div v-else-if="summary?.items.length" class="district-scroll">
        <table>
          <thead><tr><th scope="col">区县</th><th scope="col">高风险</th><th scope="col">逾期</th></tr></thead>
          <tbody>
            <tr v-for="item in summary.items" :key="item.district" :class="{ picked: selected === item.district }">
              <th scope="row">
                <button class="district-name" type="button" :aria-pressed="selected === item.district"
                  @click="emit('select', item.district)">{{ item.district }}</button>
              </th>
              <td :class="{ high: item.high > 0 }">{{ dashboardNumber(item.high) }}</td>
              <td :class="{ overdue: item.overdue > 0 }">{{ dashboardNumber(item.overdue) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else-if="summary" class="response-note">已维护区县中暂无未销项隐患。</p>
      <p v-if="summary?.omittedDistricts" class="response-note">
        另有 {{ dashboardNumber(summary.omittedDistricts) }} 个区县超出展示上限
      </p>
      <button v-if="summary?.withoutDistrict.systems" class="unassigned response-button" type="button"
        :aria-pressed="selected === ''" @click="emit('select', '')">
        未归区 · {{ dashboardNumber(summary.withoutDistrict.systems) }} 个系统
      </button>
    </section>

    <section class="district-detail" aria-label="当前范围处置详情" :aria-busy="loadingDetail">
      <header class="response-heading">
        <h3>{{ detailDistrict === null ? '全域' : detailDistrict || '未归区' }}</h3>
        <span v-if="loadingDetail && detailDistrict !== selected" class="response-note response-switching">
          正在切换至{{ selected === null ? '全域' : selected || '未归区' }}
        </span>
        <span v-else-if="detail" class="response-note">涉及 {{ dashboardNumber(detail.counts.systems) }} 个系统</span>
      </header>
      <p v-if="failedDetail" class="response-status" role="status">
        {{ detail ? '处置态势更新失败，显示最近一次数据' : '暂时无法读取处置态势' }}
        <button class="response-button" type="button" @click="emit('retryDetail')">重试</button>
      </p>
      <p v-else-if="loadingDetail && !detail" class="response-note" role="status">正在读取处置态势…</p>
      <template v-if="detail">
        <dl class="response-metrics" aria-label="当前范围待办统计">
          <div v-for="item in measures" :key="item.key" :data-tone="item.tone">
            <dt>{{ item.label }}</dt><dd :class="{ 'has-count': detail.counts[item.key] > 0 }">
              {{ dashboardNumber(detail.counts[item.key]) }}
            </dd>
          </div>
        </dl>
        <h4>重点隐患 <span class="response-note">逾期优先</span></h4>
        <div v-if="detail.tasks.length" :key="JSON.stringify(detail.district)" class="district-task-list"
          v-dashboard-scroll>
          <ul class="district-task-rows" data-dashboard-loop-original aria-label="重点隐患">
            <li v-for="item in detail.tasks" :key="item.id">
              <RouterLink class="district-task" :to="hazardPath(item.id)">
                <span>
                  <strong>{{ item.title }}</strong>
                  <small>{{ item.systemName }} · {{ item.organizationName }}</small>
                  <span class="task-status">
                    <span :class="{ high: item.severity === 'high' }">{{ risks[item.severity] }}</span>
                    <span>{{ hazardStatuses[item.status] }}</span>
                    <span v-if="item.overdue" class="overdue">已逾期</span>
                    <time :datetime="item.deadline">截止 {{ item.deadline }}</time>
                  </span>
                </span>
                <ArrowUpRight aria-hidden="true" />
              </RouterLink>
            </li>
          </ul>
          <ul class="district-task-rows" data-dashboard-loop-copy aria-hidden="true" hidden></ul>
        </div>
        <p v-else class="response-note">当前范围暂无重点事项。</p>
      </template>
    </section>
  </div>
</template>

<style scoped>
.response-layout {
  display: grid; grid-template-columns: minmax(0, 1.14fr) minmax(0, 1fr);
  flex: 1; gap: .85em; min-height: 0; padding: .15em;
  font-variant-numeric: tabular-nums;
}
.district-board, .district-detail { min-width: 0; min-height: 0; display: flex; flex-direction: column; gap: .48em; }
.district-detail { border-inline-start: 1px solid #37678a; padding-inline-start: .88em; }
.response-heading, .task-status {
  display: flex; align-items: center; flex-wrap: wrap; justify-content: space-between; gap: .38em .5em;
}
.response-heading h3, h4 { margin: 0; color: #d9eeff; font-size: .97em; overflow-wrap: anywhere; }
h4 { display: flex; justify-content: space-between; gap: .5em; padding-top: .25em; }
h4 span { font-weight: 400; }
.response-note, .response-status { margin: 0; color: var(--dashboard-muted); font-size: .82em; }
.response-switching { color: #94dcff; }
.response-status { color: #f6c681; }
.response-button, .district-name {
  appearance: none; background: transparent; color: #c2e7ff; border: 1px solid #3e6079;
  border-radius: 4px; padding: .27em .5em; font-size: .83em;
}
.response-switch { display: flex; gap: .26em; }
.response-switch .response-button { display: inline-flex; align-items: center; gap: .33em; }
.response-switch svg, .map-empty svg { width: 1em; height: 1em; }
.response-button[aria-pressed='true'], .response-button:hover, .district-name:hover { background: #183953; }
.district-scroll, .district-task-list { min-height: 0; overflow: auto; scrollbar-color: #416481 transparent; }
.response-map, .map-empty {
  flex: 1; width: 100%; min-height: 6em; border: 1px solid #25435e; border-radius: 4px;
  background: radial-gradient(ellipse at 50% 45%, #152f4c, #09192b);
}
.response-map.geo-map-relief {
  border-color: #4f81a7;
  background:
    repeating-linear-gradient(0deg, #639bc323 0 1px, transparent 1px 18px),
    repeating-linear-gradient(90deg, #639bc323 0 1px, transparent 1px 18px),
    radial-gradient(ellipse at 50% 49%, #224d71, #0a1e33 87%);
  box-shadow: inset 0 0 20px #030f2059;
}
.map-empty { display: grid; justify-items: center; align-content: center; gap: .45em; text-align: center; }
.map-empty > svg { color: var(--screen-blue); width: 1.35em; height: 1.35em; }
table { width: 100%; border-collapse: collapse; }
thead { position: sticky; top: 0; background: var(--dashboard-paper); }
th, td { text-align: right; font-size: .89em; padding: .42em .18em; border-bottom: 1px solid #263f57; }
thead th { white-space: nowrap; color: var(--dashboard-muted); }
th:first-child { width: 64%; text-align: left; }
.district-name { border-color: transparent; text-align: left; max-width: 100%; overflow-wrap: anywhere; }
.picked { background: #15314a; }
.picked .district-name { border-color: #457c9f; color: #e8f6ff; }
.unassigned { width: fit-content; max-width: 100%; text-align: left; }
.response-metrics {
  display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .28em;
  margin: 0; padding: 0; font-size: .83em;
}
.response-metrics div {
  display: flex; align-items: center; justify-content: space-between; gap: .26em;
  min-width: 0; padding: .26em .42em; border: 1px solid #2a4b67; border-radius: 4px;
  background: linear-gradient(150deg, #204665b8, #102941); color: #bdd4e8;
}
.response-metrics dd { margin: 0; color: #edf7ff; font-size: 1.2em; font-weight: 680; }
.response-metrics [data-tone='high'] .has-count { color: #ffa99d; }
.response-metrics [data-tone='overdue'] .has-count { color: #ffd097; }
.district-task-list { --dashboard-row-gap: .23em; }
.district-task-rows { display: grid; gap: var(--dashboard-row-gap); list-style: none; margin: 0; padding: 0; }
.district-task {
  display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: .46em; padding: .46em .35em;
  border-bottom: 1px solid #27425a; border-radius: 3px; overflow-wrap: anywhere; font-size: .92em;
}
.district-task:hover { background: #17344e; }
.district-task small { display: block; color: var(--dashboard-muted); }
.district-task svg { width: .94em; height: .94em; margin-top: .15em; color: #a9ddfb; }
.task-status { justify-content: flex-start; font-size: .77em; color: var(--dashboard-muted); padding-top: .24em; }
.high { color: #ffa99d; }
.overdue { color: #ffd097; }
@media (max-width: 650px) {
  .response-layout { grid-template-columns: 1fr; }
  .district-detail { border-inline-start: 0; border-top: 1px solid #294661; padding: .7em 0 0; }
  .district-scroll { max-height: 13em; }
  .district-task-list { max-height: 16em; }
  .response-map { max-height: 17em; }
}
</style>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import { ArrowUpRight, ListOrdered, Map as MapIcon } from '@lucide/vue'
import type { RouteLocationRaw } from 'vue-router'
import { districtAsset, loadPublishedDistrictBoundaries } from '../lib/dashboardBoundary'
import { projectDistrictOutlines, type DistrictOutline } from '../lib/dashboardGeo'
import {
  buildMap3D, defaultMapCamera, riskHeightOf, riskLevelOf, type District3DItem,
} from '../lib/dashboardGeo3d'
import { dashboardNumber } from '../lib/dashboard'
import { risks } from '../lib/domain'
import { hazardPath, hazardStatuses } from '../lib/hazards'
import type { DashboardArea } from '../lib/session'
import type { ResponseSummary, DistrictResponse } from '../lib/districtResponse'

/**
 * 驾驶舱的区县处置态势面板。
 *
 * 左侧为按综合风险指数浮起的三维沙盘，右侧为重点隐患清单。
 */
const props = defineProps<{
  summary?: ResponseSummary; selected: string | null; detail?: DistrictResponse
  loadingSummary: boolean; failedSummary: boolean; loadingDetail: boolean; failedDetail: boolean
  mapSettings: RouteLocationRaw; area?: DashboardArea; locked?: boolean
}>()
const emit = defineEmits<{ select: [district: string | null]; retrySummary: []; retryDetail: [] }>()

const outlines = shallowRef<DistrictOutline[]>([])
const mapFailed = ref(false)
const showMap = ref(true)
const heightScale = 34
/** 基础厚度：没有风险数据时也让区县保持可见的立体台面，避免退化成平面图。 */
const baseHeight = 15

const riskRows = computed(() => {
  const rows = new globalThis.Map<string, ResponseSummary['items'][number]>(
    (props.summary?.items ?? []).map(item => [item.district, item] as const))
  return outlines.value.map(outline => {
    const row = rows.get(outline.district)
    const index = row?.riskIndex ?? 0
    return {
      district: outline.district,
      rings: outline.rings,
      label: outline.label,
      value: index,
      level: row?.riskLevel ?? riskLevelOf(index),
      height: Math.max(baseHeight, riskHeightOf(index, heightScale)),
      beam: index >= 66,
    } satisfies District3DItem
  })
})

const model = computed(() => buildMap3D(riskRows.value, defaultMapCamera, props.selected))
/** 当前悬停的区县，用于边界高亮与跟随提示。 */
const hovered = ref<string | null>(null)
const hoveredRow = computed(() => (props.summary?.items ?? [])
  .find(item => item.district === hovered.value))
const hoveredPoint = ref<{ x: number; y: number } | null>(null)
/**
 * 用途 记录鼠标在地图上的位置，供跟随提示定位。
 * 参数 event 为鼠标事件，district 为所在区县。
 * 返回值 无。
 */
function trackHover(event: MouseEvent, district: string) {
  const box = (event.currentTarget as SVGGraphicsElement).ownerSVGElement?.getBoundingClientRect()
  hovered.value = district
  if (box) {
    hoveredPoint.value = { x: event.clientX - box.left, y: event.clientY - box.top }
  }
}
const currentScope = computed(() => {
  const detail = props.detail
  if (!detail) return { name: '全域', systems: 0, hazards: 0, overdue: 0, review: 0 }
  return {
    name: detail.district === null ? (props.area?.name ?? '全域') : detail.district || '未归区',
    systems: detail.counts.systems,
    hazards: detail.counts.high + detail.counts.medium + detail.counts.low,
    overdue: detail.counts.overdue,
    review: detail.counts.pendingReview,
  }
})
const legendRows = computed(() => {
  const levels = [
    { key: 'high', label: '高', range: '66-100', color: '#e0685a' },
    { key: 'mid', label: '中', range: '34-65', color: '#d99b3a' },
    { key: 'low', label: '低', range: '1-33', color: '#3b8fc0' },
    { key: 'none', label: '无', range: '0', color: '#3d7ba6' },
  ]
  return levels
})

const viewBox = computed(() => model.value.viewBox)
const groundPaths = computed(() => model.value.ground)
const wallPaths = computed(() => model.value.walls)
const facePaths = computed(() => model.value.faces)
const edgePaths = computed(() => model.value.edges.map(item => item.d))
const beamPaths = computed(() => model.value.beams.map(item => item.d))

/**
 * 用途 读取同源发布的区县边界并投影为三维可用的环坐标。
 * 参数 无。
 * 返回值 本次读取任务。
 */
async function showMapData() {
  showMap.value = true
  if (outlines.value.length) return
  try {
    const boundaries = districtAsset || props.area
      ? await loadPublishedDistrictBoundaries()
      : []
    outlines.value = projectDistrictOutlines(boundaries, props.area?.districts)
  } catch {
    outlines.value = []
    mapFailed.value = true
  }
}

onMounted(() => { if (districtAsset) void showMapData() })
</script>

<template>
  <div class="district-layout">
    <section class="district-board" aria-label="区县三维沙盘">
      <header class="district-heading">
        <div class="response-switch" role="group" aria-label="区县展示方式">
          <button class="response-button" type="button" :aria-pressed="!showMap" @click="showMap = false">
            <ListOrdered aria-hidden="true" />排名
          </button>
          <button class="response-button" type="button" :aria-pressed="showMap" @click="showMapData">
            <MapIcon aria-hidden="true" />地图
          </button>
        </div>
        <button v-if="!locked" class="response-button" type="button" :aria-pressed="selected === null"
          @click="emit('select', null)">{{ area ? '市域' : '全域' }}</button>
      </header>

      <p v-if="failedSummary" class="response-status" role="status">
        {{ summary ? '区县排序更新失败' : '暂时无法读取区县排序' }}
        <button class="response-button" type="button" @click="emit('retrySummary')">重试</button>
      </p>
      <p v-else-if="loadingSummary && !summary" class="response-note" role="status">正在读取区县排序…</p>

      <div v-if="showMap" class="map-stage">
        <svg v-if="riskRows.length" class="map-canvas" :viewBox="viewBox" preserveAspectRatio="xMidYMid meet">
          <defs>
            <filter id="dashboard-wall-glow" x="-40%" y="-40%" width="180%" height="190%">
              <feGaussianBlur in="SourceAlpha" stdDeviation="5" result="blur" />
              <feFlood flood-color="#5fd8ff" flood-opacity=".75" result="tint" />
              <feComposite in="tint" in2="blur" operator="in" result="glow" />
              <feMerge><feMergeNode in="glow" /><feMergeNode in="SourceGraphic" /></feMerge>
            </filter>
            <filter id="dashboard-hover-glow" x="-60%" y="-60%" width="220%" height="220%">
              <feGaussianBlur in="SourceAlpha" stdDeviation="5" result="hb" />
              <feFlood flood-color="#8de5ff" flood-opacity=".95" result="hc" />
              <feComposite in="hc" in2="hb" operator="in" result="hg" />
              <feGaussianBlur in="SourceAlpha" stdDeviation="1.5" result="hb2" />
              <feFlood flood-color="#ffffff" flood-opacity=".95" result="hc2" />
              <feComposite in="hc2" in2="hb2" operator="in" result="hg2" />
              <feMerge>
                <feMergeNode in="hg" /><feMergeNode in="hg2" /><feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
            <filter id="dashboard-edge-glow" x="-60%" y="-60%" width="220%" height="220%">
              <feGaussianBlur in="SourceAlpha" stdDeviation="6" result="blur1" />
              <feFlood flood-color="#8de5ff" flood-opacity=".95" result="tint1" />
              <feComposite in="tint1" in2="blur1" operator="in" result="glow1" />
              <feGaussianBlur in="SourceAlpha" stdDeviation="1.8" result="blur2" />
              <feFlood flood-color="#eaf8ff" flood-opacity=".9" result="tint2" />
              <feComposite in="tint2" in2="blur2" operator="in" result="glow2" />
              <feMerge>
                <feMergeNode in="glow1" /><feMergeNode in="glow2" /><feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
            <linearGradient id="dashboard-top-high" x1="0.08" y1="0" x2="0.72" y2="1">
              <stop offset="0" stop-color="#ffb59f" /><stop offset="0.46" stop-color="#e0685a" />
              <stop offset="1" stop-color="#93352f" />
            </linearGradient>
            <linearGradient id="dashboard-top-mid" x1="0.08" y1="0" x2="0.72" y2="1">
              <stop offset="0" stop-color="#ffd89b" /><stop offset="0.46" stop-color="#d99b3a" />
              <stop offset="1" stop-color="#8b5c1b" />
            </linearGradient>
            <linearGradient id="dashboard-top-low" x1="0.08" y1="0" x2="0.72" y2="1">
              <stop offset="0" stop-color="#a4dbf7" /><stop offset="0.46" stop-color="#3b8fc0" />
              <stop offset="1" stop-color="#1b4c6d" />
            </linearGradient>
            <linearGradient id="dashboard-top-none" x1="0.08" y1="0" x2="0.72" y2="1">
              <stop offset="0" stop-color="#8ec6e8" /><stop offset="0.46" stop-color="#3d7ba6" />
              <stop offset="1" stop-color="#1b3f5c" />
            </linearGradient>
            <linearGradient id="dashboard-wall-high" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stop-color="#8d3b33" /><stop offset="1" stop-color="#2c1310" />
            </linearGradient>
            <linearGradient id="dashboard-wall-mid" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stop-color="#8a6526" /><stop offset="1" stop-color="#2a2010" />
            </linearGradient>
            <linearGradient id="dashboard-wall-low" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stop-color="#255b7c" /><stop offset="1" stop-color="#0f2432" />
            </linearGradient>
            <linearGradient id="dashboard-wall-none" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stop-color="#4b8fbb" /><stop offset="0.45" stop-color="#25597c" /><stop offset="1" stop-color="#0c2032" />
            </linearGradient>
            <linearGradient id="dashboard-beam" x1="0" y1="1" x2="0" y2="0">
              <stop offset="0" stop-color="#ff7a4d" stop-opacity=".45" />
              <stop offset="0.4" stop-color="#ff8f66" stop-opacity=".14" />
              <stop offset="1" stop-color="#ff8f66" stop-opacity="0" />
            </linearGradient>
          </defs>

          <g class="map-floor">
            <path v-for="(item, index) in model.floor" :key="`fl-${index}`" :d="item.d" />
          </g>
          <g class="map-radar">
            <path v-for="(item, index) in model.radar" :key="`rd-${index}`" :d="item.d"
              :style="{ animationDelay: `${-index * 6}s` }" />
          </g>
          <g class="map-ground">
            <path v-for="(item, index) in groundPaths" :key="`g-${index}`" :d="item.d"
              :class="['district', { 'is-hover': hovered === item.district }]"
              :fill="`url(#dashboard-top-${item.level})`" fill-rule="evenodd"
              :stroke="hovered === item.district ? '#dff2ff' : 'rgba(223,242,255,.45)'"
              :stroke-width="hovered === item.district ? 2.2 : 1"
              @mouseenter="trackHover($event, item.district)" @mousemove="trackHover($event, item.district)"
              @mouseleave="hovered = null" />
          </g>
          <g class="map-walls">
            <path v-for="(item, index) in wallPaths" :key="`w-${index}`" :d="item.d"
              :class="{ 'is-hover': hovered === item.district }"
              :fill="`url(#dashboard-wall-${item.level})`" />
          </g>
          <g class="map-beams" style="mix-blend-mode:screen">
            <path v-for="(d, index) in beamPaths" :key="`b-${index}`" :d="d" fill="url(#dashboard-beam)" />
          </g>
          <g class="map-faces">
            <path v-for="(item, index) in facePaths" :key="`f-${index}`" :d="item.d"
              :class="['district', 'face', { 'is-hover': hovered === item.district }]"
              :fill="`url(#dashboard-top-${item.level})`" fill-rule="evenodd"
              :stroke="hovered === item.district ? '#ffffff' : '#dff2ff'"
              :stroke-width="hovered === item.district ? 2.4 : 1"
              stroke-opacity=".85"
              @mouseenter="trackHover($event, item.district)" @mousemove="trackHover($event, item.district)"
              @mouseleave="hovered = null" />
          </g>
          <g class="map-hover-tip" v-if="hovered && hoveredPoint && hoveredRow">
            <g :transform="`translate(${Math.min(hoveredPoint.x + 14, 196)}, ${Math.max(hoveredPoint.y - 16, 8)})`">
              <rect x="0" y="0" width="176" height="74" rx="7" />
              <text x="12" y="21" class="tip-title">{{ hoveredRow.district }}</text>
              <text x="164" y="21" text-anchor="end" class="tip-index"
                :class="hoveredRow.riskLevel">{{ hoveredRow.riskIndex }}</text>
              <text x="12" y="41" class="tip-line">未销项隐患 {{ hoveredRow.high }} 高 / {{ hoveredRow.medium }} 中 / {{ hoveredRow.low }} 低</text>
              <text x="12" y="58" class="tip-line">已逾期 {{ hoveredRow.overdue }} · 涉及系统 {{ hoveredRow.systems }}</text>
            </g>
          </g>
          <g class="map-edges">
            <path v-for="(d, index) in edgePaths" :key="`e-${index}`" :d="d" fill="none"
              stroke="#eaf8ff" stroke-width="2.2" stroke-linejoin="round" filter="url(#dashboard-edge-glow)" />
          </g>
          <g class="map-labels">
            <g v-for="label in model.labels" :key="label.district"
              :class="['map-label', { 'is-hover': hovered === label.district }]">
              <rect :x="label.x - 14" :y="label.y - 8" width="28" height="17" rx="4" fill="rgba(2,8,14,.92)" />
              <rect :x="label.x - 14" :y="label.y - 11" width="28" height="17" rx="4" fill="url(#dashboard-badge)"
                :stroke="label.selected ? '#eaf8ff' : 'rgba(141,229,255,.7)'" stroke-width="1" />
              <text :x="label.x" :y="label.y + 1" text-anchor="middle" class="map-label-value">{{ label.value }}</text>
              <text :x="label.x" :y="label.y + 18" text-anchor="middle" class="map-label-name">{{ label.district }}</text>
            </g>
          </g>
        </svg>
        <div v-else class="map-empty">
          <MapIcon aria-hidden="true" />
          <p class="response-note" role="status">
            {{ mapFailed ? '辖区地图读取失败' : '尚未配置辖区地图' }}
          </p>
          <button v-if="mapFailed" class="response-button" type="button" @click="showMapData">重试</button>
          <RouterLink v-if="!area && !districtAsset" :to="props.mapSettings" class="screen-link">
            导入区县边界<ArrowUpRight aria-hidden="true" />
          </RouterLink>
        </div>

        <div class="map-overlay map-overlay-legend">
          <p class="ov-title">综合风险指数</p>
          <div class="ov-row" v-for="row in legendRows" :key="row.key">
            <i :style="{ background: row.color }" /><span>{{ row.label }}</span><b>{{ row.range }}</b>
          </div>
        </div>
        <div class="map-overlay map-overlay-scope">
          <p class="ov-title">{{ currentScope.name }}</p>
          <div class="ov-row"><span>涉及系统</span><b>{{ currentScope.systems }}</b></div>
          <div class="ov-row"><span>未销项隐患</span><b>{{ currentScope.hazards }}</b></div>
          <div class="ov-row"><span>已逾期</span><b class="hot">{{ currentScope.overdue }}</b></div>
          <div class="ov-row"><span>待监管复核</span><b>{{ currentScope.review }}</b></div>
        </div>
        <div class="map-overlay map-overlay-note">
          风险指数 = 逾期 40% / 高风险 40% / 待复测 20%，满分 100<br>
          区块浮起高度即该区县的综合风险指数
        </div>
      </div>

      <div v-else class="district-scroll">
        <table>
          <thead><tr><th>区县</th><th>风险指数</th><th>高风险</th><th>逾期</th></tr></thead>
          <tbody>
            <tr v-for="item in props.summary?.items ?? []" :key="item.district"
              :class="{ picked: item.district === selected }">
              <th>
                <button class="district-name" type="button" :aria-pressed="item.district === selected"
                  @click="emit('select', item.district === selected ? null : item.district)">{{ item.district }}</button>
              </th>
              <td class="index" :class="item.riskLevel">{{ item.riskIndex }}</td>
              <td :class="{ hot: item.high > 0 }">{{ item.high }}</td>
              <td :class="{ hot: item.overdue > 0 }">{{ item.overdue }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <aside class="hazard-panel" aria-label="重点隐患">
      <header><h3>重点隐患</h3><span>逾期优先</span></header>
      <p v-if="failedDetail" class="response-status" role="status">
        {{ props.detail ? '处置态势更新失败，显示最近一次数据' : '暂时无法读取处置态势' }}
        <button class="response-button" type="button" @click="emit('retryDetail')">重试</button>
      </p>
      <p v-else-if="loadingDetail && !props.detail" class="response-note" role="status">正在读取处置态势…</p>
      <ul v-else-if="props.detail?.tasks.length" class="hazard-list">
        <li v-for="item in props.detail.tasks" :key="item.id">
          <RouterLink v-if="!props.area" class="hazard-item" :to="hazardPath(item.id)">
            <span class="hazard-head">
              <span class="hazard-title">{{ item.title }}</span>
              <span class="hazard-deadline">截止 {{ item.deadline }}</span>
            </span>
            <span class="hazard-meta">
              <span class="chip" :class="item.severity === 'high' ? 'high' : item.severity === 'medium' ? 'mid' : 'low'">
                {{ risks[item.severity] }}风险</span>
              <span v-if="item.overdue" class="chip high">已逾期</span>
              <span class="chip state">{{ hazardStatuses[item.status] }}</span>
              <span class="hazard-source">{{ item.organizationName }}</span>
            </span>
          </RouterLink>
          <div v-else class="hazard-item">
            <span class="hazard-head">
              <span class="hazard-title">{{ item.title }}</span>
              <span class="hazard-deadline">截止 {{ item.deadline }}</span>
            </span>
            <span class="hazard-meta">
              <span class="chip" :class="item.severity === 'high' ? 'high' : item.severity === 'medium' ? 'mid' : 'low'">
                {{ risks[item.severity] }}风险</span>
              <span v-if="item.overdue" class="chip high">已逾期</span>
              <span class="chip state">{{ hazardStatuses[item.status] }}</span>
              <span class="hazard-source">{{ item.organizationName }}</span>
            </span>
          </div>
        </li>
      </ul>
      <p v-else class="response-note">当前范围暂无重点事项。</p>
      <div class="hazard-foot">
        <span>共 {{ dashboardNumber(props.detail?.counts.high ?? 0) }} 项高风险</span>
        <RouterLink v-if="!props.area" to="/hazards">全部隐患<ArrowUpRight aria-hidden="true" /></RouterLink>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.district-layout{display:grid;grid-template-columns:minmax(0,1fr) 296px;grid-template-rows:minmax(0,1fr);gap:10px;flex:1;min-height:0}
.district-board{display:flex;flex-direction:column;gap:8px;min-width:0;min-height:0}
.district-heading{display:flex;align-items:center;justify-content:space-between;gap:10px}
.response-switch{display:flex;gap:6px}
.response-button{display:inline-flex;align-items:center;gap:5px;padding:4px 9px;border-radius:6px;
  border:1px solid rgba(111,198,255,.34);background:rgba(15,36,56,.6);color:#bdd4e8;font-size:11.5px;
  cursor:pointer;font-family:inherit;transition:.16s}
.response-button svg{width:12px;height:12px}
.response-button[aria-pressed="true"],.response-button:hover{background:#1a4a73;color:#dff1ff;border-color:rgba(141,229,255,.7)}
.response-note,.response-status{margin:0;font-size:11px;color:var(--dashboard-muted)}
.response-status{color:#f6c681}

.map-stage{position:relative;flex:1;min-height:16em;border-radius:8px;overflow:hidden;
  border:1px solid rgba(111,198,255,.26);background:radial-gradient(70% 85% at 50% 58%,rgba(14,48,82,.5),rgba(4,13,24,.86) 74%);
  box-shadow:inset 0 0 46px rgba(0,0,0,.7)}
.map-canvas{position:absolute;inset:0;width:100%;height:100%}
.district{cursor:pointer;transition:stroke .16s,stroke-width .16s,filter .16s}
.district.is-hover{filter:url(#dashboard-hover-glow)}
.face.is-hover{filter:url(#dashboard-hover-glow)}
.map-label{pointer-events:none}
.map-label.is-hover rect:first-of-type{stroke:#eaf8ff;stroke-width:1.6}
.map-label.is-hover .dnum-badge{fill:#ffffff}
.map-hover-tip{pointer-events:none}
.map-hover-tip rect{fill:rgba(6,20,34,.94);stroke:rgba(141,229,255,.55);stroke-width:1}
.tip-title{font-family:inherit;font-size:13px;font-weight:600;fill:#eaf7ff}
.tip-index{font-family:var(--mono,ui-monospace,monospace);font-size:15px;font-weight:600;fill:#8fc4ea}
.tip-index.high{fill:#ff9aa5}
.tip-index.mid{fill:#f3c88c}
.tip-line{font-family:inherit;font-size:10.5px;fill:#9fbdd4}
.map-floor path{fill:none;stroke:rgba(95,216,255,.16);stroke-width:.9}
.map-radar path{fill:none;stroke:rgba(95,216,255,.22);stroke-width:1;stroke-dasharray:6 9;
  transform-box:fill-box;transform-origin:center;animation:map-radar-spin 26s linear infinite}
.map-radar path:nth-child(2){stroke:rgba(95,216,255,.16);animation-duration:38s;animation-direction:reverse}
.map-radar path:nth-child(3){stroke:rgba(95,216,255,.11);stroke-dasharray:none;animation-duration:50s}
@keyframes map-radar-spin{to{transform:rotate(360deg)}}
.map-empty{display:grid;justify-items:center;align-content:center;gap:8px;height:100%;text-align:center;color:var(--dashboard-muted)}
.map-empty svg{width:22px;height:22px}
.map-overlay{position:absolute;z-index:2;background:rgba(7,22,38,.74);border:1px solid rgba(111,198,255,.3);
  border-radius:8px;padding:8px 10px;backdrop-filter:blur(9px)}
.map-overlay-legend{top:10px;left:10px;min-width:118px}
.map-overlay-scope{top:10px;right:10px;min-width:136px}
.map-overlay-note{bottom:10px;right:10px;font-size:10.5px;color:var(--dashboard-muted);line-height:1.7;text-align:right}
.ov-title{margin:0 0 5px;font-size:10.5px;letter-spacing:.08em;color:var(--dashboard-muted)}
.ov-row{display:flex;align-items:center;gap:7px;font-size:11px;color:var(--ink-2)}
.ov-row i{width:12px;height:9px;border-radius:2px;flex:none}
.ov-row b{margin-left:auto;font-family:var(--mono);font-weight:500;color:var(--ink)}
.ov-row b.hot{color:var(--danger)}

.map-label-value{font-family:var(--mono);font-size:11px;font-weight:600;fill:#eaf7ff}
.map-label-name{font-family:inherit;font-size:9.5px;fill:#e2f1ff;paint-order:stroke;stroke:rgba(3,12,22,.9);stroke-width:2.6px;stroke-linejoin:round}

.district-scroll{overflow:auto;min-height:0;border:1px solid rgba(111,198,255,.2);border-radius:8px;background:rgba(9,24,40,.5)}
.district-scroll table{width:100%;border-collapse:collapse}
.district-scroll th,.district-scroll td{padding:7px 10px;font-size:12px;border-bottom:1px solid rgba(28,61,88,.6);text-align:right}
.district-scroll thead th{color:var(--dashboard-muted);font-weight:400;font-size:11px;position:sticky;top:0;background:rgba(9,26,44,.96)}
.district-scroll tbody th{text-align:left;font-weight:500}
.district-scroll .picked{background:rgba(36,92,142,.5)}
.district-scroll .index{font-family:var(--mono);font-weight:600}
.district-scroll .index.high{color:var(--danger)}
.district-scroll .index.mid{color:var(--warn)}
.district-scroll .hot{color:var(--danger)}
.district-name{border:0;background:transparent;color:inherit;font:inherit;cursor:pointer;padding:0;text-align:left}
.district-name:hover{color:var(--accent)}

.hazard-panel{display:flex;flex-direction:column;min-height:0;border:1px solid rgba(111,198,255,.24);
  border-radius:8px;background:linear-gradient(160deg,rgba(17,44,72,.52),rgba(8,22,38,.46));overflow:hidden}
.hazard-panel > header{display:flex;align-items:center;justify-content:space-between;padding:8px 11px;
  border-bottom:1px solid rgba(111,198,255,.2);background:rgba(14,40,64,.7);flex:none}
.hazard-panel > header h3{margin:0;font-size:12.5px;font-weight:600;color:#e6f4ff;display:flex;align-items:center;gap:7px}
.hazard-panel > header h3::before{content:"";width:3px;height:12px;border-radius:2px;background:var(--danger);box-shadow:0 0 8px rgba(255,95,112,.8)}
.hazard-panel > header span{font-size:10.5px;color:var(--dashboard-muted)}
.hazard-list{flex:1;min-height:0;overflow:auto;margin:0;padding:0;list-style:none}
.hazard-item{display:grid;gap:4px;padding:8px 11px;border-bottom:1px solid rgba(28,61,88,.6);
  color:inherit;text-decoration:none;transition:.16s}
a.hazard-item:hover{background:rgba(24,62,98,.5)}
.hazard-head{display:flex;align-items:baseline;gap:.5em}
.hazard-title{font-size:12px;color:var(--ink);line-height:1.35;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.hazard-meta{display:flex;align-items:center;gap:5px;flex-wrap:wrap;font-size:10.5px;color:var(--dashboard-muted)}
.hazard-source{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;min-width:0}
.hazard-deadline{margin-left:auto;flex:none;font-family:var(--mono);font-size:.95em;white-space:nowrap}
.chip{display:inline-block;padding:0 5px;border-radius:4px;font-size:10px;line-height:1.6;border:1px solid transparent}
.chip.high{background:rgba(255,95,112,.14);color:#ff9aa5;border-color:rgba(255,95,112,.45)}
.chip.mid{background:rgba(240,180,85,.14);color:#f3c88c;border-color:rgba(240,180,85,.45)}
.chip.low{background:rgba(59,143,192,.16);color:#8fc4ea;border-color:rgba(59,143,192,.45)}
.chip.state{background:rgba(28,60,86,.6);color:var(--ink-2);border-color:rgba(111,198,255,.28)}
.hazard-foot{flex:none;padding:7px 11px;border-top:1px solid rgba(111,198,255,.2);font-size:11px;
  color:var(--dashboard-muted);display:flex;align-items:center;justify-content:space-between;background:rgba(11,30,48,.7)}
.hazard-foot a{color:var(--accent);text-decoration:none;display:inline-flex;align-items:center;gap:3px}
.hazard-foot svg{width:11px;height:11px}
@media (max-width:1400px){
  .district-layout{grid-template-columns:minmax(0,1fr)}
  .hazard-panel{max-height:220px}
}
</style>

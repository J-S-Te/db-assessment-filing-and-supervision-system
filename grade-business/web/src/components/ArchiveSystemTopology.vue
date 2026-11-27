<script setup lang="ts">
import { computed, useId, watch } from 'vue'
import { Network } from '@lucide/vue'
import { useArchiveInventory, type SystemAsset } from '../lib/archiveInventory'
import { layoutTopology, topologyNodeHeight, topologyNodeWidth } from '../lib/archiveTopology'
import ArchiveInventoryPager from './ArchiveInventoryPager.vue'
import AsyncState from './AsyncState.vue'

const props = defineProps<{ path: string; revision: number }>()
const path = computed(() => props.path)
const filters = computed(() => ({ kind: 'topology' }))
const { page, cursors, loading, failure, load } = useArchiveInventory<SystemAsset>(path, filters)
const graph = computed(() => layoutTopology(page.value.items))
const arrow = useId()
watch(() => props.revision, () => { void load(['']) })
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="archive-topology-title">
    <header class="panel-heading">
      <h2 id="archive-topology-title"><Network :size="18" aria-hidden="true" />系统拓扑图</h2>
      <button class="button neutral small" type="button" :disabled="loading" @click="load()">刷新拓扑</button>
    </header>
    <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
      empty-text="暂无拓扑连接登记。可在资产与安全档案中登记连接的起点、终点与来源。" @retry="load()">
      <div class="topology-content">
        <div class="topology-caption">
          <p>本页 {{ graph.nodes.length }} 个节点 · {{ graph.edges.length }} 条连接</p>
          <ul class="topology-legend" aria-label="拓扑图图例">
            <li><span></span>在用</li><li><span class="inactive"></span>停用</li>
          </ul>
        </div>
        <div class="topology-scroll" tabindex="0" role="region" aria-label="本页拓扑图，可滚动。连接明细见下表。">
          <div aria-hidden="true" class="topology-graph"
            :style="{ width: graph.width + 'px', minHeight: graph.height + 'px' }">
            <svg :width="graph.width" :height="graph.height">
              <defs>
                <marker :id="arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto">
                  <path d="M 0 0 L 8 4 L 0 8 Z" fill="context-stroke" />
                </marker>
              </defs>
              <polyline v-for="edge in graph.edges" :key="edge.id" :points="edge.points"
                class="topology-edge" :class="{ inactive: !edge.active }"
                :marker-end="'url(#' + arrow + ')'" />
            </svg>
            <div v-for="node in graph.nodes" :key="node.name" class="topology-node"
              :style="{ left: node.x + 'px', top: node.y + 'px', width: topologyNodeWidth + 'px',
                height: topologyNodeHeight + 'px' }" :title="node.name">
              <span>{{ node.name }}</span>
            </div>
          </div>
        </div>
        <div class="table-wrap">
          <table class="data-table topology-table">
            <caption>拓扑连接明细</caption>
            <thead><tr><th scope="col">起点</th><th scope="col">终点</th><th scope="col">来源</th><th scope="col">状态</th></tr></thead>
            <tbody>
              <tr v-for="row in page.items" :key="row.id">
                <td class="archive-inventory-identity">{{ row.identifier }}</td>
                <td class="archive-inventory-identity">{{ row.relatedEndpoint }}</td>
                <td class="archive-inventory-identity">
                  {{ row.source }}<div v-if="row.label || row.description"><small>{{ row.label || row.description }}</small></div>
                </td>
                <td>{{ row.active ? '在用' : '停用' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </AsyncState>
    <ArchiveInventoryPager :previous="cursors.length > 1" :next="!!page.nextCursor" :loading="loading"
      @previous="load(cursors.slice(0, -1))" @next="page.nextCursor && load([...cursors, page.nextCursor])" />
  </section>
</template>

<style scoped>
#archive-topology-title, .topology-caption, .topology-legend, .topology-legend li { display: flex; align-items: center; gap: 8px; }
.topology-content { padding: 0 16px 12px; }
.topology-caption { justify-content: space-between; margin-bottom: 8px; flex-wrap: wrap; font-size: 13px; }
.topology-legend { padding: 0; margin: 0; gap: 14px; list-style: none; }
.topology-legend span { border-top: 2px solid #2063ae; width: 19px; }
.topology-legend span.inactive { border-top: 2px dashed #596c80; }
.topology-scroll { overflow: auto; max-height: 420px; border: 1px solid #dce6f2; border-radius: 8px; }
.topology-graph {
  position: relative;
  min-width: 100%;
  background: radial-gradient(circle, #cbd9e9 1px, transparent 1px) 0 0 / 16px 16px, #f5f9fd;
}
.topology-graph > svg { position: absolute; inset: 0; }
.topology-edge { fill: none; stroke: #2063ae; stroke-width: 1.7; }
.topology-edge.inactive { stroke: #596c80; stroke-dasharray: 5 5; }
.topology-node {
  position: absolute; display: flex; align-items: center; box-sizing: border-box;
  padding: 7px 10px; border-radius: 7px; border: 1px solid #a7bfd9;
  background: #fff; color: #233b59; box-shadow: 0 2px 5px #274a7820; font-size: 13px; font-weight: 600;
}
.topology-node > span {
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical;
  overflow: hidden; overflow-wrap: anywhere;
}
.topology-table caption { text-align: left; font-weight: 600; padding: 14px 0 8px; }
</style>

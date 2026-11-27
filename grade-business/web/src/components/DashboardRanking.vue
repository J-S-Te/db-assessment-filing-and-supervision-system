<script setup lang="ts">
import { ArrowUpRight } from '@lucide/vue'
import { archiveRecordPath } from '../lib/archives'
import { dashboardNumber, type DashboardSystemRisk } from '../lib/dashboard'
import { vDashboardScroll } from '../lib/dashboardAutoScroll'

defineProps<{ systems?: DashboardSystemRisk[]; loading: boolean; compact?: boolean; filingLinks?: boolean }>()
</script>

<template>
  <div v-if="systems?.length" class="rank-list" :class="{ compact }"
    v-dashboard-scroll>
    <ol class="rank-rows" data-dashboard-loop-original aria-label="高风险系统排行">
      <li v-for="(system, index) in systems" :key="system.filingId">
        <RouterLink :to="filingLinks ? '/filings/' + system.filingId : archiveRecordPath('systems', system.filingId)">
          <span class="rank-index" aria-hidden="true">{{ index + 1 }}</span>
          <span class="rank-name"><b>{{ system.systemName }}</b><small>{{ system.organizationName }}</small></span>
          <strong>{{ dashboardNumber(system.count) }}</strong>
          <ArrowUpRight class="rank-link-arrow" aria-hidden="true" />
        </RouterLink>
      </li>
    </ol>
    <ol class="rank-rows" data-dashboard-loop-copy aria-hidden="true" hidden></ol>
  </div>
  <p v-else class="dashboard-empty">
    {{ systems ? '当前没有关联备案系统的未销项高风险隐患。' : loading ? '正在读取风险系统…' : '暂时无法读取风险系统。' }}
  </p>
</template>

<style scoped>
.rank-list {
  --dashboard-row-gap: .38em;
  margin: 0; padding: .18em; min-height: 0; max-height: 65vh;
  overflow: auto; scrollbar-color: #416481 transparent;
}
.rank-rows { display: grid; gap: var(--dashboard-row-gap); margin: 0; padding: 0; list-style: none; }
.rank-list a {
  display: grid; grid-template-columns: 1.6em minmax(0, 1fr) auto 1em; align-items: center; gap: .65em;
  border-top: 1px solid #294662; background: #11263e; border-radius: 3px; padding: .55em .72em;
}
.rank-list a:hover { background: #173550; }
.rank-index { color: #90bad6; font-weight: 690; text-align: center; }
li:first-child .rank-index { color: #ffba8a; }
.rank-name { min-width: 0; overflow-wrap: anywhere; }
.rank-name small { display: block; color: var(--dashboard-muted); font-size: .76em; }
.rank-link-arrow { width: .9em; height: .9em; color: var(--dashboard-muted); }
.rank-list.compact { --dashboard-row-gap: 0px; }
.compact a { gap: .4em; padding: .42em .2em; border-radius: 0; background: transparent; font-size: .9em; }
.compact a:hover { background: #142e47; }
.compact .rank-name b, .compact .rank-name small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.dashboard-empty { color: var(--dashboard-muted); font-size: .85em; }
</style>

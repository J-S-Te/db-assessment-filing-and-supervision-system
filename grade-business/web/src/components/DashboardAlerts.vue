<script setup lang="ts">
import { onScopeDispose, shallowRef, watch } from 'vue'
import { alertPath, alertStatuses, alertTypes } from '../lib/alerts'
import type { DashboardAlert } from '../lib/dashboard'
import { vDashboardScroll } from '../lib/dashboardAutoScroll'
import { newHighAlertIds } from '../lib/dashboardMetrics'
import { formatTime, risks } from '../lib/domain'

const props = defineProps<{
  items?: DashboardAlert[]; loading: boolean; failed?: boolean; compact?: boolean; highlightNew?: boolean; readOnly?: boolean
}>()
const highlighted = shallowRef(new Set<string>())
let highlightTimer: ReturnType<typeof setTimeout> | undefined

watch(() => props.items, (current, previous) => {
  if (!props.highlightNew || !current) return
  const ids = newHighAlertIds(previous, current)
  if (!ids.length) return
  clearTimeout(highlightTimer)
  highlighted.value = new Set(ids)
  highlightTimer = setTimeout(() => { highlighted.value = new Set() }, 8_000)
})
onScopeDispose(() => clearTimeout(highlightTimer))
</script>

<template>
  <div v-if="items?.length" class="alert-feed" :class="{ compact }" v-dashboard-scroll
    :aria-label="compact ? '最新预警' : '实时预警'" tabindex="0">
    <div class="alert-rows" data-dashboard-loop-original>
      <component :is="readOnly ? 'div' : 'RouterLink'" v-for="item in items" :key="item.id" class="alert-card" :data-severity="item.severity"
        :class="{ 'new-alert': highlighted.has(item.id) && item.status !== 'closed' }"
        :to="readOnly ? undefined : { path: alertPath, query: { alert: item.id } }">
        <div>
          <strong :title="item.systemName">{{ item.systemName }}</strong>
          <span>{{ alertTypes[item.type] }} · {{ alertStatuses[item.status] }}<template v-if="!compact"> · {{ item.organizationName }}</template></span>
        </div>
        <div class="alert-meta">
          <span><b v-if="highlighted.has(item.id) && item.status !== 'closed'" class="alert-new-label">新增</b>
            {{ risks[item.severity] }}</span>
          <time :datetime="item.triggeredAt">{{ formatTime(item.triggeredAt) }}</time>
        </div>
      </component>
    </div>
    <div class="alert-rows" data-dashboard-loop-copy aria-hidden="true" hidden></div>
  </div>
  <p v-else class="dashboard-empty">
    {{ items ? '暂无站内预警。' : failed ? '暂时无法读取预警。' : loading ? '正在读取预警…' : '暂无站内预警。' }}
  </p>
</template>

<style scoped>
.alert-feed {
  --dashboard-row-gap: .55em;
  min-height: 0; max-height: 65vh;
  overflow: auto; padding: .18em; scrollbar-color: #416481 transparent;
}
.alert-rows { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--dashboard-row-gap); }
.alert-feed.compact { max-height: min(32rem, 55vh); --dashboard-row-gap: .22em; }
.alert-card {
  --alert-tone: #58afff;
  display: flex; align-items: flex-start; justify-content: space-between; gap: .5em 1em;
  border-left: 2px solid var(--alert-tone); border-radius: 3px; padding: .6em .65em; background: #132a43;
}
.alert-card[data-severity="high"] { --alert-tone: #ff7878; }
.alert-card[data-severity="medium"] { --alert-tone: #f2b347; }
.alert-card:hover { background: #1a3150; }
.alert-card > div { min-width: 0; }
.alert-card strong, .alert-card span { display: block; overflow-wrap: anywhere; }
.alert-card strong { color: #eaf2ff; font-size: .88em; }
.alert-card span, .alert-card time, .dashboard-empty { color: var(--dashboard-muted); font-size: .77em; }
.alert-meta { text-align: right; }
.alert-meta span { color: var(--alert-tone); font-weight: 650; }
.alert-meta time { white-space: nowrap; }
.compact .alert-card { padding: .36em .43em; gap: .35em; background: #11243c; }
.compact .alert-card:hover { background: #1a3150; }
.compact .alert-card strong { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.compact .alert-card > div:first-child { flex: 1; }
.compact .alert-meta { flex: none; max-width: 46%; font-size: .86em; }
.compact .alert-meta time { white-space: normal; }
.compact .new-alert, .compact .new-alert:hover { background: #3b2133; }
.alert-new-label { margin-inline-end: .3em; color: #ffd5cf; font-weight: 650; }
@media (prefers-reduced-motion: no-preference) {
  .new-alert { animation: alert-highlight .75s ease-out; }
  @keyframes alert-highlight { from { background-color: #6b2940; } }
}
</style>

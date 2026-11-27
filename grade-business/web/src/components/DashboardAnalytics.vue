<script setup lang="ts">
import { computed } from 'vue'
import { dashboardNumber } from '../lib/dashboard'
import type { DashboardIndustryAnalysis, DashboardIssueAnalysis } from '../lib/dashboardAnalytics'
import { vDashboardScroll } from '../lib/dashboardAutoScroll'

const props = defineProps<{ industry?: DashboardIndustryAnalysis; issues?: DashboardIssueAnalysis; loading: boolean }>()
const issueMax = computed(() => Math.max(1, ...(props.issues?.items.map(issue => issue.count) ?? [])))
</script>

<template>
  <div class="analytics-content" :class="{ 'has-industry': industry }">
    <template v-if="industry">
      <p class="analytics-caption">有效备案系统及其未销项隐患</p>
      <div v-if="industry.items.length || industry.unclassified.systems" v-dashboard-scroll
        class="industry-scroll" tabindex="0" aria-label="行业分析明细">
        <table aria-label="备案行业风险分析">
          <thead><tr><th scope="col">行业</th><th scope="col">系统</th><th scope="col">高</th><th scope="col">中</th><th scope="col">低</th></tr></thead>
          <tbody data-dashboard-loop-original>
            <tr v-for="row in industry.items" :key="row.code">
              <th scope="row">{{ row.label }}</th><td>{{ dashboardNumber(row.systems) }}</td>
              <td>{{ dashboardNumber(row.high) }}</td><td>{{ dashboardNumber(row.medium) }}</td><td>{{ dashboardNumber(row.low) }}</td>
            </tr>
            <tr v-if="industry.unclassified.systems" class="unclassified">
              <th scope="row">未分类</th><td>{{ dashboardNumber(industry.unclassified.systems) }}</td>
              <td>{{ dashboardNumber(industry.unclassified.high) }}</td>
              <td>{{ dashboardNumber(industry.unclassified.medium) }}</td>
              <td>{{ dashboardNumber(industry.unclassified.low) }}</td>
            </tr>
          </tbody>
          <tbody data-dashboard-loop-copy aria-hidden="true" hidden></tbody>
        </table>
      </div>
      <p v-else class="analytics-caption">暂无有效备案系统。</p>
    </template>
    <template v-else-if="issues">
      <p class="analytics-caption">按已入账隐患的问题分类统计，含已销项</p>
      <ol v-if="issues.items.length" class="issue-ranking" aria-label="问题分类排行">
        <li v-for="item in issues.items" :key="item.code">
          <span>{{ item.label }}</span><strong>{{ dashboardNumber(item.count) }}</strong>
          <span class="issue-bar" aria-hidden="true"><span :style="{ width: item.count * 100 / issueMax + '%' }"></span></span>
        </li>
      </ol>
      <p v-else class="analytics-caption">{{ issues.unclassified ? '已入账隐患尚无已分类记录。' : '暂无已入账隐患。' }}</p>
      <p v-if="issues.unclassified" class="unclassified">未分类 <strong>{{ dashboardNumber(issues.unclassified) }}</strong> 项</p>
    </template>
    <p v-else class="analytics-caption">{{ loading ? '正在读取驾驶舱分析…' : '暂时无法读取驾驶舱分析。' }}</p>
  </div>
</template>

<style scoped>
.analytics-content { min-height: 0; overflow: auto; scrollbar-color: #416481 transparent; }
.analytics-content.has-industry { display: flex; flex-direction: column; gap: .4em; overflow: visible; }
.industry-scroll { min-height: 0; max-height: 65vh; overflow: auto; scrollbar-color: #416481 transparent; }
.industry-scroll thead { position: sticky; top: 0; z-index: 1; background: var(--dashboard-paper); }
.analytics-content table {
  width: 100%; border-collapse: separate; border-spacing: 0; font-variant-numeric: tabular-nums;
}
.analytics-content th, .analytics-content td { padding: .34em .35em; border-bottom: 1px solid #294662; text-align: right; }
.analytics-content th:first-child { text-align: left; overflow-wrap: anywhere; }
.analytics-content thead, .analytics-caption, .unclassified { color: var(--dashboard-muted); }
.analytics-caption, .unclassified { font-size: .8em; }
.analytics-content td:nth-child(3) { color: #ffb6a8; }
.analytics-content td:nth-child(4) { color: #ffd698; }
.issue-ranking { display: grid; gap: .52em; margin: .45em 0; padding: 0; list-style: none; }
.issue-ranking li { display: grid; grid-template-columns: 1fr auto; gap: .18em .5em; font-size: .86em; }
.issue-bar { grid-column: 1 / -1; height: .24em; border-radius: 2px; background: #29435e; }
.issue-bar > span { display: block; height: 100%; background: #6cbbea; border-radius: inherit; }
</style>

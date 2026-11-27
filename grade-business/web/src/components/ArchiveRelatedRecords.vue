<script setup lang="ts">
import ArchivePageLinks from './ArchivePageLinks.vue'
import type { ArchiveRelations } from '../lib/archives'
import { formatTime, risks, statuses } from '../lib/domain'
import { hazardBadge, hazardPath, hazardStatuses } from '../lib/hazards'
import { remediationBadge, remediationRecordPath, remediationStatuses } from '../lib/remediations'

defineProps<{ profile: ArchiveRelations; showSystem?: boolean }>()
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="archive-reports">
    <header class="panel-heading"><h2 id="archive-reports">测评历史</h2></header>
    <div v-if="profile.reports.items.length" class="table-wrap">
      <table class="data-table">
        <thead><tr><th>报告编号</th><th v-if="showSystem">系统</th><th>测评机构</th><th>提交时间</th><th>风险</th><th>审核状态</th></tr></thead>
        <tbody>
          <tr v-for="row in profile.reports.items" :key="row.id">
            <td><RouterLink class="text-action" :to="'/reports/' + encodeURIComponent(row.id)">{{ row.number }}</RouterLink></td>
            <td v-if="showSystem">{{ row.systemName }}</td>
            <td>{{ row.assessmentOrg }}</td>
            <td>{{ formatTime(row.submittedAt) }}</td>
            <td>{{ risks[row.risk] }}</td>
            <td><span class="status-badge" :class="'status-' + row.status">{{ statuses[row.status] }}</span></td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="archive-empty muted">本页没有已提交的测评报告。</p>
    <ArchivePageLinks field="reportsCursor" :next-cursor="profile.reports.nextCursor" />
  </section>

  <section class="panel detail-panel" aria-labelledby="archive-remediations">
    <header class="panel-heading"><h2 id="archive-remediations">整改规划</h2></header>
    <div v-if="profile.remediations.items.length" class="table-wrap">
      <table class="data-table">
        <thead><tr><th>关联测评报告</th><th v-if="showSystem">系统</th><th>整改期限</th><th>提交时间</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>
          <tr v-for="row in profile.remediations.items" :key="row.id">
            <td><RouterLink class="text-action" :to="'/reports/' + encodeURIComponent(row.reportId)">{{ row.reportNumber }}</RouterLink></td>
            <td v-if="showSystem">{{ row.systemName }}</td>
            <td>{{ row.deadline }}</td>
            <td>{{ formatTime(row.submittedAt) }}</td>
            <td><span class="status-badge" :class="remediationBadge(row.status)">{{ remediationStatuses[row.status] }}</span></td>
            <td><RouterLink class="text-action" :to="remediationRecordPath(row.id)">查看整改规划</RouterLink></td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="archive-empty muted">本页没有已提交的整改规划。</p>
    <ArchivePageLinks field="remediationsCursor" :next-cursor="profile.remediations.nextCursor" />
  </section>

  <section class="panel detail-panel" aria-labelledby="archive-hazards">
    <header class="panel-heading"><h2 id="archive-hazards">隐患与销项</h2></header>
    <div v-if="profile.hazards.items.length" class="table-wrap">
      <table class="data-table">
        <thead><tr><th>隐患</th><th v-if="showSystem">系统</th><th>风险</th><th>整改期限</th><th>状态</th><th>销项时间</th></tr></thead>
        <tbody>
          <tr v-for="row in profile.hazards.items" :key="row.id">
            <td><RouterLink class="text-action archive-hazard" :to="hazardPath(row.id)">{{ row.description }}</RouterLink></td>
            <td v-if="showSystem">{{ row.systemName }}</td>
            <td>{{ risks[row.severity] }}</td>
            <td>{{ row.deadline }}</td>
            <td><span class="status-badge" :class="hazardBadge(row.status)">{{ hazardStatuses[row.status] }}</span></td>
            <td>{{ formatTime(row.closedAt) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else class="archive-empty muted">本页没有隐患记录。</p>
    <ArchivePageLinks field="hazardsCursor" :next-cursor="profile.hazards.nextCursor" />
  </section>
</template>

<style scoped>
.archive-empty { padding: 14px 16px; }
.archive-hazard { max-width: 28rem; white-space: normal; overflow-wrap: anywhere; }
</style>

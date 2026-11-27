<script setup lang="ts">
import { hasPermission } from '../lib/session'
import { computed, ref } from 'vue'
import { ChevronLeft, ChevronRight, Plus, Settings } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { filingReviewLabel } from '../lib/domain'
import { caseLabels, formatTime, levels, risks, statuses, type CaseKind, type CaseSummary, type Precheck } from '../lib/domain'
import { params } from '../lib/api'
import { queryValue, usePagedList } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

const props = defineProps<{ kind: CaseKind }>()
const route = useRoute()
const { keyword, filter, extraFilter, page, loading, failure, load, refresh, search, reset, nextPage, previousPage }
  = usePagedList<CaseSummary>(() => '/' + props.kind, 'status', () => props.kind === 'filings' ? 'lifecycle' : 'risk')
const columns = {
  systemName: '系统名称',
  organizationName: '所属单位',
  assessmentOrg: '测评机构',
  number: '备案编号',
  level: '定级',
  status: '审核流转',
  precheckStatus: 'AI 预审',
}
const precheckBadges = {
  queued: { label: '进行中', detail: '进行中，等待预审' },
  running: { label: '进行中', detail: '进行中，正在预审' },
  succeeded: { label: '已结束', detail: '最近一次预审已结束，结果以详情页为准' },
  failed: { label: '已结束', detail: '已结束，预审失败' },
} satisfies Record<Precheck['status'], { label: string; detail: string }>
const visibleColumns = ref(Object.keys(columns))
const reportExportURL = computed(() => '/api/v1/reports/export' + params({
  q: queryValue(route.query.q), status: queryValue(route.query.status), risk: queryValue(route.query.risk),
}))
refreshBusinessOnFocus(() => { if (route.path === '/' + props.kind) void refresh() }, 10_000)
</script>

<template>
  <section class="v2-list" :aria-label="caseLabels[kind].module">
      <form class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <nav v-if="kind === 'filings' && hasPermission('admin.grade.case.edit')" class="v2-views" aria-label="备案视图">
            <RouterLink class="active" to="/filings" aria-current="page">全部</RouterLink>
            <RouterLink :to="{ path: '/filing-applications', query: { type: 'change' } }">变更</RouterLink>
            <RouterLink :to="{ path: '/filing-applications', query: { type: 'cancel' } }">注销</RouterLink>
          </nav>
        <label class="field">
          <span>审核状态</span>
          <select v-model="filter" @change="search">
            <option value="">全部状态</option>
            <template v-for="(status, key) in statuses" :key="key">
              <option v-if="kind === 'filings' || key !== 'withdrawn'" :value="key">{{ status }}</option>
            </template>
          </select>
        </label>
        <label v-if="kind === 'reports'" class="field">
          <span>风险等级</span>
          <select v-model="extraFilter" @change="search">
            <option value="">全部风险</option>
            <option v-for="(name, key) in risks" :key="key" :value="key">{{ name }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120"
            :placeholder="kind === 'filings' ? '系统 / 单位 / 备案编号' : '报告编号 / 系统 / 机构'">
        </label>
        <button class="button primary" type="submit">查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
        </div>
        <div class="v2-toolbar-row">
          <RouterLink v-if="hasPermission('admin.grade.case.edit')" class="button primary" :to="'/' + kind + '/new'">
            <Plus :size="16" aria-hidden="true" />{{ kind === 'filings' ? '备案录入' : '上传测评报告' }}
          </RouterLink>
          <RouterLink v-if="kind === 'filings' && hasPermission('admin.grade.case.edit')" class="button neutral" to="/filings/history-import">导入历史材料</RouterLink>
          <span class="v2-toolbar-spacer"></span>
          <a v-if="kind === 'reports' && !failure && hasPermission('admin.grade.case.edit')" class="button neutral" :href="reportExportURL"
            title="按已应用的筛选导出测评报告台账">导出台账</a>
          <label v-if="kind === 'filings'" class="field filing-lifecycle">
            <span>备案效力</span>
            <select v-model="extraFilter" @change="search">
              <option value="">全部备案</option>
              <option value="active">有效</option>
              <option value="cancelled">已注销</option>
            </select>
          </label>
          <details v-if="kind === 'filings'" class="column-settings">
            <summary class="button neutral small"><Settings :size="12" aria-hidden="true" />列设置</summary>
            <div class="column-options">
              <span class="muted">显示列</span>
              <label v-for="(label, key) in columns" :key="key">
                <input v-model="visibleColumns" type="checkbox" :value="key"> {{ label }}
              </label>
            </div>
          </details>
          <button class="button neutral small" type="button" :disabled="loading" @click="load">⟳ 刷新</button>
        </div>
      </form>

      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        :empty-text="kind === 'filings' ? '暂无符合条件的备案记录。' : '暂无符合条件的测评报告。'"
        @retry="load">
        <div class="table-wrap v2-list-table">
          <table class="data-table">
            <thead>
              <tr>
                <template v-if="kind === 'filings'">
                  <th v-for="(label, key) in columns" v-show="visibleColumns.includes(key)" :key="key">{{ label }}</th>
                </template>
                <template v-else>
                  <th>报告编号</th><th>系统名称</th><th>测评机构</th><th>提交时间</th><th>风险级别</th><th>审核状态</th>
                </template>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <template v-if="kind === 'filings'">
                  <td v-show="visibleColumns.includes('systemName')">{{ item.systemName }}</td>
                  <td v-show="visibleColumns.includes('organizationName')">{{ item.organizationName }}</td>
                  <td v-show="visibleColumns.includes('assessmentOrg')">{{ item.assessmentOrg }}</td>
                  <td v-show="visibleColumns.includes('number')" class="code">{{ item.number || "待分配" }}</td>
                  <td v-show="visibleColumns.includes('level')">
                    <span class="level-tag">{{ levels[item.level ?? ''] ?? '—' }}</span>
                  </td>
                </template>
                <template v-else>
                  <td class="code">{{ item.number }}</td><td class="emphasis">{{ item.systemName }}</td>
                  <td>{{ item.assessmentOrg }}</td><td>{{ formatTime(item.submittedAt) }}</td>
                  <td><span :class="'risk-' + (item.risk ?? '')">{{ item.risk ? risks[item.risk] : '—' }}</span></td>
                </template>
                <td v-show="kind === 'reports' || visibleColumns.includes('status')">
                  <span v-if="item.cancelledAt" class="level-tag">已注销</span>
                  <StatusBadge v-else :status="item.status" :label="filingReviewLabel(item.status, item.initialReviewAudience)" />
                </td>
                <td v-if="kind === 'filings'" v-show="visibleColumns.includes('precheckStatus')">
                  <span v-if="item.precheckStatus" class="status-badge precheck-badge"
                    :data-state="item.precheckStatus" :title="precheckBadges[item.precheckStatus].detail"
                    :aria-label="precheckBadges[item.precheckStatus].detail">
                    <span aria-hidden="true"></span>{{ precheckBadges[item.precheckStatus].label }}
                  </span>
                  <span v-else class="muted" title="当前版本未发起预审" aria-label="当前版本未发起预审">—</span>
                </td>
                <td><RouterLink class="text-action"
                  :to="{ path: '/' + kind + '/' + encodeURIComponent(item.id), query: route.query }"
                  :aria-label="'查看 ' + (item.number || item.systemName)">详情</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <footer class="panel-footer pager" v-if="!loading && !failure">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button class="button neutral small" type="button" :disabled="!route.query.cursor" @click="previousPage">
            <ChevronLeft :size="16" aria-hidden="true" />上一页
          </button>
          <button class="button neutral small" type="button" :disabled="!page.nextCursor" @click="nextPage">
            下一页<ChevronRight :size="16" aria-hidden="true" />
          </button>
        </div>
      </footer>
  </section>
</template>

<style scoped>
.filing-lifecycle select { min-height: 28px; font-size: 12px; padding-block: 2px; }
.precheck-badge:is([data-state='queued'], [data-state='running']) {
  background: var(--c-info-bg); color: var(--c-info);
}
.precheck-badge[data-state='succeeded'] { background: var(--c-success-bg); color: var(--c-success); }
.precheck-badge[data-state='failed'] { background: var(--c-warn-bg); color: var(--c-warn); }
.column-settings { position: relative; }
.column-settings summary { cursor: pointer; list-style: none; }
.column-settings summary::-webkit-details-marker { display: none; }
.column-options {
  position: absolute; z-index: 10; right: 0; top: calc(100% + 6px); width: 168px;
  padding: 8px; border: 1px solid var(--line); border-radius: 10px; background: var(--paper);
  box-shadow: 0 10px 30px rgba(16,24,40,.12);
}
.column-options > span { display: block; padding: 4px 8px 6px; font-size: 11px; }
.column-options label {
  display: flex; align-items: center; gap: 8px; padding: 6px 8px; border-radius: 6px;
  font-size: 13px; cursor: pointer;
}
.column-options label:hover { background: var(--c-surface-2); }
.column-options input { width: auto; min-height: 0; margin: 0; }
</style>

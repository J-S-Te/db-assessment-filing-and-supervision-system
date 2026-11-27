<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ChevronRight, Plus, Search } from '@lucide/vue'
import AsyncState from '../components/AsyncState.vue'
import { formatTime } from '../lib/domain'
import { queryValue, usePagedList } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import {
  implementationPath, implementationRecord, implementationStatusClass, implementationStatuses, implementationTypes,
  type ImplementationSummary,
} from '../lib/implementations'

const route = useRoute()
const cursor = computed(() => queryValue(route.query.cursor))
const archive = computed(() => queryValue(route.query.archive) === 'archived' ? 'archived' : 'unarchived')
const view = computed(() => {
  if (archive.value === 'archived') return 'archive'
  if (queryValue(route.query.status) === 'approved') return 'records'
  if (queryValue(route.query.type) === 'change') return 'changes'
  return 'reports'
})
const {
  keyword, filter: status, extraFilter: type, page, loading, failure,
  load, refresh, search, reset, nextPage, previousPage,
} = usePagedList<ImplementationSummary>(
  () => implementationPath, 'status', () => 'type', () => ({ archive: archive.value }),
)
refreshBusinessOnFocus(() => { if (route.path === implementationPath) void refresh() }, 10_000)
</script>

<template>
  <section class="v2-list" aria-label="实施报备列表">
      <form class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <nav class="v2-views" aria-label="实施视图">
            <RouterLink :to="implementationPath" :class="{ active: view === 'reports' }">实施报备</RouterLink>
            <RouterLink :to="{ path: implementationPath, query: { status: 'approved' } }"
              :class="{ active: view === 'records' }">实施记录</RouterLink>
            <RouterLink :to="{ path: implementationPath, query: { type: 'change' } }"
              :class="{ active: view === 'changes' }">变更记录</RouterLink>
            <RouterLink :to="{ path: implementationPath, query: { archive: 'archived' } }"
              :class="{ active: view === 'archive' }">已归档</RouterLink>
          </nav>
        <label class="field">
          <span>报备类型</span>
          <select v-model="type">
            <option value="">全部类型</option>
            <option v-for="(label, value) in implementationTypes" :key="value" :value="value">{{ label }}</option>
          </select>
        </label>
        <label class="field">
          <span>流转状态</span>
          <select v-model="status">
            <option value="">全部状态</option>
            <option v-for="(label, value) in implementationStatuses" :key="value" :value="value">{{ label }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span><input v-model="keyword" type="search" maxlength="120" placeholder="系统 / 单位 / 备案编号" />
        </label>
        <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
        </div>
        <div class="v2-toolbar-row">
          <RouterLink :to="implementationPath + '/new'" class="button primary"><Plus :size="16" aria-hidden="true" />实施报备录入</RouterLink>
          <span class="v2-toolbar-spacer"></span>
          <button class="button neutral small" type="button" :disabled="loading" @click="load">⟳ 刷新</button>
        </div>
      </form>

      <p v-if="view === 'reports' || view === 'changes'" class="muted">
        同一备案每年最多三次有效变更报备。计划可修改至测评开始日零点。发起人撤销后，同一备案须等待 15 天才能重新提交。
      </p>
      <AsyncState :loading="loading" :error="failure" :empty="page.items.length === 0" empty-text="暂无符合条件的实施报备。" @retry="load">
        <div class="table-wrap v2-list-table">
          <table class="data-table">
            <thead>
              <tr><th>系统名称</th><th>所属单位</th><th>测评机构</th><th>报备类型</th><th>实施周期</th><th>提交时间</th><th>状态</th><th>操作</th></tr>
            </thead>
            <tbody>
              <tr v-for="row in page.items" :key="row.id">
                <td><strong class="emphasis">{{ row.systemName }}</strong><br><span class="code">{{ row.filingNumber }}</span></td>
                <td>{{ row.organizationName }}</td>
                <td>{{ row.assessmentOrg || '—' }}</td>
                <td>{{ implementationTypes[row.type] }}</td>
                <td>{{ row.plannedStart }} ~ {{ row.plannedEnd }}</td>
                <td>{{ formatTime(row.submittedAt) }}</td>
                <td><span class="status-badge" :class="row.archivedAt ? 'status-draft' : implementationStatusClass(row.status)">
                  <span aria-hidden="true"></span>{{ row.archivedAt ? '已归档' : implementationStatuses[row.status] }}
                </span></td>
                <td><RouterLink :to="{ path: implementationRecord(row.id), query: route.query }" class="text-action">
                  详情<ChevronRight :size="14" aria-hidden="true" />
                </RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="cursor || page.nextCursor" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button type="button" class="button neutral small" :disabled="loading || !cursor" @click="previousPage">上一页</button>
          <button type="button" class="button neutral small" :disabled="loading || !page.nextCursor" @click="nextPage">下一页</button>
        </div>
      </footer>
  </section>
</template>

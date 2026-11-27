<script setup lang="ts">
import { ChevronLeft, ChevronRight, Search } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { formatTime } from '../lib/domain'
import { usePagedList } from '../lib/paging'

const resources = {
  filings: '备案',
  'filing-applications': '备案变更与注销',
  implementations: '实施报备',
  reports: '测评报告',
  remediations: '整改规划',
  hazards: '隐患台账',
  alerts: '站内预警',
  publications: '信息发布',
  'workday-calendar': '工作日历',
} as const

interface AuditItem {
  id: string
  resource: keyof typeof resources
  resourceId: string
  title: string
  reference?: string
  action: string
  actionLabel: string
  operator: string
  occurredAt: string
  href: string
}

const route = useRoute()
const { keyword, filter, page, loading, failure, load, search, reset, nextPage, previousPage }
  = usePagedList<AuditItem>(() => '/audit', 'resource')
</script>

<template>
  <section class="v2-list" aria-label="业务操作审计">
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field">
          <span>业务类型</span>
          <select v-model="filter" @change="search">
            <option value="">全部业务</option>
            <option v-for="(label, resource) in resources" :key="resource" :value="resource">{{ label }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="操作人、对象名称或编号">
        </label>
        <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
      </div>
    </form>

    <div class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="暂无符合条件的业务操作记录。" @retry="load">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>操作人</th><th>操作</th><th>业务对象</th><th>发生时间</th></tr></thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td>{{ item.operator }}</td>
                <td>{{ item.actionLabel }}</td>
                <td class="audit-object">
                  <span class="level-tag">{{ resources[item.resource] }}</span>
                  <RouterLink class="text-action" :to="item.href">{{ item.title }}</RouterLink>
                  <small v-if="item.reference && item.reference !== item.title" class="muted">{{ item.reference }}</small>
                </td>
                <td>{{ formatTime(item.occurredAt) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>

      <footer v-if="!loading && !failure" class="panel-footer pager">
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
    </div>
  </section>
</template>

<style scoped>
.audit-object { min-width: 13rem; max-width: 38rem; white-space: normal; overflow-wrap: anywhere; }
.audit-object .level-tag { margin-right: 8px; }
.audit-object small { display: block; margin-top: 4px; }
</style>

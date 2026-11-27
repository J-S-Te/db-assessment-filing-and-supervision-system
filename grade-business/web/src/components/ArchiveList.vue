<script setup lang="ts">
import { ChevronRight, Search } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from './AsyncState.vue'
import { formatTime, levels } from '../lib/domain'
import { queryValue, usePagedList } from '../lib/paging'
import {
  archivePaths, archiveRecordPath, archiveSystemStatuses,
  type ArchiveSystem, type ArchiveTab, type ArchiveUnit,
} from '../lib/archives'

const props = defineProps<{ kind: ArchiveTab }>()
const route = useRoute()
const { keyword, filter, extraFilter, page, failure, loading, load, search, reset, nextPage, previousPage } =
  usePagedList<ArchiveUnit | ArchiveSystem>(() => archivePaths[props.kind], 'status',
    () => props.kind === 'systems' ? 'level' : undefined)
</script>

<template>
  <section class="v2-list" :aria-label="kind === 'units' ? '单位档案' : '系统档案'">
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label v-if="kind === 'systems'" class="field">
          <span>备案状态</span>
          <select v-model="filter">
            <option value="">全部状态</option>
            <option v-for="(label, value) in archiveSystemStatuses" :key="value" :value="value">{{ label }}</option>
          </select>
        </label>
        <label v-if="kind === 'systems'" class="field">
          <span>等保级别</span>
          <select v-model="extraFilter">
            <option value="">全部级别</option>
            <option v-for="(label, value) in levels" :key="value" :value="value">{{ label }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120"
            :placeholder="kind === 'units' ? '单位名称 / 信用代码' : '系统 / 单位 / 备案编号 / 信用代码'">
        </label>
        <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
        <span class="v2-toolbar-spacer"></span>
        <button class="button neutral small" type="button" :disabled="loading" @click="load">⟳ 刷新</button>
      </div>
    </form>

    <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
      :empty-text="kind === 'units' ? '未找到符合条件的单位档案。' : '未找到符合条件的系统档案。备案终审通过后进入系统档案。'"
      @retry="load">
      <div class="table-wrap v2-list-table">
        <table class="data-table">
          <thead>
            <tr>
              <template v-if="kind === 'units'">
                <th>单位名称</th><th>单位类型</th><th>所属区县</th><th>系统数</th><th>有效系统</th><th>名录状态</th>
              </template>
              <template v-else><th>系统名称</th><th>所属单位</th><th>等保级别</th><th>备案状态</th><th>登记资产</th></template>
              <th>最近测评提交</th><th>未销项隐患</th><th>操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in page.items" :key="'key' in row ? row.key : row.id">
              <template v-if="'key' in row">
                <td><strong class="emphasis">{{ row.directoryName || row.organizationName }}</strong></td>
                <td>{{ row.classification || '—' }}</td>
                <td>{{ row.district || '—' }}</td>
                <td>{{ row.systemCount }}</td>
                <td>{{ row.activeSystemCount }}</td>
                <td>{{ row.directoryStatus === 'active' ? '有效' : row.directoryStatus === 'inactive' ? '已停用' : '—' }}</td>
              </template>
              <template v-else>
                <td><strong class="emphasis">{{ row.systemName }}</strong><br><span class="code">{{ row.filingNumber }}</span></td>
                <td>{{ row.organizationName }}</td>
                <td>{{ levels[row.level] }}</td>
                <td><span class="status-badge" :class="row.status === 'active' ? 'status-approved' : 'status-draft'">
                  {{ archiveSystemStatuses[row.status] }}
                </span></td>
                <td>{{ row.assetCount }}</td>
              </template>
              <td>{{ formatTime(row.latestReportAt) }}</td>
              <td>{{ row.openHazardCount }}</td>
              <td><RouterLink class="text-action"
                :to="{ path: archiveRecordPath(kind, 'key' in row ? row.key : row.id), query: route.query }">
                {{ 'key' in row ? '全息画像' : '详情' }}<ChevronRight :size="14" aria-hidden="true" />
              </RouterLink></td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>

    <footer v-if="queryValue(route.query.cursor) || page.nextCursor" class="panel-footer pager">
      <span>本页 {{ page.items.length }} 条</span>
      <div>
        <button class="button neutral small" type="button"
          :disabled="loading || !queryValue(route.query.cursor)" @click="previousPage">上一页</button>
        <button class="button neutral small" type="button" :disabled="loading || !page.nextCursor" @click="nextPage">下一页</button>
      </div>
    </footer>
  </section>
</template>

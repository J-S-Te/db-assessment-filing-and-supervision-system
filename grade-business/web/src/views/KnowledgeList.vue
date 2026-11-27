<script setup lang="ts">
import { hasPermission } from '../lib/session'
import { ChevronLeft, ChevronRight, Plus, RefreshCw } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { categories, formatTime } from '../lib/domain'
import { knowledgePath, knowledgeRecord, type KnowledgeEntrySummary, type KnowledgeImportResult } from '../lib/knowledge'
import { queryValue, usePagedList } from '../lib/paging'
import KnowledgeImport from './KnowledgeImport.vue'

const route = useRoute()
const router = useRouter()
const status = computed(() => queryValue(route.query.status) || 'active')
const importOpen = ref(false)
const importResult = ref<KnowledgeImportResult>()
const { keyword, filter, extraFilter, page, loading, failure, load, search, reset, nextPage, previousPage }
  = usePagedList<KnowledgeEntrySummary>(() => knowledgePath, 'category', () => 'source', () => ({
    status: queryValue(route.query.status),
  }))

/**
 * 用途 按选中的知识启停状态重新查询，并清除旧分页游标。
 * 参数 event 为状态下拉框的选择事件。
 * 返回值 保留其他筛选条件的导航任务。
 */
function changeStatus(event: Event) {
  const value = (event.target as HTMLSelectElement).value
  return router.push({
    path: knowledgePath, query: { ...route.query, status: value === 'active' ? undefined : value, cursor: undefined },
  })
}

/**
 * 用途 恢复知识列表的默认状态和筛选条件。
 * 参数 无。
 * 返回值 重置后导航或重新读取的任务。
 */
function resetFilters() {
  if (queryValue(route.query.status)) return router.push(knowledgePath)
  return reset()
}

/**
 * 用途 展示实际导入结果并刷新当前知识检索。
 * 参数 result 为服务端返回的入库数量和真实条目标识。
 * 返回值 导入后列表的刷新或导航任务。
 */
function afterImport(result: KnowledgeImportResult) {
  importOpen.value = false
  importResult.value = result
  if (route.query.cursor) return router.replace({ path: knowledgePath, query: { ...route.query, cursor: undefined } })
  return load()
}
</script>

<template>
  <section class="v2-list" aria-label="知识库">
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field">
          <span>分类</span>
          <select v-model="filter" @change="search">
            <option value="">全部分类</option>
            <option v-for="(category, value) in categories" :key="value" :value="value">{{ category }}</option>
          </select>
        </label>
        <label class="field">
          <span>来源</span>
          <input v-model="extraFilter" type="search" maxlength="500" placeholder="全部来源">
        </label>
        <label class="field">
          <span>状态</span>
          <select :value="status" @change="changeStatus">
            <option value="active">启用</option>
            <option value="inactive">停用</option>
            <option value="all">全部状态</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="全文检索：法规 / 标准 / 漏洞 / 案例">
        </label>
        <button class="button primary" type="submit">检索</button>
        <button class="button neutral" type="button" @click="resetFilters">重置</button>
      </div>
      <div class="v2-toolbar-row">
        <RouterLink v-if="hasPermission('admin.agent.kb.edit')" class="button primary" to="/knowledge/new"><Plus :size="16" aria-hidden="true" />新增知识</RouterLink>
        <button v-if="hasPermission('admin.agent.kb.edit')" class="button neutral" type="button" @click="importOpen = true">批量导入</button>
        <span class="v2-toolbar-spacer"></span>
        <button class="button neutral small" type="button" :disabled="loading" @click="load">
          <RefreshCw :size="14" aria-hidden="true" />刷新
        </button>
      </div>
    </form>
    <p v-if="importResult" class="form-message success-message" role="status">
      已导入 {{ importResult.count }} 条知识。
      <RouterLink v-if="importResult.ids[0]" class="text-action"
        :to="{ path: knowledgeRecord(importResult.ids[0]), query: route.query }">查看首条</RouterLink>
    </p>
    <div class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="未找到符合条件的知识。" @retry="load">
        <div class="table-wrap">
          <table class="data-table knowledge-table">
            <thead><tr><th>分类</th><th>名称</th><th>来源</th><th>标签</th><th>更新时间</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td><span class="level-tag" :data-category="item.category">{{ categories[item.category] ?? item.category }}</span></td>
                <td>{{ item.title }}
                  <span v-if="item.inactiveAt" class="level-tag inactive-tag">已停用</span>
                </td>
                <td>{{ item.source }}</td>
                <td>
                  <div v-if="item.tags.length" class="knowledge-tags">
                    <span v-for="tag in item.tags" :key="tag" class="level-tag knowledge-tag">{{ tag }}</span>
                  </div>
                  <span v-else class="muted">—</span>
                </td>
                <td>{{ formatTime(item.updatedAt) }}</td>
                <td><div class="knowledge-actions">
                  <RouterLink class="text-action"
                    :to="{ path: knowledgeRecord(item.id), query: route.query }"
                    :aria-label="'查看 ' + item.title">查看</RouterLink>
                  <RouterLink v-if="hasPermission('admin.agent.kb.edit')" class="text-action"
                    :to="{ path: knowledgeRecord(item.id) + '/tags', query: route.query }"
                    :aria-label="'维护标签 ' + item.title">标签</RouterLink>
                  <RouterLink v-if="hasPermission('admin.agent.kb.edit')" class="text-action"
                    :to="{ path: knowledgeRecord(item.id) + '/edit', query: route.query }"
                    :aria-label="'编辑 ' + item.title">编辑</RouterLink>
                </div></td>
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
    </div>
    <KnowledgeImport v-if="importOpen" @close="importOpen = false" @imported="afterImport" />
  </section>
</template>

<style scoped>
.level-tag[data-category="standard"] { color: #5b3b8a; background: #f0e7fa; }
.level-tag[data-category="vulnerability"] { color: #8a5700; background: #fff1d7; }
.level-tag[data-category="case"] { color: #0a7066; background: #e0f5f0; }
.inactive-tag { margin-left: 6px; }
.knowledge-actions { display: flex; gap: 9px; }
</style>

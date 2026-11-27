<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Search } from '@lucide/vue'
import AsyncState from '../components/AsyncState.vue'
import InstitutionRetestDetail from '../components/InstitutionRetestDetail.vue'
import { risks } from '../lib/domain'
import { hazardBadge, hazardDue, hazardStatuses, type HazardSummary } from '../lib/hazards'
import { queryValue, usePagedList } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

const route = useRoute()
const router = useRouter()
const path = '/institution/hazards'
const id = computed(() => queryValue(route.params.id))
const institution = computed(() => queryValue(route.query.institution).trim())
const institutionDraft = ref('')
const due = ref('')
const scopeAndSource = computed(() => ({
  institution: institution.value,
  planId: queryValue(route.query.planId),
  filingId: queryValue(route.query.filingId),
}))
const list = usePagedList<HazardSummary>(() => !id.value && institution.value ? path : '', 'status',
  () => 'severity', () => ({ ...scopeAndSource.value, due: queryValue(route.query.due) }))
const { keyword, filter, extraFilter, page, loading, failure, load, refresh, nextPage, previousPage } = list

/**
 * 用途 按当前录入的测评机构加载复测台账。
 * 参数 无，机构名称取自范围表单。
 * 返回值 打开所选机构页面或重读列表的任务。
 */
function chooseInstitution() {
  const name = institutionDraft.value.trim()
  if (!name) return
  if (name === institution.value) return load()
  return router.push({ path: !institution.value ? route.path : path, query: {
    ...(!institution.value ? route.query : {}), institution: name, cursor: undefined,
  } })
}

/**
 * 用途 应用复测台账的筛选条件，保留所属机构和明确指定的业务来源。
 * 参数 无，关键词、风险、状态和期限均来自筛选表单。
 * 返回值 导航或重新查询列表的任务。
 */
function search() {
  const scope = scopeAndSource.value
  const target = { path, query: {
    institution: scope.institution, planId: scope.planId || undefined, filingId: scope.filingId || undefined,
    q: keyword.value.trim() || undefined, status: filter.value || undefined,
    severity: extraFilter.value || undefined, due: due.value || undefined,
  } }
  return router.resolve(target).fullPath === route.fullPath ? load() : router.push(target)
}

/**
 * 用途 清除复测台账筛选与来源限制。
 * 参数 无，保留当前所选测评机构。
 * 返回值 复测台账第一页的刷新或导航任务。
 */
function reset() {
  keyword.value = filter.value = extraFilter.value = due.value = ''
  const target = { path, query: { institution: institution.value } }
  return router.resolve(target).fullPath === route.fullPath ? load() : router.push(target)
}

watch(institution, name => { institutionDraft.value = name }, { immediate: true })
watch(() => route.query.due, value => { due.value = queryValue(value) }, { immediate: true })
refreshBusinessOnFocus(() => { if (!id.value && institution.value) void refresh() }, 10_000)
</script>

<template>
  <section class="v2-list" aria-label="隐患复测">
    <header v-if="!id" class="v2-page-head"><h1>隐患复测</h1></header>
    <form v-if="!id || !institution" class="v2-list-toolbar" @submit.prevent="chooseInstitution">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword"><span>测评机构</span>
          <input v-model="institutionDraft" maxlength="200" required placeholder="输入当前测评机构名称">
        </label>
        <button class="button primary" type="submit">查看本机构记录</button>
      </div>
    </form>
    <template v-if="id && institution">
      <RouterLink class="back-link" :to="{ path, query: route.query }">‹ 返回复测台账</RouterLink>
      <InstitutionRetestDetail :key="institution + ':' + id" :id="id" :institution="institution" />
    </template>
    <template v-else-if="!id && institution">
      <form class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <label class="field"><span>办理状态</span><select v-model="filter">
            <option value="">全部状态</option>
            <template v-for="(label, value) in hazardStatuses" :key="value">
              <option v-if="value !== 'pending_dispatch'" :value="value">{{ label }}</option>
            </template>
          </select></label>
          <label class="field"><span>风险等级</span><select v-model="extraFilter">
            <option value="">全部等级</option>
            <option v-for="(label, value) in risks" :key="value" :value="value">{{ label }}</option>
          </select></label>
          <label class="field"><span>整改期限</span><select v-model="due">
            <option value="">全部期限</option>
            <option v-for="(label, value) in hazardDue" :key="value" :value="value">{{ label }}</option>
          </select></label>
          <label class="field filter-keyword"><span>关键词</span>
            <input v-model="keyword" type="search" maxlength="120" placeholder="备案编号、系统或整改事项">
          </label>
          <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
          <button class="button neutral" type="button" @click="reset">重置</button>
        </div>
      </form>
      <div class="panel v2-list-table">
        <AsyncState :loading="loading && !page.items.length" :error="failure" :empty="!page.items.length"
          empty-text="未找到符合条件的隐患复测记录。" @retry="load">
          <div class="table-wrap">
            <table class="data-table">
              <thead><tr><th scope="col">系统 / 所属单位</th><th scope="col">整改事项</th>
                <th scope="col">风险等级</th><th scope="col">整改期限</th><th scope="col">办理状态</th>
                <th scope="col">操作</th></tr></thead>
              <tbody>
                <tr v-for="item in page.items" :key="item.id">
                  <td><strong>{{ item.systemName }}</strong><br><small class="muted">{{ item.organizationName }}</small></td>
                  <td class="retest-description">
                    {{ item.title || item.description }}
                    <small v-if="item.ledger === 'major_risk'" class="level-tag">重大风险</small>
                  </td>
                  <td><span :class="'risk-' + item.severity">{{ risks[item.severity] }}</span></td>
                  <td>{{ item.deadline }}</td>
                  <td><span class="status-badge" :class="hazardBadge(item.status)"><span aria-hidden="true"></span>
                    {{ hazardStatuses[item.status] }}</span></td>
                  <td><RouterLink class="text-action" :aria-label="'查看复测 ' + (item.title || item.description)"
                    :to="{ path: path + '/' + encodeURIComponent(item.id), query: route.query }">
                    {{ item.status === 'pending_retest' ? '办理复测' : '查看进度' }}
                  </RouterLink></td>
                </tr>
              </tbody>
            </table>
          </div>
        </AsyncState>
        <footer v-if="!failure" class="panel-footer pager">
          <span>本页 {{ page.items.length }} 条</span>
          <div>
            <button class="button neutral small" type="button" :disabled="loading" @click="load">刷新</button>
            <button class="button neutral small" type="button" :disabled="loading || !route.query.cursor"
              @click="previousPage">上一页</button>
            <button class="button neutral small" type="button" :disabled="loading || !page.nextCursor"
              @click="nextPage">下一页</button>
          </div>
        </footer>
      </div>
    </template>
  </section>
</template>

<style scoped>
.retest-description { min-width: 12rem; max-width: 28rem; overflow-wrap: anywhere; }
</style>

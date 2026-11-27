<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Download, Search } from '@lucide/vue'
import OperatorWorkflowTabs from '../components/OperatorWorkflowTabs.vue'
import AsyncState from '../components/AsyncState.vue'
import HazardRegistrationDialog from '../components/HazardRegistrationDialog.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { type Page } from '../lib/domain'
import {
  hazardBadge, hazardDay, hazardDue, hazardLedgers, hazardPath, hazardStatuses,
  type HazardLedger, type HazardMutation, type HazardSummary,
} from '../lib/hazards'
import { queryValue } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { refreshWorkbench } from '../lib/workbench'

const props = defineProps<{ audience?: 'operator' }>()

const route = useRoute()
const router = useRouter()
const operator = computed(() => props.audience === 'operator')
const organization = computed(() => operator.value ? queryValue(route.query.organization).trim() : '')
const sourcePlan = computed(() => queryValue(route.query.planId))
const sourceFiling = computed(() => queryValue(route.query.filingId))
const ledger = computed<HazardLedger>(() =>
  !operator.value && route.query.ledger === 'major_risk' ? 'major_risk' : 'vulnerability')
const major = computed(() => ledger.value === 'major_risk')
const pathname = computed(() => operator.value ? '/operator/hazards' : '/hazards')
const scopeDraft = ref('')
const scopeQuery = computed(() => operator.value ? { organization: organization.value } : {})
const ledgerQuery = computed(() => ({ ledger: !operator.value && major.value ? ledger.value : undefined }))
const selection = computed(() => ({
  q: queryValue(route.query.q), status: queryValue(route.query.status),
  severity: queryValue(route.query.severity), due: queryValue(route.query.due),
}))
const tabFilters = computed(() => ({
  q: selection.value.q || undefined, status: selection.value.status || undefined,
  severity: selection.value.severity || undefined, due: selection.value.due || undefined,
  filingId: sourceFiling.value || undefined,
}))
const filtersInRoute = computed(() => ({
  ...selection.value, ...scopeQuery.value, planId: sourcePlan.value, filingId: sourceFiling.value,
  ledger: operator.value ? undefined : ledger.value,
}))
const exportURL = computed(() => '/api/v1/hazards/export' + params(filtersInRoute.value))
const fields = reactive({ q: '', status: '', severity: '', due: '' })
const page = ref<Page<HazardSummary>>({ items: [] })
const loading = ref(false)
const failure = ref('')
const prior = ref<string[]>([])
let request: AbortController | undefined
let lastCollection = ''
const severityNames = { high: '高', medium: '中', low: '低' }

/**
 * 用途 依据当前 URL 查询隐患并恢复已选筛选项。
 * 参数 quiet 表示后台刷新，保留筛选表单。
 * 返回值 当前查询完成任务。
 */
async function load(quiet = false) {
  request?.abort()
  if (operator.value && !organization.value) {
    request = undefined
    page.value = { items: [] }
    loading.value = false
    return
  }
  const next = new AbortController()
  request = next
  const { cursor } = route.query
  if (!quiet) Object.assign(fields, selection.value)
  const filters = filtersInRoute.value
  const collection = pathname.value + params(filters)
  if (collection !== lastCollection) page.value = { items: [] }
  if (collection !== lastCollection || !cursor) prior.value = []
  lastCollection = collection
  loading.value = !quiet
  failure.value = ''
  try {
    const result = await api<Page<HazardSummary>>(pathname.value + params({
      ...filters, cursor: queryValue(cursor), limit: 20,
    }), { signal: next.signal })
    if (request === next) page.value = result
  } catch (error) {
    if (request === next && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === next) {
      request = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 按所选运营单位打开其整改单列表并清除上一单位的筛选。
 * 参数 无，运营单位名称来自范围表单。
 * 返回值 导航或列表查询任务。
 */
function chooseOrganization() {
  const name = scopeDraft.value.trim()
  if (!name) return
  if (name === organization.value && !route.query.cursor) return load()
  return router.push({ path: pathname.value, query: { organization: name } })
}

/**
 * 用途 应用表单筛选并回到第一页。
 * 参数 无，表单提供关键词和业务条件。
 * 返回值 导航或重复查询任务。
 */
function search() {
  const query = {
    q: fields.q.trim() || undefined, status: fields.status || undefined,
    severity: fields.severity || undefined, due: fields.due || undefined,
    ...scopeQuery.value, ...ledgerQuery.value,
    planId: sourcePlan.value || undefined, filingId: sourceFiling.value || undefined,
  }
  const target = { path: pathname.value, query }
  return router.resolve(target).fullPath === route.fullPath ? load() : router.push(target)
}

/**
 * 用途 清空台账筛选及来源限制。
 * 参数 无。
 * 返回值 导航或读取全部台账的任务。
 */
function reset() {
  const target = { path: pathname.value, query: { ...scopeQuery.value, ...ledgerQuery.value } }
  return route.fullPath === router.resolve(target).fullPath ? load() : router.push(target)
}

/**
 * 用途 使用服务端游标在台账列表中翻页。
 * 参数 forward 表示前进一页。
 * 返回值 导航任务，禁止继续翻页时为空。
 */
function turnPage(forward: boolean) {
  if (loading.value || (forward && !page.value.nextCursor)) return
  if (forward) {
    prior.value.push(queryValue(route.query.cursor))
    return router.push({ query: { ...route.query, cursor: page.value.nextCursor } })
  }
  return router.push({ query: { ...route.query, cursor: prior.value.pop() || undefined } })
}

/**
 * 用途 打开新登记的台账详情供监管确认并派发。
 * 参数 result 为服务端已保存的登记结果。
 * 返回值 新登记详情的导航任务。
 */
function openRegistration(result: HazardMutation) {
  void refreshWorkbench()
  return router.push({ path: hazardPath(result.id), query: { ledger: ledger.value } })
}

watch(organization, name => { scopeDraft.value = name }, { immediate: true })
watch([pathname, () => route.query], () => { void load() }, { immediate: true })
refreshBusinessOnFocus(() => { if (!request && (!operator.value || organization.value)) void load(true) }, 10_000)
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="v2-list" :aria-label="operator ? '我的整改单' : hazardLedgers[ledger]">
    <header v-if="operator" class="v2-page-head"><h1>我的整改单</h1></header>
    <OperatorWorkflowTabs v-if="operator" group="rectification" />
    <nav v-if="!operator || sourcePlan" class="v2-subviews" aria-label="隐患台账视图">
      <template v-if="!operator">
        <RouterLink v-for="(label, value) in hazardLedgers" :key="value" class="button neutral"
          :class="{ active: ledger === value }" :aria-current="ledger === value ? 'page' : undefined"
          :to="{ path: '/hazards', query: {
            ...tabFilters, ledger: value === 'major_risk' ? value : undefined,
            planId: value === 'vulnerability' ? sourcePlan || undefined : undefined,
          } }">
          {{ label }}
        </RouterLink>
      </template>
      <RouterLink v-if="sourcePlan" class="button neutral"
        :to="{ path: (operator ? '/operator' : '') + '/remediations/' + encodeURIComponent(sourcePlan),
          query: scopeQuery }">返回来源规划</RouterLink>
    </nav>
    <form v-if="operator" class="v2-list-toolbar" @submit.prevent="chooseOrganization">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword"><span>运营单位名称</span>
          <input v-model="scopeDraft" maxlength="200" required placeholder="输入登记的运营单位名称">
        </label>
        <button class="button primary" type="submit">查看本单位整改单</button>
      </div>
    </form>
    <template v-if="!operator || organization">
      <form class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <HazardRegistrationDialog v-if="!operator" :key="ledger" :ledger="ledger" @saved="openRegistration" />
          <a v-if="!operator" class="button neutral" :href="exportURL" download>
            <Download :size="16" aria-hidden="true" />导出台账
          </a>
          <span class="v2-toolbar-spacer"></span>
          <label class="field"><span>风险等级</span>
            <select v-model="fields.severity" @change="search">
              <option value="">全部等级</option>
              <option v-for="(name, key) in severityNames" :key="key" :value="key">{{ name }}</option>
            </select>
          </label>
          <label class="field"><span>整改状态</span>
            <select v-model="fields.status" @change="search">
              <option value="">全部状态</option>
              <template v-for="(name, key) in hazardStatuses" :key="key">
                <option v-if="!operator || key !== 'pending_dispatch'" :value="key">{{ name }}</option>
              </template>
            </select>
          </label>
          <label class="field"><span>整改期限</span>
            <select v-model="fields.due" @change="search">
              <option value="">全部期限</option>
              <option v-for="(name, key) in hazardDue" :key="key" :value="key">{{ name }}</option>
            </select>
          </label>
          <label class="field filter-keyword"><span>关键词</span>
            <input v-model="fields.q" type="search" maxlength="120"
              :placeholder="operator ? '整改单号 / 系统 / 风险项' : '备案编号 / 系统 / 名称'">
          </label>
          <button class="button primary" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
          <button class="button neutral" type="button" @click="reset">重置</button>
        </div>
      </form>
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        :empty-text="operator ? '未找到符合条件的整改单。' : major ? '未找到符合条件的重大风险。' : '未找到符合条件的漏洞。'"
        @retry="load()">
        <div class="table-wrap v2-list-table">
          <table class="data-table">
            <thead><tr><th v-if="operator">整改单号</th><th>系统</th>
              <th>{{ operator ? '整改事项' : major ? '风险名称' : '漏洞' }}</th><th>等级</th>
              <th v-if="operator || major">{{ major ? '处置人或部门' : '责任方' }}</th>
              <th v-else>发现日期</th><th>整改期限</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td v-if="operator" class="code"><abbr :title="item.id">{{ item.id.slice(0, 8) }}</abbr></td>
                <td>{{ item.systemName }}</td>
                <td class="hazard-description">
                  <strong v-if="item.title">{{ item.title }}</strong>
                  <span v-else>{{ item.description }}</span>
                  <small v-if="operator && item.ledger === 'major_risk'" class="level-tag">重大风险</small>
                </td>
                <td><span class="status-badge" :class="'v2-risk-' + item.severity">
                  <span aria-hidden="true"></span>{{ severityNames[item.severity] }}
                </span></td>
                <td v-if="operator || major">{{ item.responsibility }}</td>
                <td v-else-if="item.discoveredOn">{{ item.discoveredOn }}</td>
                <td v-else-if="item.createdAt">
                  {{ hazardDay(new Date(item.createdAt)) }} <small class="muted">登记</small>
                </td>
                <td v-else>—</td>
                <td>{{ item.deadline }}</td>
                <td><span class="status-badge" :class="hazardBadge(item.status)"><span aria-hidden="true"></span>{{ hazardStatuses[item.status] }}</span></td>
                <td><RouterLink class="text-action"
                  :to="{ path: (operator ? '/operator' : '') + hazardPath(item.id),
                    query: operator ? route.query : { ...route.query, ledger } }"
                  :aria-label="'查看 ' + (item.title || item.description)">{{ operator ? '查看进度' : '详情' }}</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="!loading && !failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button class="button neutral small" type="button" :disabled="!route.query.cursor" @click="turnPage(false)">上一页</button>
          <button class="button neutral small" type="button" :disabled="!page.nextCursor" @click="turnPage(true)">下一页</button>
        </div>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.hazard-description { min-width: 13rem; max-width: 28rem; overflow-wrap: anywhere; }
.hazard-description .level-tag { margin-left: 6px; }
.v2-risk-high { color: var(--c-danger); background: var(--c-danger-bg); }
.v2-risk-medium { color: var(--c-warn); background: var(--c-warn-bg); }
.v2-risk-low { color: var(--c-info); background: var(--c-info-bg); }
</style>

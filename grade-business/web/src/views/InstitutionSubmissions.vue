<script setup lang="ts">
import { hasPermission } from '../lib/session'
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import InstitutionFilingCommands from '../components/InstitutionFilingCommands.vue'
import MaterialIssueList from '../components/MaterialIssueList.vue'
import { params } from '../lib/api'
import { filingReviewLabel, risks, statuses, type CaseSummary } from '../lib/domain'
import { filingApplicationStatuses, applicationTypes, type FilingApplicationSummary } from '../lib/filingApplications'
import { implementationStatuses, implementationTypes, type ImplementationSummary } from '../lib/implementations'
import { queryValue, usePagedList } from '../lib/paging'
import { businessBadge, businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'

const collections = {
  filings: { label: '备案材料', create: '新报备案', states: statuses },
  'filing-applications': { label: '备案变更与注销', create: '发起变更或注销', states: filingApplicationStatuses },
  implementations: { label: '实施报备', create: '新建实施报备', states: implementationStatuses },
  reports: { label: '测评报告', create: '新报测评报告', states: statuses },
}
const props = defineProps<{ resource: keyof typeof collections; audience?: BusinessAudience }>()
const route = useRoute()
const router = useRouter()
const audience = computed(() => props.audience ?? 'institution')
const operator = computed(() => audience.value === 'operator')
const onReports = computed(() => !operator.value && props.resource === 'reports')
const failedReports = computed(() => onReports.value && queryValue(route.query.view) === 'failed')
const scopeKey = computed(() => businessScopes[audience.value].key)
const scope = computed(() => queryValue(route.query[scopeKey.value]).trim())
const canReview = computed(() => hasPermission('org.filing.audit') && !operator.value
  && (props.resource === 'filings' || props.resource === 'filing-applications'))
const reviewQueue = computed(() => canReview.value && queryValue(route.query.review) === 'assigned')
const heading = computed(() => operator.value ? '我的备案办理' : reviewQueue.value ? '机构初审' : '机构报送')
const tabs = computed(() => operator.value
  ? { filings: collections.filings, 'filing-applications': collections['filing-applications'],
    implementations: collections.implementations }
  : collections)
const scopeDraft = ref('')
const scopeQuery = computed(() => ({ [scopeKey.value]: scope.value }))
const path = computed(() => '/' + audience.value + '/' + props.resource)
const collection = computed(() => collections[props.resource])
type FilingCommandRow = CaseSummary & { version: number }
type ListingRow = (CaseSummary & { version?: number }) | FilingApplicationSummary | ImplementationSummary
const list = usePagedList<ListingRow>(
  () => scope.value && !failedReports.value ? path.value : '', 'status', () => onReports.value ? 'risk' : undefined, () => ({
    ...scopeQuery.value,
    ...(reviewQueue.value ? { review: 'assigned' } : {}),
    ...(props.resource === 'filing-applications' ? { filingId: queryValue(route.query.filingId) } : {}),
  }),
)
const { keyword, filter, extraFilter, page, loading, failure, load, refresh, search, reset, nextPage, previousPage } = list
const hasFilingCommands = computed(() => !operator.value && !reviewQueue.value && props.resource === 'filings')
const commandKey = computed(() => [path.value, scope.value, reviewQueue.value,
  queryValue(route.query.cursor), queryValue(route.query.q), queryValue(route.query.status)].join('\n'))
const selected = ref<string[]>([])
const commands = ref<InstanceType<typeof InstitutionFilingCommands>>()
const selectedRows = computed(() => page.value.items.filter(rowWithVersion).filter(row => selected.value.includes(row.id)))
const reportExport = computed(() => '/api/v1/institution/reports/export' + params({
  institution: scope.value, q: queryValue(route.query.q), status: queryValue(route.query.status),
  risk: queryValue(route.query.risk),
}))

/**
 * 用途 切换业务范围时从第一页读取对应的报送。
 * 参数 无，运营单位或测评机构名称来自范围选择框。
 * 返回值 查询任务或更新路由的任务。
 */
function chooseScope() {
  const name = scopeDraft.value.trim()
  if (!name) return
  if (name === scope.value && !route.query.cursor && !failedReports.value) return load()
  return router.push({ path: path.value, query: {
    [scopeKey.value]: name, review: reviewQueue.value ? 'assigned' : undefined,
    view: failedReports.value ? 'failed' : undefined,
  } })
}

/**
 * 用途 将已保存在各业务表中的编号统一显示在第一列。
 * 参数 item 为机构业务列表的摘要。
 * 返回值 报告编号或原备案编号。
 */
function reference(item: CaseSummary | FilingApplicationSummary | ImplementationSummary) {
  return 'number' in item ? item.number : item.filingNumber
}

/**
 * 用途 显示变更注销或实施报备的子类型。
 * 参数 item 为机构业务列表的摘要。
 * 返回值 已定义的业务子类型，类型不适用时为空。
 */
function typeLabel(item: CaseSummary | FilingApplicationSummary | ImplementationSummary) {
  if ('plannedStart' in item) return implementationTypes[item.type]
  return 'type' in item ? applicationTypes[item.type] : ''
}

/**
 * 用途 确认列表项包含服务端授权返回的备案版本。
 * 参数 row 为三端通用清单中的一条业务摘要。
 * 返回值 含机构操作版本的真实备案为真。
 */
function rowWithVersion(row: ListingRow): row is FilingCommandRow {
  return 'number' in row && 'version' in row && typeof row.version === 'number'
}

/**
 * 用途 批次完成后重读当前页，清理已提交的选择。
 * 参数 无。
 * 返回值 无。
 */
function reloadCommands() {
  selected.value = []
  void load()
}

watch(commandKey, () => { selected.value = [] })
watch(scope, value => { scopeDraft.value = value }, { immediate: true })
refreshBusinessOnFocus(() => { if (scope.value && !failedReports.value) void refresh() }, 10_000)
</script>

<template>
  <section class="v2-list" :aria-label="heading">
    <header class="v2-page-head">
      <div><h1>{{ heading }}</h1></div>
      <RouterLink v-if="canReview" class="button neutral"
        :to="{ path, query: { ...scopeQuery, review: reviewQueue ? undefined : 'assigned' } }">
        {{ reviewQueue ? '全部报送' : '待本机构初审' }}
      </RouterLink>
    </header>
    <nav class="v2-tabs" aria-label="报送类型">
      <RouterLink v-for="(entry, key) in tabs" :key="key" class="v2-tab"
        :class="{ active: resource === key }" :aria-current="resource === key ? 'page' : undefined"
        :to="{ path: '/' + audience + '/' + key, query: { ...scopeQuery,
          review: reviewQueue && (key === 'filings' || key === 'filing-applications') ? 'assigned' : undefined } }" >{{ entry.label }}</RouterLink>
    </nav>
    <nav v-if="onReports" class="v2-tabs" aria-label="测评报告状态">
      <RouterLink class="v2-tab" :class="{ active: !failedReports }" :aria-current="!failedReports ? 'page' : undefined"
        :to="{ path, query: scopeQuery }">报告列表</RouterLink>
      <RouterLink class="v2-tab" :class="{ active: failedReports }" :aria-current="failedReports ? 'page' : undefined"
        :to="{ path, query: { ...scopeQuery, view: 'failed' } }">未成功</RouterLink>
    </nav>
    <form class="v2-list-toolbar" @submit.prevent="chooseScope">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword"><span>{{ businessScopes[audience].label }}</span>
          <input v-model="scopeDraft" readonly maxlength="200" required :placeholder="'输入登记的' + businessScopes[audience].label">
        </label>
        <button class="button primary" type="submit">查看办理记录</button>
      </div>
    </form>
    <MaterialIssueList v-if="scope && failedReports" audience="institution" :scope="scope" case-kind="report" failed-list />
    <template v-else-if="scope">
      <form class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <label v-if="!reviewQueue" class="field"><span>审核状态</span><select v-model="filter">
            <option value="">全部状态</option>
            <template v-for="(label, key) in collection.states" :key="key">
              <option v-if="resource !== 'reports' || key !== 'withdrawn'" :value="key">{{ label }}</option>
            </template>
          </select></label>
          <label v-if="onReports" class="field"><span>风险等级</span><select v-model="extraFilter">
            <option value="">全部风险</option>
            <option v-for="(label, key) in risks" :key="key" :value="key">{{ label }}</option>
          </select></label>
          <label class="field filter-keyword"><span>关键词</span><input v-model="keyword" type="search" maxlength="120"></label>
          <button class="button primary" type="submit">查询</button>
          <button class="button neutral" type="button" @click="reset">重置</button>
          <span class="v2-toolbar-spacer"></span>
          <a v-if="onReports" class="button neutral" :href="reportExport" download>导出测评台账</a>
          <RouterLink v-if="!reviewQueue" class="button primary" :to="{ path: path + '/new', query: scopeQuery }">
            {{ operator && resource === 'implementations' ? '发起换机构复测报备' : collection.create }}
          </RouterLink>
        </div>
      </form>
      <InstitutionFilingCommands v-if="hasFilingCommands" ref="commands" :key="commandKey"
        :scope="scope" :selected="selectedRows" @changed="reloadCommands" />
      <AsyncState :loading="loading && !page.items.length" :error="failure" :empty="!page.items.length"
        empty-text="暂无符合条件的报送记录。" @retry="load">
        <div class="table-wrap v2-list-table">
          <table class="data-table">
            <thead><tr><th v-if="hasFilingCommands" scope="col">选择</th>
              <th>编号</th><th>系统 / 单位</th><th>业务</th><th>当前状态</th><th>操作</th></tr></thead>
            <tbody><tr v-for="item in page.items" :key="item.id">
              <td v-if="hasFilingCommands">
                <input v-if="rowWithVersion(item) && (item.status === 'draft' || item.status === 'returned')"
                  type="checkbox" v-model="selected" :value="item.id" :aria-label="'选择 ' + item.number">
                <span v-else class="muted">—</span>
              </td>
              <td class="code">{{ reference(item) }}</td>
              <td><strong>{{ item.systemName }}</strong><br><small class="muted">{{ item.organizationName }}</small></td>
              <td>{{ typeLabel(item) || collection.label }}</td>
              <td><span v-if="'cancelledAt' in item && item.cancelledAt" class="level-tag">已注销</span>
              <span v-else class="status-badge" :class="businessBadge(item.status)">
                <span aria-hidden="true"></span>{{
                  filingReviewLabel(item.status, 'initialReviewAudience' in item ? item.initialReviewAudience : undefined)
                    ?? (collection.states as Record<string, string>)[item.status] ?? item.status
                }}
              </span></td>
              <td class="institution-row-actions"><RouterLink class="text-action" :aria-label="'查看 ' + reference(item)"
                :to="{ path: path + '/' + encodeURIComponent(item.id), query: { ...scopeQuery, review: reviewQueue ? 'assigned' : undefined } }">
                {{ reviewQueue ? '办理初审' : item.status === 'draft' ? '继续办理' : item.status === 'returned' ? '办理补正' : '查看进度' }}
              </RouterLink>
                <button v-if="hasFilingCommands && rowWithVersion(item)" type="button" class="text-action"
                  :aria-label="'复制 ' + item.number" @click="commands?.openCopy(item)">复制</button>
              </td>
            </tr></tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="!failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button class="button neutral small" type="button" :disabled="loading || !route.query.cursor"
            @click="previousPage">上一页</button>
          <button class="button neutral small" type="button" :disabled="loading || !page.nextCursor"
            @click="nextPage">下一页</button>
        </div>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.institution-row-actions { white-space: nowrap; }
.institution-row-actions button { margin-inline-start: 12px; }
.data-table input[type="checkbox"] { width: 16px; min-height: 0; height: 16px; padding: 0; }
</style>

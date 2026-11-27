<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import MaterialExpiryDetail from '../components/MaterialExpiryDetail.vue'
import MaterialExpiryRegister from '../components/MaterialExpiryRegister.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { materials, reportSubmissionMaterials } from '../lib/domain'
import { expiryStatuses, type ExpiryDetail, type ExpirySummary } from '../lib/materialExpiry'
import { queryValue } from '../lib/paging'
import { businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'

interface ExpiryPage { items: ExpirySummary[]; nextBefore?: string }
const props = defineProps<{ audience?: BusinessAudience }>()
const route = useRoute()
const router = useRouter()
const base = computed(() => (props.audience ? '/' + props.audience : '') + '/material-expiries')
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const path = computed(() => id.value ? base.value + '/' + encodeURIComponent(id.value) : base.value)
const settings = computed(() => props.audience ? businessScopes[props.audience] : undefined)
const scopeName = computed(() => settings.value ? queryValue(route.query[settings.value.key]).trim() : '')
const scopeDraft = ref('')
const scopeQuery = computed(() => settings.value ? { [settings.value.key]: scopeName.value } : {})
const reportId = computed(() => queryValue(route.query.reportId))
const listQuery = computed(() => ({ ...scopeQuery.value, reportId: reportId.value || undefined }))
const criteria = computed(() => queryValue(route.query.status))
const before = computed(() => queryValue(route.query.before))
const statusQuery = computed(() => ({ ...listQuery.value, status: criteria.value || undefined }))
const pageQuery = computed(() => ({ ...statusQuery.value, before: before.value || undefined }))
const query = computed(() => params(scopeQuery.value))
const endpoint = computed(() => path.value + params(id.value ? scopeQuery.value : pageQuery.value))
const heading = computed(() => reportId.value ? '测评材料有效期' : '材料到期台账')
const available = computed(() => !settings.value || Boolean(scopeName.value))
const list = shallowRef<ExpiryPage>()
const detail = shallowRef<ExpiryDetail>()
const showCreate = ref(false)
const loading = ref(false)
const failure = ref('')
const notice = ref('')
const sweeping = ref(false)
const mutationError = ref('')
const visibleStates = computed(() => Object.entries(expiryStatuses)
  .filter(([state]) => state !== 'suspended' || !props.audience))
let request: AbortController | undefined
let sweepRequest: AbortController | undefined

/**
 * 用途 从当前端和明确归属加载有效期总表或详情。
 * 参数 quiet 表示刷新时是否保留正在展示的内容。
 * 返回值 当前读取任务及表格或错误状态。
 */
async function load(quiet = false) {
  request?.abort()
  if (!available.value) return
  const active = new AbortController()
  request = active
  loading.value = !quiet
  failure.value = ''
  try {
    if (id.value) {
      const result = await api<ExpiryDetail>(endpoint.value, { signal: active.signal })
      if (!active.signal.aborted) detail.value = result
    } else {
      const result = await api<ExpiryPage>(endpoint.value, { signal: active.signal })
      if (!active.signal.aborted) list.value = result
    }
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === active) { request = undefined; loading.value = false }
  }
}

/**
 * 用途 切换当前端的收件范围并清除其他范围的记录位置。
 * 参数 无，读取所选的单位或测评机构名称。
 * 返回值 路由切换任务。
 */
function chooseScope() {
  return settings.value ? router.push({ path: base.value,
    query: { ...listQuery.value, [settings.value.key]: scopeDraft.value.trim() } }) : undefined
}

/**
 * 用途 重新列出指定有效期状态并从第一页返回。
 * 参数 event 为日期状态选择控件的变更。
 * 返回值 路由切换任务。
 */
function chooseState(event: Event) {
  const state = (event.target as HTMLSelectElement).value
  return router.push({ path: base.value, query: { ...listQuery.value, status: state || undefined } })
}

/**
 * 用途 登记后打开保存的记录，查看真实有效期。
 * 参数 item 为登记成功返回的业务标识。
 * 返回值 定位当前详情的路由任务。
 */
function viewSaved(item: string) {
  showCreate.value = false
  return router.push({ path: base.value + '/' + encodeURIComponent(item), query: listQuery.value })
}

/**
 * 用途 执行已选有效期记录的站内提醒扫描。
 * 参数 无，监管端只发送空请求。
 * 返回值 扫描结果及刷新业务列表的任务。
 */
async function sweep() {
  const active = new AbortController()
  sweepRequest = active
  sweeping.value = true
  notice.value = mutationError.value = ''
  try {
    const out = await api<{ notices: number }>('/material-expiries/sweep', { method: 'POST', signal: active.signal })
    if (!active.signal.aborted) {
      notice.value = '扫描完成，新增 ' + out.notices + ' 条站内提醒。'
      await load(true)
    }
  } catch (error) {
    if (!isAbort(error)) mutationError.value = errorText(error)
  } finally {
    if (sweepRequest === active) sweeping.value = false
  }
}

/**
 * 用途 查找已校验材料分类的可读名称。
 * 参数 item 为接口返回的一条审批材料。
 * 返回值 材料类别名称。
 */
function kindName(item: ExpirySummary) {
  const choices: Record<string, string> = item.caseKind === 'filing' ? materials.filings : reportSubmissionMaterials
  return choices[item.attachmentKind] || item.attachmentKind
}

watch(endpoint, () => {
  detail.value = list.value = undefined
  scopeDraft.value = scopeName.value
  void load()
}, { immediate: true })
watch(reportId, report => { showCreate.value = Boolean(report && !props.audience && !id.value) },
  { immediate: true })
refreshBusinessOnFocus(() => { if (!request) void load(true) }, 30_000)
onScopeDispose(() => { request?.abort(); sweepRequest?.abort() })
</script>

<template>
  <section class="v2-list expiry-page" :aria-label="heading">
    <header class="v2-page-head">
      <h1>{{ heading }}</h1>
      <RouterLink v-if="id" class="button neutral" :to="{ path: base, query: pageQuery }">返回台账</RouterLink>
      <div v-else-if="!audience" class="expiry-toolbar">
        <RouterLink v-if="reportId" class="button neutral" :to="'/reports/' + encodeURIComponent(reportId)">
          返回测评报告
        </RouterLink>
        <button class="button primary" type="button" :disabled="showCreate"
          @click="showCreate = true">登记有效期</button>
        <button v-if="!reportId" class="button neutral" type="button" :disabled="sweeping" @click="sweep()">
          {{ sweeping ? '扫描中…' : '同步到期提醒' }}
        </button>
      </div>
    </header>
    <form v-if="settings && !id" class="v2-list-toolbar" @submit.prevent="chooseScope">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword"><span>{{ settings.label }}</span>
          <input v-model="scopeDraft" required maxlength="200" :placeholder="'输入登记的' + settings.label">
        </label>
        <button class="button primary" type="submit">查看有效期</button>
      </div>
    </form>
    <p v-if="!available" class="muted">请先选择当前办理范围。</p>
    <MaterialExpiryRegister v-if="showCreate && !audience && !id" :key="reportId"
      :report-id="reportId" @close="showCreate = false" @saved="viewSaved" />
    <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
    <p v-if="mutationError" class="form-message error-message" role="alert">{{ mutationError }}</p>
    <template v-if="available && id">
      <AsyncState :loading="loading" :error="failure" @retry="load()">
        <MaterialExpiryDetail v-if="detail" :detail="detail" :path="path" :query="query" @changed="load(true)" />
      </AsyncState>
    </template>
    <template v-else-if="available">
      <div class="v2-list-toolbar expiry-toolbar">
        <label class="field"><span>日期状态</span>
          <select :value="criteria" @change="chooseState">
            <option value="">全部状态</option>
            <option v-for="[key, name] in visibleStates" :key="key" :value="key">{{ name }}</option>
          </select>
        </label>
        <button class="button neutral" type="button" :disabled="loading" @click="load()">刷新</button>
      </div>
      <AsyncState :loading="loading" :error="failure" :empty="!list?.items.length"
        empty-text="当前范围暂无材料有效期登记。" @retry="load()">
        <div class="table-wrap panel">
          <table class="data-table">
            <thead><tr><th scope="col">系统及编号</th><th scope="col">单位</th><th scope="col">材料</th>
              <th scope="col">有效期和状态</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="item in list?.items" :key="item.id">
                <td><strong>{{ item.systemName }}</strong><br><small>{{ item.number }}</small></td>
                <td>{{ item.organizationName }}</td><td>{{ kindName(item) }}</td>
                <td>{{ item.validUntil }} · {{ expiryStatuses[item.dateStatus] }}
                  <br v-if="item.renewalPending"><small v-if="item.renewalPending" class="muted">续传待监管审核</small>
                </td>
                <td><RouterLink class="text-action"
                  :to="{ path: base + '/' + item.id, query: pageQuery }">查看材料</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="before || list?.nextBefore" class="panel-footer pager">
        <RouterLink v-if="before" class="button neutral"
          :to="{ path: base, query: statusQuery }">最新记录</RouterLink>
        <RouterLink v-if="list?.nextBefore" class="button neutral"
          :to="{ path: base, query: { ...statusQuery, before: list.nextBefore } }">下一页</RouterLink>
      </footer>
    </template>
  </section>
</template>

<style scoped>
.expiry-page { display: grid; gap: 12px; }
.expiry-toolbar { display: flex; flex-wrap: wrap; gap: 10px; align-items: end; }
</style>

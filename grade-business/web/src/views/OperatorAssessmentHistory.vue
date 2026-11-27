<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { RefreshCw } from '@lucide/vue'
import AsyncState from '../components/AsyncState.vue'
import AssessmentAnnualTrend from '../components/AssessmentAnnualTrend.vue'
import AssessmentIssueComparison from '../components/AssessmentIssueComparison.vue'
import OperatorAssessmentMaterials from '../components/OperatorAssessmentMaterials.vue'
import OperatorWorkflowTabs from '../components/OperatorWorkflowTabs.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { assessmentConclusions, operatorAssessmentPath, type AssessmentArchive, type AssessmentComparison,
  type AssessmentPoint, type AssessmentSystem } from '../lib/assessmentHistory'
import { formatTime, risks, type Page } from '../lib/domain'
import { queryValue } from '../lib/paging'

const route = useRoute()
const router = useRouter()
const organization = computed(() => queryValue(route.query.organization).trim())
const query = computed(() => queryValue(route.query.q).trim())
const unitName = ref('')
const keyword = ref('')
const selected = ref('')
const from = ref('')
const to = ref('')
const systems = shallowRef<Page<AssessmentSystem>>({ items: [] })
const archive = shallowRef<AssessmentArchive>()
const comparison = shallowRef<AssessmentComparison>()
const materialReport = shallowRef<AssessmentPoint>()
const systemLoading = ref(false)
const archiveLoading = ref(false)
const comparisonLoading = ref(false)
const systemError = ref('')
const archiveError = ref('')
const comparisonError = ref('')
const requestPath = computed(() => operatorAssessmentPath + '/' + encodeURIComponent(selected.value))
const reportItems = computed(() => archive.value?.items ?? [])
const allowedTo = computed(() => {
  const index = reportItems.value.findIndex(item => item.id === from.value)
  return index >= 0 ? reportItems.value.slice(0, index) : reportItems.value.slice(0, -1)
})
const riskChange = { increased: '上升', decreased: '下降', same: '持平' } as const
let systemRequest: AbortController | undefined
let archiveRequest: AbortController | undefined
let comparisonRequest: AbortController | undefined

/**
 * 用途 从截断点查询当前运营单位名下已有获批测评的系统。
 * 参数 before 为上一页尾部备案标识，未传时刷新首屏。
 * 返回值 系统列表查询任务。
 */
async function loadSystems(before?: string) {
  systemRequest?.abort()
  systemError.value = ''
  if (!before) systems.value = { items: [] }
  if (!organization.value) {
    systemLoading.value = false
    return
  }
  const request = new AbortController()
  systemRequest = request
  systemLoading.value = true
  try {
    const page = await api<Page<AssessmentSystem>>(operatorAssessmentPath + '/systems'
      + params({ organization: organization.value, q: query.value, before }), { signal: request.signal })
    if (request.signal.aborted) return
    systems.value = { items: before ? [...systems.value.items, ...page.items] : page.items, nextCursor: page.nextCursor }
    if (!before && !page.items.some(item => item.id === selected.value)) selected.value = page.items[0]?.id ?? ''
  } catch (error) {
    if (!isAbort(error)) systemError.value = errorText(error)
  } finally {
    if (systemRequest === request) systemLoading.value = false
  }
}

/**
 * 用途 读取所选备案已批准报告的历史摘要。
 * 参数 cursor 为上一页末尾游标，未传时从最近一期加载。
 * 返回值 归档详情查询任务。
 */
async function loadArchive(cursor?: string) {
  archiveRequest?.abort()
  archiveError.value = ''
  if (!cursor) archive.value = undefined
  if (!organization.value || !selected.value) {
    archiveLoading.value = false
    return
  }
  const request = new AbortController()
  archiveRequest = request
  archiveLoading.value = true
  try {
    const page = await api<AssessmentArchive>(requestPath.value
      + params({ organization: organization.value, cursor }), { signal: request.signal })
    if (request.signal.aborted) return
    archive.value = {
      ...page,
      items: cursor ? [...(archive.value?.items ?? []), ...page.items] : page.items,
    }
    if (!from.value && !to.value && reportItems.value.length >= 2) {
      from.value = reportItems.value[1]?.id ?? ''
      to.value = reportItems.value[0]?.id ?? ''
    }
  } catch (error) {
    if (!isAbort(error)) archiveError.value = errorText(error)
  } finally {
    if (archiveRequest === request) archiveLoading.value = false
  }
}

/**
 * 用途 提交运营单位和可选备案编号、系统名称检索条件。
 * 参数 无，从检索表单读取筛选值。
 * 返回值 筛选导航或查询任务。
 */
function search() {
  const unit = unitName.value.trim()
  const q = keyword.value.trim()
  if (unit === organization.value && q === query.value) return refreshSelection()
  return router.push({ path: operatorAssessmentPath, query: { organization: unit, q: q || undefined } })
}

/**
 * 用途 清除上一单位或搜索条件的结果并刷新系统。
 * 参数 无。
 * 返回值 系统查询任务。
 */
function refreshSelection() {
  selected.value = ''
  return loadSystems()
}

/**
 * 用途 读取同备案的前后期测评结论和风险变化。
 * 参数 无，使用已选择的前期和后期。
 * 返回值 业务对比查询任务。
 */
async function compare() {
  if (!selected.value || !from.value || !to.value || comparisonLoading.value) return
  const request = new AbortController()
  comparisonRequest = request
  comparisonLoading.value = true
  comparisonError.value = ''
  try {
    const result = await api<AssessmentComparison>(requestPath.value + '/comparison'
      + params({ organization: organization.value, from: from.value, to: to.value }), { signal: request.signal })
    if (!request.signal.aborted) comparison.value = result
  } catch (error) {
    if (!isAbort(error)) comparisonError.value = errorText(error)
  } finally {
    if (comparisonRequest === request) comparisonLoading.value = false
  }
}

/**
 * 用途 格式化实际登记结果的前后期增减。
 * 参数 value 为变化量，unit 为分或项。
 * 返回值 可读变化文本，缺少原始记录时提示尚未登记。
 */
function changeText(value: number | undefined, unit: string) {
  return value === undefined ? '尚未登记' : (value > 0 ? '+' : '') + value + ' ' + unit
}

watch([organization, query], ([unit, q]) => {
  unitName.value = unit
  keyword.value = q
  void refreshSelection()
}, { immediate: true, flush: 'sync' })
watch(selected, () => {
  materialReport.value = undefined
  from.value = ''
  to.value = ''
  comparison.value = undefined
  void loadArchive()
}, { flush: 'sync' })
watch([from, to], () => {
  comparisonRequest?.abort()
  comparisonLoading.value = false
  comparison.value = undefined
  comparisonError.value = ''
  if (to.value && !allowedTo.value.some(item => item.id === to.value)) to.value = ''
}, { flush: 'sync' })
onScopeDispose(() => {
  systemRequest?.abort()
  archiveRequest?.abort()
  comparisonRequest?.abort()
})
</script>

<template>
  <section class="v2-list" aria-label="历史测评对比">
    <header class="v2-page-head"><h1>历史测评对比</h1></header>
    <OperatorWorkflowTabs group="rectification" />
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field"><span>运营单位全称</span>
          <input v-model="unitName" maxlength="200" placeholder="运营单位全称" required>
        </label>
        <label class="field"><span>系统名称或备案编号</span>
          <input v-model="keyword" maxlength="120" type="search" placeholder="系统名称或备案编号">
        </label>
        <button class="button primary" type="submit">查询</button>
        <button v-if="organization" class="button neutral" type="button" :disabled="systemLoading"
          @click="refreshSelection">
          <RefreshCw :size="16" aria-hidden="true" />刷新
        </button>
      </div>
    </form>
    <p v-if="!organization" class="muted">输入备案登记的运营单位全称，即可查询历史测评结果。</p>
    <template v-else>
      <AsyncState :loading="systemLoading && !systems.items.length" :error="systemError" :empty="!systems.items.length"
        empty-text="该单位暂无已通过测评的系统。" @retry="refreshSelection" />
      <section v-if="systems.items.length" class="panel assessment-panel" aria-label="测评系统">
        <div class="assessment-selection">
          <label class="field"><span>选择系统</span>
            <select v-model="selected">
              <option v-for="system in systems.items" :key="system.id" :value="system.id">
                {{ system.systemName }}（{{ system.number }}）
              </option>
            </select>
          </label>
          <button v-if="systems.nextCursor" class="button neutral" type="button" :disabled="systemLoading"
            @click="loadSystems(systems.nextCursor)">更多系统</button>
        </div>
        <AsyncState :loading="archiveLoading && !archive" :error="archiveError"
          :empty="!reportItems.length" empty-text="暂无已通过的测评报告。" @retry="loadArchive()" />
        <template v-if="archive && reportItems.length">
          <h2>历次测评记录</h2>
          <div class="assessment-table">
            <table class="v2-table">
              <thead><tr>
                <th scope="col">测评报告</th><th scope="col">报告时间</th><th scope="col">测评机构</th>
                <th scope="col">风险等级</th><th scope="col">综合得分</th><th scope="col">测评结论</th>
                <th scope="col">风险项数（高 / 中 / 低）</th><th scope="col">操作</th>
              </tr></thead>
              <tbody><tr v-for="item in reportItems" :key="item.id">
                <td>{{ item.number }}</td><td>{{ formatTime(item.assessedAt) }}</td><td>{{ item.assessmentOrg || '—' }}</td>
                <td>{{ risks[item.risk] }}</td>
                <td>{{ item.result ? item.result.score + ' 分' : '尚未登记' }}</td>
                <td>{{ item.result ? assessmentConclusions[item.result.conclusion] : '尚未登记' }}</td>
                <td>{{ item.result ? item.result.high + ' / ' + item.result.medium + ' / ' + item.result.low : '尚未登记' }}</td>
                <td><button type="button" class="text-action" @click="materialReport = item">报告与整改要求</button></td>
              </tr></tbody>
            </table>
          </div>
          <button v-if="archive.nextCursor" class="button neutral" :disabled="archiveLoading" type="button"
            @click="loadArchive(archive.nextCursor)">更多历史报告</button>
        </template>
      </section>
      <AssessmentAnnualTrend v-if="archive && selected && reportItems.length"
        :filing-id="selected" :organization="organization" />
      <section v-if="reportItems.length >= 2" class="panel assessment-panel" aria-label="前后期对比">
        <h2>前后期对比</h2>
        <form class="assessment-selection" @submit.prevent="compare">
          <label class="field"><span>前期报告</span>
            <select v-model="from" required>
              <option disabled value="">请选择前期报告</option>
              <option v-for="item in reportItems.slice(1)" :key="item.id" :value="item.id">
                {{ item.number }} · {{ formatTime(item.assessedAt) }}
              </option>
            </select>
          </label>
          <label class="field"><span>后期报告</span>
            <select v-model="to" required>
              <option disabled value="">请选择后期报告</option>
              <option v-for="item in allowedTo" :key="item.id" :value="item.id">
                {{ item.number }} · {{ formatTime(item.assessedAt) }}
              </option>
            </select>
          </label>
          <button class="button primary" type="submit" :disabled="archiveLoading || comparisonLoading">开始对比</button>
        </form>
        <AsyncState :loading="comparisonLoading" :error="comparisonError" @retry="compare" />
        <template v-if="comparison && !comparisonLoading">
          <p>{{ comparison.from.number }} 至 {{ comparison.to.number }}</p>
          <div class="assessment-table">
            <table class="v2-table">
              <thead><tr><th scope="col">指标</th><th scope="col">前期</th><th scope="col">后期</th><th scope="col">变化</th></tr></thead>
              <tbody>
                <tr><th scope="row">风险等级</th><td>{{ risks[comparison.from.risk] }}</td>
                  <td>{{ risks[comparison.to.risk] }}</td><td>{{ riskChange[comparison.riskChange] }}</td></tr>
                <tr><th scope="row">综合得分</th><td>{{ comparison.from.result?.score ?? '尚未登记' }}</td>
                  <td>{{ comparison.to.result?.score ?? '尚未登记' }}</td><td>{{ changeText(comparison.scoreDelta, '分') }}</td></tr>
                <tr><th scope="row">测评结论</th>
                  <td>{{ comparison.from.result ? assessmentConclusions[comparison.from.result.conclusion] : '尚未登记' }}</td>
                  <td>{{ comparison.to.result ? assessmentConclusions[comparison.to.result.conclusion] : '尚未登记' }}</td>
                  <td>—</td></tr>
                <tr><th scope="row">高风险项</th><td>{{ comparison.from.result?.high ?? '尚未登记' }}</td>
                  <td>{{ comparison.to.result?.high ?? '尚未登记' }}</td><td>{{ changeText(comparison.highDelta, '项') }}</td></tr>
                <tr><th scope="row">中风险项</th><td>{{ comparison.from.result?.medium ?? '尚未登记' }}</td>
                  <td>{{ comparison.to.result?.medium ?? '尚未登记' }}</td><td>{{ changeText(comparison.mediumDelta, '项') }}</td></tr>
                <tr><th scope="row">低风险项</th><td>{{ comparison.from.result?.low ?? '尚未登记' }}</td>
                  <td>{{ comparison.to.result?.low ?? '尚未登记' }}</td><td>{{ changeText(comparison.lowDelta, '项') }}</td></tr>
              </tbody>
            </table>
          </div>
          <AssessmentIssueComparison :filing-id="selected" :organization="organization"
            :from="comparison.from.id" :to="comparison.to.id" />
        </template>
      </section>
    </template>
    <OperatorAssessmentMaterials v-if="materialReport" :key="materialReport.id" :report="materialReport"
      :filing-id="selected" :organization="organization" @close="materialReport = undefined" />
  </section>
</template>

<style scoped>
.assessment-panel { display: grid; min-width: 0; gap: 14px; padding: 18px; }
.assessment-panel h2, .assessment-panel p { margin: 0; }
.assessment-selection { display: flex; flex-wrap: wrap; align-items: end; gap: 12px; }
.assessment-selection .field { flex: 1 1 220px; min-width: 0; }
.assessment-selection select { width: 100%; }
.assessment-panel .button { justify-self: start; }
.assessment-table { overflow-x: auto; }
.assessment-table table { width: 100%; min-width: 520px; text-align: left; }
</style>

<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, watch } from 'vue'
import { ArrowLeft, Plus, Search, Trash2 } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import RemediationMaterialTemplates from '../components/RemediationMaterialTemplates.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { risks } from '../lib/domain'
import { queryValue } from '../lib/paging'
import {
  eligibleName, operatorRemediationPath, remediationPath, remediationRecordPath, type EligibleReport,
  type RemediationDetail, type RemediationMutation, type RemediationRisk,
} from '../lib/remediations'

const props = defineProps<{ audience?: 'operator' }>()
const route = useRoute()
const router = useRouter()
const isOperator = computed(() => props.audience === 'operator')
const organization = computed(() => isOperator.value ? queryValue(route.query.organization).trim() : '')
const basePath = computed(() => isOperator.value ? operatorRemediationPath : remediationPath)
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const recordPath = computed(() => id.value ? remediationRecordPath(id.value, basePath.value) : basePath.value)
const scope = computed(() => ({ organization: organization.value || undefined }))
const recordEndpoint = computed(() => recordPath.value + params(scope.value))
const back = computed(() => ({ path: recordPath.value, query: scope.value }))
const savedReport = ref('')
const fields = reactive({ reportId: '', deadline: '' })
const templatePath = computed(() => id.value && fields.reportId === savedReport.value
  ? recordPath.value : basePath.value + '/eligible-reports/' + encodeURIComponent(fields.reportId))
const entries = ref<(RemediationRisk & { key: number })[]>([])
const version = ref<number>()
const reportLock = ref('')
const fixedReport = computed(() => Boolean(reportLock.value))
const selected = ref<EligibleReport>()
const available = ref<EligibleReport[]>([])
const keyword = ref('')
const loading = ref(false)
const searching = ref(false)
const saving = ref(false)
const stale = ref(false)
const failure = ref('')
const reportError = ref('')
const formError = ref('')
const maxRisks = 50
let nextKey = 0
let detailRequest: AbortController | undefined
let reportsRequest: AbortController | undefined
const choices = computed(() => selected.value && !available.value.some(item => item.id === selected.value?.id)
  ? [selected.value, ...available.value] : available.value)

/**
 * 用途 为整改规划添加一个可编辑、稳定定位的风险条目。
 * 参数 risk 为已有风险信息，缺省时创建新条目。
 * 返回值 带页面唯一键的条目。
 */
function riskEntry(risk?: RemediationRisk) {
  return {
    ...(risk ?? { risk: '', description: '', measure: '', responsibility: '', deadline: '' }),
    key: ++nextKey,
  } as RemediationRisk & { key: number }
}

/**
 * 用途 载入当前编辑对象，新建时初始化风险和可选报告。
 * 参数 无，标识及可选运营单位来自当前路由。
 * 返回值 加载任务，非法编辑状态时显示易理解的提示。
 */
async function load() {
  detailRequest?.abort()
  reportsRequest?.abort()
  reportsRequest = undefined
  const request = new AbortController()
  detailRequest = request
  loading.value = Boolean(id.value)
  failure.value = ''
  formError.value = ''
  stale.value = false
  reportLock.value = ''
  savedReport.value = ''
  selected.value = undefined
  available.value = []
  searching.value = false
  reportError.value = ''
  version.value = undefined
  fields.reportId = ''
  fields.deadline = ''
  keyword.value = ''
  entries.value = [riskEntry()]
  if (isOperator.value && !organization.value) {
    loading.value = false
    failure.value = '请返回我的整改规划并先选择运营单位。'
    return
  }
  if (!id.value) { void searchReports(); return }
  try {
    const item = await api<RemediationDetail>(recordEndpoint.value, { signal: request.signal })
    if (detailRequest === request) populate(item)
  } catch (error) {
    if (detailRequest === request && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (detailRequest === request) loading.value = false
  }
}

/**
 * 用途 回填已有草稿或补正规划中已经保存的数据。
 * 参数 item 为真实接口返回的计划详情。
 * 返回值 无。
 */
function populate(item: RemediationDetail) {
  if (!item.allowedActions.includes('edit')) {
    failure.value = '当前审核状态不允许编辑整改规划。'
    return
  }
  version.value = item.version
  if (!item.allowedActions.includes('change_report')) {
    reportLock.value = item.submission > 0 ? '整改规划已有提交记录，关联报告已锁定。' : '整改规划已有附件，关联报告已锁定。'
  }
  savedReport.value = item.reportId
  fields.reportId = item.reportId
  fields.deadline = item.deadline
  entries.value = item.risks.map(riskEntry)
  selected.value = {
    id: item.reportId, number: item.reportNumber, filingNumber: item.filingNumber,
    systemName: item.systemName, organizationName: item.organizationName,
  }
  if (!fixedReport.value) void searchReports()
}

/**
 * 用途 按关键词检索有效备案下可关联的已通过测评，运营者只选本单位每份备案最新结果。
 * 参数 无，关键词来自表单。
 * 返回值 候选报告加载完成的任务。
 */
async function searchReports() {
  reportsRequest?.abort()
  const request = new AbortController()
  reportsRequest = request
  reportError.value = ''
  searching.value = true
  try {
    const result = await api<{ items: EligibleReport[] }>(
      basePath.value + '/eligible-reports' + params({ ...scope.value, q: keyword.value.trim(), limit: 20 }),
      { signal: request.signal },
    )
    if (reportsRequest === request) available.value = result.items
  } catch (error) {
    if (reportsRequest === request && !isAbort(error)) reportError.value = errorText(error)
  } finally {
    if (reportsRequest === request) searching.value = false
  }
}

/**
 * 用途 更新报告选择，检索翻新时保留已选报告。
 * 参数 无，选择标识来自下拉框。
 * 返回值 无。
 */
function selectReport() {
  selected.value = choices.value.find(item => item.id === fields.reportId)
}

/**
 * 用途 提取只包含后端定义字段的风险录入数据。
 * 参数 entry 为表单风险条目。
 * 返回值 声明的实际请求字段。
 */
function riskPayload(entry: RemediationRisk): RemediationRisk {
  return {
    risk: entry.risk, description: entry.description.trim(), measure: entry.measure.trim(),
    responsibility: entry.responsibility.trim(), deadline: entry.deadline,
  }
}

/**
 * 用途 保存整改规划，成功后前往同一计划的材料和审核页面。
 * 参数 无，使用表单提交时录入的风险数据和版本。
 * 返回值 保存任务；失败时保留原编辑内容。
 */
async function save() {
  if (saving.value || stale.value || failure.value) return
  const path = recordEndpoint.value
  const input = {
    reportId: fields.reportId, deadline: fields.deadline,
    risks: entries.value.map(riskPayload),
    ...(id.value ? { expectedVersion: version.value } : {}),
  }
  saving.value = true
  formError.value = ''
  try {
    const saved = await api<RemediationMutation>(path, { method: id.value ? 'PATCH' : 'POST', body: input })
    if (recordEndpoint.value === path) {
      await router.push({ path: remediationRecordPath(saved.id, basePath.value), query: scope.value })
    }
  } catch (error) {
    if (recordEndpoint.value !== path) return
    formError.value = errorText(error)
    stale.value = error instanceof ApiError && error.status === 409
  } finally {
    saving.value = false
  }
}

watch(recordEndpoint, load, { immediate: true })
onScopeDispose(() => { detailRequest?.abort(); reportsRequest?.abort() })
</script>

<template>
  <section class="form-page">
    <RouterLink class="back-link" :to="back">
      <ArrowLeft :size="16" aria-hidden="true" />{{ id ? '返回详情' : '返回整改规划' }}
    </RouterLink>
    <header class="page-header">
      <div><h1>{{ id ? '编辑整改规划' : '新建整改规划' }}</h1>
        <p>保存草稿后，在详情中上传方案、责任分工和实施计划。</p></div>
    </header>

    <div class="panel form-panel">
      <AsyncState :loading="loading" :error="failure" @retry="load">
        <form @submit.prevent="save">
          <h2>关联报告和整改期限</h2>
          <fieldset class="form-grid" :disabled="saving">
            <div v-if="!fixedReport" class="field wide">
              <label for="remediation-report-search">检索测评报告</label>
              <div class="input-action">
                <input id="remediation-report-search" v-model="keyword" type="search" maxlength="120"
                  placeholder="搜索报告、备案编号、系统或单位" @keydown.enter.prevent="searchReports">
                <button class="button neutral" type="button" :disabled="searching" @click="searchReports">
                  <Search :size="16" aria-hidden="true" />{{ searching ? '检索中…' : '检索' }}
                </button>
              </div>
              <small v-if="reportError" class="danger-text" role="alert">{{ reportError }}</small>
              <small v-else-if="!searching && !choices.length">暂无可关联的已通过测评报告。</small>
            </div>
            <label class="field wide"><span>关联测评报告</span>
              <select v-model="fields.reportId" required :disabled="fixedReport" @change="selectReport">
                <option value="" disabled>{{ isOperator ? '请选择该备案最新通过的测评报告' : '请选择已通过的测评报告' }}</option>
                <option v-for="report in choices" :key="report.id" :value="report.id">
                  {{ eligibleName(report) }}
                </option>
              </select>
              <small v-if="fixedReport">{{ reportLock }}</small>
            </label>
            <label class="field"><span>整改期限</span>
              <input v-model="fields.deadline" type="date" required>
            </label>
          </fieldset>

          <RemediationMaterialTemplates v-if="fields.reportId"
            :base-path="templatePath"
            :organization="scope.organization" />

          <div class="risk-heading">
            <h2>风险项与整改措施</h2>
            <button class="button neutral small" type="button"
              :disabled="saving || entries.length >= maxRisks" @click="entries.push(riskEntry())">
              <Plus :size="16" aria-hidden="true" />添加风险项
            </button>
          </div>
          <fieldset v-for="(entry, index) in entries" :key="entry.key" class="risk-form"
            :disabled="saving" :aria-label="'风险项 ' + (index + 1)">
            <div class="risk-heading risk-heading-item">
              <h3>风险项 {{ index + 1 }}</h3>
              <button class="text-action" type="button" :disabled="entries.length === 1"
                :aria-label="'移除风险项 ' + (index + 1)" @click="entries.splice(index, 1)">
                <Trash2 :size="15" aria-hidden="true" />移除
              </button>
            </div>
            <div class="form-grid">
              <label class="field"><span>风险级别</span>
                <select v-model="entry.risk" required>
                  <option value="" disabled>请选择</option>
                  <option v-for="(label, value) in risks" :key="value" :value="value">{{ label }}</option>
                </select>
              </label>
              <label class="field"><span>风险项期限</span>
                <input v-model="entry.deadline" type="date" :max="fields.deadline || undefined" required>
              </label>
              <label class="field"><span>风险说明</span>
                <input v-model="entry.description" maxlength="300" required>
              </label>
              <label class="field"><span>责任部门或责任人</span>
                <input v-model="entry.responsibility" maxlength="160" required>
              </label>
              <label class="field wide"><span>整改措施</span>
                <textarea v-model="entry.measure" maxlength="600" rows="3" required></textarea>
              </label>
            </div>
          </fieldset>

          <p v-if="formError" class="form-message error-message" role="alert">{{ formError }}</p>
          <div class="form-actions">
            <RouterLink class="button neutral" :to="recordPath">取消</RouterLink>
            <button v-if="stale" class="button neutral" type="button" @click="load">重新加载规划</button>
            <button class="button primary" type="submit" :disabled="saving || stale">
              {{ saving ? '保存中…' : '保存草稿' }}
            </button>
          </div>
        </form>
      </AsyncState>
    </div>
  </section>
</template>

<style scoped>
.risk-heading { display: flex; justify-content: space-between; align-items: center; gap: 10px; margin: 23px 0 12px; }
.risk-heading h2 { margin: 0; }
.risk-heading h3 { font-size: .95rem; }
.risk-heading-item { margin: 0 0 11px; }
.risk-form { padding: 13px 15px; margin-bottom: 12px; border: 1px solid var(--line); border-radius: 7px; background: #f8fbfa; }
.risk-form button:disabled { opacity: .45; cursor: default; }
</style>

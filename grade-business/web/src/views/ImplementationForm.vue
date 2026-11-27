<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { ArrowLeft, Search } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import type { DirectoryOption } from '../lib/directory'
import { queryValue } from '../lib/paging'
import { useDirectoryInstitution } from '../lib/useDirectoryInstitution'
import {
  formatImplementationRuleTime, implementationPath, implementationRecord, implementationTypes,
  watchImplementationRuleTimes,
  type FilingOption, type ImplementationDetail, type ImplementationInput,
  type ImplementationMutation, type ImplementationQuota,
} from '../lib/implementations'

const props = defineProps<{ institutional?: boolean }>()
const route = useRoute()
const router = useRouter()
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const institution = computed(() => props.institutional ? queryValue(route.query.institution).trim() : '')
const prefix = computed(() => props.institutional ? '/institution' : '')
const scopeQuery = computed(() => props.institutional ? { institution: institution.value } : {})
const path = computed(() => prefix.value + (id.value ? implementationRecord(id.value) : implementationPath))
const recordURL = computed(() => path.value + params(scopeQuery.value))
const returnTo = computed(() => ({ path: path.value, query: scopeQuery.value }))
const blank = {
  filingId: '', type: 'routine', reporter: '', reporterId: '', contact: '', assessmentOrg: '', plannedStart: '', plannedEnd: '', plan: '',
} as const
const fields = reactive<Required<ImplementationInput>>({ ...blank })
const loading = ref(false)
const loadError = ref('')
const formError = ref('')
const stale = ref(false)
const saving = ref(false)
const version = ref<number>()
const isCorrection = ref(false)
const isAmendment = ref(false)
const lockFiling = computed(() => isCorrection.value || isAmendment.value)
const planEditDeadline = ref<string>()
const existingCooldown = ref<string>()
const searchText = ref('')
const options = ref<FilingOption[]>([])
const selected = ref<FilingOption>()
const selectedInstitution = computed(() => options.value.find(item => item.id === fields.filingId)?.assessmentOrg
  ?? selected.value?.assessmentOrg)
const retestHandoff = computed(() => Boolean(institution.value && selectedInstitution.value
  && selectedInstitution.value !== institution.value))
const searching = ref(false)
const searchError = ref('')
const quota = ref<ImplementationQuota>()
const quotaError = ref('')
const resubmitAvailableAt = computed(() => existingCooldown.value ?? quota.value?.resubmitAvailableAt)
const preferDirectory = ref(false)
const effectiveInstitution = computed(() => (institution.value || fields.assessmentOrg || selected.value?.assessmentOrg || '').trim())
const { option: directoryInstitution, loading: checkingInstitution } =
  useDirectoryInstitution(() => effectiveInstitution.value)
const useReporterPicker = computed(() => Boolean(effectiveInstitution.value)
  && Boolean(directoryInstitution.value?.id || fields.reporterId))
const checkingReporter = computed(() => !isAmendment.value && checkingInstitution.value && !useReporterPicker.value)
let detailRequest: AbortController | undefined
let choicesRequest: AbortController | undefined
let quotaRequest: AbortController | undefined

const filingOptions = computed(() => selected.value && !options.value.some(item => item.id === selected.value?.id)
  ? [selected.value, ...options.value] : options.value)

/**
 * 用途 检索当前办理端允许关联的有效备案。
 * 参数 无，检索条件来自表单。
 * 返回值 候选读取任务。
 */
async function findFilings() {
  choicesRequest?.abort()
  const request = new AbortController()
  choicesRequest = request
  searching.value = true
  searchError.value = ''
  try {
    const suffix = params({ ...scopeQuery.value, q: searchText.value.trim() })
    const result = await api<{ items: FilingOption[] }>(prefix.value + implementationPath + '/filing-options' + suffix, {
      signal: request.signal,
    })
    if (!request.signal.aborted) options.value = result.items
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) searchError.value = errorText(error)
  } finally {
    if (choicesRequest === request) searching.value = false
  }
}

/**
 * 用途 加载草稿所选备案的年度额度与冷却时间。
 * 参数 无，备案来自当前表单。
 * 返回值 规则读取任务。
 */
async function loadQuota() {
  quotaRequest?.abort()
  quota.value = undefined
  quotaError.value = ''
  if (lockFiling.value || !fields.filingId) return
  const request = new AbortController()
  quotaRequest = request
  try {
    const suffix = params({ ...scopeQuery.value, filingId: fields.filingId })
    const result = await api<ImplementationQuota>(prefix.value + implementationPath + '/quota' + suffix,
      { signal: request.signal })
    if (!request.signal.aborted) quota.value = result
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) quotaError.value = errorText(error)
  }
}

/**
 * 用途 将可编辑报备及其备案快照回填到表单。
 * 参数 record 为服务端返回的报备详情。
 * 返回值 无。
 */
function populate(record: ImplementationDetail) {
  if (!record.allowedActions.includes('edit')) {
    loadError.value = '当前状态不允许编辑报备。'
    return
  }
  isCorrection.value = record.status === 'returned'
  isAmendment.value = record.status === 'submitted'
  planEditDeadline.value = record.planEditDeadline
  existingCooldown.value = record.resubmitAvailableAt
  version.value = record.version
  const { filingId, type, reporter, plannedStart, plannedEnd, plan, assessmentOrg } = record
  Object.assign(fields, {
    filingId, type, reporter, reporterId: record.reporterId ?? '', contact: record.contact ?? '',
    assessmentOrg, plannedStart, plannedEnd, plan,
  })
  selected.value = {
    id: filingId, number: record.filingNumber, systemName: record.systemName,
    organizationName: record.organizationName, assessmentOrg,
  }
}

/**
 * 用途 初始化新建表单或加载已保存的报备。
 * 参数 无，报备标识从路由获取。
 * 返回值 表单准备任务。
 */
async function load() {
  detailRequest?.abort()
  choicesRequest?.abort()
  const request = new AbortController()
  detailRequest = request
  loadError.value = ''
  formError.value = ''
  stale.value = false
  selected.value = undefined
  version.value = undefined
  isCorrection.value = false
  isAmendment.value = false
  planEditDeadline.value = undefined
  existingCooldown.value = undefined
  options.value = []
  searchText.value = ''
  searchError.value = ''
  preferDirectory.value = false
  Object.assign(fields, blank)
  loading.value = Boolean(id.value)
  if (props.institutional && !institution.value) {
    loadError.value = '请先在机构报送页选择测评机构。'
    loading.value = false
    return
  }
  if (!id.value) {
    void findFilings()
    return
  }
  try {
    const result = await api<ImplementationDetail>(recordURL.value, { signal: request.signal })
    if (request.signal.aborted) return
    populate(result)
    if (!lockFiling.value && !loadError.value) void findFilings()
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) loadError.value = errorText(error)
  } finally {
    if (detailRequest === request) loading.value = false
  }
}

/**
 * 用途 记录关联备案选项，切换后保留候选快照和继承的测评机构。
 * 参数 无，当前选中项来自绑定的原生下拉框。
 * 返回值 无。
 */
function chooseFiling() {
  const choice = options.value.find(item => item.id === fields.filingId)
  if (!choice) return
  const previousInstitution = effectiveInstitution.value
  if (fields.assessmentOrg === selected.value?.assessmentOrg) fields.assessmentOrg = ''
  selected.value = choice
  if (retestHandoff.value) fields.type = 'change'
  if (previousInstitution !== effectiveInstitution.value) clearReporter()
}

/**
 * 用途 中止沿用原机构名录中的人员。
 * 参数 无，当前机构及人员来自实施报备表单。
 * 返回值 无。
 */
function clearReporter() {
  if (fields.reporterId) fields.reporter = ''
  fields.reporterId = ''
}

/**
 * 用途 保存人员目录项用于显示，同时带上稳定标识。
 * 参数 item 为本次选中的测评人员，或清空选择时的空值。
 * 返回值 无。
 */
function chooseReporter(item?: DirectoryOption) {
  fields.reporter = item?.name ?? ''
}

/**
 * 用途 到达测评开始日零点时提醒用户重新查看报备，并阻止继续保存编辑。
 * 参数 无，截止时间取自当前报备。
 * 返回值 已超过计划编辑期限时为真。
 */
function closeElapsedPlan() {
  if (!planEditDeadline.value || Date.now() < Date.parse(planEditDeadline.value)) return false
  stale.value = true
  formError.value = '已到测评开始日零点，无法保存计划修改。'
  return true
}

/**
 * 用途 保存实施报备表单的必要字段。
 * 参数 无，使用表单快照和编辑时的版本。
 * 返回值 保存及成功后的导航任务。
 */
async function save() {
  if (saving.value || stale.value || loading.value || loadError.value || checkingReporter.value) return
  if (closeElapsedPlan()) return
  const current = recordURL.value
  const editing = Boolean(id.value)
  const { filingId, type, plannedStart, plannedEnd } = fields
  const reporter = fields.reporter.trim()
  const plan = fields.plan.trim()
  const contact = fields.contact.trim()
  const assessmentOrg = fields.assessmentOrg.trim()
  formError.value = ''
  if (!filingId || !reporter || !plan || !plannedStart || !plannedEnd || plannedEnd < plannedStart) {
    formError.value = '请填写必填项，并确保计划完成日期不早于开始日期。'
    return
  }
  if (!isAmendment.value && useReporterPicker.value && !fields.reporterId) {
    formError.value = '请从测评人员名录选择报备人员。'
    return
  }
  const body = {
    filingId, type, reporter, plannedStart, plannedEnd, plan,
    ...(contact ? { contact } : {}), ...(!props.institutional && assessmentOrg ? { assessmentOrg } : {}),
    ...(fields.reporterId ? { reporterId: fields.reporterId } : {}),
    ...(editing ? { expectedVersion: version.value } : {}),
  }
  saving.value = true
  try {
    const result = await api<ImplementationMutation>(current, { method: editing ? 'PATCH' : 'POST', body })
    if (recordURL.value === current) await router.push({
      path: prefix.value + implementationRecord(result.id), query: scopeQuery.value,
    })
  } catch (error) {
    if (recordURL.value !== current) return
    formError.value = errorText(error)
    if (error instanceof ApiError && ['stale_version', 'plan_edit_deadline'].includes(error.code)) stale.value = true
  } finally {
    saving.value = false
  }
}

watch(recordURL, load, { immediate: true })
watch([() => fields.filingId, lockFiling], loadQuota)
watchImplementationRuleTimes(() => ({ planEditDeadline: planEditDeadline.value }), closeElapsedPlan)
watchImplementationRuleTimes(() => ({ resubmitAvailableAt: resubmitAvailableAt.value }), () => {
  if (lockFiling.value) existingCooldown.value = undefined
  else void loadQuota()
})
onBeforeUnmount(() => {
  detailRequest?.abort()
  choicesRequest?.abort()
  quotaRequest?.abort()
})
</script>

<template>
  <section class="form-page">
    <RouterLink class="back-link" :to="returnTo"><ArrowLeft :size="16" aria-hidden="true" />{{ id ? '返回报备详情' : '返回实施管理' }}</RouterLink>
    <header class="page-header">
      <div><h1>{{ isAmendment ? '修改实施计划' : id ? '编辑实施报备' : '新建实施报备' }}</h1>
        <p v-if="isCorrection">按退回意见补充报备信息，保存后重新提交。</p>
        <p v-else-if="isAmendment">修改计划日期和范围，报备保持待受理。</p>
        <p v-else>选择有效备案并填写实施计划。</p>
      </div>
    </header>
    <AsyncState :loading="loading" :error="loadError" @retry="load">
      <form class="panel form-panel" @submit.prevent="save">
        <div class="form-grid">
          <div v-if="!lockFiling" class="field wide">
            <label for="implementation-filing-search">检索备案</label>
            <div class="input-action">
              <input id="implementation-filing-search" v-model="searchText" type="search" maxlength="120"
                placeholder="备案编号、系统或单位名称" @keydown.enter.prevent="findFilings" />
              <button class="button neutral" type="button" :disabled="searching" @click="findFilings">
                <Search :size="16" aria-hidden="true" />{{ searching ? '搜索中…' : '搜索' }}
              </button>
            </div>
            <small v-if="searchError" role="alert">{{ searchError }}</small>
          </div>
          <div class="field">
            <label for="implementation-filing"><span>关联备案</span></label>
            <select id="implementation-filing" v-model="fields.filingId" name="filingId"
              :aria-describedby="selected ? 'implementation-filing-unit' : undefined"
              required :disabled="lockFiling || saving" @change="chooseFiling">
              <option value="" disabled>{{ searching ? '加载候选…' : '选择已通过的有效备案' }}</option>
              <option v-for="option in filingOptions" :key="option.id" :value="option.id">{{ option.number }} · {{ option.systemName }}</option>
            </select>
            <small v-if="selected" id="implementation-filing-unit">{{ selected.organizationName }}</small>
            <small v-else-if="!searching && !searchError && !filingOptions.length">当前没有匹配的有效备案。</small>
          </div>
          <label class="field">
            <span>报备类型</span>
            <select v-model="fields.type" :disabled="retestHandoff || lockFiling || saving">
              <option v-for="(label, value) in implementationTypes" :key="value" :value="value">{{ label }}</option>
            </select>
            <small v-if="retestHandoff">运营单位已指定本机构复测，请办理变更实施报备。</small>
          </label>
          <p v-if="quota && fields.type === 'change' && !lockFiling" class="wide" role="status">
            {{ quota.year }} 年变更已用 {{ quota.used }} 次，剩余 {{ quota.limit - quota.used }} 次。
            {{ quota.used === quota.limit ? '当前额度已用完，可以保存草稿。' : '首次提交后占用一次额度。' }}
          </p>
          <p v-if="resubmitAvailableAt" class="wide" role="status">
            本备案的撤销冷却期截至 {{ formatImplementationRuleTime(resubmitAvailableAt) }}。
          </p>
          <p v-if="quotaError" class="form-message error-message wide" role="alert">{{ quotaError }}</p>
          <DirectoryPicker v-if="useReporterPicker" v-model="fields.reporterId" kind="person"
            select-id="implementation-reporter" label="报备人员" :institution="effectiveInstitution"
            :initial-label="fields.reporter"
            required :disabled="isAmendment || saving" @picked="chooseReporter" />
          <p v-else-if="checkingReporter" class="field" role="status"><span>报备人员</span><small>正在核对机构名录…</small></p>
          <label v-else class="field">
            <span>报备人员</span>
            <input id="implementation-reporter" v-model="fields.reporter" name="reporter"
              type="text" required maxlength="120" :disabled="isAmendment || saving" />
          </label>
          <label class="field">
            <span>联系电话（选填）</span><input v-model="fields.contact" type="tel" maxlength="60" :disabled="isAmendment || saving" />
          </label>
          <div v-if="!institutional" class="field wide">
            <DirectoryPicker v-if="preferDirectory" v-model="fields.assessmentOrg" kind="institution"
              label="测评机构" :initial-label="fields.assessmentOrg"
              :disabled="isAmendment || saving" @picked="clearReporter" />
            <label v-else class="field"><span>测评机构</span>
              <input v-model="fields.assessmentOrg" type="text" maxlength="200"
                :placeholder="selected?.assessmentOrg || '留空时沿用备案测评机构'"
                :disabled="isAmendment || saving" @change="clearReporter" />
            </label>
            <button v-if="!isAmendment" class="text-action directory-mode" type="button"
              :disabled="saving" @click="preferDirectory = !preferDirectory">{{ preferDirectory ? '手工录入办理名称' : '从机构名录选择' }}</button>
          </div>
          <p v-else class="field wide"><span>测评机构</span><strong>{{ institution }}</strong></p>
          <label class="field">
            <span>计划开始</span><input v-model="fields.plannedStart" type="date" min="0001-01-01" max="9999-12-31" required :disabled="saving" />
          </label>
          <label class="field">
            <span>计划完成</span><input v-model="fields.plannedEnd" type="date" :min="fields.plannedStart || '0001-01-01'"
              max="9999-12-31" required :disabled="saving" />
          </label>
          <label class="field wide">
            <span>实施计划与范围</span><textarea v-model="fields.plan" rows="5" required maxlength="5000"
              placeholder="填写实施内容、影响范围及计划时间窗口" :disabled="saving" />
            <small v-if="planEditDeadline">计划修改截止于 {{ formatImplementationRuleTime(planEditDeadline) }}。</small>
            <small v-else>测评开始前，发起人可在提交后 24 小时内且未受理时撤销。测评开始后可撤销未办结报备。撤销后，同一备案须等待 15 天才能重报。</small>
          </label>
        </div>
        <p v-if="formError" class="form-message error-message" role="alert">{{ formError }}</p>
        <div class="form-actions">
          <RouterLink :to="returnTo" class="button neutral">取消</RouterLink>
          <button v-if="stale" type="button" class="button neutral" @click="load">重新加载记录</button>
          <button type="submit" class="button primary" :disabled="saving || stale || checkingReporter">{{ saving ? '保存中…' : id ? '保存报备' : '保存草稿' }}</button>
        </div>
      </form>
    </AsyncState>
  </section>
</template>

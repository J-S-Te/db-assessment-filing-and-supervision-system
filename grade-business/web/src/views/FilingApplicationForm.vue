<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, shallowRef, watch } from 'vue'
import { ArrowLeft } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { levels, type CaseDetail, type CaseMutation, type CaseSummary, type Page } from '../lib/domain'
import { applicationTypes, displayFilingValue, filingApplicationPath, filingApplicationsPath,
  filingFieldKeys, filingFields, proposedChanges, type FilingApplicationDetail,
  type FilingApplicationType, type FilingValues } from '../lib/filingApplications'
import { queryValue } from '../lib/paging'

const props = defineProps<{ institutional?: boolean; operator?: boolean }>()
const route = useRoute()
const router = useRouter()
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const institution = computed(() => props.institutional ? queryValue(route.query.institution).trim() : '')
const organization = computed(() => props.operator ? queryValue(route.query.organization).trim() : '')
const prefix = computed(() => props.institutional ? '/institution' : props.operator ? '/operator' : '')
const scopeQuery = computed(() => props.institutional ? { institution: institution.value }
  : props.operator ? { organization: organization.value } : {})
const availableFields = computed(() => props.operator
  ? filingFieldKeys.filter(field => field !== 'organizationName') : filingFieldKeys)
const scopeSuffix = computed(() => params(scopeQuery.value))
const filingID = computed(() => source.value?.id || queryValue(route.query.filingId))
const returnTo = computed(() => ({
  path: prefix.value + (id.value ? filingApplicationPath(id.value)
    : !prefix.value && filingID.value ? '/filings/' + encodeURIComponent(filingID.value) : filingApplicationsPath),
  query: scopeQuery.value,
}))
const source = shallowRef<CaseDetail>()
const original = shallowRef<FilingValues>()
const version = ref<number>()
const type = ref<FilingApplicationType>('change')
const reason = ref('')
const draft = reactive<FilingValues>({ systemName: '', organizationName: '', assessmentOrg: '', level: '' })
const loading = ref(true)
const loadError = ref('')
const saveError = ref('')
const saveErrorCode = ref('')
const saving = ref(false)
const options = shallowRef<CaseSummary[]>([])
const keyword = ref('')
const selectedFiling = ref('')
const optionsError = ref('')
let loadRequest: AbortController | undefined
let saveRequest: AbortController | undefined
let optionsRequest: AbortController | undefined

/**
 * 用途 加载主备案与已有申请，以审批时记录的前后值回填表单。
 * 参数 无，备案标识、编辑标识与初始类型来自当前路由。
 * 返回值 表单加载任务，读取失败时显示可重试提示。
 */
async function load() {
  loadRequest?.abort()
  saveRequest?.abort()
  saving.value = false
  const request = new AbortController()
  loadRequest = request
  source.value = undefined
  original.value = undefined
  version.value = undefined
  saveError.value = ''
  saveErrorCode.value = ''
  loadError.value = ''
  loading.value = true
  try {
    if ((props.institutional && !institution.value) || (props.operator && !organization.value)) {
      loadError.value = props.operator
        ? '请先在我的备案办理中选择所属单位。' : '请先在机构报送页选择测评机构。'
      return
    }
    const item = id.value ? await api<FilingApplicationDetail>(
      prefix.value + filingApplicationPath(id.value) + scopeSuffix.value, { signal: request.signal },
    ) : undefined
    const selected = item?.filingId || queryValue(route.query.filingId)
    if (request.signal.aborted) return
    if (!selected) {
      if (prefix.value) void findFilings()
      return
    }
    const filing = await api<CaseDetail>(prefix.value + '/filings/' + encodeURIComponent(selected) + scopeSuffix.value,
      { signal: request.signal })
    if (request.signal.aborted) return
    populate(filing, item)
  } catch (error) {
    if (loadRequest === request && !isAbort(error)) loadError.value = errorText(error)
  } finally {
    if (loadRequest === request) loading.value = false
  }
}

/**
 * 用途 在所选机构的已通过有效备案中查找发起申请的对象。
 * 参数 无，检索条件来自当前表单。
 * 返回值 查询任务。
 */
async function findFilings() {
  optionsRequest?.abort()
  const request = new AbortController()
  optionsRequest = request
  optionsError.value = ''
  options.value = []
  selectedFiling.value = ''
  try {
    const result = await api<Page<CaseSummary>>(prefix.value + '/filings' + params({
      ...scopeQuery.value, status: 'approved', lifecycle: 'active', q: keyword.value.trim(),
    }), { signal: request.signal })
    if (!request.signal.aborted) options.value = result.items
  } catch (error) {
    if (!isAbort(error) && !request.signal.aborted) optionsError.value = errorText(error)
  }
}

/**
 * 用途 从候选备案进入完整的机构变更或注销表单。
 * 参数 无，备案标识来自当前选择。
 * 返回值 更新当前路由的任务。
 */
function chooseFiling() {
  if (selectedFiling.value) return router.replace({ query: {
    ...scopeQuery.value, filingId: selectedFiling.value, type: route.query.type,
  } })
}

/**
 * 用途 检查备案和申请的可办理条件，并将提案回填到编辑表单。
 * 参数 filing 为当前备案，item 为已有待编辑申请，新建时无该参数。
 * 返回值 无。
 */
function populate(filing: CaseDetail, item?: FilingApplicationDetail) {
  source.value = filing
  if (filing.status !== 'approved' || filing.cancelledAt) {
    loadError.value = '请选择已审核通过且有效的备案。'
    return
  }
  if (item && !item.allowedActions.includes('edit')) {
    loadError.value = '当前审核状态无法编辑。'
    return
  }
  type.value = item?.type ?? (queryValue(route.query.type) === 'cancel' ? 'cancel' : 'change')
  reason.value = item?.reason ?? ''
  version.value = item?.version
  const before: FilingValues = {
    systemName: filing.systemName, organizationName: filing.organizationName,
    assessmentOrg: filing.assessmentOrg, level: filing.level ?? '',
  }
  Object.assign(draft, before)
  for (const change of item?.changes ?? []) {
    before[change.field] = change.before
    draft[change.field] = change.after
  }
  original.value = before
}

/**
 * 用途 保存申请及实际变化字段，出现版本冲突时保留当前输入。
 * 参数 无，使用当前表单、申请类型和读取版本。
 * 返回值 保存任务，成功后跳转到真实申请记录。
 */
async function save() {
  if (saving.value || !original.value || !source.value || saveErrorCode.value === 'conflict') return
  const changes = type.value === 'change' ? proposedChanges(original.value, draft) : undefined
  const cleanReason = reason.value.trim()
  if (!cleanReason || (changes && (!Object.keys(changes).length || Object.values(changes).some((v) => !v)))) {
    saveError.value = '请填写申请理由，并为变更申请填写至少一项有效修改。'
    return
  }
  const body = {
    reason: cleanReason, ...(changes ? { changes } : {}),
    ...(id.value ? { expectedVersion: version.value } : { filingId: source.value.id, type: type.value }),
  }
  const request = new AbortController()
  saveRequest = request
  saving.value = true
  saveError.value = ''
  saveErrorCode.value = ''
  try {
    const endpoint = id.value ? filingApplicationPath(id.value) : filingApplicationsPath
    const result = await api<CaseMutation>(prefix.value + endpoint + scopeSuffix.value,
      { method: id.value ? 'PATCH' : 'POST', body, signal: request.signal })
    if (!request.signal.aborted) await router.push({
      path: prefix.value + filingApplicationPath(result.id), query: scopeQuery.value,
    })
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    saveError.value = errorText(error)
    saveErrorCode.value = error instanceof ApiError ? error.code : ''
  } finally {
    if (saveRequest === request) saving.value = false
  }
}

watch(() => [id.value, route.query.filingId, route.query.type, scopeSuffix.value], load, { immediate: true })
onScopeDispose(() => {
  loadRequest?.abort()
  saveRequest?.abort()
  optionsRequest?.abort()
})
</script>

<template>
  <section class="form-page">
    <RouterLink class="back-link" :to="returnTo"><ArrowLeft :size="17" aria-hidden="true" />返回</RouterLink>
    <header class="page-header">
      <h1>{{ id ? '编辑' + applicationTypes[type] + '申请' : '发起备案变更或注销申请' }}</h1>
    </header>
    <AsyncState :loading="loading" :error="loadError" @retry="load">
      <div v-if="!original || !source" class="panel form-panel">
        <p>请从已通过且有效的备案发起申请。</p>
        <form v-if="prefix" class="form-grid" @submit.prevent="chooseFiling">
          <label class="field"><span>检索备案</span><input v-model="keyword" type="search" maxlength="120"
            @keydown.enter.prevent="findFilings"></label>
          <button class="button neutral" type="button" @click="findFilings">检索</button>
          <label class="field wide"><span>关联备案</span><select v-model="selectedFiling" required>
            <option value="" disabled>请选择本机构的有效备案</option>
            <option v-for="item in options" :key="item.id" :value="item.id">{{ item.number }} · {{ item.systemName }}</option>
          </select></label>
          <p v-if="optionsError" class="form-message error-message wide" role="alert">{{ optionsError }}</p>
          <button class="button primary" type="submit" :disabled="!selectedFiling">确认备案</button>
        </form>
        <div v-else class="form-actions">
          <RouterLink class="button primary" :to="{ path: '/filings', query: { status: 'approved', lifecycle: 'active' } }">
            选择备案
          </RouterLink>
        </div>
      </div>
      <form v-else class="panel form-panel" @submit.prevent="save">
        <h2>{{ source.number }} · {{ source.systemName }}</h2>
        <div class="form-grid">
          <label class="field" v-if="!id">
            <span>申请类型</span>
            <select v-model="type" :disabled="saving">
              <option v-for="(label, key) in applicationTypes" :key="key" :value="key">{{ label }}</option>
            </select>
          </label>
          <label class="field wide">
            <span>申请理由</span>
            <textarea v-model="reason" rows="4" maxlength="1000" required :disabled="saving" />
          </label>
          <template v-if="type === 'change'">
            <template v-for="field in availableFields" :key="field">
              <div v-if="operator && field === 'assessmentOrg'" class="field">
                <DirectoryPicker v-model="draft.assessmentOrg" kind="institution" label="测评机构"
                  :initial-label="draft.assessmentOrg" required :disabled="saving" />
                <span class="muted">原值 {{ original.assessmentOrg }}</span>
              </div>
              <label v-else class="field">
                <span>{{ filingFields[field] }}</span>
                <span class="muted">原值 {{ displayFilingValue(field, original[field]) }}</span>
                <select v-if="field === 'level'" v-model="draft[field]" required :disabled="saving">
                  <option v-for="(label, key) in levels" :key="key" :value="key">{{ label }}</option>
                </select>
                <input v-else v-model="draft[field]" maxlength="200" required :disabled="saving">
              </label>
            </template>
          </template>
          <p v-else class="wide muted">终审通过后，备案将标记为已注销，关联记录仍可查询。</p>
          <p v-if="operator && type === 'change' && draft.assessmentOrg.trim() !== original.assessmentOrg"
            class="wide form-message warning-message">
            保存草稿后，请联系拟变更的测评机构在机构端办理实施报备。报备通过监管复核后可提交备案变更。
          </p>
          <p v-if="operator && type === 'cancel'" class="wide muted">保存后请上传注销及数据留存或销毁说明。</p>
        </div>
        <p v-if="saveError" class="form-message error-message" role="alert">{{ saveError }}</p>
        <div class="form-actions">
          <RouterLink v-if="saveErrorCode === 'active_application'" class="button neutral"
            :to="{ path: prefix + filingApplicationsPath, query: { ...scopeQuery, filingId: source.id } }">查看已有申请</RouterLink>
          <button v-if="saveErrorCode === 'conflict'" class="button neutral" type="button" @click="load">
            重新加载表单
          </button>
          <RouterLink class="button neutral" :to="returnTo">取消</RouterLink>
          <button class="button primary" type="submit" :disabled="saving || saveErrorCode === 'conflict'">
            {{ saving ? '保存中…' : '保存草稿' }}
          </button>
        </div>
      </form>
    </AsyncState>
  </section>
</template>

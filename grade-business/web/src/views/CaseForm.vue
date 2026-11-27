<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { ArrowLeft, Search } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import ClassificationAssessmentPanel from '../components/ClassificationAssessment.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import type { ClassificationAssessment, ClassificationInput } from '../lib/classification'
import { caseLabels, levels, risks, type CaseDetail, type CaseKind, type CaseMutation, type CaseSummary, type Page } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { useDirectoryInstitution } from '../lib/useDirectoryInstitution'

const props = defineProps<{ kind: CaseKind; institutional?: boolean; operator?: boolean }>()
const route = useRoute()
const router = useRouter()
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const institution = computed(() => props.institutional ? queryValue(route.query.institution).trim() : '')
const organization = computed(() => props.operator ? queryValue(route.query.organization).trim() : '')
const collection = computed(() => (props.institutional ? '/institution/' : props.operator ? '/operator/' : '/') + props.kind)
const path = computed(() => collection.value + (id.value ? '/' + encodeURIComponent(id.value) : ''))
const scopeQuery = computed(() => props.institutional ? { institution: institution.value }
  : props.operator ? { organization: organization.value } : {})
const resourceURL = computed(() => path.value + params(scopeQuery.value))
const returnTo = computed(() => ({ path: path.value, query: scopeQuery.value }))
const label = computed(() => caseLabels[props.kind])
const blank = {
  number: '', systemName: '', organizationName: '', assessmentOrg: '', level: '', filingId: '', risk: '',
  assessmentPersonId: '',
}
const fields = reactive({ ...blank })
const awaitingNumber = ref(false)
const provisionalNumber = ref(false)
const showNumber = computed(() => props.kind !== 'filings'
  || (!props.operator && (props.institutional || provisionalNumber.value || !awaitingNumber.value)))
const lockedNumber = computed(() => props.kind === 'filings'
  && props.institutional && Boolean(id.value) && !provisionalNumber.value)
const optionalNumber = computed(() => props.kind === 'filings' && !lockedNumber.value
  && (props.institutional || provisionalNumber.value))
const requireNumber = computed(() => showNumber.value && !optionalNumber.value && !lockedNumber.value)
const version = ref<number>()
const classificationInitial = ref<ClassificationAssessment>()
const classificationInput = ref<ClassificationInput>()
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const formError = ref('')
const filingKeyword = ref('')
const filingOptions = ref<CaseSummary[]>([])
const savedFiling = ref<{ id: string; label: string }>()
const searching = ref(false)
const searchError = ref('')
const stale = ref(false)
const institutionPicker = ref(false)
const unitPicker = ref(false)
const savedPersonName = ref('')
const assessmentScope = computed(() => (institution.value || fields.assessmentOrg).trim())
const { option: directoryInstitution } = useDirectoryInstitution(() => props.kind === 'reports' ? assessmentScope.value : '')
const usePersonPicker = computed(() => props.kind === 'reports' && Boolean(assessmentScope.value)
  && Boolean(directoryInstitution.value?.id || fields.assessmentPersonId))
let detailsRequest: AbortController | undefined
let filingsRequest: AbortController | undefined

/**
 * 用途 载入记录或重置新建表单。
 * 参数 无，记录类型与标识来自路由。
 * 返回值 当前表单加载任务。
 */
async function load() {
  detailsRequest?.abort()
  const request = new AbortController()
  detailsRequest = request
  loadError.value = ''
  formError.value = ''
  stale.value = false
  version.value = undefined
  classificationInitial.value = undefined
  classificationInput.value = undefined
  Object.assign(fields, blank)
  awaitingNumber.value = false
  provisionalNumber.value = false
  institutionPicker.value = false
  unitPicker.value = false
  savedPersonName.value = ''
  savedFiling.value = undefined
  filingOptions.value = []
  filingKeyword.value = ''
  loading.value = Boolean(id.value)
  if ((props.institutional && !institution.value) || (props.operator && !organization.value)) {
    loadError.value = props.operator ? '请先在我的备案办理中选择所属单位。' : '请先在机构报送页选择测评机构。'
    loading.value = false
    return
  }
  if (props.kind === 'reports') void findFilings()
  else filingsRequest?.abort()
  if (!id.value) return
  try {
    const result = await api<CaseDetail>(resourceURL.value, { signal: request.signal })
    if (detailsRequest !== request) return
    populate(result)
  } catch (error) {
    if (detailsRequest === request && !isAbort(error)) loadError.value = errorText(error)
  } finally {
    if (detailsRequest === request) loading.value = false
  }
}

/**
 * 用途 将可编辑记录回填到对应表单。
 * 参数 result 为服务端详情。
 * 返回值 无。
 */
function populate(result: CaseDetail) {
  if (!result.allowedActions.includes('edit')) {
    loadError.value = '当前审核状态不允许编辑。'
    return
  }
  version.value = result.version
  classificationInitial.value = result.classificationAssessment
  classificationInput.value = result.classificationAssessment && {
    information: result.classificationAssessment.information,
    service: result.classificationAssessment.service,
    basis: result.classificationAssessment.basis,
    competentAuthority: result.classificationAssessment.competentAuthority,
    filingAuthority: result.classificationAssessment.filingAuthority,
  }
  fields.number = result.number
  awaitingNumber.value = !result.number
  provisionalNumber.value = result.filingNumberProvisional === true
  fields.systemName = result.systemName
  fields.organizationName = result.organizationName
  fields.assessmentOrg = result.assessmentOrg
  fields.level = result.level ?? ''
  fields.filingId = result.filingId ?? ''
  fields.risk = result.risk ?? ''
  fields.assessmentPersonId = result.assessmentPersonId ?? ''
  savedPersonName.value = result.directorySnapshot?.person?.name ?? ''
  if (fields.filingId) savedFiling.value = {
    id: fields.filingId,
    label: (result.filingNumber || '') + ' · ' + result.systemName,
  }
}

/**
 * 用途 清空测评机构变动前选中的人员。
 * 参数 无，人员与机构取自当前表单。
 * 返回值 无。
 */
function clearAssessmentPerson() {
  fields.assessmentPersonId = ''
  savedPersonName.value = ''
}

/**
 * 用途 检索少量候选备案供测评报告关联。
 * 参数 无，检索文本来自当前表单。
 * 返回值 候选加载任务。
 */
async function findFilings() {
  filingsRequest?.abort()
  const request = new AbortController()
  filingsRequest = request
  searching.value = true
  searchError.value = ''
  try {
    const page = await api<Page<CaseSummary>>((props.institutional ? '/institution/filings' : '/filings') + params({
      ...scopeQuery.value, status: props.institutional ? 'approved' : undefined,
      q: filingKeyword.value.trim(), lifecycle: 'active', limit: 20,
    }), { signal: request.signal })
    if (filingsRequest === request) filingOptions.value = page.items
  } catch (error) {
    if (filingsRequest === request && !isAbort(error)) searchError.value = errorText(error)
  } finally {
    if (filingsRequest === request) searching.value = false
  }
}

/**
 * 用途 保存当前业务草稿的必要字段。
 * 参数 无，使用表单提交时的快照。
 * 返回值 提交任务，保存成功后打开实际记录。
 */
async function save() {
  if (saving.value || stale.value || loadError.value) return
  const currentId = id.value
  const kind = props.kind
  const url = resourceURL.value
  if ((requireNumber.value && !fields.number.trim())
    || !(props.institutional ? institution.value : fields.assessmentOrg.trim())
    || (kind === 'filings' ? (!fields.systemName.trim() || (!props.operator && !fields.organizationName.trim()) || !fields.level)
      : (!fields.filingId || !fields.risk))) {
    formError.value = '请填写完整的业务信息。'
    return
  }
  if (usePersonPicker.value && !fields.assessmentPersonId) {
    formError.value = '请从测评人员名录选择本报告的测评人员。'
    return
  }
  const body = {
    ...(showNumber.value ? { number: fields.number.trim() } : {}),
    ...(!props.institutional ? { assessmentOrg: fields.assessmentOrg.trim() } : {}),
    ...(kind === 'filings'
      ? { systemName: fields.systemName.trim(), level: fields.level,
        ...(classificationInput.value ? { classificationAssessment: classificationInput.value } : {}),
        ...(!props.operator ? { organizationName: fields.organizationName.trim() } : {}) }
      : { filingId: fields.filingId, risk: fields.risk,
        ...(fields.assessmentPersonId ? { assessmentPersonId: fields.assessmentPersonId } : {}) }),
    ...(currentId ? { expectedVersion: version.value } : {}),
  }
  saving.value = true
  formError.value = ''
  try {
    const result = await api<CaseMutation>(url, { method: currentId ? 'PATCH' : 'POST', body })
    if (resourceURL.value === url) {
      await router.push({ path: collection.value + '/' + encodeURIComponent(result.id), query: route.query })
    }
  } catch (error) {
    if (resourceURL.value !== url) return
    formError.value = errorText(error)
    if (error instanceof ApiError && error.status === 409 && /stale|version|conflict/.test(error.code)) {
      stale.value = true
    }
  } finally {
    saving.value = false
  }
}

watch(resourceURL, load, { immediate: true })
onBeforeUnmount(() => {
  detailsRequest?.abort()
  filingsRequest?.abort()
})
</script>

<template>
  <section class="form-page">
    <RouterLink class="back-link" :to="returnTo">
      <ArrowLeft :size="16" aria-hidden="true" />{{ id ? '返回详情' : '返回列表' }}
    </RouterLink>
    <header class="page-header">
      <div>
        <h1>{{ id ? '编辑' : '新建' }}{{ label.record }}</h1>
        <p>保存草稿后，可在详情中添加材料并提交审核。</p>
      </div>
    </header>

    <div class="panel form-panel">
      <AsyncState :loading="loading" :error="loadError" @retry="load">
        <form @submit.prevent="save">
          <h2>基本信息</h2>
          <fieldset class="form-grid" :disabled="saving">
            <label v-if="showNumber" class="field">
              <span>{{ optionalNumber ? '预填备案编号（选填）' : label.number }}</span>
              <input v-model="fields.number" :required="requireNumber" :readonly="lockedNumber"
                maxlength="80" :placeholder="optionalNumber ? '终审通过后生效' : '请输入' + label.number">
            </label>
            <div v-if="!institutional" class="field directory-field">
              <DirectoryPicker v-if="institutionPicker" v-model="fields.assessmentOrg" kind="institution"
                label="测评机构" :initial-label="fields.assessmentOrg" required :disabled="saving"
                @picked="clearAssessmentPerson" />
              <label v-else class="field">
                <span>测评机构</span>
                <input v-model="fields.assessmentOrg" required maxlength="200" placeholder="请输入机构名称"
                  @change="clearAssessmentPerson">
              </label>
              <button class="text-action directory-mode" type="button" @click="institutionPicker = !institutionPicker">
                {{ institutionPicker ? '手工录入办理名称' : '从机构名录选择' }}
              </button>
            </div>
            <p v-else class="field"><span>测评机构</span><strong>{{ institution }}</strong></p>
            <DirectoryPicker v-if="usePersonPicker" v-model="fields.assessmentPersonId" kind="person"
              label="测评人员" :institution="assessmentScope" :initial-label="savedPersonName" required :disabled="saving" />
            <template v-if="kind === 'filings'">
              <label class="field">
                <span>系统名称</span>
                <input v-model="fields.systemName" required maxlength="200" placeholder="请输入系统名称">
              </label>
              <div v-if="!operator" class="field directory-field">
                <DirectoryPicker v-if="unitPicker" v-model="fields.organizationName" kind="unit"
                  label="所属单位" :institution="institution" :initial-label="fields.organizationName" required :disabled="saving" />
                <label v-else class="field">
                  <span>所属单位</span>
                  <input v-model="fields.organizationName" required maxlength="200" placeholder="请输入所属单位">
                </label>
                <button class="text-action directory-mode" type="button" @click="unitPicker = !unitPicker">
                  {{ unitPicker ? '手工录入办理名称' : '从单位名录选择' }}
                </button>
              </div>
              <p v-else class="field"><span>所属单位</span><strong>{{ organization }}</strong></p>
              <label class="field">
                <span>定级</span>
                <select v-model="fields.level" required>
                  <option disabled value="">请选择等级</option>
                  <option v-for="(text, key) in levels" :key="key" :value="key">{{ text }}</option>
                </select>
              </label>
              <ClassificationAssessmentPanel :key="id" :initial="classificationInitial" :disabled="saving"
                :expanded="route.query.classification === '1'" @change="classificationInput = $event"
                @adopt="fields.level = $event" />
            </template>
            <template v-else>
              <label class="field">
                <span>风险级别</span>
                <select v-model="fields.risk" required>
                  <option disabled value="">请选择风险级别</option>
                  <option v-for="(text, key) in risks" :key="key" :value="key">{{ text }}</option>
                </select>
              </label>
              <div class="field wide">
                <label for="find-filing">检索备案</label>
                <div class="input-action">
                  <input id="find-filing" v-model="filingKeyword" type="search" maxlength="120"
                    placeholder="按备案编号、系统或所属单位检索" @keydown.enter.prevent="findFilings">
                  <button type="button" class="button neutral" :disabled="searching" @click="findFilings">
                    <Search :size="16" aria-hidden="true" />{{ searching ? '检索中…' : '检索' }}
                  </button>
                </div>
                <small v-if="searchError" class="danger-text" role="alert">{{ searchError }}</small>
              </div>
              <label class="field wide">
                <span>关联备案</span>
                <select v-model="fields.filingId" required>
                  <option disabled value="">请选择备案记录</option>
                  <option v-if="savedFiling && !filingOptions.some(({ id }) => id === savedFiling?.id)"
                    :value="savedFiling.id">{{ savedFiling.label }}</option>
                  <option v-for="filing in filingOptions" :key="filing.id" :value="filing.id">
                    {{ filing.number }} · {{ filing.systemName }} · {{ filing.organizationName }}
                  </option>
                </select>
                <small v-if="!searching && !filingOptions.length">暂无符合条件的备案记录。</small>
              </label>
            </template>
          </fieldset>

          <p v-if="formError" class="form-message error-message" role="alert">{{ formError }}</p>
          <div class="form-actions">
            <RouterLink class="button neutral" :to="returnTo">取消</RouterLink>
            <button v-if="stale" class="button neutral" type="button" @click="load">重新加载记录</button>
            <button class="button primary" type="submit" :disabled="saving || stale">保存草稿</button>
          </div>
        </form>
      </AsyncState>
    </div>
  </section>
</template>

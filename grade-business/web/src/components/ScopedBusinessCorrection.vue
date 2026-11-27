<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, errorText, isAbort, ApiError } from '../lib/api'
import { levels, risks, type CaseDetail } from '../lib/domain'
import {
  displayFilingValue, filingFieldKeys, filingFields, proposedChanges, type FilingField, type FilingValues,
} from '../lib/filingApplications'
import type { RemediationRisk } from '../lib/remediations'
import { scopedBusinessPath, type BusinessAudience, type EditableBusinessRecord } from '../lib/scopedBusiness'

const props = defineProps<{
  record: EditableBusinessRecord
  query: string
  audience: BusinessAudience
  busy: boolean
  stale: boolean
}>()
const emit = defineEmits<{ save: [body: object]; cancel: [] }>()
const fields = reactive({
  number: '', systemName: '', organizationName: '', assessmentOrg: '', level: '', reason: '',
  reporter: '', contact: '', plannedStart: '', plannedEnd: '', plan: '', deadline: '',
})
const original = ref<FilingValues>()
const editableFields = ref<FilingField[]>([])
const entries = ref<Array<RemediationRisk & { key: number }>>([])
const loading = ref(false)
const failure = ref('')
const validation = ref('')
const operator = computed(() => props.audience === 'operator')
const disabled = computed(() => props.busy || props.stale)
let request: AbortController | undefined
let nextKey = 0

/**
 * 用途 把已有风险或空白风险添加到整改规划表单。
 * 参数 risk 为可选的已保存风险。
 * 返回值 无。
 */
function addRisk(risk?: RemediationRisk) {
  entries.value.push({
    ...(risk ?? { risk: '', description: '', measure: '', responsibility: '', deadline: '' }),
    key: ++nextKey,
  })
}

/**
 * 用途 回填可补正业务在读取时保存的表单值。
 * 参数 无，当前业务记录通过组件属性传入。
 * 返回值 变更申请原值的加载任务，其余业务同步完成。
 */
function populate() {
  const record = props.record
  if (record.resource === 'filings') {
    const { number, systemName, organizationName, assessmentOrg, level } = record.detail
    Object.assign(fields, { number, systemName, organizationName, assessmentOrg, level: level ?? '' })
    return
  }
  if (record.resource === 'implementations') {
    const { reporter, contact, plannedStart, plannedEnd, plan } = record.detail
    Object.assign(fields, { reporter, contact: contact ?? '', plannedStart, plannedEnd, plan })
    return
  }
  if (record.resource === 'remediations') {
    fields.deadline = record.detail.deadline
    entries.value = []
    record.detail.risks.forEach(addRisk)
    return
  }
  fields.reason = record.detail.reason
  if (record.detail.type === 'change') return loadOriginal()
}

/**
 * 用途 从备案和申请快照确定可编辑字段及其原值。
 * 参数 无，通过当前申请读取相关备案并保留申请中已存的变更。
 * 返回值 备案原值加载任务。
 */
async function loadOriginal() {
  const record = props.record
  if (record.resource !== 'filing-applications') return
  const application = record.detail
  request?.abort()
  const next = new AbortController()
  request = next
  loading.value = true
  failure.value = ''
  try {
    const path = scopedBusinessPath(props.audience, 'filings', application.filingId) + props.query
    const source = await api<CaseDetail>(path, { signal: next.signal })
    if (next.signal.aborted) return
    useFilingValues(source)
  } catch (error) {
    if (next.signal.aborted || isAbort(error)) return
    if (error instanceof ApiError && error.status === 404 && application.changes?.length) {
      useFilingValues()
    } else {
      failure.value = errorText(error)
    }
  } finally {
    if (request === next) loading.value = false
  }
}

/**
 * 用途 把变更表单回填到申请保存时的字段快照。
 * 参数 source 为当前可访问的备案，缺失时仅使用申请自身的变更字段。
 * 返回值 无。
 */
function useFilingValues(source?: CaseDetail) {
  if (props.record.resource !== 'filing-applications') return
  const changes = props.record.detail.changes ?? []
  const before: FilingValues = {
    systemName: source?.systemName ?? '', organizationName: source?.organizationName ?? '',
    assessmentOrg: source?.assessmentOrg ?? '', level: source?.level ?? '',
  }
  Object.assign(fields, before)
  for (const change of changes) {
    before[change.field] = change.before
    fields[change.field] = change.after
  }
  original.value = before
  const candidates = source ? filingFieldKeys : changes.map(change => change.field)
  editableFields.value = operator.value ? candidates.filter(field => field !== 'organizationName') : candidates
}

/**
 * 用途 组装备案变更或注销的补正请求。
 * 参数 type 为申请类型，expectedVersion 为进入编辑时的版本。
 * 返回值 仅含理由、版本和实际变更的请求，无效表单返回空。
 */
function applicationBody(type: 'change' | 'cancel', expectedVersion: number) {
  const reason = fields.reason.trim()
  if (!reason) return
  if (type === 'cancel') return { reason, expectedVersion }
  if (!original.value) return
  const changes = proposedChanges(original.value, fields)
  const values = Object.values(changes)
  if (values.length && values.every(Boolean)) return { reason, expectedVersion, changes }
}

/**
 * 用途 提取整改规划补正所需的期限和风险字段。
 * 参数 reportId 为当前规划已关联的测评报告，expectedVersion 为当前版本。
 * 返回值 去除界面键和多余空白后的请求，无效表单返回空。
 */
function remediationBody(reportId: string, expectedVersion: number) {
  const deadline = fields.deadline
  const risks = entries.value.map(({ risk, description, responsibility, measure, deadline }) => ({
    risk, description: description.trim(), responsibility: responsibility.trim(), measure: measure.trim(), deadline,
  }))
  if (!deadline || !risks.length || risks.some(item => !item.risk || !item.description
    || !item.measure || !item.responsibility || !item.deadline || item.deadline > deadline)) return
  return { reportId, deadline, risks, expectedVersion }
}

/**
 * 用途 按当前业务的编辑接口生成经本地校验的请求。
 * 参数 record 为当前编辑的业务记录及版本。
 * 返回值 对应业务的请求体，校验失败时为空。
 */
function correctionBody(record: EditableBusinessRecord) {
  const expectedVersion = record.detail.version
  if (record.resource === 'filing-applications') return applicationBody(record.detail.type, expectedVersion)
  if (record.resource === 'remediations') return remediationBody(record.detail.reportId, expectedVersion)
  if (record.resource === 'filings') {
    const number = fields.number.trim()
    const systemName = fields.systemName.trim()
    const organizationName = fields.organizationName.trim()
    const level = fields.level
    const assessmentOrg = fields.assessmentOrg.trim()
    if (number && systemName && organizationName && assessmentOrg && level) return {
      number, systemName, level, assessmentOrg, expectedVersion,
      ...(!operator.value ? { organizationName } : {}),
    }
    return
  }
  const reporter = fields.reporter.trim()
  const plan = fields.plan.trim()
  const { plannedStart, plannedEnd } = fields
  if (reporter && plan && plannedStart && plannedEnd && plannedEnd >= plannedStart) return {
    filingId: record.detail.filingId, type: record.detail.type, assessmentOrg: record.detail.assessmentOrg,
    reporter, contact: fields.contact.trim(), plannedStart, plannedEnd, plan, expectedVersion,
  }
}

/**
 * 用途 校验表单并将可提交的补正请求交给详情页。
 * 参数 无，使用当前表单和记录版本。
 * 返回值 无。
 */
function save() {
  if (disabled.value || loading.value || failure.value) return
  const body = correctionBody(props.record)
  if (body) {
    validation.value = ''
    emit('save', body)
    return
  }
  validation.value = props.record.resource === 'filing-applications' && props.record.detail.type === 'change'
    ? '请填写申请理由和至少一项有效变更。' : '请完整填写补正内容，并核对日期。'
}

watch(() => props.record, populate, { immediate: true })
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="panel form-panel" aria-labelledby="correction-form-title">
    <h2 id="correction-form-title">编辑补正内容</h2>
    <AsyncState :loading="loading" :error="failure" @retry="loadOriginal">
      <form @submit.prevent="save">
        <fieldset class="form-grid" :disabled="disabled">
          <template v-if="record.resource === 'filings'">
            <label class="field"><span>备案编号</span><input v-model="fields.number" required maxlength="80"></label>
            <label class="field"><span>系统名称</span><input v-model="fields.systemName" required maxlength="200"></label>
            <label v-if="!operator" class="field"><span>所属单位</span><input v-model="fields.organizationName" required maxlength="200"></label>
            <label v-if="operator && record.detail.status === 'draft'" class="field"><span>测评机构</span>
              <input v-model="fields.assessmentOrg" required maxlength="200">
            </label>
            <label class="field"><span>定级</span>
              <select v-model="fields.level" required>
                <option v-for="(label, key) in levels" :key="key" :value="key">{{ label }}</option>
              </select>
            </label>
          </template>

          <template v-else-if="record.resource === 'filing-applications'">
            <label class="field wide"><span>申请理由</span>
              <textarea v-model="fields.reason" rows="4" maxlength="1000" required />
            </label>
            <label v-for="field in editableFields" :key="field" class="field">
              <span>{{ filingFields[field] }}</span>
              <small v-if="original" class="muted">原值 {{ displayFilingValue(field, original[field]) }}</small>
              <select v-if="field === 'level'" v-model="fields[field]" required>
                <option v-for="(label, key) in levels" :key="key" :value="key">{{ label }}</option>
              </select>
              <input v-else v-model="fields[field]" maxlength="200" required>
            </label>
          </template>

          <template v-else-if="record.resource === 'implementations'">
            <label class="field"><span>报备人员</span><input v-model="fields.reporter" required maxlength="120"></label>
            <label class="field"><span>联系电话</span><input v-model="fields.contact" type="tel" maxlength="60"></label>
            <label class="field"><span>计划开始</span><input v-model="fields.plannedStart" type="date" required></label>
            <label class="field"><span>计划完成</span>
              <input v-model="fields.plannedEnd" type="date" required :min="fields.plannedStart || undefined">
            </label>
            <label class="field wide"><span>实施计划与范围</span>
              <textarea v-model="fields.plan" rows="5" maxlength="5000" required />
            </label>
          </template>

          <template v-else>
            <label class="field"><span>规划整改期限</span><input v-model="fields.deadline" type="date" required></label>
            <div class="risk-heading wide">
              <h3>风险项与整改措施</h3>
              <button class="button neutral small" type="button" :disabled="entries.length >= 50"
                @click="addRisk()">添加风险项</button>
            </div>
            <fieldset v-for="(entry, index) in entries" :key="entry.key" class="wide risk-form form-grid">
              <div class="risk-heading wide">
                <h4>风险项 {{ index + 1 }}</h4>
                <button class="text-action" type="button" :disabled="entries.length === 1"
                  :aria-label="'移除风险项 ' + (index + 1)" @click="entries.splice(index, 1)">移除</button>
              </div>
              <label class="field"><span>风险级别</span>
                <select v-model="entry.risk" required>
                  <option value="" disabled>请选择</option>
                  <option v-for="(label, value) in risks" :key="value" :value="value">{{ label }}</option>
                </select>
              </label>
              <label class="field"><span>风险项期限</span>
                <input v-model="entry.deadline" type="date" required :max="fields.deadline || undefined">
              </label>
              <label class="field"><span>风险说明</span><input v-model="entry.description" maxlength="300" required></label>
              <label class="field"><span>责任人或部门</span>
                <input v-model="entry.responsibility" maxlength="160" required>
              </label>
              <label class="field wide"><span>整改措施</span>
                <textarea v-model="entry.measure" maxlength="600" rows="3" required />
              </label>
            </fieldset>
          </template>
        </fieldset>
        <p v-if="validation" class="form-message error-message" role="alert">{{ validation }}</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="busy" @click="emit('cancel')">取消编辑</button>
          <button class="button primary" type="submit" :disabled="disabled">{{ busy ? '保存中…' : '保存补正' }}</button>
        </div>
      </form>
    </AsyncState>
    <button v-if="loading || failure" class="button neutral small" type="button" @click="emit('cancel')">取消编辑</button>
  </section>
</template>

<style scoped>
.risk-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.risk-form { padding: 14px; border: 1px solid var(--line); border-radius: 6px; }
</style>

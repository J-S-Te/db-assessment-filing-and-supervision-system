<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref } from 'vue'
import { Plus } from '@lucide/vue'
import { api, ApiError, errorText, params } from '../lib/api'
import { risks } from '../lib/domain'
import {
  hazardDateError, hazardDay, hazardRegistrations, hazardSources,
  type HazardFilingOption, type HazardLedger, type HazardMutation, type HazardRegistration,
} from '../lib/hazards'

const props = defineProps<{ ledger: HazardLedger }>()
const emit = defineEmits<{ saved: [result: HazardMutation] }>()
const dialog = ref<HTMLDialogElement>()
const today = ref('')
const keyword = ref('')
const filings = ref<HazardFilingOption[]>([])
const finding = ref(false)
const saving = ref(false)
const filingError = ref('')
const failure = ref('')
const form = reactive<HazardRegistration>({
  ledger: props.ledger, filingId: '', title: '', severity: 'medium', source: 'manual',
  discoveredOn: '', deadline: '', description: '', measure: '', responsibility: '',
})
const selected = computed(() => filings.value.find(item => item.id === form.filingId))
const major = computed(() => props.ledger === 'major_risk')
let lookupRequest: AbortController | undefined
let saveRequest: AbortController | undefined

/**
 * 用途 查询可用于登记的系统备案并保留当前已选项。
 * 参数 无，使用备案关键字输入。
 * 返回值 备案选项读取任务。
 */
async function findFilings() {
  if (saving.value) return
  lookupRequest?.abort()
  const request = new AbortController()
  lookupRequest = request
  finding.value = true
  filingError.value = ''
  try {
    const result = await api<{ items: HazardFilingOption[] }>(
      '/hazards/filing-options' + params({ q: keyword.value.trim() }), { signal: request.signal },
    )
    if (lookupRequest !== request || request.signal.aborted) return
    const current = selected.value
    filings.value = current && !result.items.some(item => item.id === current.id)
      ? [current, ...result.items] : result.items
  } catch (error) {
    if (!request.signal.aborted) filingError.value = errorText(error)
  } finally {
    if (lookupRequest === request) {
      lookupRequest = undefined
      finding.value = false
    }
  }
}

/**
 * 用途 打开当前台账的登记表单并读取备案选项。
 * 参数 无，台账类型由打开表单的页面确定。
 * 返回值 备案选项读取任务。
 */
function open() {
  today.value = hazardDay()
  keyword.value = failure.value = filingError.value = ''
  filings.value = []
  Object.assign(form, {
    ledger: props.ledger, filingId: '', title: '', severity: major.value ? 'high' : 'medium',
    source: 'manual', discoveredOn: today.value, deadline: '', description: '', measure: '',
    responsibility: '',
  } satisfies HazardRegistration)
  dialog.value?.showModal()
  return findFilings()
}

/**
 * 用途 校验登记字段并创建由监管派发的隐患。
 * 参数 无，数据来自当前登记表单。
 * 返回值 业务保存任务，成功后交由列表打开登记详情。
 */
async function save() {
  if (saving.value || !selected.value) return
  const body = {
    ...form, title: form.title.trim(), description: form.description.trim(),
    measure: form.measure.trim(), responsibility: form.responsibility.trim(),
  }
  failure.value = hazardDateError(body.discoveredOn, body.deadline, today.value)
  if (failure.value) return
  if (!body.title || !body.description || !body.measure || !body.responsibility) {
    failure.value = '请填写名称、责任方、描述与影响及整改措施。'
    return
  }
  const request = new AbortController()
  saveRequest = request
  saving.value = true
  try {
    const result = await api<HazardMutation>('/hazards', { method: 'POST', body, signal: request.signal })
    if (request.signal.aborted) return
    dialog.value?.close()
    emit('saved', result)
  } catch (error) {
    if (request.signal.aborted) return
    failure.value = error instanceof ApiError && error.code === 'duplicate_registration'
      ? '该备案下已有同名登记，请查看台账中的已有记录。' : errorText(error)
  } finally {
    if (saveRequest === request) {
      saveRequest = undefined
      saving.value = false
    }
  }
}

/**
 * 用途 关闭登记时停止无效的备案请求。
 * 参数 无。
 * 返回值 无。
 */
function stopLookup() {
  lookupRequest?.abort()
  lookupRequest = undefined
  finding.value = false
}

onScopeDispose(() => {
  stopLookup()
  saveRequest?.abort()
})
</script>

<template>
  <button class="button primary" type="button" @click="open">
    <Plus :size="16" aria-hidden="true" />{{ hazardRegistrations[ledger] }}
  </button>
  <Teleport to="body">
    <dialog ref="dialog" class="decision-dialog registration-dialog" aria-labelledby="hazard-registration-title"
      @cancel.prevent="!saving && dialog?.close()" @close="stopLookup">
      <form @submit.prevent="save">
        <h2 id="hazard-registration-title">{{ hazardRegistrations[ledger] }}</h2>
        <fieldset class="form-grid" :disabled="saving">
          <div class="wide filing-lookup">
            <label class="field">
              <span>备案关键词</span>
              <input v-model="keyword" type="search" maxlength="120" placeholder="备案编号、系统或单位"
                @keydown.enter.prevent="findFilings">
            </label>
            <button class="button neutral" type="button" :disabled="finding" @click="findFilings">查找备案</button>
          </div>
          <label class="field wide">
            <span>系统备案</span>
            <select v-model="form.filingId" required :disabled="finding && !filings.length">
              <option value="" disabled>请选择已生效的系统备案</option>
              <option v-for="item in filings" :key="item.id" :value="item.id">
                {{ item.systemName }} · {{ item.number }} · {{ item.organizationName }}
              </option>
            </select>
            <small v-if="selected" class="muted">测评机构 {{ selected.assessmentOrg }}</small>
            <small v-else-if="!finding && !filingError && !filings.length" class="muted">未找到符合条件的备案。</small>
          </label>
          <p v-if="filingError" class="wide form-message error-message" role="alert">{{ filingError }}</p>
          <label class="field">
            <span>{{ major ? '风险名称' : '漏洞名称' }}</span>
            <input v-model="form.title" maxlength="160" required>
          </label>
          <label class="field">
            <span>{{ major ? '处置人或部门' : '责任人或部门' }}</span>
            <input v-model="form.responsibility" maxlength="160" required>
          </label>
          <label class="field">
            <span>风险等级</span>
            <select v-model="form.severity" required>
              <option v-for="(label, value) in risks" :key="value" :value="value">{{ label }}</option>
            </select>
          </label>
          <label class="field">
            <span>来源</span>
            <select v-model="form.source" required>
              <option v-for="(label, value) in hazardSources" :key="value" :value="value">{{ label }}</option>
            </select>
          </label>
          <label class="field">
            <span>发现日期</span>
            <input v-model="form.discoveredOn" type="date" :max="today" required>
          </label>
          <label class="field">
            <span>整改期限</span>
            <input v-model="form.deadline" type="date" :min="form.discoveredOn" required>
          </label>
          <label class="field wide">
            <span>描述与影响</span>
            <textarea v-model="form.description" rows="3" maxlength="300" required />
          </label>
          <label class="field wide">
            <span>整改措施</span>
            <textarea v-model="form.measure" rows="3" maxlength="600" required />
          </label>
        </fieldset>
        <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="saving" @click="dialog?.close()">取消</button>
          <button type="submit" class="button primary" :disabled="saving || !selected">
            {{ saving ? '保存中…' : '保存登记' }}
          </button>
        </div>
      </form>
    </dialog>
  </Teleport>
</template>

<style scoped>
.registration-dialog {
  width: min(680px, calc(100vw - 32px));
  max-height: calc(100dvh - 32px);
}
.filing-lookup {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: end;
  gap: 8px;
}
</style>

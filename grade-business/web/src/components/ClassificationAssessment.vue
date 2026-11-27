<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { api, errorText, isAbort } from '../lib/api'
import { impactDamages, impactSubjects, type ClassificationAssessment, type ClassificationInput,
  type Impact } from '../lib/classification'

const props = defineProps<{
  initial?: ClassificationAssessment
  expanded?: boolean
  disabled?: boolean
}>()
const emit = defineEmits<{
  change: [input?: ClassificationInput]
  adopt: [level: string]
}>()
const dimensions = { information: '业务信息安全', service: '系统服务安全' } as const
const input = ref<ClassificationInput>({ information: [], service: [], basis: '' })
const result = ref<ClassificationAssessment>()
const busy = ref(false)
const error = ref('')
let request: AbortController | undefined

watch(() => props.initial, value => {
  request?.abort()
  input.value = {
    information: [...(value?.information ?? [])], service: [...(value?.service ?? [])],
    basis: value?.basis ?? '', competentAuthority: value?.competentAuthority ?? '',
    filingAuthority: value?.filingAuthority ?? '',
  }
  result.value = value
  busy.value = false
  error.value = ''
}, { immediate: true })
onBeforeUnmount(() => request?.abort())

/**
 * 用途 更新某一个定级维度的损害并使旧的建议过期。
 * 参数 dimension、subject、event 为维度、客体及下拉选择事件。
 * 返回值 无。
 */
function changeImpact(dimension: keyof typeof dimensions, subject: Impact['subject'], event: Event) {
  const damage = (event.target as HTMLSelectElement).value as Impact['damage'] | ''
  const next = input.value[dimension].filter(item => item.subject !== subject)
  if (damage) next.push({ subject, damage })
  input.value[dimension] = next
  notifyChange()
}

/**
 * 用途 向草稿提交表单传入当前完整的事实快照并取消已过期的计算请求。
 * 参数 无。
 * 返回值 无。
 */
function notifyChange() {
  request?.abort()
  busy.value = false
  result.value = undefined
  error.value = ''
  const source = input.value
  const hasInput = source.information.length || source.service.length || source.basis.trim()
    || source.competentAuthority?.trim() || source.filingAuthority?.trim()
  emit('change', hasInput ? { ...source, information: [...source.information], service: [...source.service] } : undefined)
}

/**
 * 用途 通过服务端统一矩阵计算当前事实的初步建议。
 * 参数 无。
 * 返回值 预览请求任务，失败时显示业务原因。
 */
async function preview() {
  request?.abort()
  const current = new AbortController()
  request = current
  busy.value = true
  error.value = ''
  try {
    const next = await api<ClassificationAssessment>('/filings/classification-preview', {
      method: 'POST', body: input.value, signal: current.signal,
    })
    if (!current.signal.aborted) result.value = next
  } catch (failure) {
    if (!isAbort(failure)) error.value = errorText(failure)
  } finally {
    if (request === current) busy.value = false
  }
}
</script>

<template>
  <details class="wide classification-panel" :open="expanded || Boolean(initial)">
    <summary>系统定级自评</summary>
    <p class="muted">根据已核实的影响事实计算初步等级，正式申报等级由经办人确认。</p>
    <fieldset v-for="(title, dimension) in dimensions" :key="dimension" :disabled="disabled" class="classification-row">
      <legend>{{ title }}</legend>
      <label v-for="(subjectLabel, subject) in impactSubjects" :key="subject" class="field">
        <span>{{ subjectLabel }}</span>
        <select :value="input[dimension].find(item => item.subject === subject)?.damage ?? ''"
          @change="changeImpact(dimension, subject, $event)">
          <option value="">未选择</option>
          <option v-for="(label, damage) in impactDamages" :key="damage" :value="damage">{{ label }}</option>
        </select>
      </label>
    </fieldset>
    <label class="field">
      <span>研判事实及依据</span>
      <textarea v-model="input.basis" rows="3" maxlength="2000" :disabled="disabled" @input="notifyChange" />
    </label>
    <div class="classification-row">
      <label class="field"><span>主管部门（选填）</span>
        <input v-model="input.competentAuthority" maxlength="200" :disabled="disabled" @input="notifyChange">
      </label>
      <label class="field"><span>备案机关（选填）</span>
        <input v-model="input.filingAuthority" maxlength="200" :disabled="disabled" @input="notifyChange">
      </label>
    </div>
    <div class="classification-actions">
      <button class="button neutral" type="button" :disabled="disabled || busy" @click="preview">
        {{ busy ? '计算中…' : '计算初步等级' }}
      </button>
      <button v-if="result" class="button neutral" type="button" :disabled="disabled"
        @click="emit('adopt', String(result.suggestedLevel))">带入申报等级</button>
    </div>
    <p v-if="error" class="form-message error-message" role="alert">{{ error }}</p>
    <p v-if="result" class="form-message" role="status">
      业务信息 {{ result.informationLevel }} 级，系统服务 {{ result.serviceLevel }} 级，
      初步建议 {{ result.suggestedLevel }} 级 · {{ result.standard }}
      <span v-if="result.expertReview"> · 需组织专家评审</span>
    </p>
  </details>
</template>

<style scoped>
.classification-panel { border: 1px solid var(--c-border, #dce4ec); padding: .8rem 1rem; border-radius: .5rem; }
.classification-panel summary { cursor: pointer; font-weight: 600; }
.classification-row { display: flex; flex-wrap: wrap; gap: .8rem; margin: .8rem 0; border: 0; padding: 0; }
.classification-row > *:not(legend) { flex: 1 1 9rem; }
.classification-actions { display: flex; flex-wrap: wrap; gap: .6rem; margin-top: .8rem; }
</style>

<script setup lang="ts">
import { computed, onScopeDispose, ref } from 'vue'
import { api, errorText } from '../lib/api'
import type { ReviewAction } from '../lib/domain'

const props = defineProps<{ path: string; suffix: string; version: number; disabled?: boolean }>()
const emit = defineEmits<{ saved: []; reload: [] }>()
const dialog = ref<HTMLDialogElement>()
const decision = ref<ReviewAction>('approve')
const selectedVersion = ref<number>()
const comment = ref('')
const failure = ref('')
const saving = ref(false)
const changed = computed(() => selectedVersion.value !== props.version)
const label = computed(() => decision.value === 'approve' ? '机构初审通过' : '初审退回补正')
let request: AbortController | undefined

/**
 * 用途 使用当前看到的备案版本打开机构初审确认。
 * 参数 action 为通过或退回动作。
 * 返回值 无。
 */
function confirm(action: ReviewAction) {
  if (saving.value || props.disabled) return
  decision.value = action
  selectedVersion.value = props.version
  comment.value = failure.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 提交机构初审决定及实际审核意见。
 * 参数 无，动作和版本来自已确认的当前事项。
 * 返回值 办理和刷新任务。
 */
async function save() {
  const content = comment.value.trim()
  if (saving.value || changed.value || (decision.value === 'return_for_correction' && !content)) return
  const next = new AbortController()
  request = next
  saving.value = true
  failure.value = ''
  try {
    await api(props.path + '/review-actions' + props.suffix, {
      method: 'POST', signal: next.signal,
      body: { decision: decision.value, expectedVersion: selectedVersion.value, ...(content ? { comment: content } : {}) },
    })
    if (!next.signal.aborted) {
      dialog.value?.close()
      emit('saved')
    }
  } catch (error) {
    if (!next.signal.aborted) failure.value = errorText(error)
  } finally {
    if (request === next) saving.value = false
  }
}

/**
 * 用途 结束当前确认并重新读取实际审核环节和记录版本。
 * 参数 无。
 * 返回值 无。
 */
function reload() {
  dialog.value?.close()
  emit('reload')
}

onScopeDispose(() => request?.abort())
</script>

<template>
  <div class="page-actions">
    <button type="button" class="button neutral" :disabled="disabled || saving"
      @click="confirm('return_for_correction')">退回补正</button>
    <button type="button" class="button primary" :disabled="disabled || saving"
      @click="confirm('approve')">初审通过</button>
  </div>
  <dialog ref="dialog" class="decision-dialog" aria-labelledby="institution-filing-review"
    @cancel.prevent="!saving && dialog?.close()">
    <form @submit.prevent="save">
      <h2 id="institution-filing-review">{{ label }}</h2>
      <p v-if="decision === 'approve'">通过后转入监管终审。</p>
      <label class="field">
        <span>{{ decision === 'approve' ? '初审意见（选填）' : '退回原因' }}</span>
        <textarea v-model="comment" rows="4" maxlength="1000" :required="decision !== 'approve'" :disabled="saving || changed" />
      </label>
      <p v-if="changed || failure" class="form-message error-message" role="alert">
        {{ changed ? '当前记录已更新，请刷新后重新确认。' : failure }}
      </p>
      <div class="form-actions">
        <button class="button neutral" type="button" :disabled="saving" @click="dialog?.close()">取消</button>
        <button v-if="changed || failure" class="button neutral" type="button" :disabled="saving" @click="reload">刷新详情</button>
        <button class="button primary" type="submit" :disabled="saving || changed || disabled">
          {{ saving ? '处理中…' : '确认' + label }}
        </button>
      </div>
    </form>
  </dialog>
</template>

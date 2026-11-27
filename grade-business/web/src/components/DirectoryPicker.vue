<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { api, errorText, isAbort } from '../lib/api'
import {
  directoryOptionPath, directoryOptionValue, type DirectoryKind, type DirectoryOption,
} from '../lib/directory'

const props = defineProps<{
  kind: DirectoryKind
  label: string
  institution?: string
  initialLabel?: string
  disabled?: boolean
  required?: boolean
  useId?: boolean
}>()
const selection = defineModel<string>({ required: true })
const emit = defineEmits<{ picked: [item: DirectoryOption | undefined] }>()
const filter = ref('')
const choices = shallowRef<DirectoryOption[]>([])
const saved = shallowRef<DirectoryOption>()
const error = ref('')
const loading = ref(false)
let active: AbortController | undefined

/**
 * 用途 将候选转换成当前表单需要的目录标识或稳定范围。
 * 参数 item 为服务端确认的候选。
 * 返回值 可安全保存的标识字符串。
 */
function optionValue(item: DirectoryOption) {
  return props.useId ? item.id ?? '' : directoryOptionValue(props.kind, item)
}

const available = computed(() => choices.value.filter(item => item.usable && optionValue(item)))
const displayed = computed(() => saved.value && optionValue(saved.value) === selection.value
  && !available.value.some(item => optionValue(item) === selection.value)
  ? [saved.value, ...available.value] : available.value)
const hasSelection = computed(() => displayed.value.some(item => optionValue(item) === selection.value))

/**
 * 用途 请求与当前选择器同范围的少量可用名录项。
 * 参数 无，类别、机构和关键字来自当前表单。
 * 返回值 可取消的候选查询任务。
 */
async function search() {
  active?.abort()
  const request = new AbortController()
  active = request
  error.value = ''
  if (props.kind === 'person' && !props.institution) return
  loading.value = true
  try {
    const data = await api<{ items: DirectoryOption[] }>(
      directoryOptionPath(props.kind, props.institution ?? '', filter.value),
      { signal: request.signal },
    )
    if (active === request) choices.value = data.items
  } catch (cause) {
    if (active === request && !isAbort(cause)) error.value = errorText(cause)
  } finally {
    if (active === request) loading.value = false
  }
}

/**
 * 用途 记录选中的真实名录项并通知业务表单。
 * 参数 event 为原生列表选择事件。
 * 返回值 无。
 */
function choose(event: Event) {
  const value = (event.currentTarget as HTMLSelectElement).value
  saved.value = available.value.find(item => optionValue(item) === value)
  selection.value = value
  emit('picked', saved.value)
}

watch(() => [props.kind, props.institution, props.useId], () => {
  saved.value = undefined
  choices.value = []
  filter.value = ''
  loading.value = false
  void search()
}, { immediate: true })
onScopeDispose(() => active?.abort())
</script>

<template>
  <div class="field directory-picker">
    <label>
      <span>{{ label }}</span>
      <select :value="selection" :required="required" :disabled="disabled" @change="choose">
        <option value="">{{ loading ? '正在读取名录…' : '请选择' + label }}</option>
        <option v-if="selection && !hasSelection" :value="selection" disabled>{{ initialLabel || '原选项' }} · 原记录</option>
        <option v-for="item in displayed" :key="optionValue(item)" :value="optionValue(item)">
          {{ item.name }}{{ kind === 'person' && item.code ? ' · ' + item.code : '' }}
        </option>
      </select>
    </label>
    <div class="directory-search">
      <input v-model="filter" type="search" maxlength="120" :aria-label="'检索' + label"
        :disabled="disabled" @keydown.enter.prevent="search">
      <button class="button neutral small" type="button" :disabled="disabled || loading" @click="search">检索</button>
    </div>
    <small v-if="error" class="form-message error-message" role="alert">{{ error }}</small>
    <small v-else-if="!loading && !available.length" class="muted">暂无可选项</small>
  </div>
</template>

<style scoped>
.directory-picker > label { display: grid; gap: 5px; }
.directory-search { display: flex; gap: 6px; align-items: center; }
.directory-search input { min-width: 0; flex: 1; }
</style>

<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { api, isAbort, params } from '../lib/api'
import { businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'
import { describeDeadline, reviewAssignee } from '../lib/workbenchItems'

const props = defineProps<{
  audience: BusinessAudience
  scope: string
  resource: string
  id: string
  version?: number
}>()
type CurrentDeadline = { deadline?: string; label?: string; waitingFor?: string }
const endpoint = computed(() => props.id && props.scope
  ? '/' + props.audience + '/workbench/deadlines/' + encodeURIComponent(props.resource)
    + '/' + encodeURIComponent(props.id) + params({ [businessScopes[props.audience].key]: props.scope })
  : '')
const result = shallowRef<CurrentDeadline>()
const failure = ref(false)
let request: AbortController | undefined

/**
 * 用途 读取同一单据的当前环节、审核方与截止日期。
 * 参数 无，单据和归属来自已加载的本端详情。
 * 返回值 时限更新任务。
 */
async function load() {
  request?.abort()
  if (!endpoint.value) return
  const current = new AbortController()
  request = current
  failure.value = false
  try {
    const found = await api<CurrentDeadline>(endpoint.value, { signal: current.signal })
    if (!current.signal.aborted) result.value = found
  } catch (error) {
    if (!current.signal.aborted && !isAbort(error)) failure.value = true
  } finally {
    if (request === current) request = undefined
  }
}

watch(endpoint, () => {
  result.value = undefined
  void load()
}, { immediate: true })
watch(() => props.version, () => void load())
refreshBusinessOnFocus(() => { if (!request) void load() }, 30_000)
onScopeDispose(() => request?.abort())
</script>

<template>
  <p v-if="result?.deadline" class="form-message" aria-label="当前办理时限">
    <strong>{{ result.label }}</strong>
    <template v-if="result.waitingFor"> · {{ reviewAssignee(result.waitingFor) }}</template>
    · 截止 {{ result.deadline }}（{{ describeDeadline(result.deadline).text }}）
  </p>
  <p v-if="failure" class="form-message warning-message" role="status">
    暂时无法更新办理时限。
    <button type="button" class="text-action" @click="load()">重新读取</button>
  </p>
</template>

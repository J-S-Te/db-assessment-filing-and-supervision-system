<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { alertPath, alertTypes, type AlertItem } from '../lib/alerts'
import type { Page } from '../lib/domain'

const props = defineProps<{ disabled?: boolean }>()
const selected = defineModel<string>({ required: true })
const query = ref('')
const candidates = shallowRef<AlertItem[]>([])
const fallback = shallowRef<AlertItem>()
const options = computed(() => fallback.value ? [fallback.value, ...candidates.value] : candidates.value)
const loading = ref(false)
const failure = ref('')
let active: AbortController | undefined

/**
 * 用途 查询已有站内预警，并保留旧修订当前引用的有效选项。
 * 参数 无，使用监管员的搜索词和当前选择。
 * 返回值 候选项加载任务。
 */
async function searchAlerts() {
  active?.abort()
  const request = new AbortController()
  active = request
  loading.value = true
  failure.value = ''
  try {
    const result = await api<Page<AlertItem>>(alertPath + params({ q: query.value.trim(), limit: 20 }), {
      signal: request.signal,
    })
    if (request.signal.aborted) return
    candidates.value = result.items
    fallback.value = undefined
    if (selected.value && !result.items.some(item => item.id === selected.value)) {
      const current = await api<AlertItem>(alertPath + '/' + encodeURIComponent(selected.value), { signal: request.signal })
      if (!request.signal.aborted && selected.value === current.id) fallback.value = current
    }
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) loading.value = false
  }
}

void searchAlerts()
onScopeDispose(() => active?.abort())
</script>

<template>
  <div class="alert-picker">
    <div class="alert-search">
      <label class="field"><span>预警关键词</span><input v-model="query" type="search" maxlength="120"
        :disabled="props.disabled" @keydown.enter.prevent="searchAlerts"></label>
      <button class="button neutral" type="button" :disabled="props.disabled || loading" @click="searchAlerts">查找预警</button>
    </div>
    <p v-if="failure" class="error-message" role="alert">{{ failure }}</p>
    <label class="field"><span>关联站内预警（可选）</span>
      <select v-model="selected" :disabled="props.disabled || loading">
        <option value="">无关联预警</option>
        <option v-for="item in options" :key="item.id" :value="item.id">
          {{ item.systemName }} · {{ alertTypes[item.type] }} · {{ item.deadline }}
        </option>
      </select>
    </label>
  </div>
</template>

<style scoped>
.alert-picker { display: grid; gap: 8px; }
.alert-search { display: flex; flex-wrap: wrap; align-items: end; gap: 8px; }
.alert-search .field { flex: 1 1 150px; }
</style>

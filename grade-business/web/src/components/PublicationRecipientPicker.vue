<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { recipientLabels, type PublicationTarget } from '../lib/publicationDelivery'
import { publicationPath } from '../lib/publications'
import type { BusinessAudience } from '../lib/scopedBusiness'

const props = defineProps<{ disabled?: boolean }>()
const targets = defineModel<PublicationTarget[]>({ required: true })
const audience = ref<BusinessAudience>('operator')
const keyword = ref('')
const candidates = shallowRef<PublicationTarget[]>([])
const loading = ref(false)
const failure = ref('')
let active: AbortController | undefined

/**
 * 用途 从已登记单位或机构中查找可用于公告定向发布的收件对象。
 * 参数 无，使用选定的对象类型及搜索词。
 * 返回值 候选项查询任务。
 */
async function findRecipients() {
  active?.abort()
  const request = new AbortController()
  active = request
  candidates.value = []
  loading.value = true
  failure.value = ''
  try {
    const query = params({ audience: audience.value, q: keyword.value.trim() })
    const data = await api<{ items: PublicationTarget[] }>(publicationPath + '/recipients' + query, {
      signal: request.signal,
    })
    if (!request.signal.aborted) candidates.value = data.items
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) loading.value = false
  }
}

/**
 * 用途 查看候选对象是否已加入当前指定收件范围。
 * 参数 item 为候选类型及名称。
 * 返回值 已选择该对象时为真。
 */
function includesTarget(item: PublicationTarget) {
  return targets.value.some(target => target.audience === item.audience && target.recipient === item.recipient)
}

/**
 * 用途 从当前公告的定向发布对象中移除一项。
 * 参数 item 为需要移除的已选对象。
 * 返回值 无。
 */
function removeTarget(item: PublicationTarget) {
  targets.value = targets.value.filter(target => target.audience !== item.audience || target.recipient !== item.recipient)
}

watch(audience, findRecipients, { immediate: true })
onScopeDispose(() => active?.abort())
</script>

<template>
  <fieldset class="recipient-picker" :disabled="props.disabled">
    <legend>指定收件对象</legend>
    <ul v-if="targets.length" class="picked-recipients" aria-label="已选收件对象">
      <li v-for="item in targets" :key="item.audience + ':' + item.recipient">
        {{ recipientLabels[item.audience] }} · {{ item.displayName || item.recipient }}
        <button class="text-action" type="button" :aria-label="'移除 ' + (item.displayName || item.recipient)"
          @click="removeTarget(item)">×</button>
      </li>
    </ul>
    <p v-else class="muted">请选择至少一个收件对象。</p>
    <div class="recipient-search">
      <label class="field"><span>对象类型</span><select v-model="audience">
        <option v-for="(label, key) in recipientLabels" :key="key" :value="key">{{ label }}</option>
      </select></label>
      <label class="field recipient-query"><span>收件对象名称</span>
        <input v-model="keyword" type="search" maxlength="120" @keydown.enter.prevent="findRecipients">
      </label>
      <button class="button neutral" type="button" :disabled="loading" @click="findRecipients">查找对象</button>
    </div>
    <p v-if="loading" class="muted" role="status">正在查找收件对象…</p>
    <p v-else-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <p v-else-if="!candidates.length" class="muted">暂无符合条件的收件对象。</p>
    <ul v-else class="recipient-results" aria-label="候选收件对象">
      <li v-for="item in candidates" :key="item.audience + ':' + item.recipient">
        <span>{{ item.displayName || item.recipient }}</span>
        <button class="text-action" type="button" :disabled="includesTarget(item)"
          :aria-label="(includesTarget(item) ? '已选择 ' : '添加 ') + (item.displayName || item.recipient)"
          @click="targets = [...targets, item]">{{ includesTarget(item) ? '已选' : '添加' }}</button>
      </li>
    </ul>
    <p v-if="candidates.length === 30" class="muted">显示前 30 项，可用名称缩小查找范围。</p>
  </fieldset>
</template>

<style scoped>
.recipient-picker { min-width: 0; border: 1px solid var(--line); border-radius: 8px; padding: 10px; }
.recipient-picker legend { font-size: 13px; padding-inline: 4px; }
.recipient-picker p { margin: 6px 0; }
.recipient-search, .picked-recipients { display: flex; align-items: end; gap: 8px; flex-wrap: wrap; }
.recipient-query { flex: 1 1 150px; }
.picked-recipients, .recipient-results { list-style: none; margin: 0; padding: 0; }
.picked-recipients { margin-bottom: 10px; }
.picked-recipients li { border-radius: 5px; background: var(--c-info-bg); padding: 3px 7px; }
.recipient-results { max-height: 10rem; overflow: auto; margin-top: 6px; }
.recipient-results li { display: flex; justify-content: space-between; align-items: center; gap: 12px;
  padding: 5px 8px; border-bottom: 1px solid var(--line); overflow-wrap: anywhere; }
</style>

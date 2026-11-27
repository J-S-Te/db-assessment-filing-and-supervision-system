<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import PublicationAttachmentLinks from './PublicationAttachmentLinks.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { recipientLabels, type PublicationDeliveries } from '../lib/publicationDelivery'
import { publicationPath } from '../lib/publications'
import type { BusinessAudience } from '../lib/scopedBusiness'

const props = defineProps<{
  id: string; versions: number[]; historyLoading: boolean; historyNext: string; historyError: string;
}>()
const emit = defineEmits<{ moreVersions: [] }>()
const version = ref<number | ''>('')
const audience = ref<BusinessAudience | ''>('')
const keyword = ref('')
const page = shallowRef<PublicationDeliveries>()
const loading = ref(false)
const failure = ref('')
let active: AbortController | undefined
let applied = { audience: '', q: '' }

/**
 * 用途 读取指定发布版本的送达、阅读和签收回执。
 * 参数 before 为服务器返回的下一页游标，省略时返回第一页。
 * 返回值 回执读取任务。
 */
async function load(before = '') {
  active?.abort()
  const request = new AbortController()
  active = request
  failure.value = ''
  loading.value = true
  try {
    const path = publicationPath + '/' + encodeURIComponent(props.id) + '/deliveries'
    const requestedVersion = before ? page.value?.version ?? version.value : version.value
    const result = await api<PublicationDeliveries>(path + params({ version: requestedVersion, ...applied, before }), {
      signal: request.signal,
    })
    if (!request.signal.aborted) {
      const previous = before && page.value?.version === result.version ? page.value.items : []
      page.value = { ...result, items: [...previous, ...result.items] }
    }
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) loading.value = false
  }
}

/**
 * 用途 按当前收件类型或名称筛选版本回执。
 * 参数 无，使用正在编辑的筛选内容。
 * 返回值 首页回执读取任务。
 */
function search() {
  applied = { audience: audience.value, q: keyword.value.trim() }
  return load()
}

watch(version, () => { page.value = undefined; void search() }, { immediate: true })
onScopeDispose(() => active?.abort())
</script>

<template>
  <section class="delivery-panel" aria-label="送达回执">
    <form class="delivery-filters" @submit.prevent="search">
      <label class="field"><span>发布版本</span><select v-model="version">
        <option value="">最新发布版本</option>
        <option v-for="item in versions" :key="item" :value="item">版本 {{ item }}</option>
      </select></label>
      <label class="field"><span>收件对象类型</span><select v-model="audience">
        <option value="">全部类型</option>
        <option v-for="(label, key) in recipientLabels" :key="key" :value="key">{{ label }}</option>
      </select></label>
      <label class="field delivery-query"><span>收件对象名称</span>
        <input v-model="keyword" type="search" maxlength="120">
      </label>
      <button type="submit" class="button neutral" :disabled="loading">查询回执</button>
    </form>
    <p v-if="historyError" class="form-message warning-message" role="status">{{ historyError }}</p>
    <button v-if="historyNext || historyError" class="text-action" type="button" :disabled="historyLoading"
      @click="emit('moreVersions')">{{ historyError ? '重试加载发布版本' : '加载更早发布版本' }}</button>
    <div v-if="page" class="delivery-counts" aria-label="本版本回执统计">
      <span>发布版本 {{ page.version }}</span>
      <span>送达 <strong>{{ page.counts.delivered }}</strong></span>
      <span>已读 <strong>{{ page.counts.read }}</strong></span>
      <span>已签收 <strong>{{ page.counts.acknowledged }}</strong></span>
    </div>
    <PublicationAttachmentLinks v-if="page" :id="id" :version="page.version" :attachments="page.attachments" />
    <AsyncState :loading="loading" :error="failure" :empty="!page?.items.length"
      empty-text="暂无符合条件的送达回执。" @retry="load()">
      <ul class="delivery-list">
        <li v-for="item in page?.items" :key="item.audience + ':' + item.recipient">
          <div class="delivery-recipient"><strong>{{ item.displayName || item.recipient }}</strong>
            <small class="muted">{{ recipientLabels[item.audience] }}</small></div>
          <dl class="delivery-times">
            <div><dt>送达时间</dt><dd>{{ formatTime(item.deliveredAt) }}</dd></div>
            <div><dt>阅读时间</dt><dd>{{ item.readAt ? formatTime(item.readAt) : '未读' }}</dd></div>
            <div><dt>签收时间</dt><dd>{{ item.acknowledgedAt ? formatTime(item.acknowledgedAt) : '未签收' }}</dd></div>
          </dl>
        </li>
      </ul>
    </AsyncState>
    <button v-if="page?.nextBefore" class="button neutral small" type="button" :disabled="loading"
      @click="load(page.nextBefore)">加载更多回执</button>
  </section>
</template>

<style scoped>
.delivery-panel { display: grid; gap: 10px; min-width: 0; }
.delivery-panel > button { justify-self: start; }
.delivery-filters, .delivery-counts { display: flex; align-items: end; gap: 8px 12px; flex-wrap: wrap; }
.delivery-query { flex: 1 1 140px; }
.delivery-counts { background: var(--c-info-bg); border-radius: 6px; padding: 8px 10px; }
.delivery-list { list-style: none; margin: 0; padding: 0; }
.delivery-list li { display: grid; grid-template-columns: minmax(7rem, 1fr) minmax(0, 3fr); gap: 8px;
  border-bottom: 1px solid var(--line); padding: 8px 0; }
.delivery-recipient { overflow-wrap: anywhere; }
.delivery-recipient small { display: block; }
.delivery-times { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 8px; margin: 0; }
.delivery-times dt { color: var(--muted); font-size: 12px; }
.delivery-times dd { margin: 0; }
@media (max-width: 600px) {
  .delivery-list li { grid-template-columns: minmax(0, 1fr); }
  .delivery-times { grid-template-columns: repeat(auto-fit, minmax(6.3rem, 1fr)); }
}
</style>

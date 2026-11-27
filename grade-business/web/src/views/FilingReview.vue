<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { api, errorText, isAbort } from '../lib/api'
import type { Attachment, CaseDetail, Page } from '../lib/domain'
import FilingMaterialReview from '../components/FilingMaterialReview.vue'
import PrecheckPanel from '../components/PrecheckPanel.vue'

const props = defineProps<{ id: string }>()
const path = computed(() => '/filings/' + encodeURIComponent(props.id))
const record = shallowRef<CaseDetail>()
const attachments = shallowRef<Attachment[]>([])
const revision = computed(() => `${record.value?.version ?? ''}:${attachments.value.map(item => item.id).join(',')}`)
const failure = ref('')
let request: AbortController | undefined

/**
 * 用途 加载当前监管范围内的备案信息与材料清单。
 * 参数 无，备案标识来自独立审阅页面的路由。
 * 返回值 详情加载任务。
 */
async function load() {
  request?.abort()
  const current = new AbortController()
  request = current
  const scope = path.value
  failure.value = ''
  try {
    const [detail, page] = await Promise.all([
      api<CaseDetail>(scope, { signal: current.signal }),
      api<Page<Attachment>>(scope + '/attachments', { signal: current.signal }),
    ])
    if (request !== current) return
    record.value = detail
    attachments.value = page.items
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  }
}

watch(path, () => {
  record.value = undefined
  attachments.value = []
  void load()
}, { immediate: true })
onBeforeUnmount(() => request?.abort())
</script>

<template>
  <header class="review-page-heading">
    <div>
      <h1>材料审阅</h1>
      <p v-if="record" class="muted">{{ record.systemName }} · {{ record.organizationName }}</p>
    </div>
    <RouterLink :to="path" class="button neutral">返回备案详情</RouterLink>
  </header>
  <p v-if="failure" class="form-message error-message" role="alert">
    {{ failure }} <button type="button" class="text-action" @click="load">重试</button>
  </p>
  <p v-else-if="!record" class="hint-row" role="status">正在读取备案材料…</p>
  <PrecheckPanel v-if="record" :path="path" :revision="revision" v-slot="precheck">
    <p v-if="precheck.failure" class="form-message error-message" role="alert">
      {{ precheck.failure }} <button type="button" class="text-action" @click="precheck.refresh">重试</button>
    </p>
    <p v-else-if="precheck.job?.stale" class="form-message warning-message" role="status">
      材料或规则已更新，请返回备案详情重新预审。
    </p>
    <p v-else-if="precheck.loading || precheck.job?.status === 'queued' || precheck.job?.status === 'running'"
      class="hint-row" role="status">正在读取或等待预审结果…</p>
    <p v-else-if="!precheck.checks?.length" class="hint-row">暂无预审结果，可返回备案详情发起或重试预审。</p>
    <FilingMaterialReview :path="path" :revision="revision" :attachments="attachments"
      :checks="precheck.checks" :source="precheck.job?.checkSource" @refresh="precheck.refresh(); load()" />
  </PrecheckPanel>
</template>

<style scoped>
.review-page-heading { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: .8rem; }
.review-page-heading h1 { margin: 0; }
.review-page-heading p { margin: .35rem 0 0; overflow-wrap: anywhere; }
</style>

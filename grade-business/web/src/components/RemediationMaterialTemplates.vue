<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { Download } from '@lucide/vue'
import { isAbort, params } from '../lib/api'
import { remediationMaterials, remediationRequiredMaterials } from '../lib/remediations'

const props = defineProps<{ basePath: string; organization?: string }>()
const types = {
  plan: { extension: 'docx', mime: 'wordprocessingml.document' },
  responsibility: { extension: 'xlsx', mime: 'spreadsheetml.sheet' },
  schedule: { extension: 'xlsx', mime: 'spreadsheetml.sheet' },
} as const
type TemplateKind = keyof typeof types
const endpoint = computed(() => '/api/v1' + props.basePath + '/material-templates/')
const query = computed(() => params({ organization: props.organization }))
const failure = ref('')
const pending = ref<TemplateKind>()
let request: AbortController | undefined

/**
 * 用途 取消旧范围的下载并清除页面提示。
 * 参数 无。
 * 返回值 无。
 */
function resetDownload() {
  request?.abort()
  request = undefined
  pending.value = undefined
  failure.value = ''
}

/**
 * 用途 下载当前业务范围的指定空白模板，并在原页面展示异常。
 * 参数 kind 为材料类别，event 指向带同源下载地址及文件名的链接。
 * 返回值 文件下载或异常提示的任务。
 */
async function download(kind: TemplateKind, event: MouseEvent) {
  const origin = event.currentTarget
  if (pending.value || !(origin instanceof HTMLAnchorElement)) return
  const controller = new AbortController()
  request = controller
  pending.value = kind
  failure.value = ''
  try {
    const response = await fetch(origin.href, { signal: controller.signal, redirect: 'error' })
    if (request !== controller) return
    const expectedType = 'application/vnd.openxmlformats-officedocument.' + types[kind].mime
    if (!response.ok || response.headers.get('Content-Type') !== expectedType) {
      failure.value = response.status === 404
        ? '当前备案或测评报告已不可用于模板下载，请刷新页面后重试。'
        : '模板下载失败，请稍后重试。'
      return
    }
    const contents = await response.blob()
    if (request !== controller) return
    const objectURL = URL.createObjectURL(contents)
    const link = document.createElement('a')
    link.href = objectURL
    link.download = origin.download
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(objectURL), 30000)
  } catch (error) {
    if (request === controller && !isAbort(error)) failure.value = '模板下载失败，请稍后重试。'
  } finally {
    if (request === controller) {
      request = undefined
      pending.value = undefined
    }
  }
}

watch([endpoint, query], resetDownload, { flush: 'sync' })
onScopeDispose(resetDownload)
</script>

<template>
  <div class="material-templates" role="group" aria-label="整改材料模板" :aria-busy="!!pending">
    <p class="muted">模板供本地填报参考。填写完成后，请按材料类别分别上传。</p>
    <div class="material-template-links">
      <a v-for="kind in remediationRequiredMaterials" :key="kind" class="button neutral small"
        :href="endpoint + kind + query" :download="remediationMaterials[kind] + '模板.' + types[kind].extension"
        :data-remediation-template="kind" :aria-disabled="pending ? 'true' : undefined"
        @click.prevent="download(kind, $event)">
        <Download :size="15" aria-hidden="true" />
        {{ pending === kind ? '下载中…' : remediationMaterials[kind] + '模板' }}
      </a>
    </div>
    <p v-if="failure" class="danger-text" role="alert">{{ failure }}</p>
  </div>
</template>

<style scoped>
.material-templates { display: grid; gap: 8px; padding: 10px 0; }
.material-templates p { margin: 0; font-size: .875rem; }
.material-template-links { display: flex; flex-wrap: wrap; gap: 8px; }
.material-template-links a[aria-disabled="true"] { opacity: .62; cursor: default; }
</style>

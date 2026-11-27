<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { formatTime, type Attachment } from '../lib/domain'

const props = defineProps<{ path: string; attachments: Attachment[] }>()

const names = {
  assets: '资产清单',
  findings: '漏洞详情',
  risk: '风险等级',
  recommendations: '整改建议',
}
type FieldKey = keyof typeof names
interface ExtractedField {
  key: FieldKey
  original: string
  value?: string
  startParagraph: number
  endParagraph: number
  startPage?: number
  endPage?: number
  source?: keyof typeof evidenceSources
  truncated?: boolean
}
interface Extraction {
  fields: ExtractedField[]
  version: number
  confirmedAt?: string
  confirmedBy?: string
}

const evidenceSources = {
  pdf_text: 'PDF 文字层',
  pdf_ocr: '扫描识别',
  pdf_mixed: '文字层与扫描识别',
}
const sources = computed(() => props.attachments.filter(
  (file) => file.kind === 'report_file' && /\.(?:docx|pdf)$/i.test(file.fileName),
))
const source = ref('')
const isPDF = computed(() => sources.value.some((file) => file.id === source.value && /\.pdf$/i.test(file.fileName)))
const endpoint = computed(() => props.path + '/attachments/' + encodeURIComponent(source.value) + '/extraction')
const extraction = shallowRef<Extraction>()
const corrections = ref<Partial<Record<FieldKey, string>>>({})
const loading = ref(false)
const busy = ref(false)
const failure = ref('')
const actionError = ref('')
const notice = ref('')
const conflict = ref(false)
let request: AbortController | undefined
let write: AbortController | undefined

/**
 * 用途 选中报告正文并加载已有的提取数据。
 * 参数 无。
 * 返回值 无。
 */
function syncSource() {
  if (!sources.value.some((file) => file.id === source.value)) {
    source.value = sources.value.at(-1)?.id ?? ''
  }
}

/**
 * 用途 同步服务端已保存的数据和可编辑字段。
 * 参数 value 为当前附件的提取结果。
 * 返回值 无。
 */
function syncResult(value: Extraction) {
  corrections.value = Object.fromEntries(value.fields.map((field) => [field.key, field.value ?? field.original]))
  extraction.value = value
}

/**
 * 用途 获取选中文件的提取结果并取消旧请求。
 * 参数 无。
 * 返回值 本次读取完成后的任务。
 */
async function refresh() {
  request?.abort()
  write?.abort()
  const next = new AbortController()
  request = next
  failure.value = ''
  actionError.value = ''
  notice.value = ''
  conflict.value = false
  extraction.value = undefined
  loading.value = Boolean(source.value)
  if (!source.value) return
  try {
    const value = await api<Extraction>(endpoint.value, { signal: next.signal })
    if (request === next) syncResult(value)
  } catch (error) {
    if (request === next && !isAbort(error) &&
      !(error instanceof ApiError && error.code === 'extraction_not_found')) {
      failure.value = errorText(error)
    }
  } finally {
    if (request === next) loading.value = false
  }
}

/**
 * 用途 发起报告提取或提交发生变更的校对字段。
 * 参数 mode 为提取或校对操作。
 * 返回值 写入结束后的任务。
 */
async function run(mode: 'POST' | 'PATCH') {
  if (!source.value || busy.value || (mode === 'PATCH' && !extraction.value)) return
  const next = new AbortController()
  const url = endpoint.value
  write = next
  busy.value = true
  actionError.value = ''
  notice.value = ''
  const current = extraction.value
  const changes = current?.fields.flatMap((field) => {
    const value = corrections.value[field.key] ?? ''
    return value === (field.value ?? field.original) ? [] : [{ key: field.key, value }]
  })
  try {
    const body = mode === 'PATCH' && current ? { expectedVersion: current.version, changes } : undefined
    const value = await api<Extraction>(url, { method: mode, body, signal: next.signal })
    if (url !== endpoint.value) return
    syncResult(value)
    notice.value = mode === 'PATCH' ? '校对结果已保存。' : '提取完成，请核对原文和字段。'
  } catch (error) {
    if (url !== endpoint.value || isAbort(error)) return
    actionError.value = errorText(error)
    conflict.value = error instanceof ApiError && error.status === 409
  } finally {
    if (write === next) busy.value = false
  }
}

const hasChanges = computed(() => extraction.value?.fields.some(
  (field) => corrections.value[field.key] !== (field.value ?? field.original),
) ?? false)

watch(sources, syncSource, { immediate: true })
watch(endpoint, refresh, { immediate: true })
onBeforeUnmount(() => { request?.abort(); write?.abort() })
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="extract-title">
    <header class="panel-heading panel-compact"><h2 id="extract-title">报告提取与校对</h2></header>
    <p v-if="!sources.length">上传含栏目标题的 DOCX 或 PDF 测评报告正文后可提取。</p>
    <template v-else>
      <label class="field">
        <span>测评报告正文</span>
        <select v-model="source" :disabled="busy">
          <option v-for="file in sources" :key="file.id" :value="file.id">
            {{ file.fileName }} · {{ formatTime(file.uploadedAt) }}
          </option>
        </select>
      </label>
      <p v-if="isPDF && !extraction">PDF 的扫描页使用本地文字识别，提取后请对照附件校对。</p>
      <AsyncState :loading="loading" :error="failure" @retry="refresh">
        <template v-if="extraction">
          <form class="extract-form" @submit.prevent="run('PATCH')">
            <div v-for="field in extraction.fields" :key="field.key" class="extract-field">
              <label class="field">
                <span>{{ names[field.key] }}</span>
                <textarea v-model="corrections[field.key]" rows="3" maxlength="4000" :disabled="busy || conflict" />
              </label>
              <details>
                <summary>
                  <template v-if="field.startPage && field.endPage">
                    PDF 第 {{ field.startPage }}<template v-if="field.endPage !== field.startPage">–{{ field.endPage }}</template> 页 ·
                  </template>
                  第 {{ field.startParagraph }}<template v-if="field.endParagraph !== field.startParagraph">–{{ field.endParagraph }}</template>
                  {{ field.startPage ? '个文本块' : '段' }}
                  <template v-if="field.source"> · {{ evidenceSources[field.source] }}</template>
                </summary>
                <p class="extract-original">{{ field.original }}<span v-if="field.truncated">（原文较长，请以附件为准）</span></p>
              </details>
            </div>
            <p v-if="extraction.confirmedAt"><strong>已校对</strong> · {{ extraction.confirmedBy }} · {{ formatTime(extraction.confirmedAt) }}</p>
            <button v-if="conflict" class="button neutral" type="button" @click="refresh">重新加载提取结果</button>
            <button v-else class="button primary" type="submit"
              :disabled="busy || Boolean(extraction.confirmedAt && !hasChanges)">
              {{ busy ? '保存中…' : '确认校对' }}
            </button>
          </form>
        </template>
        <button v-else class="button primary" type="button" :disabled="busy" @click="run('POST')">
          {{ busy ? '提取中…' : '提取报告字段' }}
        </button>
      </AsyncState>
      <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
      <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
    </template>
  </section>
</template>

<style scoped>
.extract-form, .extract-field { display: grid; gap: 0.75rem; }
.extract-form { margin-top: 1rem; justify-items: start; }
.extract-field { width: 100%; }
.extract-original { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

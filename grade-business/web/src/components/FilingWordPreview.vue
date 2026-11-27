<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { FileSearch } from '@lucide/vue'
import { api, errorText, isAbort } from '../lib/api'
import type { Attachment } from '../lib/domain'
import type { FilingTextDocument } from '../lib/filingReviewEvidence'

const props = defineProps<{ file: Attachment; path: string; query?: string }>()
const dialog = ref<HTMLDialogElement>()
const documents = shallowRef<FilingTextDocument[]>([])
const loading = ref(false)
const failure = ref('')
let request: AbortController | undefined

/**
 * 用途 打开原件文字预览，沿用当前备案与办理范围读取内容。
 * 参数 无，附件和业务范围取自当前行。
 * 返回值 预览请求任务。
 */
async function showPreview() {
  if (!dialog.value) return
  if (!dialog.value.open) dialog.value.showModal()
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  failure.value = ''
  documents.value = []
  try {
    const url = props.path + '/attachments/' + encodeURIComponent(props.file.id) + '/text' + (props.query ?? '')
    const result = await api<{ items: FilingTextDocument[] }>(url, { signal: current.signal })
    if (request === current) documents.value = result.items
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) loading.value = false
  }
}

/**
 * 用途 停止旧附件或已关闭预览的请求，并清除其内容。
 * 参数 无。
 * 返回值 无。
 */
function clearPreview() {
  request?.abort()
  request = undefined
  loading.value = false
  failure.value = ''
  documents.value = []
}

watch(() => [props.path, props.query, props.file.id], () => {
  dialog.value?.close()
  clearPreview()
})
onBeforeUnmount(clearPreview)
</script>

<template>
  <button class="text-action" type="button" :aria-label="'查看解析 ' + file.fileName" @click="showPreview">
    <FileSearch :size="15" aria-hidden="true" />查看解析
  </button>
  <dialog ref="dialog" class="decision-dialog filing-word-dialog" :aria-label="'材料解析 · ' + file.fileName"
    @close="clearPreview">
    <form class="filing-word-preview" method="dialog">
      <header class="panel-heading panel-compact">
        <div>
          <h2>材料解析</h2>
          <p class="muted">{{ file.fileName }}</p>
        </div>
        <button class="button neutral small" type="submit">关闭</button>
      </header>
      <p v-if="loading" class="hint-row" role="status">正在读取正文和表格文字…</p>
      <p v-else-if="failure" class="form-message error-message" role="alert">
        {{ failure }}
        <button class="text-action" type="button" @click="showPreview">重试</button>
      </p>
      <div v-else class="filing-word-content">
        <p class="muted">按原件顺序展示文字。图片、勾选状态和排版请核对原件。</p>
        <section v-for="document in documents" :key="document.category" class="filing-word-document">
          <h3>{{ document.category }} · {{ document.fileName }}</h3>
          <p v-if="document.message" class="muted">{{ document.message }}</p>
          <p v-if="document.truncated" class="muted">当前展示部分文字，完整内容可下载原件核对。</p>
          <details v-for="(section, index) in document.sections" :key="index" :open="index === 0">
            <summary>{{ section.title }}</summary>
            <div class="filing-word-paragraphs">
              <p v-for="(paragraph, number) in section.paragraphs" :key="number">{{ paragraph }}</p>
            </div>
          </details>
        </section>
      </div>
    </form>
  </dialog>
</template>

<style scoped>
.filing-word-dialog {
  width: min(780px, calc(100vw - 32px));
  max-height: min(78dvh, 800px);
}
.filing-word-preview { display: grid; gap: 12px; }
.filing-word-preview header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; }
.filing-word-preview header p { margin: 4px 0 0; overflow-wrap: anywhere; }
.filing-word-content { display: grid; gap: 14px; }
.filing-word-content > p { margin: 0; }
.filing-word-document h3 { margin: 0 0 8px; font-size: 14px; overflow-wrap: anywhere; }
.filing-word-document details { padding: 8px 0; border-top: 1px solid var(--line); }
.filing-word-document summary { cursor: pointer; font-weight: 600; }
.filing-word-paragraphs { display: grid; gap: 5px; margin: 8px 0 2px; }
.filing-word-paragraphs p { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

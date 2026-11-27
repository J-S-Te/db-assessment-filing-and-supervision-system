<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { Download, FileUp } from '@lucide/vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { categories } from '../lib/domain'
import { knowledgeImportPath, type KnowledgeImportPreview, type KnowledgeImportResult } from '../lib/knowledge'

const emit = defineEmits<{ close: []; imported: [result: KnowledgeImportResult] }>()
const dialog = ref<HTMLDialogElement>()
const file = shallowRef<File>()
const preview = shallowRef<KnowledgeImportPreview>()
const operation = ref<'preview' | 'import'>()
const failure = ref('')
const ready = computed(() => Boolean(file.value && preview.value?.items.length && !preview.value.issues.length))
let request: AbortController | undefined

/**
 * 用途 读取用户选择的文件并清除此前的预检结果。
 * 参数 event 为文件选择控件触发的事件。
 * 返回值 无，文件无效时显示可修正提示。
 */
function selectFile(event: Event) {
  const picker = event.target as HTMLInputElement
  const selected = picker.files?.[0]
  preview.value = undefined
  failure.value = ''
  file.value = undefined
  if (!selected) return
  if (!/\.csv$/i.test(selected.name) || !selected.size || selected.size > 2 * 1024 * 1024) {
    failure.value = '请选择不超过 2 MiB 的非空 CSV 文件。'
    picker.value = ''
    return
  }
  file.value = selected
}

/**
 * 用途 将当前 CSV 发送到预检或正式导入接口。
 * 参数 endpoint 为批量导入路径后缀，signal 为当前页面的取消信号。
 * 返回值 对应接口的业务响应，上传失败时抛出接口异常。
 */
function sendFile<T>(endpoint: string, signal: AbortSignal): Promise<T> {
  const body = new FormData()
  if (file.value) body.append('file', file.value)
  return api<T>(knowledgeImportPath + endpoint, { method: 'POST', body, signal })
}

/**
 * 用途 校验当前 CSV 文件并呈现有效项和全部逐行问题。
 * 参数 无，读取当前选择的文件。
 * 返回值 当前批次的预检任务。
 */
async function checkFile() {
  if (!file.value || operation.value) return
  const current = new AbortController()
  request = current
  operation.value = 'preview'
  failure.value = ''
  preview.value = undefined
  try {
    const result = await sendFile<KnowledgeImportPreview>('/preview', current.signal)
    if (!current.signal.aborted) preview.value = result
  } catch (error) {
    if (!current.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

/**
 * 用途 正式导入已通过预检的整批知识。
 * 参数 无，上传预检时选择的同一文件。
 * 返回值 保存任务，通过事件返回实际入库数量和标识。
 */
async function importFile() {
  if (!ready.value || operation.value) return
  const current = new AbortController()
  request = current
  operation.value = 'import'
  failure.value = ''
  try {
    const result = await sendFile<KnowledgeImportResult>('', current.signal)
    if (!current.signal.aborted) emit('imported', result)
  } catch (error) {
    if (current.signal.aborted || isAbort(error)) return
    if (error instanceof ApiError && error.code === 'validation_error') preview.value = undefined
    failure.value = errorText(error)
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

onMounted(() => dialog.value?.showModal())
onScopeDispose(() => request?.abort())
</script>

<template>
  <dialog ref="dialog" class="decision-dialog knowledge-import" aria-labelledby="knowledge-import-title"
    @cancel.prevent="!operation && emit('close')">
    <header class="import-heading">
      <h2 id="knowledge-import-title">批量导入知识</h2>
      <a class="button neutral small" :href="'/api/v1' + knowledgeImportPath + '/template'" download>
        <Download :size="15" aria-hidden="true" />下载 CSV 模板
      </a>
    </header>
    <p>使用 UTF-8 CSV，每批最多 100 条，文件上限 2 MiB。标签可留空或填写 JSON 字符串数组。</p>
    <details class="import-format">
      <summary>分类代码</summary>
      <dl>
        <div v-for="(label, code) in categories" :key="code"><dt>{{ label }}</dt><dd><code>{{ code }}</code></dd></div>
      </dl>
    </details>
    <form class="import-picker" @submit.prevent="checkFile">
      <label class="field">
        <span>选择 CSV 文件</span>
        <input type="file" accept=".csv,text/csv" required :disabled="Boolean(operation)" @change="selectFile">
      </label>
      <button type="submit" class="button neutral" :disabled="!file || Boolean(operation)">
        <FileUp :size="15" aria-hidden="true" />{{ operation === 'preview' ? '预检中…' : '预检文件' }}
      </button>
    </form>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <section v-if="preview" class="import-preview" aria-labelledby="knowledge-preview-title">
      <h3 id="knowledge-preview-title">预检结果</h3>
      <p role="status">读取 {{ preview.total }} 条，{{ preview.items.length }} 条通过预检。</p>
      <ul v-if="preview.issues.length" class="form-message warning-message import-issues">
        <li v-for="(issue, index) in preview.issues" :key="index">第 {{ issue.row }} 行 · {{ issue.message }}</li>
      </ul>
      <div v-if="preview.items.length" class="table-wrap import-table">
        <table class="data-table">
          <thead><tr><th>行号</th><th>标题</th><th>分类</th><th>来源</th><th>标签</th></tr></thead>
          <tbody>
            <tr v-for="item in preview.items" :key="item.row">
              <td>{{ item.row }}</td><td>{{ item.title }}</td><td>{{ categories[item.category] }}</td>
              <td>{{ item.source }}</td><td>{{ item.tags.join('、') || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
    <footer class="form-actions">
      <button type="button" class="button neutral" :disabled="Boolean(operation)" @click="emit('close')">关闭</button>
      <button type="button" class="button primary" :disabled="!ready || Boolean(operation)" @click="importFile">
        {{ operation === 'import' ? '导入中…' : '确认导入' }}
      </button>
    </footer>
  </dialog>
</template>

<style scoped>
.knowledge-import { width: min(760px, calc(100vw - 32px)); max-height: calc(100vh - 40px); overflow: auto; }
.import-heading { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 12px; }
.import-heading h2 { margin: 0; }
.import-format { margin-top: 10px; }
.import-format summary { cursor: pointer; color: var(--primary); }
.import-format dl { display: flex; flex-wrap: wrap; gap: 8px 20px; margin-top: 6px; }
.import-format dt { color: var(--muted); }
.import-picker { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 12px; margin: 14px 0; }
.import-picker .field { min-width: 0; }
.import-preview { margin-top: 14px; }
.import-table { max-height: 260px; overflow: auto; }
.import-issues { padding-left: 32px; }
</style>

<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'

const endpoint = '/filing-history-import'
const labels = {
  ready: '可导入', duplicate: '重复', conflict: '冲突', invalid: '材料或字段有误', imported: '已导入',
} as const
type ImportStatus = keyof typeof labels

interface ImportRow {
  line: number
  number: string
  systemName: string
  organizationName: string
  status: ImportStatus
  reason?: string
  filingId?: string
}

interface ImportResult {
  batchId?: string
  operator?: string
  importedAt?: string
  rows: ImportRow[]
  counts: Record<ImportStatus, number>
}

const route = useRoute()
const router = useRouter()
const file = shallowRef<File>()
const result = shallowRef<ImportResult>()
const operation = ref<'preview' | 'commit' | 'receipt'>()
const failure = ref('')
const confirmation = ref(false)
const canCommit = computed(() => Boolean(file.value && result.value && !result.value.batchId))
let request: AbortController | undefined

/**
 * 用途 保存用户选取的 ZIP，并清理不同文件的预检信息。
 * 参数 event 为原生文件选择控件的事件。
 * 返回值 无，格式或大小错误显示在页面。
 */
function selectFile(event: Event) {
  const picker = event.target as HTMLInputElement
  const chosen = picker.files?.[0]
  result.value = undefined
  confirmation.value = false
  failure.value = ''
  file.value = undefined
  if (route.query.batch) void router.replace({ query: {} })
  if (!chosen) return
  if (!/\.zip$/i.test(chosen.name) || !chosen.size || chosen.size > 200 * 1024 * 1024) {
    picker.value = ''
    failure.value = '请选择不超过 200 MiB 的非空 ZIP 文件。'
    return
  }
  file.value = chosen
}

/**
 * 用途 加载已确认的历史导入记录或提交当前选定的同一 ZIP。
 * 参数 action 区分预检、确认和留痕查询，batch 为可选的既有批次标识。
 * 返回值 无，结果和业务错误分别展示。
 */
async function run(action: 'preview' | 'commit' | 'receipt', batch?: string) {
  if (operation.value || (action === 'receipt' ? !batch : !file.value)) return
  const current = new AbortController()
  request = current
  operation.value = action
  failure.value = ''
  confirmation.value = false
  if (action === 'preview') result.value = undefined
  try {
    if (action === 'receipt' && batch) {
      result.value = await api<ImportResult>(endpoint + '/batches/' + encodeURIComponent(batch), {
        signal: current.signal,
      })
      return
    }
    const body = new FormData()
    if (file.value) body.append('file', file.value)
    const response = await api<ImportResult>(endpoint + '/' + action, {
      method: 'POST', body, signal: current.signal,
    })
    if (current.signal.aborted) return
    result.value = response
    if (response.batchId) void router.replace({ query: { batch: response.batchId } })
  } catch (error) {
    if (!current.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) {
      request = undefined
      operation.value = undefined
    }
  }
}

onMounted(() => {
  if (typeof route.query.batch === 'string' && route.query.batch) void run('receipt', route.query.batch)
})
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="history-import" aria-labelledby="history-import-title">
    <header class="page-header">
      <div>
        <h1 id="history-import-title">导入历史备案材料</h1>
        <p>编号、单位和系统相同的备案自动跳过。同一编号对应不同单位或系统时，保留原备案。</p>
      </div>
      <RouterLink class="button neutral" to="/filings">返回备案管理</RouterLink>
    </header>

    <section class="panel import-panel" aria-label="导入文件">
      <div class="import-instructions">
        <p>将填写好的 manifest.csv 放在 ZIP 根目录。材料路径填写包内相对路径，备案表和定级报告支持 PDF、DOC 和 DOCX。</p>
        <a class="button neutral" href="/api/v1/filing-history-import/template" download>下载清单模板</a>
      </div>
      <form class="import-picker" @submit.prevent="run('preview')">
        <label class="field">
          <span>历史备案 ZIP（最多 100 条、200 MiB）</span>
          <input type="file" accept=".zip,application/zip" required :disabled="Boolean(operation)"
            @change="selectFile">
        </label>
        <button class="button neutral" type="submit" :disabled="!file || Boolean(operation)">
          {{ operation === 'preview' ? '预检中…' : '预检材料' }}
        </button>
      </form>
      <p v-if="operation === 'receipt'" role="status">正在读取导入批次…</p>
      <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    </section>

    <section v-if="result" class="panel import-result" aria-label="导入结果">
      <div class="panel-heading">
        <div>
          <h2>{{ result.batchId ? '批次结果' : '预检结果' }}</h2>
          <p v-if="result.batchId" class="muted">
            导入时间 {{ formatTime(result.importedAt) }} · {{ result.operator }} · 批次 {{ result.batchId }}
          </p>
        </div>
      </div>
      <div class="import-summary" role="status">
        <span v-if="result.batchId">已导入 <strong>{{ result.counts.imported }}</strong></span>
        <span v-else>可导入 <strong>{{ result.counts.ready }}</strong></span>
        <span>重复 <strong>{{ result.counts.duplicate }}</strong></span>
        <span>冲突 <strong>{{ result.counts.conflict }}</strong></span>
        <span>需补正 <strong>{{ result.counts.invalid }}</strong></span>
      </div>
      <div class="table-wrap">
        <table class="data-table import-table">
          <thead><tr><th>行号</th><th>备案编号</th><th>系统</th><th>单位</th><th>结果</th><th>说明</th></tr></thead>
          <tbody>
            <tr v-for="row in result.rows" :key="row.line">
              <td>{{ row.line }}</td>
              <td><RouterLink v-if="row.filingId" :to="'/filings/' + row.filingId">
                {{ row.number }}
              </RouterLink><span v-else>{{ row.number || '—' }}</span></td>
              <td>{{ row.systemName || '—' }}</td><td>{{ row.organizationName || '—' }}</td>
              <td>{{ labels[row.status] }}</td><td>{{ row.reason || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <footer v-if="canCommit" class="panel-footer">
        <p v-if="confirmation" class="muted">
          系统会再次核对。通过的记录保存为草稿，重复、冲突及校验失败的记录保留结果。
        </p>
        <button v-if="confirmation" class="button neutral" type="button" @click="confirmation = false">返回核对</button>
        <button v-if="confirmation" class="button primary" type="button" :disabled="Boolean(operation)"
          @click="run('commit')">确认导入</button>
        <button v-else class="button primary" type="button" :disabled="Boolean(operation)" @click="confirmation = true">
          {{ operation === 'commit' ? '正在导入…' : '核对并导入' }}
        </button>
      </footer>
    </section>
  </section>
</template>

<style scoped>
.history-import { display: grid; gap: 14px; }
.import-panel { display: grid; gap: 12px; padding: 15px; }
.import-instructions, .import-picker, .import-summary { display: flex; align-items: center; flex-wrap: wrap; gap: 10px 16px; }
.import-instructions { justify-content: space-between; }
.import-instructions p, .import-summary p, .panel-heading p { margin: 0; }
.import-picker { align-items: flex-end; }
.import-summary { padding: 12px 15px; }
.import-table td { overflow-wrap: anywhere; }
.import-result .panel-footer { flex-wrap: wrap; }
</style>

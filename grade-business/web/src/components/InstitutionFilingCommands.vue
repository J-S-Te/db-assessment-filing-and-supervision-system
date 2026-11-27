<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import { api, errorText, isAbort, params } from '../lib/api'
import { type CaseMutation, type CaseSummary } from '../lib/domain'

type FilingRow = CaseSummary & { version: number }
type Action = 'submit' | 'discard'
type Outcome = { id: string; outcome: 'submitted' | 'discarded' | 'rejected'; code?: string }
type Operation = { copy: FilingRow } | { action: Action; rows: FilingRow[] }

const props = defineProps<{ scope: string; selected: FilingRow[] }>()
const emit = defineEmits<{ changed: [] }>()
const router = useRouter()
const dialog = ref<HTMLDialogElement>()
const operation = shallowRef<Operation>()
const results = shallowRef<Outcome[]>()
const copyNumber = ref('')
const loading = ref(false)
const failure = ref('')
const canDelete = computed(() => props.selected.some(row => row.status === 'draft'))
let active: AbortController | undefined
let identity: { key: string; fingerprint: string } | undefined
const actionText = { submit: '批量送审', discard: '批量删除' } as const
const reasoning: Readonly<Record<string, string>> = {
  not_found: '记录不存在或已移出当前机构范围',
  conflict: '版本或状态已变化，请刷新核对',
  materials_required: '必传材料不齐',
  material_issues_open: '请先补正材料异常',
  withdrawal_pending: '该记录正在等待撤回确认',
}

/**
 * 用途 打开复制对话框，保存源备案版本。
 * 参数 row 是列表中当前可复制的已加载机构备案摘要。
 * 返回值 无。
 */
function openCopy(row: FilingRow) {
  copyNumber.value = ''
  open({ copy: row })
}

/**
 * 用途 依据当前选中备案打开批量确认，冻结所见记录与版本。
 * 参数 action 为送审或删除。
 * 返回值 无。
 */
function openBatch(action: Action) {
  if (!props.selected.length) return
  open({ action, rows: [...props.selected] })
}

/**
 * 用途 初始化一次独立的机构办理操作和结果容器。
 * 参数 next 为本次对话框保存的复制源或批处理快照。
 * 返回值 无。
 */
function open(next: Operation) {
  if (loading.value) return
  operation.value = next
  results.value = undefined
  identity = undefined
  failure.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 以原请求标识安全重试同一操作；改动内容后启用新标识。
 * 参数 payload 为本次确认的实际业务字段。
 * 返回值 该内容使用的唯一请求标识。
 */
function requestID(payload: object) {
  const fingerprint = JSON.stringify(payload)
  if (identity?.fingerprint !== fingerprint) identity = { key: crypto.randomUUID(), fingerprint }
  return identity.key
}

/**
 * 用途 确认复制或批量办理，显示确切结果并刷新来源列表。
 * 参数 无，使用当前确认快照。
 * 返回值 本轮提交任务。
 */
async function confirm() {
  const current = operation.value
  if (!current || loading.value) return
  const request = new AbortController()
  active = request
  const scope = props.scope
  loading.value = true
  failure.value = ''
  try {
    if ('copy' in current) await copyDraft(current.copy, scope, request.signal)
    else await runBatch(current, scope, request.signal)
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) {
      active = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 创建独立草稿并打开可重新上传材料的详情。
 * 参数 row、scope 为源备案和机构范围，signal 控制请求。
 * 返回值 创建及导航任务。
 */
async function copyDraft(row: FilingRow, scope: string, signal: AbortSignal) {
  const path = '/institution/filings'
  const fields = { number: copyNumber.value.trim(), expectedVersion: row.version }
  const result = await api<CaseMutation>(path + '/' + row.id + '/copy' + params({ institution: scope }), {
    method: 'POST', body: { ...fields, requestId: requestID(fields) }, signal,
  })
  if (signal.aborted) return
  dialog.value?.close()
  await router.push({ path: path + '/' + result.id, query: { institution: scope } })
}

/**
 * 用途 批量送审或删除已确认记录，按实际行保留回执。
 * 参数 current 为已确认的动作和记录，scope、signal 为机构范围和取消信号。
 * 返回值 批量提交和列表更新任务。
 */
async function runBatch(current: Extract<Operation, { action: Action }>, scope: string, signal: AbortSignal) {
  const fields = { action: current.action,
    items: current.rows.map(row => ({ id: row.id, expectedVersion: row.version })) }
  const result = await api<{ items: Outcome[] }>('/institution/filings/batch' + params({ institution: scope }), {
    method: 'POST', body: { ...fields, requestId: requestID(fields) }, signal,
  })
  if (signal.aborted) return
  results.value = result.items
  emit('changed')
}

const title = computed(() => operation.value && ('copy' in operation.value
  ? '复制为备案草稿' : actionText[operation.value.action]))
const counts = computed(() => results.value?.filter(item => item.outcome !== 'rejected').length ?? 0)

onScopeDispose(() => active?.abort())
defineExpose({ openCopy })
</script>

<template>
  <div class="institution-batch-actions">
    <button type="button" class="button primary" :disabled="!selected.length" @click="openBatch('submit')">
      批量送审
    </button>
    <button type="button" class="button neutral" :disabled="!canDelete" @click="openBatch('discard')">
      批量删除
    </button>
    <small v-if="selected.length" class="muted">已选 {{ selected.length }} 条</small>
  </div>
  <dialog ref="dialog" class="decision-dialog institution-batch-dialog" aria-labelledby="institution-batch-title"
    @cancel.prevent="!loading && dialog?.close()">
    <form v-if="operation" @submit.prevent="confirm">
      <h2 id="institution-batch-title">{{ title }}</h2>
      <template v-if="'copy' in operation">
        <p>源备案 {{ operation.copy.number }}。新草稿复制系统、单位和定级，材料需重新上传。</p>
        <label class="field"><span>新备案编号</span>
          <input v-model="copyNumber" maxlength="80" required :disabled="loading">
        </label>
      </template>
      <template v-else>
        <p v-if="operation.action === 'submit'">
          核对所选 {{ operation.rows.length }} 条备案的版本及材料，每条分别返回送审结果。
        </p>
        <p v-else>仅删除从未送审的草稿，原材料和操作留痕保留。</p>
        <template v-if="results">
          <p role="status">办理成功 {{ counts }} 条，未办理 {{ results.length - counts }} 条。</p>
          <ul class="institution-batch-results">
            <li v-for="(item, index) in results" :key="item.id">
              {{ operation.rows[index]?.number }} ·
              {{ item.outcome === 'submitted' ? '已送审' : item.outcome === 'discarded' ? '已删除'
                : reasoning[item.code ?? ''] ?? '未办理' }}
            </li>
          </ul>
        </template>
        <ul v-else class="institution-batch-results">
          <li v-for="row in operation.rows" :key="row.id">{{ row.number }} · {{ row.systemName }}</li>
        </ul>
      </template>
      <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
      <div class="form-actions">
        <button type="button" class="button neutral" :disabled="loading" @click="dialog?.close()">
          {{ results ? '关闭' : '取消' }}
        </button>
        <button v-if="!results" type="submit" class="button primary" :disabled="loading">
          {{ loading ? '办理中…' : '确认' + title }}
        </button>
      </div>
    </form>
  </dialog>
</template>

<style scoped>
.institution-batch-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.institution-batch-dialog { max-width: min(640px, 94vw); }
.institution-batch-results { max-height: 250px; overflow: auto; padding-inline-start: 1.4em; }
.institution-batch-results li { padding-block: .25em; }
</style>

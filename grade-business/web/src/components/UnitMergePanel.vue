<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import DirectoryPicker from './DirectoryPicker.vue'
import { ApiError, api, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'

type MergeMode = 'simple' | 'complex'
type MergeDisposition = 'transfer' | 'preserve' | 'blocked'

interface UnitMergeImpact {
  id: string
  kind: string
  filingId?: string
  number: string
  title: string
  status: string
  sourceName: string
  disposition: MergeDisposition
  reason: string
}

interface UnitMergePreview {
  mode?: MergeMode
  impacts?: UnitMergeImpact[]
  target: { id: string; name: string; scopeName: string }
  sources: { id: string; name: string; scopeName: string }[]
  aliases?: { id: string; name: string; scopeName: string; mergedIntoId: string }[]
  moves: { id: string; number: string; systemName: string; sourceName: string }[]
  blockers: { code: string; message: string; count: number }[]
  membershipsToAdd: number
  retainedCancelled: number
  token?: string
}

interface UnitMergeResult {
  id: string
  movedCount: number
  mergedAt: string
  replayed: boolean
}

interface UnitMergeHistory extends UnitMergeResult {
  actor: string
  preview: UnitMergePreview
}

const path = '/directory/unit-merges'
const emit = defineEmits<{ merged: [movedCount: number] }>()
const dialog = ref<HTMLDialogElement>()
const visible = ref(false)
const mode = ref<'prepare' | 'history'>('prepare')
const mergeMode = ref<MergeMode>('simple')
const targetId = ref('')
const sourceRows = ref<{ key: number; id: string }[]>([])
const preview = shallowRef<UnitMergePreview>()
const history = shallowRef<UnitMergeHistory>()
const agreed = ref(false)
const pending = ref(false)
const failure = ref('')
let nextKey = 0
let request: AbortController | undefined

const sourceIds = computed(() => sourceRows.value.map(row => row.id))
const duplicate = computed(() => {
  const sources = sourceIds.value.filter(Boolean)
  return sources.includes(targetId.value) || new Set(sources).size !== sources.length
})
const ready = computed(() => Boolean(targetId.value) && sourceIds.value.length > 0
  && sourceIds.value.every(Boolean) && !duplicate.value)
const displayed = computed(() => history.value?.preview ?? preview.value)
const impactTypes: Record<string, string> = {
  filing: '备案', 'filing-withdrawal': '备案撤回', report: '测评报告',
  implementation: '实施报备', 'filing-application': '备案申请',
  remediation: '整改规划', hazard: '隐患整改', 'material-issue': '材料异常',
  alert: '风险预警', coverage: '应备案清册', supervision: '监督检查', 'supervision-site': '现场检查', 'material-expiry': '材料有效期',
}
const impactStatuses: Record<string, string> = {
  active: '在册', inactive: '已停用', draft: '草稿', submitted: '已提交待审', pending_initial: '待初审', pending_final: '待终审',
  pending_supervision: '待监管复核', pending_review: '待复核', returned: '退回补正',
  approved: '已通过', cancelled: '已取消或注销', withdrawn: '已撤回', open: '办理中',
  closed: '已办结', pending: '待确认', processing: '处置中', pending_dispatch: '待派发', pending_retest: '待复测',
  scheduled: '已安排', recorded: '已记录', following_up: '整改跟进中',
  follow_up_submitted: '跟进待复核', valid: '有效', due: '临期', expired: '已过期',
  retired: '已结束', suspended: '待核对归属',
}
const impactDispositions: Record<MergeDisposition, string> = {
  transfer: '移交主单位', preserve: '原范围保留', blocked: '须先办理',
}

watch([targetId, sourceIds, mergeMode], () => {
  preview.value = undefined
  agreed.value = false
})
onScopeDispose(() => request?.abort())

/**
 * 用途 初始化待核对的主从单位并打开原生对话框。
 * 参数 无，打开时清除上一次未提交的预检。
 * 返回值 无。
 */
function open() {
  mode.value = 'prepare'
  mergeMode.value = 'simple'
  targetId.value = ''
  sourceRows.value = [{ key: nextKey++, id: '' }]
  preview.value = undefined
  history.value = undefined
  agreed.value = false
  failure.value = ''
  visible.value = true
  dialog.value?.showModal()
}

/**
 * 用途 从已合并单位打开当时保存的归属和备案明细。
 * 参数 id 为目录保存的合并记录标识。
 * 返回值 可取消的历史读取任务。
 */
async function openHistory(id: string) {
  mode.value = 'history'
  preview.value = undefined
  history.value = undefined
  failure.value = ''
  visible.value = true
  dialog.value?.showModal()
  try {
    history.value = await send<UnitMergeHistory>(path + '/' + encodeURIComponent(id))
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  }
}

/**
 * 用途 按编辑中的单位读取一致的归属预检。
 * 参数 无，主从单位由选择器提供。
 * 返回值 不会修改归属的预检任务。
 */
async function check() {
  if (!ready.value || pending.value) return
  preview.value = undefined
  agreed.value = false
  failure.value = ''
  try {
    preview.value = await send<UnitMergePreview>(path + '/preview', {
      targetId: targetId.value, sourceIds: sourceIds.value, mode: mergeMode.value,
    })
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  }
}

/**
 * 用途 确认经预检并人工同意的准确归属范围。
 * 参数 无，令牌封存预检所见的记录及版本。
 * 返回值 写入与显示结果的任务，状态有变化时提示重新预检。
 */
async function confirmMerge() {
  const token = preview.value?.token
  if (!ready.value || !agreed.value || !token || pending.value) return
  failure.value = ''
  try {
    const result = await send<UnitMergeResult>(path, {
      targetId: targetId.value, sourceIds: sourceIds.value, mode: mergeMode.value, token,
    })
    emit('merged', result.movedCount)
    close()
  } catch (error) {
    if (error instanceof ApiError && error.status === 409) {
      preview.value = undefined
      agreed.value = false
    }
    if (!isAbort(error)) failure.value = errorText(error)
  }
}

/**
 * 用途 与名录后端交互并限制同一对话框内的并发请求。
 * 参数 url 为同源业务接口，body 为可选预检或确认字段。
 * 返回值 业务响应或标准请求错误。
 */
async function send<T>(url: string, body?: object): Promise<T> {
  request?.abort()
  const active = new AbortController()
  request = active
  pending.value = true
  try {
    return await api<T>(url, { method: body ? 'POST' : 'GET', body, signal: active.signal })
  } finally {
    if (request === active) pending.value = false
  }
}

/**
 * 用途 在当前请求完成后关闭对话框。
 * 参数 无，提交期间保持业务结果可见。
 * 返回值 无。
 */
function close() {
  if (pending.value) return
  visible.value = false
  dialog.value?.close()
}

defineExpose({ open, openHistory })
</script>

<template>
  <dialog ref="dialog" class="decision-dialog unit-merge" aria-labelledby="unit-merge-title" @cancel.prevent="close">
    <template v-if="visible">
      <h2 id="unit-merge-title">{{ mode === 'prepare' ? '单位档案合并' : '单位合并记录' }}</h2>
      <form v-if="mode === 'prepare'" @submit.prevent="check">
        <fieldset class="unit-merge-fields" :disabled="pending">
          <DirectoryPicker v-model="targetId" kind="unit" label="保留的主单位" use-id required />
          <div v-for="(source, index) in sourceRows" :key="source.key" class="unit-merge-source">
            <DirectoryPicker v-model="source.id" kind="unit" :label="'来源单位 ' + (index + 1)" use-id required />
            <button v-if="sourceRows.length > 1" class="button neutral small" type="button"
              :aria-label="'移除来源单位 ' + (index + 1)" @click="sourceRows.splice(index, 1)">移除</button>
          </div>
          <fieldset class="unit-merge-modes">
            <legend>归并方式</legend>
            <label><input v-model="mergeMode" type="radio" name="merge-mode" value="simple"> 简单归并</label>
            <label><input v-model="mergeMode" type="radio" name="merge-mode" value="complex"> 复杂归并</label>
          </fieldset>
          <div class="form-actions">
            <button class="button neutral" type="button" :disabled="sourceRows.length >= 8"
              @click="sourceRows.push({ key: nextKey++, id: '' })">添加来源单位</button>
            <button class="button primary" type="submit" :disabled="!ready">核对合并范围</button>
          </div>
        </fieldset>
      </form>
      <p v-if="mode === 'prepare' && duplicate" class="form-message error-message" role="alert">主单位与来源单位须不同，来源单位不能重复。</p>
      <p v-if="pending" role="status">正在处理单位归属…</p>
      <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
      <section v-if="displayed" class="unit-merge-summary" aria-label="合并明细">
        <p v-if="history">{{ displayed.mode === 'complex' ? '复杂归并' : '简单归并' }} ·
          {{ formatTime(history.mergedAt) }} · 经办 {{ history.actor }}</p>
        <p><strong>主单位</strong> {{ displayed.target.name }}
          <span v-if="displayed.target.scopeName !== displayed.target.name">（{{ displayed.target.scopeName }}）</span></p>
        <p><strong>来源单位</strong> {{ displayed.sources.map(unit => unit.name).join('、') }}</p>
        <p>{{ history ? '已迁移' : '拟迁移' }} {{ displayed.moves.length }} 条备案，
          保留 {{ displayed.retainedCancelled }} 条注销备案的原归属，{{ history ? '已补充' : '拟补充' }}
          {{ displayed.membershipsToAdd }} 项机构关联。</p>
        <p v-if="displayed.aliases?.length"><strong>历史归并单位</strong>
          {{ displayed.aliases.map(unit => unit.name).join('、') }}。各单位原有收件范围和归并回执继续保留。</p>
        <ul v-if="displayed.blockers.length" class="form-message error-message" aria-label="合并阻塞项">
          <li v-for="blocker in displayed.blockers" :key="blocker.code">{{ blocker.message }}（{{ blocker.count }}）</li>
        </ul>
        <div v-if="displayed.moves.length" class="table-wrap unit-merge-table">
          <table class="data-table">
            <thead><tr><th>备案号</th><th>系统名称</th><th>来源单位</th></tr></thead>
            <tbody><tr v-for="item in displayed.moves" :key="item.id">
              <td>{{ item.number }}</td><td>{{ item.systemName }}</td><td>{{ item.sourceName }}</td>
            </tr></tbody>
          </table>
        </div>
        <section v-if="displayed.mode === 'complex'" aria-labelledby="unit-merge-impacts">
          <h3 id="unit-merge-impacts">逐笔核对事项</h3>
          <p v-if="!displayed.impacts?.length">没有关联待移交、保留或须先办理的事项。</p>
          <div v-else class="table-wrap unit-merge-table" tabindex="0" aria-label="逐笔核对事项表">
            <table class="data-table">
              <thead><tr><th>事项</th><th>原范围</th><th>状态</th><th>归并处理</th></tr></thead>
              <tbody>
                <tr v-for="item in displayed.impacts" :key="item.kind + item.id + item.reason">
                  <td>{{ impactTypes[item.kind] ?? item.kind }} · {{ item.title }}<br><small>{{ item.number }}</small></td>
                  <td>{{ item.sourceName }}</td>
                  <td>{{ impactStatuses[item.status] ?? item.status }}</td>
                  <td><strong>{{ impactDispositions[item.disposition] }}</strong><br>{{ item.reason }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
        <label v-if="preview?.token" class="unit-merge-agreement">
          <input v-model="agreed" type="checkbox" :disabled="pending">
          <span v-if="preview.mode === 'complex'">
            我已核对逐笔责任与历史权限。备案和可交接事项由主单位接续，原件、回执和既有送达按原范围保留。
          </span>
          <span v-else>我已核对主从单位。有效备案及其当前材料访问范围将迁至主单位，原回执和公告归属保留。</span>
        </label>
      </section>
      <div class="form-actions">
        <button class="button neutral" type="button" :disabled="pending" @click="close">关闭</button>
        <button v-if="preview?.token" class="button primary" type="button" :disabled="!agreed || pending"
          @click="confirmMerge">确认合并</button>
      </div>
    </template>
  </dialog>
</template>

<style scoped>
.unit-merge { width: min(760px, 94vw); max-height: 90vh; overflow: auto; }
.unit-merge-fields { display: grid; min-width: 0; gap: 12px; padding: 0; border: 0; }
.unit-merge-modes { display: flex; flex-wrap: wrap; gap: 6px 20px; padding: 6px 0; border: 0; }
.unit-merge-modes input { width: auto; }
.unit-merge-source { display: flex; align-items: end; gap: 10px; }
.unit-merge-source .directory-picker { flex: 1; min-width: 0; }
.unit-merge-summary { display: grid; gap: 8px; }
.unit-merge-summary .error-message { padding-inline-start: 24px; }
.unit-merge-table { max-height: 220px; overflow: auto; }
.unit-merge-agreement { display: flex; align-items: start; gap: 8px; }
.unit-merge-agreement input { width: auto; margin-top: 3px; }
</style>

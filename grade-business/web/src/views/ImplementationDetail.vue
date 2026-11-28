<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { Archive, ArrowLeft, Check, ChevronRight, History, Pencil, Send } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import BusinessReceiptDialog from '../components/BusinessReceiptDialog.vue'
import ImplementationPrecheck from '../components/ImplementationPrecheck.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { refreshWorkbench } from '../lib/workbench'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { formatTime } from '../lib/domain'
import { directorySnapshotLabel } from '../lib/directory'
import {
  formatImplementationRuleTime, implementationActionLabel, implementationPath, implementationRecord,
  implementationStatusClass, implementationStatuses, implementationTypes, watchImplementationRuleTimes,
  type ImplementationAction, type ImplementationDetail, type ImplementationEvent,
  type ImplementationHistory, type ImplementationMutation,
} from '../lib/implementations'

const route = useRoute()
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const path = computed(() => implementationRecord(id.value))
const record = shallowRef<ImplementationDetail>()
const history = shallowRef<ImplementationEvent[]>([])
const nextBeforeVersion = ref<number>()
const loading = ref(false)
const loadError = ref('')
const historyLoading = ref(false)
const historyError = ref('')
const actionError = ref('')
const actionNotice = ref('')
const stale = ref(false)
const dialog = ref<HTMLDialogElement>()
const command = ref<ImplementationAction>('submit')
const comment = ref('')
const changing = ref(false)
let recordRequest: AbortController | undefined
let historyRequest: AbortController | undefined

const label = computed(() => implementationActionLabel(command.value, record.value?.status))
const stage = computed(() => record.value?.status ?? 'draft')
const opinionLabels: Partial<Record<ImplementationAction, string>> = {
  approve: '审核意见（选填）',
  return_for_correction: '退回补正意见',
  restart_initial: '重新初审原因',
  archive: '归档备注（选填）',
}
const opinionLabel = computed(() => opinionLabels[command.value])
const opinionRequired = computed(() => ['return_for_correction', 'restart_initial'].includes(command.value))

/**
 * 用途 获取实施报备的表单和可执行动作。
 * 参数 无，记录标识来自当前路由。
 * 返回值 详情读取任务。
 */
async function loadRecord() {
  recordRequest?.abort()
  const request = new AbortController()
  recordRequest = request
  loading.value = !record.value
  loadError.value = ''
  try {
    const result = await api<ImplementationDetail>(path.value, { signal: request.signal })
    if (!request.signal.aborted) record.value = result
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) loadError.value = errorText(error)
  } finally {
    if (recordRequest === request) loading.value = false
  }
}

/**
 * 用途 获取当前报备的最新流转记录或下一页更早事件。
 * 参数 append 为是否在已有记录后追加更早的事件。
 * 返回值 流转记录读取任务。
 */
async function loadHistory(append = false) {
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  historyLoading.value = true
  historyError.value = ''
  const suffix = params({ beforeVersion: append ? nextBeforeVersion.value : undefined })
  try {
    const result = await api<ImplementationHistory>(path.value + '/history' + suffix, { signal: request.signal })
    if (request.signal.aborted) return
    history.value = append ? [...history.value, ...result.items] : result.items
    nextBeforeVersion.value = result.nextBeforeVersion
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === request) historyLoading.value = false
  }
}

/**
 * 用途 同步服务端详情和当前审核时间线。
 * 参数 无。
 * 返回值 并行读取任务。
 */
function refresh() {
  return Promise.all([loadRecord(), loadHistory()])
}

/**
 * 用途 打开当前允许动作的确认窗口。
 * 参数 action 为业务动作。
 * 返回值 无。
 */
function confirm(action: ImplementationAction) {
  if (changing.value || !record.value?.allowedActions.includes(action)) return
  command.value = action
  comment.value = ''
  actionError.value = ''
  stale.value = false
  dialog.value?.showModal()
}

/**
 * 用途 重新载入冲突后的记录，让用户再次确认动作。
 * 参数 无。
 * 返回值 详情和历史的读取任务。
 */
async function reloadAction() {
  await refresh()
  if (!loadError.value && record.value?.allowedActions.includes(command.value)) {
    stale.value = false
    actionError.value = ''
  } else if (loadError.value) {
    actionError.value = loadError.value
  } else {
    actionError.value = '当前状态已变化，请关闭窗口查看详情。'
  }
}

/**
 * 用途 提交服务端允许的动作及必要的意见字段。
 * 参数 无，使用确认时的记录版本。
 * 返回值 动作写入及详情刷新任务。
 */
async function act() {
  const current = path.value
  const snapshot = record.value
  const action = command.value
  if (!snapshot || changing.value || stale.value || !snapshot.allowedActions.includes(action)) return
  const note = opinionLabel.value ? comment.value.trim() : ''
  if (opinionRequired.value && !note) {
    actionError.value = '请输入' + opinionLabel.value + '。'
    return
  }
  changing.value = true
  actionError.value = ''
  actionNotice.value = ''
  try {
    await api<ImplementationMutation>(current + '/actions', {
      method: 'POST',
      body: { action, expectedVersion: snapshot.version, ...(note ? { comment: note } : {}) },
    })
    void refreshWorkbench()
    if (path.value !== current) return
    dialog.value?.close()
    actionNotice.value = implementationActionLabel(action, snapshot.status) + '已保存。'
    await refresh()
  } catch (error) {
    if (path.value !== current) return
    actionError.value = errorText(error)
    if (error instanceof ApiError && error.status === 409 && error.code !== 'change_quota') stale.value = true
  } finally {
    changing.value = false
  }
}

watch(path, () => {
  record.value = undefined
  history.value = []
  nextBeforeVersion.value = undefined
  actionNotice.value = ''
  dialog.value?.close()
  void refresh()
}, { immediate: true })

refreshBusinessOnFocus(() => {
  if (record.value && !changing.value && !loading.value && !dialog.value?.open) void refresh()
}, 10_000)
watchImplementationRuleTimes(() => record.value, () => {
  if (!changing.value && !dialog.value?.open) void loadRecord()
})
onBeforeUnmount(() => {
  recordRequest?.abort()
  historyRequest?.abort()
})
</script>

<template>
  <section>
    <RouterLink class="back-link" :to="{ path: implementationPath, query: route.query }">
      <ArrowLeft :size="16" aria-hidden="true" />返回实施管理
    </RouterLink>
    <AsyncState :loading="loading" :error="loadError" @retry="refresh">
      <template v-if="record">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>{{ record.systemName }}</h1>
              <span class="status-badge" :class="implementationStatusClass(record.status)">{{ implementationStatuses[record.status] }}</span>
              <span v-if="record.archivedAt" class="status-badge status-draft">已归档</span>
            </div>
            <p>{{ implementationTypes[record.type] }} · <span class="code">{{ record.filingNumber }}</span></p>
          </div>
          <div class="page-actions">
            <BusinessReceiptDialog resource="implementations" :record-id="record.id" />
            <RouterLink v-if="record.allowedActions.includes('edit')" class="button neutral" :to="path + '/edit'">
              <Pencil :size="16" aria-hidden="true" />{{ stage === 'submitted' ? '修改实施计划' : stage === 'returned' ? '编辑补正' : '编辑草稿' }}
            </RouterLink>
            <button v-if="record.allowedActions.includes('withdraw')" type="button" class="button neutral" @click="confirm('withdraw')">撤销报备</button>
            <button v-if="record.allowedActions.includes('return_for_correction')" type="button" class="button neutral"
              @click="confirm('return_for_correction')">退回补正</button>
            <button v-if="record.allowedActions.includes('restart_initial')" type="button" class="button neutral"
              @click="confirm('restart_initial')">重新进入初审</button>
            <button v-if="record.allowedActions.includes('submit')" type="button" class="button primary" @click="confirm('submit')">
              <Send :size="16" aria-hidden="true" />{{ stage === 'returned' ? '重新提交' : '提交报备' }}
            </button>
            <button v-if="record.allowedActions.includes('accept')" type="button" class="button primary" @click="confirm('accept')">受理报备</button>
            <button v-if="record.allowedActions.includes('approve')" type="button" class="button primary" @click="confirm('approve')">
              <Check :size="16" aria-hidden="true" />{{ implementationActionLabel('approve', stage) }}
            </button>
            <button v-if="record.allowedActions.includes('archive')" type="button" class="button primary" @click="confirm('archive')">
              <Archive :size="16" aria-hidden="true" />归档报备
            </button>
          </div>
        </header>

        <p v-if="actionNotice" class="form-message success-message detail-notice" role="status">{{ actionNotice }}</p>
        <p v-if="record.reviewBlockReason" class="detail-notice muted" role="status">{{ record.reviewBlockReason }}</p>
        <p v-if="record.resubmitAvailableAt" class="detail-notice muted" role="status">
          本备案的撤销冷却期截至 {{ formatImplementationRuleTime(record.resubmitAvailableAt) }}。
        </p>
        <p v-if="record.planEditDeadline && (stage === 'submitted' || stage === 'returned')" class="detail-notice muted">
          计划修改截止于 {{ formatImplementationRuleTime(record.planEditDeadline) }}。
        </p>
        <div class="detail-grid">
          <div class="detail-main">
            <section class="panel detail-panel" aria-labelledby="implementation-overview">
              <header class="panel-heading panel-compact"><h2 id="implementation-overview">报备信息</h2></header>
              <dl class="facts">
                <div><dt>关联备案</dt><dd>
                  <RouterLink class="text-action" :to="'/filings/' + encodeURIComponent(record.filingId)">
                    {{ record.filingNumber }}<ChevronRight :size="14" aria-hidden="true" />
                  </RouterLink>
                </dd></div>
                <div><dt>所属单位</dt><dd>{{ record.directorySnapshot?.unit?.name || record.organizationName }}</dd></div>
                <div><dt>测评机构</dt><dd>{{ record.directorySnapshot?.institution?.name || record.assessmentOrg || '—' }}</dd></div>
                <div><dt>报备人员</dt><dd>{{ record.reporter }}</dd></div>
                <div v-if="record.contact"><dt>联系电话</dt><dd>{{ record.contact }}</dd></div>
                <div><dt>计划开始</dt><dd>{{ record.plannedStart }}</dd></div>
                <div><dt>计划完成</dt><dd>{{ record.plannedEnd }}</dd></div>
                <div v-if="record.submittedAt"><dt>最近提交</dt><dd>{{ formatTime(record.submittedAt) }}</dd></div>
                <div v-if="record.acceptedAt"><dt>首次受理</dt><dd>{{ formatTime(record.acceptedAt) }}</dd></div>
                <div v-if="record.archivedAt"><dt>归档时间</dt><dd>{{ formatTime(record.archivedAt) }}</dd></div>
              </dl>
            </section>
            <section class="panel detail-panel" aria-labelledby="implementation-plan">
              <header class="panel-heading panel-compact"><h2 id="implementation-plan">实施计划与范围</h2></header>
              <p class="implementation-plan">{{ record.plan }}</p>
            </section>
            <ImplementationPrecheck v-if="record.canPrecheck" :path="path" :filing-id="record.filingId"
              :version="record.version" :disabled="changing || loading" @refresh="refresh" />
          </div>
          <section class="panel detail-panel timeline-panel" aria-labelledby="implementation-history">
            <header class="panel-heading panel-compact">
              <div class="heading-icon"><History :size="20" aria-hidden="true" /><h2 id="implementation-history">流转记录</h2></div>
            </header>
            <AsyncState :loading="historyLoading && !history.length" :error="!history.length ? historyError : ''"
              :empty="!history.length" empty-text="暂无流转记录。" @retry="loadHistory()">
              <ol class="timeline">
                <li v-for="(item, index) in history" :key="item.occurredAt + ':' + index">
                  <span class="timeline-dot" aria-hidden="true"></span>
                  <strong>{{ implementationActionLabel(item.action, item.fromStatus) }}</strong>
                  <span v-if="item.fromStatus && item.fromStatus !== item.toStatus">{{ implementationStatuses[item.fromStatus] }} → {{ implementationStatuses[item.toStatus] }}</span>
                  <span v-else>{{ implementationStatuses[item.toStatus] }}</span>
                  <p v-if="item.comment">{{ item.comment }}</p>
                  <p v-if="directorySnapshotLabel(item.directorySnapshot)">{{ directorySnapshotLabel(item.directorySnapshot) }}</p>
                  <small>{{ item.operator }} · {{ formatTime(item.occurredAt) }}</small>
                </li>
              </ol>
            </AsyncState>
            <p v-if="history.length && historyError" class="form-message error-message" role="alert">{{ historyError }}</p>
            <div v-if="nextBeforeVersion" class="panel-footer">
              <button type="button" class="button neutral small" :disabled="historyLoading" @click="loadHistory(true)">
                {{ historyLoading ? '加载中…' : '加载更早记录' }}
              </button>
            </div>
          </section>
        </div>
      </template>
    </AsyncState>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="implementation-decision" @cancel.prevent="!changing && dialog?.close()">
      <form @submit.prevent="act">
        <h2 id="implementation-decision">{{ label }}</h2>
        <p v-if="command === 'submit'">提交后进入待受理状态。</p>
        <p v-else-if="command === 'accept'">受理后进入实施报备初审。</p>
        <p v-else-if="command === 'withdraw'">撤销后释放已占用的变更额度，同一备案接下来 15 天内不能重新提交报备。</p>
        <p v-else-if="command === 'archive'">归档后可在「已归档」视图查询本次实施报备及其审核记录。</p>
        <label v-if="opinionLabel" class="field">
          <span>{{ opinionLabel }}</span>
          <textarea v-model="comment" rows="4" maxlength="1000" :required="opinionRequired" :disabled="changing" />
        </label>
        <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="changing" @click="dialog?.close()">取消</button>
          <button v-if="stale" type="button" class="button neutral" :disabled="loading || historyLoading" @click="reloadAction">重新加载记录</button>
          <button type="submit" class="button primary" :disabled="changing || stale">{{ changing ? '提交中…' : '确认' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.implementation-plan {
  margin: 0;
  padding: 16px 20px;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
</style>

<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { formatTime, materials, reportSubmissionMaterials, type Page } from '../lib/domain'
import {
  filingPackageKind, isFilingWordKind, materialIssueLabels, materialIssuePath, materialIssueResolutions, retryMaterialForm,
  type MaterialCaseKind, type MaterialIssue, type MaterialIssueAudience, type MaterialIssueStatus,
} from '../lib/materialIssues'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

const props = defineProps<{
  audience?: MaterialIssueAudience
  scope: string
  caseKind?: MaterialCaseKind
  selected?: string
  failedList?: boolean
  initialStatus?: MaterialIssueStatus | 'all'
}>()
const emit = defineEmits<{ clearSelection: [] }>()
const status = ref<MaterialIssueStatus | 'all'>(props.initialStatus ?? 'open')
const history = ref<string[]>([])
const page = shallowRef<Page<MaterialIssue>>({ items: [] })
const busy = ref(false)
const failure = ref('')
const actionFailure = ref('')
const notice = ref('')
const dialog = ref<HTMLDialogElement>()
const fileInput = ref<HTMLInputElement>()
const action = shallowRef<{ type: 'retry' | 'discard'; item: MaterialIssue }>()
const changing = ref(false)
const cursor = computed(() => history.value.at(-1))
const referenceQuery = computed(() => props.audience === 'institution' ? { institution: props.scope } : {})
const url = computed(() => materialIssuePath(props.audience, props.scope, {
  id: props.selected, status: status.value, caseKind: props.caseKind, before: cursor.value,
}))
let queryRequest: AbortController | undefined
let writeRequest: AbortController | undefined

/**
 * 用途 在当前业务范围读取材料异常及处置结果。
 * 参数 quiet 为是否在后台刷新以保留正在阅读的列表。
 * 返回值 当前列表的加载任务。
 */
async function load(quiet = false) {
  if (props.audience && !props.scope) return
  queryRequest?.abort()
  const request = new AbortController()
  queryRequest = request
  busy.value = !quiet
  try {
    const result = await api<Page<MaterialIssue>>(url.value, { signal: request.signal })
    if (queryRequest === request && !request.signal.aborted) {
      page.value = result
      failure.value = ''
    }
  } catch (error) {
    if (queryRequest === request && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (queryRequest === request) {
      queryRequest = undefined
      busy.value = false
    }
  }
}

/**
 * 用途 切换材料异常状态筛选并查询第一页。
 * 参数 event 为状态选择框的变更事件。
 * 返回值 无，列表根据新条件刷新。
 */
function chooseStatus(event: Event) {
  history.value = []
  status.value = (event.target as HTMLSelectElement).value as typeof status.value
  notice.value = ''
}

/**
 * 用途 通过服务端游标浏览下一页材料异常。
 * 参数 无。
 * 返回值 无，页码变化后自动读取。
 */
function nextPage() {
  const next = page.value.nextCursor
  if (next && !busy.value) history.value = [...history.value, next]
}

/**
 * 用途 使用浏览时保留的游标返回上页。
 * 参数 无。
 * 返回值 无，页码变化后自动读取。
 */
function previousPage() {
  if (!busy.value) history.value = history.value.slice(0, -1)
}

/**
 * 用途 按业务类别显示异常材料的名称。
 * 参数 item 为材料异常记录。
 * 返回值 用户可识别的业务材料名。
 */
function materialLabel(item: MaterialIssue) {
  return item.caseKind === 'filing' ? materials.filings[item.materialKind] ?? '备案材料'
    : reportSubmissionMaterials[item.materialKind] ?? '测评材料'
}

/**
 * 用途 生成监管或机构可查看的业务详情链接。
 * 参数 item 为包含业务归属的材料异常。
 * 返回值 原业务详情地址，或无访问入口时为空。
 */
function caseLink(item: MaterialIssue) {
  if (!item.caseId || props.audience === 'operator' || item.resolution === 'discarded') return undefined
  const collection = item.caseKind === 'filing' ? 'filings' : 'reports'
  return {
    path: (props.audience === 'institution' ? '/institution' : '') + '/' + collection + '/'
      + encodeURIComponent(item.caseId),
    query: referenceQuery.value,
  }
}

/**
 * 用途 确认所选重发或删除动作并防止处理其他选项。
 * 参数 type 为操作，item 为当前列表中的异常及版本。
 * 返回值 对话框准备并打开的任务。
 */
async function showAction(type: 'retry' | 'discard', item: MaterialIssue) {
  if (changing.value) return
  action.value = { type, item }
  notice.value = actionFailure.value = ''
  if (fileInput.value) fileInput.value.value = ''
  await nextTick()
  if (action.value?.item === item && !dialog.value?.open) dialog.value?.showModal()
}

/**
 * 用途 关闭异常处置弹窗并清除当前选择。
 * 参数 无。
 * 返回值 无。
 */
function closeAction() {
  if (changing.value) return
  dialog.value?.close()
  action.value = undefined
  actionFailure.value = ''
}

/**
 * 用途 发送异常对应的补正材料或清理未经审核的报告草稿。
 * 参数 current 为所选操作及已读取的异常版本，signal 为当前范围的取消信号。
 * 返回值 实际写入完成的任务。
 */
function sendAction(current: NonNullable<typeof action.value>, signal: AbortSignal) {
  const { item, type } = current
  const query = params({ institution: props.scope })
  if (type === 'discard' && item.canDelete && item.caseVersion) {
    return api<void>('/institution/material-issues/' + encodeURIComponent(item.id) + '/discard' + query, {
      method: 'POST', body: { expectedVersion: item.caseVersion }, signal,
    })
  }
  const file = fileInput.value?.files?.[0]
  if (type === 'retry' && item.canRetry && item.caseId && file) {
    const collection = item.caseKind === 'filing' ? 'filings' : 'reports'
    const path = '/institution/' + collection + '/' + encodeURIComponent(item.caseId) + '/attachments'
    return api(path + query, {
      method: 'POST', body: retryMaterialForm(item.materialKind, file, item.id),
      materialKind: item.materialKind, signal,
    })
  }
  return undefined
}

/**
 * 用途 提交对话框中的操作并按服务端最新状态重新读取队列。
 * 参数 无，操作及材料来自当前确认窗口。
 * 返回值 上传、删除及刷新任务。
 */
async function confirmAction() {
  const current = action.value
  if (changing.value || !current || props.audience !== 'institution') return
  const request = new AbortController()
  const task = sendAction(current, request.signal)
  if (!task) return
  writeRequest = request
  changing.value = true
  actionFailure.value = ''
  try {
    await task
    if (!request.signal.aborted) {
      notice.value = current.type === 'discard' ? '未提交的报告草稿已删除。' : '补正材料已登记。'
      dialog.value?.close()
      action.value = undefined
      await load(true)
    }
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    if (error instanceof ApiError && (error.status === 409 || error.status === 404)) {
      dialog.value?.close()
      action.value = undefined
      notice.value = ''
    }
    await load(true)
    if (!request.signal.aborted) actionFailure.value = errorText(error)
  } finally {
    if (writeRequest === request) {
      writeRequest = undefined
      changing.value = false
    }
  }
}

watch(() => [props.audience, props.scope, props.caseKind, props.selected], () => {
  writeRequest?.abort()
  writeRequest = undefined
  changing.value = false
  closeAction()
  notice.value = ''
  history.value = []
})
watch(() => props.initialStatus, value => {
  history.value = []
  status.value = value ?? 'open'
})
watch(url, () => {
  page.value = { items: [] }
  failure.value = ''
  void load()
}, { immediate: true })
refreshBusinessOnFocus(() => {
  if (!queryRequest && !changing.value && !dialog.value?.open) void load(true)
}, 10_000)
onScopeDispose(() => {
  queryRequest?.abort()
  writeRequest?.abort()
})
</script>

<template>
  <section class="panel material-issues" :aria-label="failedList ? '未成功报告列表' : '材料异常预警列表'">
    <header class="v2-list-toolbar" aria-label="材料异常筛选">
      <div class="v2-toolbar-row">
        <label v-if="!selected && !failedList" class="field"><span>处置状态</span>
          <select :value="status" :disabled="changing" @change="chooseStatus">
            <option value="open">待补正</option><option value="closed">已关闭</option><option value="all">全部状态</option>
          </select>
        </label>
        <button v-if="selected" class="button neutral" type="button"
          @click="$emit('clearSelection')">查看全部材料异常</button>
        <button class="button neutral" type="button" :disabled="busy || changing" @click="load()">刷新</button>
        <RouterLink v-if="failedList && scope" class="button neutral"
          :to="{ path: '/institution/alerts', query: { institution: scope, view: 'materials',
            materialStatus: 'closed' } }">查看补正结果</RouterLink>
      </div>
    </header>
    <p v-if="notice" class="form-message success-message issue-message" role="status">{{ notice }}</p>
    <p v-if="actionFailure && !action" class="form-message error-message issue-message" role="alert">{{ actionFailure }}</p>
    <AsyncState :loading="busy && !page.items.length" :error="failure" :empty="!page.items.length"
      :empty-text="failedList ? '暂无未成功的测评报告材料。' : '暂无符合条件的材料异常。'" @retry="load()">
      <div class="table-wrap v2-list-table">
        <table class="data-table">
          <thead>
            <tr><th>业务对象</th><th>材料</th><th>失败原因</th><th>触发时间</th><th>处置状态</th>
              <th v-if="audience !== 'operator'">操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="item in page.items" :key="item.id">
              <td><strong>{{ item.systemName }}</strong>
                <small class="muted issue-line">{{ item.caseKind === 'filing' ? '备案' : '测评报告' }} · {{ item.reference }}</small>
                <small v-if="item.organizationName && !audience" class="muted issue-line">{{ item.organizationName }}</small>
                <small v-if="item.institution && !audience" class="muted issue-line">{{ item.institution }}</small>
              </td>
              <td>{{ materialLabel(item) }}
                <small v-if="item.fileName" class="muted issue-line">{{ item.fileName }}</small>
              </td>
              <td class="issue-reason">{{ item.reason }}</td>
              <td>{{ formatTime(item.openedAt) }}</td>
              <td><span class="status-badge" :class="item.status === 'closed' ? 'status-approved' : 'status-returned'">
                {{ materialIssueLabels[item.status] }}</span>
                <small v-if="item.resolution" class="muted issue-line">{{ materialIssueResolutions[item.resolution] }}</small>
                <small v-if="item.closedAt" class="muted issue-line">{{ formatTime(item.closedAt) }}</small>
              </td>
              <td v-if="audience !== 'operator'" class="issue-actions">
                <RouterLink v-if="caseLink(item)" :to="caseLink(item)!" class="text-action">查看业务</RouterLink>
                <button v-if="item.canRetry && audience === 'institution'" class="text-action"
                  type="button" :disabled="changing" @click="showAction('retry', item)">重发</button>
                <button v-if="item.canDelete && audience === 'institution'" class="text-action issue-delete"
                  type="button" :disabled="changing" @click="showAction('discard', item)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <footer v-if="!selected && (cursor || page.nextCursor)" class="panel-footer pager">
      <span>本页 {{ page.items.length }} 条</span>
      <div>
        <button type="button" class="button neutral small" :disabled="busy || !cursor" @click="previousPage">上一页</button>
        <button type="button" class="button neutral small" :disabled="busy || !page.nextCursor" @click="nextPage">下一页</button>
      </div>
    </footer>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="material-action-title" @cancel.prevent="closeAction">
      <form v-if="action" @submit.prevent="confirmAction">
        <h2 id="material-action-title">{{ action.type === 'retry' ? '重发材料' : '删除未提交的报告草稿' }}</h2>
        <p v-if="action.type === 'retry'">{{ materialLabel(action.item) }} · {{ action.item.reference }}</p>
        <p v-else>确认删除 {{ action.item.reference }}？报告将退出办理列表，监管可查看异常关闭结果。</p>
        <label v-if="action.type === 'retry'" class="field"><span>重新选择材料</span>
          <input ref="fileInput" type="file" :disabled="changing" required
            :accept="action.item.materialKind === filingPackageKind ? '.zip'
              : action.item.caseKind === 'filing' && isFilingWordKind(action.item.materialKind)
                ? '.pdf,.doc,.docx,.xlsx,.zip' : '.pdf,.docx,.xlsx,.zip'">
        </label>
        <p v-if="actionFailure" class="form-message error-message" role="alert">{{ actionFailure }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="changing" @click="closeAction">取消</button>
          <button type="submit" class="button primary" :disabled="changing">
            {{ changing ? '处理中…' : action.type === 'retry' ? '确认重发' : '确认删除' }}
          </button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.material-issues .v2-list-toolbar { border: 0; border-radius: 0; }
.issue-line { display: block; overflow-wrap: anywhere; }
.issue-reason { max-width: 38ch; white-space: normal; overflow-wrap: anywhere; }
.issue-actions > * + * { margin-inline-start: 10px; }
.issue-delete { color: var(--c-danger); }
.issue-message { margin-inline: 16px; }
.decision-dialog .field { margin-top: 12px; }
</style>

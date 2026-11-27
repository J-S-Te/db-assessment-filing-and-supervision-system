<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import SupervisionBatch from '../components/SupervisionBatch.vue'
import SupervisionSiteInspection from '../components/SupervisionSiteInspection.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import { refreshWorkbench } from '../lib/workbench'
import {
  materialLabels, nextBusinessDate, supervisionActionLabels, supervisionDownload, supervisionPath,
  supervisionStates, type Action, type Correction, type Material, type SupervisionFiling,
  type SupervisionPage, type Task, type TaskDetail, type TaskEvent,
} from '../lib/supervision'

const props = defineProps<{ operator?: boolean }>()
const route = useRoute()
const router = useRouter()
const scope = computed(() => props.operator ? queryValue(route.query.organization).trim() : '')
const scopeQuery = computed(() => ({ organization: scope.value || undefined }))
const base = computed(() => (props.operator ? '/operator' : '') + supervisionPath)
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const lead = computed(() => props.operator ? '' : queryValue(route.query.leadAlertId))
const ready = computed(() => !props.operator || !!scope.value)
const detailPath = computed(() => base.value + '/' + encodeURIComponent(id.value))
const organization = ref('')
const filter = ref('')
const items = shallowRef<SupervisionPage<Task>>()
const detail = shallowRef<TaskDetail>()
const history = shallowRef<SupervisionPage<TaskEvent>>()
const busy = ref(false)
const loading = ref(false)
const earlierLoading = ref(false)
const notice = ref('')
const error = ref('')
const failure = ref('')
let currentRead: AbortController | undefined
let earlierRead: AbortController | undefined

const showCreate = ref(false)
const showBatch = ref(false)
const title = ref('')
const deadline = ref('')
const filing = ref('')
const query = ref('')
const options = shallowRef<SupervisionFiling[]>([])
const optionError = ref('')
const searching = ref(false)
let optionRequest: AbortController | undefined
let draftCommit: { signature: string; key: string } | undefined

const dialog = ref<HTMLDialogElement>()
const activeAction = ref<Action>()
const comment = ref('')
const correction = ref<Correction>('both')
const materials = Object.keys(materialLabels) as Material[]
const visibleStates = computed(() => Object.entries(supervisionStates)
  .filter(([status]) => !props.operator || status !== 'draft'))

/**
 * 用途 获取当前页面的列表或细节，切换归属时由监听器清除旧快照。
 * 参数 before 为继续读取任务分页的游标，空值刷新首屏。
 * 返回值 当前加载任务。
 */
async function load(before?: string) {
  if (!ready.value) return
  currentRead?.abort()
  const request = new AbortController()
  currentRead = request
  loading.value = true
  failure.value = ''
  try {
    if (id.value) await loadDetail(request.signal)
    else await loadTasks(request.signal, before)
  } catch (reason) {
    if (!request.signal.aborted && !isAbort(reason)) failure.value = errorText(reason)
  } finally {
    if (currentRead === request) loading.value = false
  }
}

/**
 * 用途 按当前席位、运营单位、状态和游标读取任务。
 * 参数 signal 为取消触发器，before 为后续页游标。
 * 返回值 无，旧请求取消时保留当前页面。
 */
async function loadTasks(signal: AbortSignal, before?: string) {
  const page = await api<SupervisionPage<Task>>(base.value + params({ ...scopeQuery.value,
    status: filter.value || undefined, before }), { signal })
  if (!signal.aborted) items.value = { ...page, items: [...(before ? items.value?.items ?? [] : []), ...page.items] }
}

/**
 * 用途 显示指定任务的材料、当轮操作权限及历史。
 * 参数 signal 为跟随当前页面的取消触发器。
 * 返回值 无，范围切换后不落旧请求的结果。
 */
async function loadDetail(signal: AbortSignal) {
  const suffix = params(scopeQuery.value)
  const [task, events] = await Promise.all([
    api<TaskDetail>(detailPath.value + suffix, { signal }),
    api<SupervisionPage<TaskEvent>>(detailPath.value + '/history' + suffix, { signal }),
  ])
  if (!signal.aborted) {
    detail.value = task
    history.value = events
  }
}

/**
 * 用途 继续加载当前监督任务的历史留痕。
 * 参数 无，从当前历史记录读取游标。
 * 返回值 独立的历史查询任务。
 */
async function loadEarlier() {
  const page = history.value
  if (!page?.nextBefore || earlierLoading.value) return
  const request = new AbortController()
  earlierRead = request
  earlierLoading.value = true
  error.value = ''
  try {
    const next = await api<SupervisionPage<TaskEvent>>(detailPath.value + '/history' + params({
      ...scopeQuery.value, before: page.nextBefore,
    }), { signal: request.signal })
    if (!request.signal.aborted) history.value = { ...next, items: [...page.items, ...next.items] }
  } catch (reason) {
    if (!isAbort(reason)) error.value = errorText(reason)
  } finally {
    if (earlierRead === request) earlierLoading.value = false
  }
}

/**
 * 用途 切换所选运营单位并进入其监督检查任务列表。
 * 参数 无，从范围表单读取名称。
 * 返回值 页面导航任务。
 */
function chooseOrganization() {
  return router.push({ path: base.value, query: { organization: organization.value.trim() } })
}

/**
 * 用途 用已登记备案或当前超期线索提供真实收件候选。
 * 参数 无，从当前监管页面取搜索条件。
 * 返回值 候选查询任务。
 */
async function loadOptions() {
  optionRequest?.abort()
  const request = new AbortController()
  optionRequest = request
  optionError.value = ''
  searching.value = true
  try {
    const result = await api<SupervisionPage<SupervisionFiling>>(supervisionPath + '/filing-options' +
      params({ q: query.value.trim(), leadAlertId: lead.value || undefined }), { signal: request.signal })
    if (request.signal.aborted) return
    options.value = result.items
    if (!result.items.some(item => item.id === filing.value)) {
      filing.value = result.items.length === 1 ? result.items[0]?.id ?? '' : ''
    }
  } catch (reason) {
    if (!isAbort(reason)) optionError.value = errorText(reason)
  } finally {
    if (optionRequest === request) searching.value = false
  }
}

/**
 * 用途 打开监管创建任务区并查找最初的真实备案候选。
 * 参数 无。
 * 返回值 无。
 */
function openCreate() {
  showCreate.value = true
  error.value = ''
  void loadOptions()
}

/**
 * 用途 创建监督检查草稿，相同内容重试复用同一请求标识。
 * 参数 无，从创建表单和可选超期线索读取。
 * 返回值 无，成功时导航到可上传模板的任务详情。
 */
async function createTask() {
  if (busy.value || !filing.value || !title.value.trim() || !deadline.value) return
  const payload = { filingId: filing.value, title: title.value.trim(), dueOn: deadline.value, leadAlertId: lead.value || undefined }
  const signature = JSON.stringify(payload)
  if (draftCommit?.signature !== signature) draftCommit = { signature, key: crypto.randomUUID() }
  const task = await withMutation(() => api<Task>(supervisionPath, {
    method: 'POST', body: { ...payload, requestId: draftCommit?.key },
  }))
  if (task) {
    draftCommit = undefined
    showCreate.value = false
    await router.push({ path: base.value + '/' + task.id })
  }
}

/**
 * 用途 为创建、上传和审核操作共享忙碌状态与可读错误。
 * 参数 action 为一次经过席位约束的异步写入。
 * 返回值 成功响应或在错误时为空。
 */
async function withMutation<T>(action: () => Promise<T>): Promise<T | undefined> {
  if (busy.value) return undefined
  busy.value = true
  error.value = ''
  notice.value = ''
  currentRead?.abort()
  try {
    return await action()
  } catch (reason) {
    if (!isAbort(reason)) error.value = errorText(reason)
    if (reason instanceof ApiError && reason.status === 409 && id.value) void load()
  } finally {
    busy.value = false
  }
  return undefined
}

/**
 * 用途 上传被选类别的模板或结果文件，仅传当前任务版本和材料。
 * 参数 event 为文件控件事件，kind 为用户选定的栏位。
 * 返回值 上传及之后的详情刷新任务。
 */
async function uploadFile(event: Event, kind: Material) {
  const control = event.target as HTMLInputElement
  const file = control.files?.[0]
  const task = detail.value
  control.value = ''
  if (!file || !task) return
  if (!/\.(pdf|docx)$/i.test(file.name) || !file.size || file.size > 20 * 1024 * 1024) {
    error.value = '请选择非空且不超过 20 MiB 的 PDF 或 DOCX 材料。'
    return
  }
  const body = new FormData()
  body.set('file', file)
  body.set('kind', kind)
  body.set('expectedVersion', String(task.version))
  const response = await withMutation(() => api<{version: number}>(detailPath.value + '/attachments' +
    params(scopeQuery.value), { method: 'POST', body }))
  if (response) {
    notice.value = materialLabels[kind] + '已保存。'
    await load()
  }
}

/**
 * 用途 打开确认框，明确本次状态变化或需要的补正说明。
 * 参数 action 为任务当前允许的操作。
 * 返回值 无。
 */
function confirm(action: Action) {
  activeAction.value = action
  error.value = ''
  comment.value = ''
  correction.value = 'both'
  dialog.value?.showModal()
}

/**
 * 用途 在用户确认后提交带版本的业务动作并更新两端状态。
 * 参数 无，从当前确认框取操作和可选补正字段。
 * 返回值 已关闭确认框并刷新详情的任务。
 */
async function sendAction() {
  const action = activeAction.value
  const task = detail.value
  if (!action || !task) return
  const response = await withMutation(() => api<{version: number}>(detailPath.value + '/actions' +
    params(scopeQuery.value), { method: 'POST', body: {
      action, expectedVersion: task.version, comment: comment.value.trim() || undefined,
      correctionKind: action === 'return' ? correction.value : undefined,
    } }))
  if (response) {
    dialog.value?.close()
    notice.value = supervisionActionLabels[action] + '成功。'
    await load()
  }
}

/**
 * 用途 判断当前任务和角色是否可以上传指定类别。
 * 参数 kind 为材料类型。
 * 返回值 可编辑并且属于当前角色时为真。
 */
function canEdit(kind: Material) {
  const template = kind === 'inspection_template' || kind === 'summary_template'
  return Boolean(detail.value?.allowedActions.includes('upload') && Boolean(props.operator) !== template)
}

/**
 * 用途 为绑定当前任务的附件形成最小范围的同源 URL。
 * 参数 file 为附件标识。
 * 返回值 当前权限范围的附件地址。
 */
function fileURL(file: string) {
  return supervisionDownload(detailPath.value, file, scope.value || undefined)
}

/**
 * 用途 现场动作生效后同步任务详情以及监管顶栏共用的待办数据。
 * 参数 无。
 * 返回值 当前监督详情刷新任务。
 */
async function refreshAfterSiteChange() {
  if (!props.operator) void refreshWorkbench()
  await load()
}

watch([scope, id, lead, () => props.operator], () => {
  dialog.value?.close()
  currentRead?.abort()
  earlierRead?.abort()
  optionRequest?.abort()
  organization.value = scope.value
  detail.value = undefined
  history.value = undefined
  items.value = undefined
  error.value = ''
  failure.value = ''
  notice.value = ''
  showCreate.value = !!lead.value && !id.value
  showBatch.value = false
  if (showCreate.value) void loadOptions()
  void load()
}, { immediate: true })
watch(filter, () => { items.value = undefined; void load() })
refreshBusinessOnFocus(() => {
  if (!loading.value && !busy.value && !dialog.value?.open && (history.value?.items.length ?? 0) <= 20 &&
    (items.value?.items.length ?? 0) <= 20) void load()
}, 30_000)
onScopeDispose(() => { currentRead?.abort(); earlierRead?.abort(); optionRequest?.abort() })
</script>

<template>
  <section class="v2-list supervision" aria-label="监督检查">
    <header class="v2-page-head supervision-head">
      <h1>{{ operator ? '监督检查自查' : '监督检查任务' }}</h1>
      <RouterLink v-if="id" class="button neutral" :to="{ path: base, query: scopeQuery }">返回任务列表</RouterLink>
      <div v-else-if="!operator && !showCreate && !showBatch" class="supervision-actions">
        <button type="button" class="button primary" @click="openCreate">新建监督检查</button>
        <button type="button" class="button neutral" @click="showBatch = true">批量下发自查</button>
      </div>
    </header>
    <SupervisionBatch v-if="showBatch && !operator && !id" @close="showBatch = false" @published="load()" />

    <form v-if="operator && !id" class="v2-list-toolbar" @submit.prevent="chooseOrganization">
      <div class="v2-toolbar-row"><label class="field filter-keyword"><span>运营单位全称</span>
        <input v-model="organization" maxlength="200" required placeholder="请输入运营单位全称">
      </label><button class="button primary" type="submit">查看自查任务</button></div>
    </form>

    <form v-if="showCreate && !operator && !id" class="panel supervision-panel" @submit.prevent="createTask">
      <h2>新建监督检查</h2>
      <p v-if="lead" class="muted">从当前未关闭的超期线索核对所属备案。</p>
      <div v-else class="supervision-fields">
        <label class="field"><span>查找有效备案</span><input v-model="query" type="search" maxlength="120"
          placeholder="输入备案编号、系统或单位"></label>
        <button class="button neutral" type="button" :disabled="searching || busy" @click="loadOptions">查询备案</button>
      </div>
      <p v-if="optionError" class="form-message error-message" role="alert">{{ optionError }}</p>
      <label class="field"><span>关联系统和收件单位</span>
        <select v-model="filing" required :disabled="searching || busy">
          <option value="">{{ searching ? '正在匹配' : '请选择有效备案' }}</option>
          <option v-for="item in options" :key="item.id" :value="item.id">
            {{ item.systemName }} · {{ item.organizationName }} · {{ item.number }}
          </option>
        </select>
      </label>
      <p v-if="!searching && !optionError && !options.length" class="muted">当前没有符合条件的有效备案。</p>
      <div class="supervision-fields">
        <label class="field"><span>任务标题</span><input v-model="title" maxlength="200" required></label>
        <label class="field"><span>自查截止日期</span><input v-model="deadline" type="date" :min="nextBusinessDate" required></label>
      </div>
      <div class="supervision-actions">
        <RouterLink v-if="lead" class="button neutral" :to="{ path: '/alerts', query: { view: 'supervision' } }">返回监督线索</RouterLink>
        <button v-else class="button neutral" type="button" @click="showCreate = false">取消新建</button>
        <button class="button primary" type="submit" :disabled="busy || !filing">保存草稿</button>
      </div>
    </form>

    <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
    <p v-if="error && !activeAction" class="form-message error-message" role="alert">{{ error }}</p>
    <template v-if="ready && !id">
      <div class="v2-list-toolbar"><div class="v2-toolbar-row">
        <label class="field"><span>任务状态</span><select v-model="filter">
          <option value="">全部状态</option>
          <option v-for="[value, name] in visibleStates" :key="value" :value="value">{{ name }}</option>
        </select></label>
        <button class="button neutral" type="button" :disabled="loading" @click="load()">刷新</button>
      </div></div>
      <AsyncState :loading="loading && !items" :error="failure" :empty="!items?.items.length"
        empty-text="暂无监督检查任务。" @retry="load()">
        <div class="panel table-wrap">
          <table class="data-table">
            <thead><tr><th scope="col">任务</th><th scope="col">系统 / 收件单位</th>
              <th scope="col">自查截止</th><th scope="col">状态</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="item in items?.items" :key="item.id">
              <td>{{ item.title }}</td><td>{{ item.systemName }}<br><small class="muted">{{ item.organizationName }}</small></td>
              <td>{{ item.dueOn }}</td><td>{{ supervisionStates[item.status] }}</td>
              <td><RouterLink class="text-action" :to="{ path: base + '/' + item.id, query: scopeQuery }">查看任务</RouterLink></td>
            </tr></tbody>
          </table>
        </div>
      </AsyncState>
      <button v-if="items?.nextBefore" class="button neutral" type="button" :disabled="loading"
        @click="load(items?.nextBefore)">更多任务</button>
    </template>

    <AsyncState v-if="id && ready" :loading="loading && !detail" :error="failure" @retry="load()">
      <template v-if="detail">
        <article class="panel supervision-panel">
          <header><h2>{{ detail.title }}</h2></header>
          <dl class="facts">
            <div><dt>备案系统</dt><dd>{{ detail.systemName }} · {{ detail.filingNumber }}</dd></div>
            <div><dt>收件单位</dt><dd>{{ detail.organizationName }}</dd></div>
            <div><dt>自查截止</dt><dd>{{ detail.dueOn }}</dd></div>
            <div><dt>办理状态</dt><dd>{{ supervisionStates[detail.status] }}</dd></div>
            <div v-if="detail.rectification"><dt>关联备案整改</dt>
              <dd>未销项 {{ detail.rectification.open }} 项，已销项 {{ detail.rectification.closed }} 项</dd></div>
            <div v-if="detail.correctionKind"><dt>本轮需补正</dt>
              <dd>{{ detail.correctionKind === 'both' ? '两项材料' : materialLabels[detail.correctionKind] }}</dd></div>
          </dl>
        </article>
        <SupervisionSiteInspection :path="detailPath" :organization="scope || undefined"
          :task-version="detail.version" :focus-section="queryValue(route.query.section) === 'site'"
          @changed="refreshAfterSiteChange" />
        <section class="panel supervision-panel" aria-label="任务材料">
          <h2>任务材料</h2>
          <div v-for="kind in materials" :key="kind" class="supervision-material">
            <div><strong>{{ materialLabels[kind] }}</strong>
              <template v-for="file in detail.attachments.filter(item => item.kind === kind)" :key="file.id">
                <br><a class="text-action" :href="fileURL(file.id)" download>{{ file.fileName }}</a>
              </template>
              <p v-if="!detail.attachments.some(item => item.kind === kind)" class="muted">待上传</p>
            </div>
            <label v-if="canEdit(kind)" class="field"><span>{{ '上传' + materialLabels[kind] }}</span>
              <input type="file" :name="kind" accept=".pdf,.docx" :disabled="busy" @change="uploadFile($event, kind)">
            </label>
          </div>
          <div v-if="detail.allowedActions.some(item => item !== 'upload')" class="supervision-actions">
            <template v-for="action in detail.allowedActions" :key="action">
              <button v-if="action !== 'upload'" type="button" :disabled="busy" class="button"
                :class="action === 'cancel' ? 'neutral' : 'primary'" @click="confirm(action)">
                {{ supervisionActionLabels[action] }}
              </button>
            </template>
          </div>
        </section>

        <section class="panel supervision-panel" aria-label="办理留痕">
          <h2>办理留痕</h2>
          <ol class="supervision-history">
            <li v-for="event in history?.items" :key="event.version">
              <strong>{{ event.action === 'create' ? '创建草稿' : supervisionActionLabels[event.action] }}</strong>
              <small class="muted">{{ formatTime(event.createdAt) }} · {{ event.actor }}</small>
              <p v-if="event.comment">{{ event.comment }}</p>
              <div v-if="event.materials" class="supervision-links">
                <a v-for="file in event.materials" :key="file.id" class="text-action" :href="fileURL(file.id)" download>
                  下载当轮{{ materialLabels[file.kind] }}
                </a>
              </div>
            </li>
          </ol>
          <button v-if="history?.nextBefore" class="button neutral" type="button" :disabled="earlierLoading"
            @click="loadEarlier">查看早期留痕</button>
        </section>
      </template>
    </AsyncState>

    <dialog ref="dialog" class="decision-dialog" aria-labelledby="supervision-confirmation"
      @close="activeAction = undefined" @cancel.prevent="!busy && dialog?.close()">
      <form v-if="activeAction" class="supervision-panel" @submit.prevent="sendAction">
        <h2 id="supervision-confirmation">{{ supervisionActionLabels[activeAction] }}</h2>
        <p v-if="activeAction === 'publish'">确认后向所选单位下发任务和模板，并同步到站内消息及待办。</p>
        <p v-else-if="activeAction === 'submit'">确认本次自查表、整改汇总和关联备案信息，提交监管审查。</p>
        <label v-if="activeAction === 'return'" class="field"><span>要求补正的材料</span>
          <select v-model="correction"><option value="both">两项材料</option>
            <option value="inspection_form">监督检查自查表</option><option value="rectification_summary">整改情况汇总表</option>
          </select>
        </label>
        <label v-if="['return','approve','cancel'].includes(activeAction)" class="field"><span>办理说明</span>
          <textarea v-model="comment" maxlength="1000" rows="3" :required="activeAction !== 'approve'"></textarea>
        </label>
        <p v-if="error" class="form-message error-message" role="alert">{{ error }}</p>
        <div class="supervision-actions">
          <button class="button neutral" type="button" :disabled="busy" @click="dialog?.close()">取消</button>
          <button class="button primary" type="submit" :disabled="busy">确认{{ supervisionActionLabels[activeAction] }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.supervision { display: grid; gap: 12px; }
.supervision-head { display: flex; align-items: center; justify-content: space-between; }
.supervision-panel { display: grid; gap: 12px; padding: 16px; }
.supervision-panel h2, .supervision-panel p { margin: 0; }
.supervision-fields, .supervision-material, .supervision-actions, .supervision-links {
  display: flex; align-items: end; flex-wrap: wrap; gap: 12px;
}
.supervision-fields .field { flex: 1; min-width: min(15em, 100%); }
.supervision-material { justify-content: space-between; align-items: center; padding: .65em 0; border-bottom: 1px solid #dee6ef; }
.supervision-material .field { max-width: 100%; }
.supervision-history { display: grid; gap: 10px; padding-inline-start: 1.4rem; margin: 0; }
.supervision-history small { margin-inline-start: .7em; }
.supervision-links { margin-top: .3em; }
</style>

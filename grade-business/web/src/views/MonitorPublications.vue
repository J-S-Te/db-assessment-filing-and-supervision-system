<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import PublicationAlertPicker from '../components/PublicationAlertPicker.vue'
import PublicationAttachmentLinks from '../components/PublicationAttachmentLinks.vue'
import PublicationDeliveryReceipts from '../components/PublicationDeliveryReceipts.vue'
import PublicationRecipientPicker from '../components/PublicationRecipientPicker.vue'
import V2RouteDialog from '../components/V2RouteDialog.vue'
import { alertPath } from '../lib/alerts'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { formatSize, formatTime } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { publicationAttachmentForm, publicationAttachmentSelectionError, publicationAudiences,
  publicationDraftPayload, recipientLabels, type PublicationAttachment } from '../lib/publicationDelivery'
import { isPublicationMaterial, publicationActions, publicationPath, publicationStatuses, publicationTypes, usePublicationList,
  type PublicationAction, type PublicationCategory, type PublicationContent, type PublicationDetail,
  type PublicationPage, type PublicationRevision } from '../lib/publications'

const { page, category, status, keyword, loading, failure, cursors, load, search, nextPage, previousPage }
  = usePublicationList(publicationPath)
const route = useRoute()
const router = useRouter()
const current = shallowRef<PublicationDetail>()
const form = ref<PublicationContent>({ category: 'bulletin', title: '', publisher: '', body: '', pinned: false })
const draftMaterial = computed(() => isPublicationMaterial(form.value.category))
const publishFileMissing = computed(() => current.value && isPublicationMaterial(current.value.category)
  && !current.value.attachments?.length)
const targets = computed({
  get: () => form.value.targets ?? [],
  set: value => { form.value.targets = value },
})
const mode = ref<'detail' | 'edit' | ''>('')
const receiptsShown = ref(false)
const confirmation = ref<PublicationAction>()
const queuedFiles = shallowRef<File[]>([])
const queuedFileError = computed(() => publicationAttachmentSelectionError(queuedFiles.value))
const removal = shallowRef<PublicationAttachment>()
const detailLoading = ref(false)
const busy = ref(false)
const dialogError = ref('')
const versionConflict = ref(false)
const notice = ref('')
const history = shallowRef<PublicationRevision[]>([])
const historyShown = ref(false)
const historyNext = ref('')
const historyLoading = ref(false)
const historyError = ref('')
const publishedVersions = computed(() => history.value.filter(item => item.action === 'publish').map(item => item.version))
let detailRequest: AbortController | undefined
let saveRequest: AbortController | undefined
let historyRequest: AbortController | undefined
let linkedID = ''
let savedContent = ''

/**
 * 用途 清理当前弹窗的读取和写入任务，避免回填至另一个公告。
 * 参数 无。
 * 返回值 无。
 */
function resetDialog() {
  detailRequest?.abort()
  historyRequest?.abort()
  saveRequest?.abort()
  saveRequest = undefined
  busy.value = false
  mode.value = ''
  current.value = undefined
  receiptsShown.value = false
  confirmation.value = undefined
  queuedFiles.value = []
  removal.value = undefined
  savedContent = ''
  dialogError.value = ''
  versionConflict.value = false
  historyShown.value = false
  history.value = []
  historyNext.value = ''
  historyError.value = ''
  historyLoading.value = false
}

/**
 * 用途 关闭弹窗并清理公告深链，保留列表筛选。
 * 参数 无。
 * 返回值 无。
 */
function closeDialog() {
  if (busy.value) return
  resetDialog()
  linkedID = ''
  if (route.query.publication !== undefined) {
    void router.replace({ query: { ...route.query, publication: undefined } })
  }
}

/**
 * 用途 起草指定类型的全新站内信息。
 * 参数 type 为用户在工具栏所选的信息类型。
 * 返回值 无。
 */
function createDraft(type: PublicationCategory) {
  closeDialog()
  form.value = { category: type, title: '', publisher: '', body: '', pinned: false,
    audience: isPublicationMaterial(type) ? 'operator' : 'all' }
  mode.value = 'edit'
}

/**
 * 用途 将监管详情中的实际版本放进修订表单。
 * 参数 无，使用已经读取的当前记录。
 * 返回值 无。
 */
function editCurrent() {
  if (!current.value) return
  const { category, title, publisher, body, pinned, alertId, audience, targets } = current.value
  form.value = { category, title, publisher, body, pinned, alertId, audience: audience ?? 'all', targets }
  savedContent = JSON.stringify(publicationDraftPayload(form.value))
  receiptsShown.value = false
  dialogError.value = ''
  mode.value = 'edit'
}

/**
 * 用途 记录浏览器选择的一批文件，允许重新选择同名文件。
 * 参数 event 为原生多文件控件的变更事件。
 * 返回值 无，所选文件及校验结果直接显示在表单中。
 */
function chooseAttachments(event: Event) {
  const input = event.target as HTMLInputElement
  queuedFiles.value = Array.from(input.files ?? [])
  input.value = ''
  if (!versionConflict.value) dialogError.value = ''
}

/**
 * 用途 从尚未上传的批次移除一份文件。
 * 参数 index 为本地所选文件在批次中的位置。
 * 返回值 无。
 */
function unqueueAttachment(index: number) {
  queuedFiles.value = queuedFiles.value.filter((_, currentIndex) => currentIndex !== index)
}

/**
 * 用途 在版本冲突后装载服务端新正文和附件清单。
 * 参数 无，使用当前公告及正在编辑的本地文件。
 * 返回值 重载任务，未提交的选择在成功重载后仍可上传。
 */
async function reloadCurrent() {
  const id = current.value?.id
  if (!id || busy.value) return
  const request = new AbortController()
  saveRequest = request
  busy.value = true
  const editing = mode.value === 'edit'
  try {
    const latest = await api<PublicationDetail>(publicationPath + '/' + encodeURIComponent(id), {
      signal: request.signal,
    })
    if (request.signal.aborted) return
    current.value = latest
    removal.value = undefined
    confirmation.value = undefined
    dialogError.value = notice.value = ''
    versionConflict.value = false
    if (editing) editCurrent()
  } catch (error) {
    if (!request.signal.aborted) dialogError.value = errorText(error)
  } finally {
    if (saveRequest === request) busy.value = false
  }
}

/**
 * 用途 加载指定公告的最新正文和可执行操作。
 * 参数 id 为公告标识，editing 表示进入版本编辑，receipts 表示查看送达回执。
 * 返回值 详情读取任务。
 */
async function openItem(id: string, editing = false, receipts = false) {
  resetDialog()
  mode.value = 'detail'
  detailLoading.value = true
  const request = new AbortController()
  detailRequest = request
  try {
    const item = await api<PublicationDetail>(publicationPath + '/' + encodeURIComponent(id), { signal: request.signal })
    if (request.signal.aborted) return
    current.value = item
    if (editing) editCurrent()
    if (receipts) showReceipts()
  } catch (error) {
    if (!isAbort(error)) dialogError.value = errorText(error)
  } finally {
    if (detailRequest === request) detailLoading.value = false
  }
}

/**
 * 用途 读取当前公告版本留痕，可按实际游标追加更早记录。
 * 参数 before 为可选历史游标，reveal 表示打开正文中的留痕面板。
 * 返回值 历史查询任务。
 */
async function loadHistory(before = '', reveal = true) {
  if (!current.value) return
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  historyLoading.value = true
  if (reveal) historyShown.value = true
  historyError.value = ''
  try {
    const path = publicationPath + '/' + encodeURIComponent(current.value.id) + '/history'
    const data = await api<PublicationPage<PublicationRevision>>(path + params({ before }), { signal: request.signal })
    if (request.signal.aborted) return
    history.value = before ? [...history.value, ...data.items] : data.items
    historyNext.value = data.nextBefore ?? ''
  } catch (error) {
    if (!isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === request) historyLoading.value = false
  }
}

/**
 * 用途 打开送达回执并载入已发布版本供查询。
 * 参数 无。
 * 返回值 无。
 */
function showReceipts() {
  receiptsShown.value = true
  if (!history.value.length && !historyLoading.value) void loadHistory('', false)
}

/**
 * 用途 先保存草稿内容，再对同一预期版本原子上传公文。
 * 参数 request 为当前写入任务的取消控制器。
 * 返回值 写入后的公告，取消时为空。
 */
async function saveDraft(request: AbortController) {
  const fingerprint = JSON.stringify(publicationDraftPayload(form.value))
  let saved = current.value
  if (!saved || savedContent !== fingerprint) {
    const prior = saved
    const path = prior ? publicationPath + '/' + encodeURIComponent(prior.id) : publicationPath
    saved = await api<PublicationDetail>(path, {
      method: prior ? 'PATCH' : 'POST', body: publicationDraftPayload(form.value, prior?.version),
      signal: request.signal,
    })
    if (request.signal.aborted) return
    current.value = saved
    savedContent = fingerprint
  }
  if (!queuedFiles.value.length) return saved

  notice.value = '正文已保存。'
  const path = publicationPath + '/' + encodeURIComponent(saved.id) + '/attachments'
  const changed = await api<PublicationDetail>(path, {
    method: 'POST', body: publicationAttachmentForm(saved.version, queuedFiles.value), signal: request.signal,
  })
  if (request.signal.aborted) return
  queuedFiles.value = []
  return changed
}

/**
 * 用途 展示真实写入失败，需重新确认的过期版本会阻止继续写入。
 * 参数 error 为保存、发布或移除返回的异常。
 * 返回值 版本过期时刷新监管列表的任务。
 */
async function showWriteError(error: unknown) {
  if (isAbort(error)) return
  confirmation.value = undefined
  dialogError.value = errorText(error)
  if (error instanceof ApiError && error.status === 409 && error.code === 'version_conflict') {
    versionConflict.value = true
    await load()
  }
}

/**
 * 用途 保存草稿或确认发布、撤回，并同步页面展示的真实状态。
 * 参数 action 为保存修订或用户已确认的公开状态动作。
 * 返回值 保存及列表刷新的任务。
 */
async function persist(action: 'save' | PublicationAction) {
  if (busy.value || versionConflict.value || removal.value) return
  const item = current.value
  const savingDraft = action === 'save'
  if (savingDraft ? queuedFileError.value : !item) return
  const request = new AbortController()
  saveRequest = request
  dialogError.value = notice.value = ''
  busy.value = true
  try {
    const saved = savingDraft ? await saveDraft(request)
      : item && await api<PublicationDetail>(publicationPath + '/' + encodeURIComponent(item.id) + '/actions', {
        method: 'POST', body: { action, expectedVersion: item.version }, signal: request.signal,
      })
    if (!saved || request.signal.aborted) return
    current.value = saved
    mode.value = 'detail'
    confirmation.value = undefined
    notice.value = savingDraft ? '草稿已保存。' : action === 'publish' ? '站内信息已发布。' : '站内信息已撤回。'
    await Promise.all([load(), historyShown.value ? loadHistory() : undefined])
  } catch (error) {
    if (request.signal.aborted) return
    await showWriteError(error)
    if (!versionConflict.value && savingDraft && item?.version !== current.value?.version) await load()
  } finally {
    if (saveRequest === request) busy.value = false
  }
}

/**
 * 用途 从当前待发布修订移除一份已保存附件，保留正在编辑的表单。
 * 参数 无，由确认区提供当前版本及待移除项。
 * 返回值 更新附件并刷新公告列表的任务。
 */
async function removeAttachment() {
  const item = current.value
  const file = removal.value
  if (busy.value || versionConflict.value || !item || !file) return
  const request = new AbortController()
  saveRequest = request
  busy.value = true
  dialogError.value = notice.value = ''
  const path = publicationPath + '/' + encodeURIComponent(item.id) + '/attachments/' + encodeURIComponent(file.id)
  try {
    const changed = await api<PublicationDetail>(path, {
      method: 'DELETE', body: { expectedVersion: item.version }, signal: request.signal,
    })
    if (request.signal.aborted) return
    current.value = changed
    removal.value = undefined
    notice.value = '附件已从当前修订移除。'
    await load()
  } catch (error) {
    if (!request.signal.aborted) await showWriteError(error)
  } finally {
    if (saveRequest === request) busy.value = false
  }
}

watch(() => queryValue(route.query.publication), (id) => {
  if (id) void openItem(id)
  else if (linkedID) resetDialog()
  linkedID = id
}, { immediate: true })
onScopeDispose(() => { detailRequest?.abort(); saveRequest?.abort(); historyRequest?.abort() })
</script>

<template>
  <section class="v2-list" aria-label="信息发布">
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <button class="button primary" type="button" @click="createDraft('bulletin')">＋ 发布通报</button>
        <button class="button neutral" type="button" @click="createDraft('security_advisory')">＋ 安全预警公告</button>
        <button class="button neutral" type="button" @click="createDraft('announcement')">＋ 通知公告</button>
        <button class="button neutral" type="button" @click="createDraft('policy_standard')">＋ 政策资料</button>
        <span class="v2-toolbar-spacer"></span>
        <label class="field"><span>信息类型</span><select v-model="category">
          <option value="">全部类型</option>
          <option v-for="(title, key) in publicationTypes" :key="key" :value="key">{{ title }}</option>
        </select></label>
        <label class="field"><span>发布状态</span><select v-model="status">
          <option value="">全部状态</option>
          <option v-for="(title, key) in publicationStatuses" :key="key" :value="key">{{ title }}</option>
        </select></label>
        <label class="field filter-keyword"><span>关键词</span><input v-model="keyword" type="search" maxlength="120"></label>
        <button class="button primary" type="submit">查询</button>
      </div>
    </form>
    <p v-if="notice && !mode" class="form-message" role="status">{{ notice }}</p>
    <div class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length" empty-text="未找到符合条件的信息。" @retry="load()">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>标题</th><th>类型</th><th>发布单位</th><th>发布时间</th><th>状态</th><th>操作</th></tr></thead>
            <tbody><tr v-for="item in page.items" :key="item.id">
              <td class="emphasis">{{ item.title }}<small v-if="item.pinned" class="muted"> · 置顶</small></td>
              <td>{{ publicationTypes[item.category] }}</td><td>{{ item.publisher }}</td>
              <td>{{ item.publishedAt ? formatTime(item.publishedAt) : '—' }}</td>
              <td><span class="status-badge" :class="item.status === 'published' ? 'status-approved' : 'status-draft'">
                {{ publicationStatuses[item.status] }}
              </span><small v-if="item.publishedVersion && item.version !== item.publishedVersion" class="muted revision-note">有未发布修订</small></td>
              <td class="row-actions">
                <button class="text-action" type="button" @click="openItem(item.id)">查看</button>
                <button class="text-action" type="button" @click="openItem(item.id, true)">编辑</button>
                <button v-if="item.publishedVersion || item.status !== 'draft'" class="text-action" type="button"
                  @click="openItem(item.id, false, true)">送达回执</button>
              </td>
            </tr></tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="!loading && !failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div><button class="button neutral small" type="button" :disabled="cursors.length < 2" @click="previousPage">上一页</button>
          <button class="button neutral small" type="button" :disabled="!page.nextBefore" @click="nextPage">下一页</button></div>
      </footer>
    </div>

    <V2RouteDialog v-if="mode" :label="mode === 'edit' ? '编辑站内信息' : '站内信息详情'" :width="720" @close="closeDialog">
      <div class="dialog-content">
        <h2>{{ mode === 'edit' ? (current ? '修订站内信息' : '起草站内信息') : current?.title ?? '站内信息详情' }}</h2>
        <p v-if="dialogError" class="form-message error-message" role="alert">
          {{ dialogError }} <button v-if="versionConflict && current" class="text-action" type="button"
            :disabled="busy" @click="reloadCurrent">{{ mode === 'edit' ? '载入最新正文并保留待上传文件' : '查看服务端最新版本' }}</button>
        </p>
        <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
        <form v-if="mode === 'edit'" class="edit-form" @submit.prevent="persist('save')">
          <label class="field"><span>信息类型</span><select v-model="form.category" :disabled="busy" required>
            <option v-for="(title, key) in publicationTypes" :key="key" :value="key">{{ title }}</option>
          </select></label>
          <label class="field"><span>发布单位</span><input v-model="form.publisher" maxlength="120" :disabled="busy" required></label>
          <label class="field wide"><span>标题</span><input v-model="form.title" maxlength="200" :disabled="busy" required></label>
          <label class="field wide"><span>发布范围</span><select v-model="form.audience" :disabled="busy">
            <option v-for="(label, key) in publicationAudiences" :key="key" :value="key">{{ label }}</option>
          </select></label>
          <PublicationRecipientPicker v-if="form.audience === 'selected'" v-model="targets" class="wide" :disabled="busy" />
          <PublicationAlertPicker v-if="form.category === 'security_advisory'" class="wide"
            :model-value="form.alertId ?? ''" :disabled="busy" @update:model-value="form.alertId = $event || undefined" />
          <label class="field wide"><span>正文</span><textarea v-model="form.body" rows="7" maxlength="8000" :disabled="busy" required /></label>
          <div class="wide attachment-editor">
            <PublicationAttachmentLinks v-if="current" :id="current.id" :version="current.version"
              :attachments="current.attachments" removable :disabled="busy || versionConflict || Boolean(removal)"
              @remove="removal = $event" />
            <div v-if="removal" class="confirm-panel" aria-label="确认移除附件">
              <p>移除《{{ removal.fileName }}》会生成新修订，历史版本保留该附件。</p>
              <button class="button neutral small" type="button" :disabled="busy" @click="removal = undefined">取消移除</button>
              <button class="button primary small" type="button" :disabled="busy || versionConflict"
                @click="removeAttachment">确认移除</button>
            </div>
            <label class="button neutral publication-file-chooser">
              选择公文附件
              <input class="publication-file-input" type="file" accept=".pdf,.docx" multiple
                :disabled="busy || Boolean(removal)" @change="chooseAttachments">
            </label>
            <p class="muted attachment-hint">支持 PDF、DOCX。每份限 20 MB，每个版本最多 8 份、合计 40 MB。</p>
            <p v-if="draftMaterial" class="muted attachment-hint">发布政策标准或常用资料前，至少要保存一份有效附件。</p>
            <p v-if="queuedFileError" class="form-message error-message" role="alert">{{ queuedFileError }}</p>
            <ul v-if="queuedFiles.length" class="queued-files" aria-label="待上传附件">
              <li v-for="(file, index) in queuedFiles" :key="index">
                <span>{{ file.name }} <small class="muted">{{ formatSize(file.size) }}</small></span>
                <button class="text-action" type="button" :disabled="busy" :aria-label="'取消选择 ' + file.name"
                  @click="unqueueAttachment(index)">取消选择</button>
              </li>
            </ul>
          </div>
          <label class="wide pin-option"><input v-model="form.pinned" type="checkbox" :disabled="busy"> 置顶展示</label>
          <div class="dialog-actions wide">
            <button class="button neutral" type="button" :disabled="busy" @click="closeDialog">取消</button>
            <button class="button primary" type="submit"
              :disabled="busy || versionConflict || Boolean(removal || queuedFileError) || (form.audience === 'selected' && !targets.length)">
              {{ busy ? '正在保存…' : '保存草稿' }}
            </button>
          </div>
        </form>
        <p v-else-if="detailLoading" role="status">正在读取公告…</p>
        <template v-else-if="current">
          <p class="muted">{{ publicationTypes[current.category] }} · {{ current.publisher }} · {{ publicationStatuses[current.status] }}
            · 当前版本 {{ current.version }}<span v-if="current.publishedVersion"> · 已发布版本 {{ current.publishedVersion }}</span></p>
          <div v-if="current.publishedVersion || current.status !== 'draft'" class="detail-view-tabs" aria-label="公告查看方式">
            <button type="button" class="button small" :class="receiptsShown ? 'neutral' : 'primary'"
              :disabled="busy" :aria-pressed="!receiptsShown" @click="receiptsShown = false">公告正文</button>
            <button type="button" class="button small" :class="receiptsShown ? 'primary' : 'neutral'"
              :disabled="busy || Boolean(confirmation)" :aria-pressed="receiptsShown"
              @click="showReceipts">送达回执</button>
          </div>
          <PublicationDeliveryReceipts v-if="receiptsShown" :id="current.id" :versions="publishedVersions"
            :history-loading="historyLoading" :history-next="historyNext" :history-error="historyError"
            @more-versions="loadHistory(historyError ? '' : historyNext, false)" />
          <template v-else>
            <p class="muted">发布范围 {{ publicationAudiences[current.audience ?? 'all'] }}</p>
            <ul v-if="current.targets?.length" class="target-summary" aria-label="指定收件对象">
              <li v-for="item in current.targets" :key="item.audience + ':' + item.recipient">
                {{ recipientLabels[item.audience] }} · {{ item.displayName || item.recipient }}
              </li>
            </ul>
            <RouterLink v-if="current.alertId" class="text-action"
              :to="{ path: alertPath, query: { alert: current.alertId } }">查看关联站内预警</RouterLink>
            <pre class="publication-body">{{ current.body }}</pre>
            <PublicationAttachmentLinks :id="current.id" :version="current.version" :attachments="current.attachments" />
            <p v-if="publishFileMissing" class="muted">请编辑并上传至少一份有效附件后发布。</p>
            <div v-if="confirmation" class="confirm-panel">
              <p v-if="confirmation === 'publish'">
                确认按「{{ publicationAudiences[current.audience ?? 'all'] }}」范围发布当前版本？
              </p>
              <p v-else>确认撤回公告？撤回后各收件端将看不到该公告。</p>
              <button class="button neutral" type="button" :disabled="busy" @click="confirmation = undefined">取消</button>
              <button class="button primary" type="button" :disabled="busy" @click="persist(confirmation)">
                {{ busy ? '正在处理…' : '确认' + publicationActions[confirmation] }}
              </button>
            </div>
            <div v-else class="dialog-actions">
              <button class="button neutral" type="button" :disabled="versionConflict" @click="editCurrent">编辑</button>
              <button v-if="current.allowedActions?.includes('withdraw')" class="button neutral" type="button" :disabled="versionConflict"
                @click="confirmation = 'withdraw'">撤回</button>
              <button v-if="current.allowedActions?.includes('publish')" class="button primary" type="button"
                :disabled="versionConflict || publishFileMissing"
                @click="confirmation = 'publish'">发布</button>
            </div>
            <button v-if="!historyShown" class="text-action" type="button" @click="loadHistory()">查看版本留痕</button>
            <div v-else class="version-history">
              <h3>版本留痕</h3>
              <p v-if="historyError" class="error-message" role="alert">{{ historyError }}
                <button class="text-action" type="button" @click="loadHistory()">重试</button>
              </p>
              <p v-if="historyLoading" class="muted" role="status">正在读取留痕…</p>
              <details v-for="item in history" :key="item.version">
                <summary>版本 {{ item.version }} · {{ publicationActions[item.action] }} · {{ item.operator }} · {{ formatTime(item.occurredAt) }}</summary>
                <p>{{ item.title }} · {{ item.publisher }} · {{ publicationTypes[item.category] }}
                  · {{ publicationAudiences[item.audience ?? 'all'] }}</p>
                <pre class="publication-body">{{ item.body }}</pre>
                <PublicationAttachmentLinks :id="current.id" :version="item.version" :attachments="item.attachments" />
                <RouterLink v-if="item.alertId" class="text-action" :to="{ path: alertPath, query: { alert: item.alertId } }">
                  查看该版本关联预警
                </RouterLink>
              </details>
              <button v-if="historyNext" class="text-action" type="button" :disabled="historyLoading"
                @click="loadHistory(historyNext)">加载更早版本</button>
            </div>
          </template>
        </template>
      </div>
    </V2RouteDialog>
  </section>
</template>

<style scoped>
.dialog-content { padding: 20px 24px; }
.dialog-content h2 { padding-right: 30px; }
.edit-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.wide { grid-column: 1 / -1; }
.revision-note { display: block; }
.row-actions, .dialog-actions, .confirm-panel { display: flex; flex-wrap: wrap; gap: 8px; }
.detail-view-tabs { display: flex; flex-wrap: wrap; gap: 8px; margin-block: 10px; }
.target-summary { padding-left: 20px; overflow-wrap: anywhere; }
.dialog-actions { justify-content: flex-end; }
.confirm-panel p { flex-basis: 100%; }
.publication-body { white-space: pre-wrap; overflow-wrap: anywhere; overflow: auto; max-height: 20rem; }
.version-history { margin-top: 12px; }
.version-history summary { cursor: pointer; padding-block: 5px; }
.attachment-editor { min-width: 0; }
.publication-file-chooser { position: relative; width: fit-content; }
.publication-file-chooser:focus-within { outline: 2px solid currentColor; outline-offset: 2px; }
.publication-file-chooser:has(input:disabled) { opacity: 0.5; }
.publication-file-input { position: absolute; inset: 0; width: 100%; height: 100%; opacity: 0; cursor: pointer; }
.pin-option { display: flex; align-items: center; gap: 6px; }
.pin-option input { width: 16px; height: 16px; min-height: 0; margin: 0; padding: 0; flex: none; }
.attachment-hint { margin: 5px 0; }
.queued-files { list-style: none; padding: 0; margin: 6px 0 0; }
.queued-files li { display: flex; gap: 8px; align-items: center; justify-content: space-between; padding-block: 4px; }
.queued-files span { min-width: 0; overflow-wrap: anywhere; }
.queued-files button { flex: none; }
</style>

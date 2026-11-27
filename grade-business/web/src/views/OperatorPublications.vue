<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import OperatorWorkflowTabs from '../components/OperatorWorkflowTabs.vue'
import AsyncState from '../components/AsyncState.vue'
import PublicationAttachmentLinks from '../components/PublicationAttachmentLinks.vue'
import V2RouteDialog from '../components/V2RouteDialog.vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { institutionPublicationPath, operatorPublicationPath, recipientPublicationUrl,
  type PublicationReceipt, type PublicationReceiptAction } from '../lib/publicationDelivery'
import { isPublicationMaterial, publicationMaterialTypes, publicationTypes, publicationUnread,
  refreshPublicationUnread, usePublicationList,
  type PublicationDetail } from '../lib/publications'
import { businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'

const props = defineProps<{ audience: BusinessAudience }>()
const route = useRoute()
const router = useRouter()
const path = computed(() => props.audience === 'institution' ? institutionPublicationPath : operatorPublicationPath)
const settings = computed(() => businessScopes[props.audience])
const scope = computed(() => queryValue(route.query[settings.value.key]).trim())
const selected = computed(() => queryValue(route.query.publication))
const library = computed(() => props.audience === 'operator' && queryValue(route.query.view) === 'materials')
const available = computed(() => Boolean(scope.value) || (props.audience === 'operator' && !library.value))
const typeOptions = computed(() => library.value ? publicationMaterialTypes : publicationTypes)
const tabScope = computed(() => ({ [settings.value.key]: scope.value || undefined }))
const criteria = computed(() => ({
  [settings.value.key]: scope.value, group: library.value ? 'materials' : undefined,
  category: queryValue(route.query.category),
  q: queryValue(route.query.q), read: scope.value ? queryValue(route.query.read) : '',
}))
const list = usePublicationList(() => available.value ? path.value : '', () => criteria.value)
const { page, category, keyword, loading, failure, cursors, load, nextPage, previousPage } = list
const scopeDraft = ref('')
const readFilter = ref('')
const current = shallowRef<PublicationDetail>()
const reading = ref(false)
const busy = ref(false)
const detailError = ref('')
const actionError = ref('')
const notice = ref('')
let readRequest: AbortController | undefined
let receiptRequest: AbortController | undefined

/**
 * 用途 查询输入的测评机构或运营单位收到的公告。
 * 参数 无，使用范围表单中输入的名称。
 * 返回值 范围切换导航或列表刷新任务。
 */
function chooseScope() {
  const name = scopeDraft.value.trim()
  if (!name) return
  if (name === scope.value) return load()
  return router.push({ path: path.value, query: {
    ...route.query, [settings.value.key]: name, publication: undefined,
  } })
}

/**
 * 用途 在当前收件范围内应用类别、关键词和已读状态筛选。
 * 参数 无，使用工具栏正在编辑的条件。
 * 返回值 更新查询地址或刷新当前条件的任务。
 */
function search() {
  const q = keyword.value.trim()
  if (category.value === criteria.value.category && q === criteria.value.q
    && readFilter.value === criteria.value.read) return load()
  return router.push({ query: { ...route.query, category: category.value || undefined,
    q: q || undefined, read: scope.value ? readFilter.value || undefined : undefined } })
}

/**
 * 用途 定位待查看的公告并保留列表筛选。
 * 参数 id 为公告标识，省略时关闭详情。
 * 返回值 更新公告详情位置的导航任务。
 */
function selectNotice(id?: string) {
  if (busy.value) return
  return router.push({ query: { ...route.query, publication: id } })
}

/**
 * 用途 重新读取所选收件范围内当前有效的已发布公告。
 * 参数 id 为列表上的站内公告标识。
 * 返回值 已发布详情和回执的加载任务。
 */
async function loadNotice(id: string) {
  readRequest?.abort()
  const request = new AbortController()
  readRequest = request
  detailError.value = ''
  reading.value = true
  try {
    const url = recipientPublicationUrl(props.audience, scope.value, id)
    const data = await api<PublicationDetail>(url, { signal: request.signal })
    if (request.signal.aborted) return
    if (library.value && !isPublicationMaterial(data.category)) {
      detailError.value = '未找到该资料。'
      await load()
      return
    }
    current.value = data
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    current.value = undefined
    detailError.value = errorText(error)
    if (error instanceof ApiError && error.status === 404) await load()
  } finally {
    if (readRequest === request) reading.value = false
  }
}

/**
 * 用途 写入一条公告回执或标记范围内全部公告已读，再同步列表与未读数。
 * 参数 action 为已读、确认签收或批量已读动作。
 * 返回值 写入与界面状态刷新的任务。
 */
async function updateReceipt(action: PublicationReceiptAction | 'read-all') {
  const item = action === 'read-all' ? undefined : current.value
  if (!scope.value || busy.value || (action !== 'read-all' && (!item || reading.value))) return
  const request = new AbortController()
  receiptRequest = request
  busy.value = true
  notice.value = actionError.value = ''
  const url = recipientPublicationUrl(props.audience, scope.value, item?.id, action)
  const body = item ? { expectedVersion: item.version } : {}
  try {
    const result = await api<PublicationReceipt & { updated?: number }>(url, {
      method: 'POST', body, signal: request.signal,
    })
    if (request.signal.aborted) return
    if (item && selected.value === item.id) current.value = { ...item, ...result }
    notice.value = action === 'read-all' ? '已标记 ' + (result.updated ?? 0) + ' 条公告为已读。'
      : action === 'read' ? '公告已标记为已读。' : '公告已签收。'
    await Promise.all([load(), refreshPublicationUnread(props.audience, scope.value, true)])
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    actionError.value = errorText(error)
    if (item && selected.value === item.id && error instanceof ApiError && [404, 409].includes(error.status)) {
      await Promise.all([loadNotice(item.id), load(), refreshPublicationUnread(props.audience, scope.value, true)])
    }
  } finally {
    if (receiptRequest === request) {
      receiptRequest = undefined
      busy.value = false
    }
  }
}

watch([path, scope], () => {
  scopeDraft.value = scope.value
  receiptRequest?.abort()
  receiptRequest = undefined
  busy.value = false
  notice.value = actionError.value = ''
}, { immediate: true })
watch(criteria, value => {
  category.value = value.category as typeof category.value
  keyword.value = value.q
  readFilter.value = value.read
}, { immediate: true })
watch([path, scope, selected, library], () => {
  readRequest?.abort()
  current.value = undefined
  detailError.value = notice.value = actionError.value = ''
  reading.value = false
  if (selected.value && available.value) void loadNotice(selected.value)
}, { immediate: true })
refreshBusinessOnFocus(() => {
  if (!busy.value && available.value) void list.refresh()
}, 10_000)
onScopeDispose(() => { readRequest?.abort(); receiptRequest?.abort() })
</script>

<template>
  <section class="v2-list" aria-label="站内公告">
    <header class="v2-page-head"><h1>消息中心</h1></header>
    <OperatorWorkflowTabs group="messages" :audience="audience" />
    <nav v-if="audience === 'operator'" class="publication-views" aria-label="公告与资料">
      <RouterLink class="button small" :class="library ? 'neutral' : 'primary'"
        :aria-current="library ? undefined : 'page'" :to="{ path, query: tabScope }">站内公告</RouterLink>
      <RouterLink class="button small" :class="library ? 'primary' : 'neutral'" :aria-current="library ? 'page' : undefined"
        :to="{ path, query: { ...tabScope, view: 'materials' } }">政策标准与常用资料</RouterLink>
    </nav>
    <form class="v2-list-toolbar" @submit.prevent="chooseScope">
      <div class="v2-toolbar-row">
        <label class="field filter-keyword">
          <span>{{ settings.label }}</span>
          <input v-model="scopeDraft" required maxlength="200" :placeholder="'输入登记的' + settings.label">
        </label>
        <button class="button primary" type="submit" :disabled="busy">{{ library ? '查看资料' : '查看公告' }}</button>
      </div>
    </form>
    <p v-if="!available" class="muted">输入{{ settings.label }}后查看{{ library ? '资料' : '公告' }}。</p>
    <template v-else>
      <form class="v2-list-toolbar" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <label class="field"><span>{{ library ? '资料类型' : '信息类型' }}</span><select v-model="category">
            <option value="">全部类型</option>
            <option v-for="(name, key) in typeOptions" :key="key" :value="key">{{ name }}</option>
          </select></label>
          <label v-if="scope" class="field"><span>阅读状态</span><select v-model="readFilter">
            <option value="">全部阅读状态</option><option value="unread">未读</option><option value="read">已读</option>
          </select></label>
          <label class="field filter-keyword"><span>关键词</span><input v-model="keyword" type="search"
            maxlength="120" placeholder="搜索标题或发布单位"></label>
          <button class="button primary" type="submit" :disabled="busy">查询</button>
          <span class="v2-toolbar-spacer"></span>
          <span v-if="!library && scope && publicationUnread" class="muted">未读 {{ publicationUnread.count }} 条</span>
          <button v-if="!library && scope" class="button neutral" type="button" :disabled="busy || publicationUnread?.count === 0"
            @click="updateReceipt('read-all')">全部标为已读</button>
        </div>
      </form>
      <p v-if="notice && !selected" class="form-message success-message" role="status">{{ notice }}</p>
      <p v-if="actionError && !selected" class="form-message error-message" role="alert">{{ actionError }}</p>
      <div class="panel v2-list-table" :aria-label="library ? '政策标准与常用资料' : undefined">
        <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
          :empty-text="library ? '暂无符合条件的资料。' : '暂无符合条件的站内公告。'" @retry="load()">
          <ul v-if="library" class="materials-grid">
            <li v-for="item in page.items" :key="item.id" class="material-card">
              <div class="material-card-head">
                <span class="status-badge status-draft">{{ publicationTypes[item.category] }}</span>
                <small v-if="item.pinned" class="muted">置顶</small>
              </div>
              <h2>{{ item.title }}</h2>
              <p class="muted">{{ item.publisher }}<template v-if="item.publishedAt"> · {{ formatTime(item.publishedAt) }}</template></p>
              <PublicationAttachmentLinks :id="item.id" :version="item.version" :attachments="item.attachments"
                :recipient="{ audience, recipient: scope }" compact />
              <button class="text-action" type="button" :disabled="busy" @click="selectNotice(item.id)">查看详情</button>
            </li>
          </ul>
          <ul v-else class="announcement-list">
            <li v-for="item in page.items" :key="item.id" class="announcement-row">
              <div class="announcement-content">
                <strong>{{ item.title }}</strong>
                <small v-if="item.pinned" class="muted"> · 置顶</small>
                <p class="muted">{{ publicationTypes[item.category] }} · {{ item.publisher }}
                  <template v-if="item.publishedAt"> · {{ formatTime(item.publishedAt) }}</template></p>
              </div>
              <div class="announcement-actions">
                <template v-if="scope">
                  <span class="status-badge" :class="item.readAt ? 'status-draft' : 'status-pending_initial'">
                    {{ item.readAt ? '已读' : '未读' }}
                  </span>
                  <span v-if="item.acknowledgedAt" class="status-badge status-approved">已签收</span>
                </template>
                <button class="text-action" type="button" :disabled="busy" @click="selectNotice(item.id)">查看</button>
              </div>
            </li>
          </ul>
        </AsyncState>
        <footer v-if="!loading && !failure" class="panel-footer pager">
          <span>本页 {{ page.items.length }} 条</span>
          <div><button class="button neutral small" type="button" :disabled="cursors.length < 2 || busy"
            @click="previousPage">上一页</button>
            <button class="button neutral small" type="button" :disabled="!page.nextBefore || busy"
              @click="nextPage">下一页</button></div>
        </footer>
      </div>
    </template>
    <V2RouteDialog v-if="selected && available" :label="library ? '资料详情' : '站内公告详情'" :width="680" @close="selectNotice()">
      <div class="notice-dialog">
        <h2>{{ current?.title ?? (library ? '资料详情' : '站内公告详情') }}</h2>
        <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
        <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
        <p v-if="reading" class="muted" role="status">{{ library ? '正在读取资料…' : '正在读取公告…' }}</p>
        <p v-else-if="detailError" class="form-message error-message" role="alert">{{ detailError }}</p>
        <template v-else-if="current">
          <p class="muted">{{ publicationTypes[current.category] }} · {{ current.publisher }}
            <template v-if="current.publishedAt"> · {{ formatTime(current.publishedAt) }}</template>
            · 版本 {{ current.version }}</p>
          <pre>{{ current.body }}</pre>
          <template v-if="scope">
            <PublicationAttachmentLinks :id="current.id" :version="current.version" :attachments="current.attachments"
              :recipient="{ audience, recipient: scope }" />
            <dl class="facts receipt-facts">
              <div><dt>送达时间</dt><dd>{{ current.deliveredAt ? formatTime(current.deliveredAt) : '—' }}</dd></div>
              <div><dt>阅读时间</dt><dd>{{ current.readAt ? formatTime(current.readAt) : '未读' }}</dd></div>
              <div><dt>签收时间</dt><dd>{{ current.acknowledgedAt ? formatTime(current.acknowledgedAt) : '未签收' }}</dd></div>
            </dl>
            <div v-if="!current.acknowledgedAt" class="receipt-actions">
              <button v-if="!current.readAt" class="button neutral" type="button" :disabled="busy"
                @click="updateReceipt('read')">标记已读</button>
              <button class="button primary" type="button" :disabled="busy"
                @click="updateReceipt('acknowledge')">确认签收</button>
            </div>
          </template>
        </template>
      </div>
    </V2RouteDialog>
  </section>
</template>

<style scoped>
.publication-views { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 12px; }
.publication-views a { text-decoration: none; }
.materials-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 17rem), 1fr));
  gap: 12px; list-style: none; margin: 0; padding: 16px; }
.material-card { min-width: 0; padding: 14px; border: 1px solid var(--line); border-radius: 8px;
  display: flex; flex-direction: column; align-items: stretch; overflow-wrap: anywhere; }
.material-card-head { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
.material-card h2 { margin-block: 8px 2px; font-size: 16px; }
.material-card p { margin: 0; }
.material-card > button { align-self: flex-start; margin-top: auto; }
.announcement-list { margin: 0; padding: 0; list-style: none; }
.announcement-row { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap;
  gap: 8px 16px; padding: 12px 16px; border-bottom: 1px solid var(--line); }
.announcement-row:last-child { border-bottom: 0; }
.announcement-content { flex: 1 1 220px; overflow-wrap: anywhere; }
.announcement-content p { margin: 4px 0 0; }
.announcement-actions, .receipt-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.notice-dialog { padding: 20px 24px; }
.notice-dialog h2 { padding-right: 30px; overflow-wrap: anywhere; }
.notice-dialog pre { white-space: pre-wrap; overflow-wrap: anywhere; }
.receipt-facts { padding: 12px 0; border-block: 1px solid var(--line); }
.receipt-actions { justify-content: flex-end; }
</style>

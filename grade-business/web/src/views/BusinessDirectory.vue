<script setup lang="ts">
import { computed, nextTick, onScopeDispose, reactive, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import UnitMergePanel from '../components/UnitMergePanel.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { archiveSupplierLocation } from '../lib/archiveInventory'
import { directoryStatusText, type DirectoryCollection, type DirectoryEntry, type DirectoryHistory } from '../lib/directory'
import { formatTime, type Page } from '../lib/domain'
import { queryValue } from '../lib/paging'

type DirectoryRow = DirectoryEntry & { mergedIntoId?: string; mergeId?: string }

const props = defineProps<{ institutional?: boolean }>()
const route = useRoute()
const router = useRouter()
const tabs = computed(() => props.institutional
  ? [{ key: 'people', name: '测评人员' }, { key: 'units', name: '被测单位' }, { key: 'institutions', name: '本机构资质' }] as const
  : [{ key: 'units', name: '单位档案' }, { key: 'institutions', name: '机构资质' }, { key: 'people', name: '测评师档案' }] as const)
const collection = computed<DirectoryCollection>(() => (
  tabs.value.find(item => item.key === route.query.tab)?.key ?? tabs.value[0].key
))
const institutionPeople = computed(() => props.institutional && collection.value === 'people')
const title = computed(() => tabs.value.find(item => item.key === collection.value)?.name ?? '')
const institution = computed(() => props.institutional ? queryValue(route.query.institution).trim() : '')
const scope = computed(() => props.institutional ? { institution: institution.value } : {})
const path = computed(() => (props.institutional ? '/institution/directory/' : '/directory/') + collection.value)
const hasScope = computed(() => !props.institutional || Boolean(institution.value))
const canCreate = computed(() => !props.institutional || collection.value !== 'institutions')
const scopeDraft = ref('')
const useScopePicker = ref(false)
const keyword = ref('')
const status = ref('')
const search = ref({ q: '', status: '' })
const before = ref('')
const back = ref<string[]>([])
const page = shallowRef<Page<DirectoryRow>>({ items: [] })
const loading = ref(false)
const failure = ref('')
const notice = ref('')
const mergePanel = ref<InstanceType<typeof UnitMergePanel>>()
const editor = ref<HTMLDialogElement>()
const editorVisible = ref(false)
const historyDialog = ref<HTMLDialogElement>()
const selected = ref<DirectoryEntry>()
const assessorQualifications = ['初级', '中级', '高级'].map(level => '网络安全等级保护测评师（' + level + '）')
const legacyAssessorQualification = computed(() => {
  const qualification = selected.value?.qualification
  return qualification && !assessorQualifications.includes(qualification) ? qualification : undefined
})
const mode = ref<'edit' | 'status' | 'link'>('edit')
const saving = ref(false)
const saveError = ref('')
const linked = ref('')
const historyItems = shallowRef<Page<DirectoryHistory>>({ items: [] })
const historyLoading = ref(false)
const historyError = ref('')
const blank = {
  name: '', code: '', classification: '', district: '', qualification: '', validUntil: '', institutionId: '',
  certificateNo: '',
}
const fields = reactive({ ...blank })
let active: AbortController | undefined
let historyRequest: AbortController | undefined
let writeRequest: AbortController | undefined

/**
 * 用途 使用当前查询和游标读取一页档案。
 * 参数 无，范围、筛选和游标来自当前页面。
 * 返回值 可取消的读取任务。
 */
async function load() {
  active?.abort()
  const request = new AbortController()
  active = request
  loading.value = false
  failure.value = ''
  if (!hasScope.value) return
  loading.value = true
  try {
    const result = await api<Page<DirectoryRow>>(
      path.value + params({ ...scope.value, ...search.value, before: before.value }),
      { signal: request.signal },
    )
    if (active === request) page.value = result
  } catch (error) {
    if (active === request && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) loading.value = false
  }
}

/**
 * 用途 应用当前机构范围并保留所在页签。
 * 参数 无，机构范围由搜索框选取或录入。
 * 返回值 完成范围切换的导航任务。
 */
function chooseScope() {
  return router.push({ query: { tab: collection.value, institution: scopeDraft.value.trim() || undefined } })
}

/**
 * 用途 按当前输入重新筛选并从目录第一页读取。
 * 参数 无，使用表单中的关键词和状态。
 * 返回值 重新加载任务。
 */
function applyFilters() {
  search.value = { q: keyword.value.trim(), status: status.value }
  before.value = ''
  back.value = []
  return load()
}

/**
 * 用途 使用服务端游标前进或返回目录页。
 * 参数 direction 为翻页方向。
 * 返回值 翻页任务，游标无效时不读取。
 */
function movePage(direction: 'next' | 'previous') {
  if (direction === 'next') {
    if (!page.value.nextCursor) return
    back.value.push(before.value)
    before.value = page.value.nextCursor
  } else {
    before.value = back.value.pop() ?? ''
  }
  return load()
}

/**
 * 用途 显示档案编辑、状态变更或挂接对话框。
 * 参数 item 为已有档案或可登记的历史名称，kind 为对话框操作。
 * 返回值 无。
 */
function openEditor(item?: DirectoryEntry, kind: 'edit' | 'status' | 'link' = 'edit') {
  selected.value = item
  mode.value = kind
  linked.value = ''
  saveError.value = ''
  notice.value = ''
  Object.assign(fields, blank)
  if (item) {
    const { name, code, classification, district, qualification, validUntil, institutionId, certificateNo } = item
    Object.assign(fields, {
      name, code: code ?? '', classification: classification ?? '', district: district ?? '',
      qualification: qualification ?? '', validUntil: validUntil ?? '', institutionId: institutionId ?? '',
      certificateNo: certificateNo ?? '',
    })
  }
  editorVisible.value = true
  void nextTick(() => { if (editorVisible.value) editor.value?.showModal() })
}

/**
 * 用途 清理可取消的写入状态后关闭当前动作框。
 * 参数 无，提交中的动作保留当前对话框。
 * 返回值 无。
 */
function closeEditor() {
  if (!saving.value) editor.value?.close()
}

/**
 * 用途 仅组装当前档案类型可写的字段。
 * 参数 kind 为要写入的目录类型。
 * 返回值 当前表单经过整理的请求字段。
 */
function entryBody(kind: DirectoryCollection) {
  const common = { name: fields.name.trim(), code: fields.code.trim() }
  if (kind === 'units') return {
    name: common.name, classification: fields.classification.trim(),
    ...(!selected.value?.codeManaged ? { code: common.code.toUpperCase() } : {}),
    ...(!props.institutional ? { district: fields.district.trim() } : {}),
  }
  if (kind === 'institutions') return { ...common, validUntil: fields.validUntil }
  return {
    name: common.name, qualification: fields.qualification.trim(),
    certificateNo: fields.certificateNo.trim(), validUntil: fields.validUntil,
    ...(!props.institutional ? { code: common.code, institutionId: fields.institutionId } : {}),
  }
}

/**
 * 用途 执行所选的维护动作，并用服务端结果刷新当前列表。
 * 参数 无，使用打开对话框时的记录和版本。
 * 返回值 写入与刷新任务。
 */
async function save() {
  if (saving.value) return
  const item = selected.value
  const kind = collection.value
  const collectionURL = path.value
  const query = params(scope.value)
  const isLink = mode.value === 'link'
  if (isLink && !linked.value) return
  const itemID = isLink ? linked.value : item?.id
  const record = itemID ? '/' + encodeURIComponent(itemID) : ''
  const desiredStatus = item?.status === 'inactive' ? 'active' : 'inactive'
  const body = isLink ? undefined : mode.value === 'status'
    ? { expectedVersion: item?.version, status: desiredStatus }
    : { ...entryBody(kind), ...(item?.id ? { expectedVersion: item.version } : {}) }
  const request = new AbortController()
  writeRequest = request
  saving.value = true
  saveError.value = ''
  try {
    await api(collectionURL + record + (isLink ? '/link' : '') + query, {
      method: item?.id && !isLink ? 'PATCH' : 'POST', body, signal: request.signal,
    })
    if (writeRequest === request) {
      editor.value?.close()
      notice.value = isLink ? '已加入被测单位。' : '已保存档案。'
      await load()
    }
  } catch (error) {
    if (writeRequest === request && !isAbort(error)) saveError.value = errorText(error)
  } finally {
    if (writeRequest === request) saving.value = false
  }
}

/**
 * 用途 读取档案变更留痕，保留已经查看的历史页。
 * 参数 item 为现有档案，cursor 为下一页标识。
 * 返回值 历史查询任务。
 */
async function loadHistory(item: DirectoryEntry, cursor?: string) {
  if (!item.id) return
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  historyLoading.value = true
  historyError.value = ''
  try {
    const result = await api<Page<DirectoryHistory>>(
      path.value + '/' + encodeURIComponent(item.id) + '/history' + params({ ...scope.value, before: cursor }),
      { signal: request.signal },
    )
    if (historyRequest === request) historyItems.value = {
      items: cursor ? [...historyItems.value.items, ...result.items] : result.items,
      nextCursor: result.nextCursor,
    }
  } catch (error) {
    if (historyRequest === request && !isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === request) historyLoading.value = false
  }
}

/**
 * 用途 打开指定档案的历史对话框。
 * 参数 item 为需要查看留痕的档案。
 * 返回值 历史查询任务。
 */
function openHistory(item: DirectoryEntry) {
  selected.value = item
  historyItems.value = { items: [] }
  historyDialog.value?.showModal()
  return loadHistory(item)
}

/**
 * 用途 将历史快照中能读懂的归属和状态并列显示。
 * 参数 item 为变更前或变更后快照。
 * 返回值 用于历史表格的业务信息。
 */
function historyText(item?: DirectoryEntry) {
  if (!item) return '—'
  const identifier = institutionPeople.value ? item.certificateNo : item.code
  return [
    item.name, item.institutionName || item.institutionId, identifier, item.qualification, item.validUntil,
    item.status === 'inactive' ? '停用' : '启用',
  ].filter(Boolean).join(' · ')
}

/**
 * 用途 根据实际合并回执刷新目录并显示生效结果。
 * 参数 movedCount 为已迁移的有效备案数。
 * 返回值 更新目录页面的查询任务。
 */
function reloadAfterMerge(movedCount: number) {
  notice.value = `单位已合并，已迁移 ${movedCount} 条有效备案。`
  return load()
}

const historyActions: Record<string, string> = {
  create: '登记', update: '编辑', edit: '编辑', disable: '停用', enable: '启用', transfer: '调整归属', link: '关联被测单位', merge: '归并档案',
}

watch([collection, institution], () => {
  scopeDraft.value = institution.value
  keyword.value = ''
  status.value = ''
  search.value = { q: '', status: '' }
  before.value = ''
  back.value = []
  notice.value = ''
  page.value = { items: [] }
  historyRequest?.abort()
  writeRequest?.abort()
  writeRequest = undefined
  saving.value = false
  editorVisible.value = false
  editor.value?.close()
  historyDialog.value?.close()
  void load()
}, { immediate: true })
onScopeDispose(() => {
  active?.abort()
  historyRequest?.abort()
  writeRequest?.abort()
})
</script>

<template>
  <section class="v2-list" :aria-label="institutional ? '机构基础数据' : '监管名录档案'">
    <header class="v2-page-head">
      <div><h1>{{ institutional ? '基础数据' : '名录档案' }}</h1></div>
      <div v-if="!institutional" class="directory-header-actions">
        <RouterLink class="button neutral" to="/archives/units">查看单位业务画像</RouterLink>
        <RouterLink class="button neutral" :to="archiveSupplierLocation()">供应商主档</RouterLink>
      </div>
    </header>
    <nav class="v2-tabs" aria-label="档案分类">
      <RouterLink v-for="tab in tabs" :key="tab.key" class="v2-tab" :class="{ active: tab.key === collection }"
        :aria-current="tab.key === collection ? 'page' : undefined" :to="{ query: { ...scope, tab: tab.key } }">{{ tab.name }}</RouterLink>
    </nav>

    <form v-if="institutional" class="v2-list-toolbar" @submit.prevent="chooseScope">
      <div class="v2-toolbar-row">
        <DirectoryPicker v-if="useScopePicker" v-model="scopeDraft" kind="institution"
          label="测评机构" :initial-label="scopeDraft" required />
        <label v-else class="field filter-keyword"><span>测评机构</span>
          <input v-model="scopeDraft" required maxlength="200" placeholder="输入机构办理名称">
        </label>
        <button class="button neutral" type="button" @click="useScopePicker = !useScopePicker">{{ useScopePicker ? '手工录入办理名称' : '从机构名录选择' }}</button>
        <button class="button primary" type="submit">查看基础数据</button>
      </div>
    </form>
    <div v-if="hasScope" class="panel v2-list-table">
      <form class="v2-list-toolbar" @submit.prevent="applyFilters">
        <div class="v2-toolbar-row">
          <label class="field filter-keyword"><span>检索{{ title }}</span>
            <input v-model="keyword" type="search" maxlength="120"
              :placeholder="institutionPeople ? '输入姓名或证书编号' : '输入名称或编号'">
          </label>
          <label class="field"><span>状态</span><select v-model="status">
            <option value="">全部状态</option><option value="active">启用</option><option value="inactive">停用</option>
          </select></label>
          <button class="button neutral" type="submit">查询</button>
          <button v-if="canCreate" class="button primary" type="button" @click="openEditor()">＋ {{ collection === 'people' ? '登记测评师' : collection === 'units' ? '新增单位' : '登记测评机构' }}</button>
          <button v-if="!institutional && collection === 'units'" class="button neutral" type="button"
            @click="mergePanel?.open()">合并单位</button>
          <button v-if="institutional && collection === 'units'" class="button neutral" type="button" @click="openEditor(undefined, 'link')">关联已有单位</button>
        </div>
      </form>
      <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length" empty-text="暂无符合条件的档案。" @retry="load">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr>
              <th>{{ collection === 'people' ? '姓名' : '名称' }}</th>
              <th v-if="institutionPeople">证书编号</th>
              <th v-else>{{ collection === 'units' ? '统一社会信用代码' : collection === 'people' ? '工号／证书编号' : '资质证书编号' }}</th>
              <th>{{ collection === 'units' ? '类型／区县' : '资质' }}</th>
              <th v-if="collection === 'people'">归属机构</th>
              <th v-if="collection === 'units'">所属系统</th>
              <th v-else>有效期</th>
              <th v-if="collection === 'people'">在审业务</th><th>状态</th><th>操作</th>
            </tr></thead>
            <tbody><tr v-for="item in page.items" :key="item.id || item.scopeName || item.name">
              <td><strong>{{ item.name }}</strong>
                <small v-if="item.scopeName && item.scopeName !== item.name" class="muted subtext">办理名称 {{ item.scopeName }}</small></td>
              <td v-if="institutionPeople" class="code">{{ item.certificateNo || '—' }}</td>
              <td v-else class="code">{{ item.code || '—' }}
                <small v-if="item.certificateNo" class="muted subtext">{{ item.certificateNo }}</small></td>
              <td>{{ collection === 'units' ? [item.classification, item.district].filter(Boolean).join('／') || '—' : item.qualification || '—' }}</td>
              <td v-if="collection === 'people'">{{ item.institutionName || '—' }}</td>
              <td>{{ collection === 'units' ? item.systemCount ?? '—' : item.validUntil || '—' }}</td>
              <td v-if="collection === 'people'">{{ item.inReviewCount ?? '—' }}</td>
              <td><span class="status-badge" :class="item.id && item.usable ? 'status-approved' : 'status-draft'">{{ item.mergedIntoId ? '已合并' : directoryStatusText(item) }}</span></td>
              <td class="directory-actions">
                <button v-if="item.canEdit" class="text-action" type="button" @click="openEditor(item)">编辑</button>
                <button v-if="item.canEdit && item.id" class="text-action" type="button"
                  @click="openEditor(item, 'status')">{{ item.status === 'inactive' ? '启用' : '停用' }}</button>
                <button v-if="item.id" class="text-action" type="button" @click="openHistory(item)">变更历史</button>
                <button v-if="!institutional && item.mergeId" class="text-action" type="button"
                  @click="mergePanel?.openHistory(item.mergeId)">合并记录</button>
                <button v-if="!item.id && canCreate" class="text-action" type="button" @click="openEditor(item)">完善档案</button>
                <RouterLink v-if="!institutional && collection === 'units' && item.systemCount && item.scopeName"
                  class="text-action" :to="'/archives/units/' + encodeURIComponent(item.scopeName)">业务画像</RouterLink>
              </td>
            </tr></tbody>
          </table>
        </div>
      </AsyncState>
      <footer v-if="!failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span><div>
          <button class="button neutral small" type="button" :disabled="!before || loading" @click="movePage('previous')">上一页</button>
          <button class="button neutral small" type="button" :disabled="!page.nextCursor || loading" @click="movePage('next')">下一页</button>
        </div>
      </footer>
    </div>

    <UnitMergePanel v-if="!institutional && collection === 'units'" ref="mergePanel" @merged="reloadAfterMerge" />

    <dialog ref="editor" class="decision-dialog directory-dialog" aria-labelledby="directory-editor-title"
      @cancel.prevent="closeEditor" @close="editorVisible = false">
      <form v-if="editorVisible" @submit.prevent="save">
        <h2 id="directory-editor-title">{{ mode === 'link' ? '关联已有单位' : mode === 'status' ? (selected?.status === 'inactive' ? '启用档案' : '停用档案') : selected?.id ? '编辑档案' : '登记' + title }}</h2>
        <DirectoryPicker v-if="mode === 'link'" v-model="linked" kind="unit" label="被测单位" use-id required :disabled="saving" />
        <template v-else-if="mode === 'status'">
          <p>{{ selected?.name }}</p>
          <p v-if="selected?.status !== 'inactive'" class="muted">停用后，新业务无法选用此档案，已办理的记录仍可查看。</p>
        </template>
        <fieldset v-else class="form-grid" :disabled="saving">
          <label class="field"><span>{{ collection === 'people' ? '姓名' : '名称' }}</span>
            <input v-model="fields.name" maxlength="200" required>
          </label>
          <label v-if="!institutionPeople" class="field">
            <span>{{ collection === 'units' ? '统一社会信用代码' : collection === 'people' ? '工号' : '资质证书编号' }}</span>
            <input v-model="fields.code" :maxlength="collection === 'units' ? 18 : 80"
              :pattern="collection === 'units' ? '[0-9A-Za-z]{18}' : undefined"
              :readonly="selected?.codeManaged" required>
            <small v-if="selected?.codeManaged" class="muted">已核定单位的信用代码由系统管理员维护</small>
          </label>
          <template v-if="collection === 'units'">
            <label class="field"><span>单位类型</span><input v-model="fields.classification" maxlength="80"></label>
            <label class="field"><span>所属区县</span>
              <input v-model="fields.district" maxlength="80" :readonly="institutional"
                :placeholder="institutional ? '由系统管理员核定' : undefined">
            </label>
          </template>
          <template v-else>
            <label v-if="collection === 'people'" class="field">
              <span>测评资质</span>
              <select v-model="fields.qualification">
                <option value="">请选择测评资质</option>
                <option v-if="legacyAssessorQualification" :value="legacyAssessorQualification">
                  {{ legacyAssessorQualification }}（历史值）
                </option>
                <option v-for="qualification in assessorQualifications" :key="qualification" :value="qualification">
                  {{ qualification }}
                </option>
              </select>
            </label>
            <label class="field"><span>资质有效期</span>
              <input v-model="fields.validUntil" type="date" min="0001-01-01" max="9999-12-31" :required="collection === 'institutions'">
            </label>
          </template>
          <template v-if="collection === 'people'">
            <label class="field"><span>证书编号</span><input v-model="fields.certificateNo" maxlength="80"></label>
            <DirectoryPicker v-if="!institutional" v-model="fields.institutionId" kind="institution" label="归属机构"
              :initial-label="selected?.institutionName" use-id required :disabled="saving" />
          </template>
        </fieldset>
        <p v-if="saveError" class="form-message error-message" role="alert">{{ saveError }}</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="saving" @click="closeEditor">取消</button>
          <button class="button primary" type="submit" :disabled="saving">{{ saving ? '正在保存…' : mode === 'link' ? '确认关联' : mode === 'status' ? '确认变更' : '保存' }}</button>
        </div>
      </form>
    </dialog>

    <dialog ref="historyDialog" class="decision-dialog directory-dialog" aria-labelledby="directory-history-title">
      <h2 id="directory-history-title">{{ selected?.name }} · 变更历史</h2>
      <AsyncState :loading="historyLoading && historyItems.items.length === 0" :error="historyError"
        :empty="historyItems.items.length === 0" @retry="selected && loadHistory(selected)">
        <div class="table-wrap directory-history-table">
          <table class="data-table">
            <thead><tr><th>时间</th><th>动作／经办</th><th>变更前</th><th>变更后</th></tr></thead>
            <tbody><tr v-for="event in historyItems.items" :key="event.sequence">
              <td>{{ formatTime(event.changedAt) }}</td>
              <td>{{ historyActions[event.action] || event.action }}<small class="muted subtext">{{ event.actor }}</small></td>
              <td>{{ historyText(event.before) }}</td><td>{{ historyText(event.after) }}</td>
            </tr></tbody>
          </table>
        </div>
      </AsyncState>
      <div class="form-actions">
        <button v-if="historyItems.nextCursor" class="button neutral" type="button" :disabled="historyLoading"
          @click="selected && loadHistory(selected, historyItems.nextCursor)">加载更多</button>
        <button class="button neutral" type="button" @click="historyDialog?.close()">关闭</button>
      </div>
    </dialog>
  </section>
</template>

<style scoped>
.directory-header-actions { display: flex; flex-wrap: wrap; gap: 8px; }
.directory-actions { white-space: nowrap; }
.directory-actions > * { margin-inline-end: 8px; }
.subtext { display: block; margin-top: 2px; }
.directory-dialog { width: min(720px, calc(100vw - 32px)); }
.directory-dialog fieldset { border: 0; padding: 0; margin: 14px 0 0; min-width: 0; }
.directory-history-table .data-table { table-layout: fixed; min-width: 0; }
.directory-history-table th:nth-child(1) { width: 17%; }
.directory-history-table th:nth-child(2) { width: 19%; }
.directory-history-table th, .directory-history-table td { white-space: normal; overflow-wrap: anywhere; }
</style>

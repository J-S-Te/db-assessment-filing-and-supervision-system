<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, watch } from 'vue'
import { ArrowLeft, FileUp, X } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { categories, type KnowledgeDetail, type Page } from '../lib/domain'
import { tagCatalogPath, type TagCatalogEntry } from '../lib/tagCatalog'

const bodyLimit = 32 * 1024
const tagLimit = 12
const props = defineProps<{ mode?: 'edit' | 'tags' }>()
const route = useRoute()
const router = useRouter()
const fields = reactive({ title: '', category: '', source: '', body: '' })
const version = ref<number>()
const tags = ref<string[]>([])
const tagText = ref('')
const tagOptions = ref<TagCatalogEntry[]>([])
const loading = ref(false)
const saving = ref(false)
const importing = ref(false)
const failure = ref('')
const loadFailure = ref('')
const stale = ref(false)
const itemPath = computed(() => props.mode && typeof route.params.id === 'string'
  ? '/knowledge/' + encodeURIComponent(route.params.id) : '')
const heading = computed(() => props.mode === 'tags' ? '知识标签' : props.mode === 'edit' ? '编辑知识' : '新增知识')
const bodySize = computed(() => new TextEncoder().encode(fields.body).length)
let loadRequest: AbortController | undefined
let tagRequest: AbortController | undefined

/**
 * 用途 读取待编辑知识条目和版本，供保存时检测更新冲突。
 * 参数 无，条目从当前路由获取。
 * 返回值 最新条目的加载任务。
 */
async function load() {
  loadRequest?.abort()
  if (!itemPath.value) return
  const current = new AbortController()
  loadRequest = current
  loading.value = true
  loadFailure.value = ''
  try {
    const item = await api<KnowledgeDetail>(itemPath.value, { signal: current.signal })
    if (current !== loadRequest || current.signal.aborted) return
    Object.assign(fields, { title: item.title, category: item.category, source: item.source, body: item.body })
    version.value = item.version
    tags.value = [...item.tags]
    tagText.value = ''
    stale.value = false
    failure.value = ''
  } catch (error) {
    if (current === loadRequest && !isAbort(error)) loadFailure.value = errorText(error)
  } finally {
    if (current === loadRequest) loading.value = false
  }
}

/**
 * 用途 从统一目录读取当前输入匹配的启用标签。
 * 参数 无，标签范围由当前输入和知识编辑模式确定。
 * 返回值 候选读取任务，失败时可继续手工输入。
 */
async function loadTagOptions() {
  tagRequest?.abort()
  tagOptions.value = []
  if (props.mode !== 'tags') return
  const request = new AbortController()
  tagRequest = request
  try {
    const query = params({ q: tagText.value.trim(), status: 'active', limit: 20 })
    const result = await api<Page<TagCatalogEntry>>(tagCatalogPath + query, { signal: request.signal })
    if (!request.signal.aborted) tagOptions.value = result.items
  } catch {
    if (!request.signal.aborted) tagOptions.value = []
  }
}

/**
 * 用途 从本机 UTF-8 文本文件导入知识正文。
 * 参数 event 为文件选择事件。
 * 返回值 内容读取任务，格式不符时保留原正文。
 */
async function importBody(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  failure.value = ''
  if (!/\.(txt|md)$/i.test(file.name) || !file.size || file.size > bodyLimit) {
    failure.value = '请选择不超过 32 KiB 的非空 TXT 或 Markdown 文件。'
    return
  }
  importing.value = true
  const currentPath = route.path
  try {
    const body = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
    if (!body.trim() || body.includes('\0')) {
      failure.value = '文件需要包含可读的 UTF-8 正文。'
      return
    }
    if (route.path === currentPath) fields.body = body
  } catch {
    failure.value = '无法读取文件，请确认它是 UTF-8 编码的文本。'
  } finally {
    importing.value = false
  }
}

/**
 * 用途 将当前输入的标签加入待保存的列表。
 * 参数 无，使用标签输入和已维护的标签。
 * 返回值 标签可保存或没有新增内容时为真。
 */
function addTag(): boolean {
  const tag = tagText.value.trim()
  if (!tag) return true
  if (Array.from(tag).length > 32 || tags.value.length >= tagLimit) {
    failure.value = '每个标签最多 32 字符，单条知识最多 12 个标签。'
    return false
  }
  if (tags.value.some(item => item.toLowerCase() === tag.toLowerCase())) {
    failure.value = '同一条目不能添加重复标签。'
    return false
  }
  tags.value.push(tag)
  tagText.value = ''
  failure.value = ''
  return true
}

/**
 * 用途 保存知识正文或标签，并保留维护流程中的列表筛选。
 * 参数 无，使用当前表单信息。
 * 返回值 提交任务，成功后进入条目详情。
 */
async function save() {
  if (saving.value || importing.value || loading.value || stale.value) return
  if (props.mode && !version.value) return
  failure.value = ''
  const content = { ...fields, title: fields.title.trim(), source: fields.source.trim() }
  if (props.mode === 'tags' && !addTag()) return
  if (props.mode !== 'tags' && (!content.title || !content.category || !content.source ||
    !content.body.trim() || bodySize.value > bodyLimit)) {
    failure.value = '请填写完整的标题、分类、来源与正文，正文最多 32 KiB。'
    return
  }
  saving.value = true
  const record = itemPath.value
  const currentPath = route.path
  try {
    const detail = await saveRecord(record, content)
    if (route.path === currentPath) await router.push({ path: detail, query: route.query })
  } catch (error) {
    if (route.path === currentPath) {
      stale.value = error instanceof ApiError && error.code === 'version_conflict'
      failure.value = errorText(error)
    }
  } finally {
    saving.value = false
  }
}

/**
 * 用途 发送当前操作需要的知识字段。
 * 参数 record 为已有条目地址，content 为表单正文数据。
 * 返回值 保存成功后的知识详情地址。
 */
async function saveRecord(record: string, content: typeof fields): Promise<string> {
  if (!props.mode) {
    const result = await api<{ id: string }>('/knowledge', { method: 'POST', body: content })
    return '/knowledge/' + encodeURIComponent(result.id)
  }
  if (props.mode === 'tags') {
    await api(record + '/tags', { method: 'PATCH', body: { tags: tags.value, expectedVersion: version.value } })
  } else {
    await api(record, { method: 'PATCH', body: { ...content, expectedVersion: version.value } })
  }
  return record
}

watch(itemPath, () => {
  version.value = undefined
  void load()
}, { immediate: true })
watch(() => [props.mode, tagText.value], loadTagOptions, { immediate: true })
onScopeDispose(() => {
  loadRequest?.abort()
  tagRequest?.abort()
})
</script>

<template>
  <section class="form-page">
    <RouterLink class="back-link" to="/knowledge"><ArrowLeft :size="16" aria-hidden="true" />返回知识库</RouterLink>
    <header class="page-header">
      <div><h1>{{ heading }}</h1></div>
    </header>
    <AsyncState :loading="loading" :error="loadFailure" @retry="load">
    <div v-if="!mode || version" class="panel form-panel">
      <form @submit.prevent="save">
        <fieldset v-if="mode === 'tags'" class="form-grid" :disabled="saving || stale">
          <label class="field wide">
            <span>知识名称</span><input :value="fields.title" readonly>
          </label>
          <div class="field wide">
            <span>已有标签</span>
            <div class="knowledge-tags">
              <button v-for="(tag, index) in tags" :key="tag" type="button" class="button neutral small"
                :aria-label="'移除标签 ' + tag" @click="tags.splice(index, 1)">
                {{ tag }} <X :size="13" aria-hidden="true" />
              </button>
              <small v-if="!tags.length">暂无标签</small>
            </div>
          </div>
          <label class="field wide">
            <span>新增标签</span>
            <input v-model="tagText" maxlength="64" list="knowledge-catalog-options" placeholder="输入或选择标签后按回车添加"
              @keydown.enter.prevent="addTag">
            <datalist id="knowledge-catalog-options">
              <option v-for="option in tagOptions" :key="option.id" :value="option.name" />
            </datalist>
            <RouterLink class="text-action" :to="tagCatalogPath">查看标签目录</RouterLink>
          </label>
        </fieldset>
        <fieldset v-else class="form-grid" :disabled="saving || importing || stale">
          <label class="field"><span>知识标题</span>
            <input v-model="fields.title" required maxlength="200" placeholder="请输入标题">
          </label>
          <label class="field"><span>分类</span>
            <select v-model="fields.category" required>
              <option value="" disabled>请选择分类</option>
              <option v-for="(category, key) in categories" :key="key" :value="key">{{ category }}</option>
            </select>
          </label>
          <label class="field wide"><span>来源</span>
            <input v-model="fields.source" required maxlength="500" placeholder="填写发布单位、文号或出处">
          </label>
          <div class="field wide">
            <label for="knowledge-body">正文</label>
            <textarea id="knowledge-body" v-model="fields.body" rows="9" required maxlength="32768"
              placeholder="填写或导入可检索的正文" />
            <small>{{ bodySize }} / {{ bodyLimit }} 字节</small>
          </div>
          <label class="field wide import-field">
            <span><FileUp :size="16" aria-hidden="true" />导入正文</span>
            <input type="file" accept=".txt,.md,text/plain,text/markdown" @change="importBody">
            <small>支持 UTF-8 编码的 TXT 和 Markdown，最多 32 KiB。</small>
          </label>
        </fieldset>
        <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
        <div class="form-actions">
          <RouterLink class="button neutral" :to="{ path: itemPath || '/knowledge', query: route.query }">取消</RouterLink>
          <button v-if="stale" class="button neutral" type="button" @click="load">重新加载条目</button>
          <button class="button primary" type="submit" :disabled="saving || importing || stale">
            {{ mode === 'tags' ? '保存标签' : '保存知识' }}
          </button>
        </div>
      </form>
    </div>
    </AsyncState>
  </section>
</template>

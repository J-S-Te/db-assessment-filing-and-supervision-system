<script setup lang="ts">
import { onMounted, onScopeDispose, reactive, ref, shallowRef, useTemplateRef } from 'vue'
import { errorText, isAbort } from '../lib/api'
import {
  listDashboardClassifications, saveDashboardClassification, type ClassificationKind,
  type DashboardClassification, type DashboardClassificationPage,
} from '../lib/dashboardAnalytics'
import { dashboardNumber } from '../lib/dashboard'

const props = defineProps<{ kind: ClassificationKind }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const dialog = useTemplateRef<HTMLDialogElement>('dialog')
const query = ref('')
const appliedQuery = ref('')
const page = ref(1)
const data = shallowRef<DashboardClassificationPage>()
const draft = reactive<Record<string, string>>({})
const loading = ref(false)
const saving = ref('')
const error = ref('')
const feedback = ref('')
let queryRequest: AbortController | undefined
let saveRequest: AbortController | undefined

/**
 * 用途 按明确的页面和筛选替换维护列表，并取消旧检索。
 * 参数 nextPage 为目标页，nextQuery 为可选检索条件。
 * 返回值 本次请求结束后的任务。
 */
async function load(nextPage: number, nextQuery = appliedQuery.value) {
  if (saving.value) return
  queryRequest?.abort()
  const controller = new AbortController()
  queryRequest = controller
  loading.value = true
  error.value = ''
  feedback.value = ''
  try {
    const result = await listDashboardClassifications(props.kind, nextQuery, nextPage, controller.signal)
    if (queryRequest !== controller) return
    for (const id of Object.keys(draft)) delete draft[id]
    for (const item of result.items) draft[item.id] = item.code
    data.value = result
    page.value = nextPage
    appliedQuery.value = nextQuery
  } catch (reason) {
    if (queryRequest === controller && !isAbort(reason)) error.value = errorText(reason)
  } finally {
    if (queryRequest === controller) {
      loading.value = false
      queryRequest = undefined
    }
  }
}

/**
 * 用途 保存行分类并以接口返回的最新版本同步当前列表。
 * 参数 row 为用户选择保存的业务记录及其当前版本。
 * 返回值 本次保存结束后的任务。
 */
async function save(row: DashboardClassification) {
  const code = draft[row.id]
  if (saving.value || loading.value || code === undefined || code === row.code) return
  const controller = new AbortController()
  saveRequest = controller
  saving.value = row.id
  error.value = ''
  feedback.value = ''
  try {
    const saved = await saveDashboardClassification(props.kind, row.id, code, row.version, controller.signal)
    if (saveRequest !== controller || !data.value) return
    data.value = { ...data.value, items: data.value.items.map(item => item.id === row.id ? { ...item, ...saved } : item) }
    draft[row.id] = saved.code
    feedback.value = `${row.name}的分类已保存。`
    emit('updated')
  } catch (reason) {
    if (saveRequest === controller && !isAbort(reason)) error.value = errorText(reason)
  } finally {
    if (saveRequest === controller) {
      saving.value = ''
      saveRequest = undefined
    }
  }
}

/**
 * 用途 同步关闭对话框并通知页面释放本次归类编辑。
 * 参数 无。
 * 返回值 无，正在保存时保留当前编辑。
 */
function dismiss() {
  if (saving.value) return
  dialog.value?.close()
  emit('close')
}

onMounted(() => {
  dialog.value?.showModal()
  void load(1)
})
onScopeDispose(() => {
  queryRequest?.abort()
  saveRequest?.abort()
  queryRequest = undefined
  saveRequest = undefined
})
</script>

<template>
  <dialog ref="dialog" class="classification-dialog" aria-labelledby="classification-title" @cancel.prevent="dismiss">
    <header>
      <div>
        <h2 id="classification-title">{{ kind === 'industry' ? '维护备案行业' : '维护隐患问题分类' }}</h2>
        <p>{{ kind === 'industry' ? '按备案材料确认行业类别，每个有效系统维护一个行业。'
          : '根据已入账隐患的内容选择主要问题分类。' }}</p>
      </div>
      <button type="button" :disabled="!!saving" @click="dismiss">关闭</button>
    </header>
    <form class="classification-search" @submit.prevent="load(1, query.trim())">
      <label for="classification-search">检索记录</label>
      <input id="classification-search" v-model="query" maxlength="120"
        :placeholder="kind === 'industry' ? '备案编号、系统、单位' : '隐患、备案编号、系统、单位'" />
      <button type="submit" :disabled="!!saving">查询</button>
      <button type="button" :disabled="loading || !!saving" @click="load(page)">刷新</button>
    </form>
    <p v-if="error" class="classification-error" role="alert">{{ error }}</p>
    <p v-if="feedback" role="status">{{ feedback }}</p>
    <p v-if="loading" role="status">正在读取记录…</p>
    <p v-else-if="data && !data.items.length">没有符合当前条件的记录。</p>
    <ol v-if="data?.items.length" class="classification-list" aria-label="可维护归类记录">
      <li v-for="row in data.items" :key="row.id">
        <div>
          <strong>{{ row.name }}</strong>
          <small>{{ row.number }} · {{ row.context }}</small>
        </div>
        <label>
          <span class="visually-hidden">{{ row.name }}的分类</span>
          <select v-model="draft[row.id]" :disabled="loading || !!saving">
            <option value="">未分类</option>
            <option v-for="option in data.options" :key="option.code" :value="option.code">{{ option.label }}</option>
          </select>
        </label>
        <button type="button" :disabled="loading || !!saving || draft[row.id] === row.code" @click="save(row)">
          {{ saving === row.id ? '保存中' : '保存' }}
        </button>
      </li>
    </ol>
    <footer v-if="data">
      <button type="button" :disabled="page === 1 || loading || !!saving" @click="load(page - 1)">上一页</button>
      <span>第 {{ dashboardNumber(page) }} 页</span>
      <button type="button" :disabled="!data.hasMore || loading || !!saving" @click="load(page + 1)">下一页</button>
    </footer>
  </dialog>
</template>

<style scoped>
.classification-dialog {
  position: fixed; inset: 0; width: min(46rem, calc(100% - 2rem)); max-height: min(80dvh, 46rem);
  margin: auto; padding: 1rem; border: 1px solid #517fa0; border-radius: 6px;
  color: #e3effa; background: #102139; color-scheme: dark; overflow: auto;
}
.classification-dialog::backdrop { background: #071320b3; }
.classification-dialog header, .classification-search, .classification-dialog footer {
  display: flex; align-items: center; flex-wrap: wrap; gap: .55rem;
}
.classification-dialog header { justify-content: space-between; }
.classification-dialog h2 { color: #eef8ff; font-size: 1.12rem; }
.classification-dialog p, .classification-dialog small { color: #b4cce2; }
.classification-dialog p { margin: .35rem 0 .75rem; }
.classification-dialog .classification-error { color: #ffb7ab; }
.classification-dialog button, .classification-dialog select, .classification-dialog input {
  padding: .4rem .55rem; border: 1px solid #496984; border-radius: 4px; color: #e3effa; background: #19334e;
}
.classification-dialog button:disabled { opacity: .56; }
.classification-dialog button:not(:disabled):hover { background: #254969; }
.classification-search input { flex: 1 1 10rem; min-width: 0; }
.classification-list { list-style: none; margin: .65rem 0; padding: 0; }
.classification-list li {
  display: grid; grid-template-columns: minmax(0, 1fr) minmax(9rem, auto) auto;
  align-items: center; gap: .5rem; border-top: 1px solid #35516e; padding: .5rem 0;
}
.classification-list strong, .classification-list small { display: block; overflow-wrap: anywhere; }
.classification-list select { width: 100%; }
.classification-dialog footer { justify-content: center; }
.visually-hidden { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
@media (max-width: 560px) {
  .classification-list li { grid-template-columns: minmax(0, 1fr) auto; }
  .classification-list li > div { grid-column: 1 / -1; }
}
</style>

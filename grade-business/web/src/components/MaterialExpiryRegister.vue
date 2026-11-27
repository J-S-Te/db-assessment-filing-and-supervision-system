<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { api, attachmentURL, errorText, isAbort, params } from '../lib/api'
import { materials, reportSubmissionMaterials } from '../lib/domain'

interface Source {
  attachmentId: string
  attachmentKind: string
  caseId: string
  caseKind: 'filing' | 'report'
  number: string
  systemName: string
  organizationName: string
  fileName: string
}

interface SourcePage { items: Source[]; nextBefore?: string }
type SourceKind = Source['caseKind'] | ''
const props = defineProps<{ reportId?: string }>()
const emit = defineEmits<{ close: []; saved: [id: string] }>()
const heading = computed(() => props.reportId ? '登记测评材料有效期' : '登记材料有效期')
const keyword = ref('')
const caseKind = ref<SourceKind>('')
const sources = shallowRef<SourcePage>()
const previousCursors = shallowRef<(string | undefined)[]>([])
const selected = ref('')
const selectedSource = computed(() => sources.value?.items.find(item => item.attachmentId === selected.value))
const downloadURL = computed(() => {
  const item = selectedSource.value
  if (!item) return ''
  const collection = item.caseKind === 'filing' ? '/filings/' : '/reports/'
  return attachmentURL(collection + encodeURIComponent(item.caseId), item.attachmentId)
})
const validUntil = ref('')
const warningDays = ref(7)
const evidence = ref('')
const busy = ref(false)
const searching = ref(false)
const failure = ref('')
let lookup: AbortController | undefined
let request: AbortController | undefined
let lastInput = ''
let requestId = ''
let activeFilters: { q: string; caseKind: SourceKind } = { q: '', caseKind: '' }
let activeCursor: string | undefined

/**
 * 用途 按已提交条件或表单条件逐页检索正式材料。
 * 参数 direction 指定发起新查询、后一页或前一页。
 * 返回值 候选加载任务。
 */
async function search(direction: 'first' | 'next' | 'previous' = 'first') {
  if (searching.value || busy.value) return
  const filters = direction === 'first' ? { q: keyword.value.trim(), caseKind: caseKind.value } : activeFilters
  const before = direction === 'next' ? sources.value?.nextBefore
    : direction === 'previous' ? previousCursors.value.at(-1) : undefined
  const active = new AbortController()
  lookup = active
  searching.value = true
  failure.value = ''
  try {
    const result = await api<SourcePage>('/material-expiries/source-options'
      + params({ ...filters, before, reportId: props.reportId }), { signal: active.signal })
    if (!active.signal.aborted) {
      if (direction === 'first') previousCursors.value = []
      if (direction === 'next') previousCursors.value = [...previousCursors.value, activeCursor]
      if (direction === 'previous') previousCursors.value = previousCursors.value.slice(0, -1)
      sources.value = result
      activeFilters = filters
      activeCursor = before
      selected.value = result.items.length === 1 ? result.items[0]?.attachmentId ?? '' : ''
    }
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (lookup === active) {
      lookup = undefined
      searching.value = false
    }
  }
}

/**
 * 用途 读取候选原件在对应业务中的正式类别名称。
 * 参数 source 为当前候选中的一份材料。
 * 返回值 办理页面显示的材料类别。
 */
function categoryName(source: Source) {
  const labels = source.caseKind === 'filing' ? materials.filings : reportSubmissionMaterials
  return labels[source.attachmentKind] || source.attachmentKind
}

/**
 * 用途 提交原件、有效期及监管登记依据，同一内容重试复用请求号。
 * 参数 无，读取当前表单。
 * 返回值 登记任务，成功后发送已保存记录标识。
 */
async function save() {
  if (!selected.value || busy.value || searching.value || !evidence.value.trim()) return
  const input = { sourceAttachmentId: selected.value, validUntil: validUntil.value,
    warningDays: warningDays.value, evidence: evidence.value.trim() }
  const fingerprint = JSON.stringify(input)
  if (lastInput !== fingerprint) {
    requestId = crypto.randomUUID()
    lastInput = fingerprint
  }
  const active = new AbortController()
  request = active
  busy.value = true
  failure.value = ''
  try {
    const response = await api<{ id: string }>('/material-expiries',
      { method: 'POST', body: { ...input, requestId }, signal: active.signal })
    if (!active.signal.aborted) emit('saved', response.id)
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === active) busy.value = false
  }
}

watch(selected, () => {
  validUntil.value = ''
  evidence.value = ''
  warningDays.value = 7
})
onMounted(() => { if (props.reportId) void search() })
onScopeDispose(() => { lookup?.abort(); request?.abort() })
</script>

<template>
  <section class="panel expiry-register" :aria-label="heading">
    <h2>{{ heading }}</h2>
    <p v-if="reportId && searching" class="muted" role="status">正在查找本报告的正式测评材料。</p>
    <button v-if="reportId && !sources && !searching && failure" class="button neutral" type="button"
      @click="search()">重试</button>
    <form v-if="!reportId" class="expiry-source-search" @submit.prevent="search()">
      <label class="field"><span>核对材料来源</span>
        <input v-model="keyword" type="search" maxlength="120" :disabled="searching || busy"
          placeholder="备案或报告编号、系统、单位、原件文件名">
      </label>
      <label class="field"><span>业务类型</span><select v-model="caseKind" :disabled="searching || busy">
        <option value="">全部类型</option>
        <option value="filing">已通过备案</option>
        <option value="report">终审通过报告</option>
      </select></label>
      <button class="button neutral" type="submit" :disabled="searching || busy">
        {{ searching ? '查找中…' : '查找正式材料' }}
      </button>
    </form>
    <p v-if="sources && !sources.items.length" class="muted" role="status">暂无符合条件的待登记材料。</p>
    <div v-if="previousCursors.length || sources?.nextBefore" class="expiry-fields" aria-label="材料候选分页">
      <button class="button neutral" type="button" :disabled="searching || busy || !previousCursors.length"
        @click="search('previous')">上一页</button>
      <button class="button neutral" type="button" :disabled="searching || busy || !sources?.nextBefore"
        @click="search('next')">下一页</button>
    </div>
    <form v-if="sources?.items.length" class="expiry-form" @submit.prevent="save">
      <label class="field"><span>来源材料</span><select v-model="selected" required :disabled="busy || searching">
        <option value="">请选择正式材料</option>
        <option v-for="item in sources.items" :key="item.attachmentId" :value="item.attachmentId">
          {{ item.number }} · {{ item.systemName }} · {{ item.organizationName }} ·
          {{ categoryName(item) }} · {{ item.fileName }}
        </option>
      </select></label>
      <a v-if="downloadURL" class="button neutral" :href="downloadURL" download>下载原件</a>
      <p class="muted">按原件明确记载的有效期登记。原件缺少有效期依据时，保留待登记。</p>
      <div class="expiry-fields">
        <label class="field"><span>有效期至</span>
          <input v-model="validUntil" type="date" required :disabled="busy || searching || !selected">
        </label>
        <label class="field"><span>提前提醒天数</span>
          <input v-model.number="warningDays" type="number" min="0" max="90" required
            :disabled="busy || searching || !selected">
        </label>
      </div>
      <label class="field"><span>日期依据</span>
        <textarea v-model="evidence" rows="3" maxlength="1000" required :disabled="busy || searching || !selected"
          placeholder="填写原件所载日期及对应页码、条款或签注"></textarea>
      </label>
      <button class="button primary" type="submit" :disabled="busy || searching || !selected || !evidence.trim()">
        {{ busy ? '登记中…' : '保存有效期' }}
      </button>
    </form>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <button class="button neutral" type="button" :disabled="busy" @click="emit('close')">关闭登记</button>
  </section>
</template>

<style scoped>
.expiry-register,.expiry-form { display: grid; gap: 12px; }
.expiry-register { padding: 16px; justify-items: start; }
.expiry-form { width: min(100%, 50rem); justify-items: start; }
.expiry-form > .field { width: 100%; }
.expiry-source-search,.expiry-fields { display: flex; gap: 12px; align-items: end; flex-wrap: wrap; }
.expiry-source-search input { min-width: min(18rem, 100%); }
</style>

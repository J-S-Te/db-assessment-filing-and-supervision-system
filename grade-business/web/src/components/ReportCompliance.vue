<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { api, ApiError, attachmentURL, errorText, isAbort, params } from '../lib/api'
import { clausePage, type ClauseEntry, type ClauseSnapshot, writeIdentity } from '../lib/clauses'
import { formatTime, type Attachment, type Page } from '../lib/domain'
import { knowledgePath, knowledgeRecord, type KnowledgeEntry, type KnowledgeEntrySummary,
  type KnowledgeRevisionDetail } from '../lib/knowledge'

const props = defineProps<{ path: string; attachments: Attachment[]; reportVersion: number; reportStatus: string }>()
const emit = defineEmits<{ refresh: [] }>()
const fieldLabels = { assets: '资产清单', findings: '问题清单', risk: '风险等级', recommendations: '整改建议' }
const decisions = { compliant: '符合', needs_remediation: '需整改', insufficient_evidence: '证据不足' }
type Decision = keyof typeof decisions
interface EvidenceField {
  key: keyof typeof fieldLabels
  original: string
  value?: string
  startParagraph: number
  endParagraph: number
  truncated?: boolean
  startPage?: number
  endPage?: number
}
interface Extraction { fields: EvidenceField[]; version: number; confirmedAt?: string }
interface Audit {
  id: string
  reportVersion: number
  attachmentId: string
  attachmentName: string
  extractionVersion: number
  field: EvidenceField
  knowledgeId: string
  knowledgeVersion: number
  knowledgeTitle: string
  knowledgeSource: string
  clause?: ClauseSnapshot
  decision: Decision
  comment: string
  operator: string
  createdAt: string
}
interface AuditPage { items: Audit[]; canRecord: boolean; nextBefore?: string }
interface Candidate {
  id: string
  title: string
  source: string
  version: number
  inOriginal: boolean
  inCorrected: boolean
  clause?: ClauseSnapshot
}
interface CandidatePage { items: Candidate[]; hasMore: boolean }

const endpoint = computed(() => props.path + '/compliance')
const permitted = ref(false)
const editable = computed(() => permitted.value && ['pending_initial', 'pending_final'].includes(props.reportStatus))
const files = computed(() => props.attachments.filter(item => item.kind === 'report_file' && /\.(?:docx|pdf)$/i.test(item.fileName)))
const history = shallowRef<Audit[]>([])
const nextBefore = ref('')
const historyError = ref('')
const loading = ref(false)
const attachment = ref('')
const field = ref('')
const extraction = shallowRef<Extraction>()
const evidence = computed(() => extraction.value?.fields.find(item => item.key === field.value))
const evidenceNote = ref('')
const candidates = shallowRef<CandidatePage>()
const comparison = shallowRef<CandidatePage>()
const candidateError = ref('')
const comparisonError = ref('')
const candidateLoading = ref(false)
const selectedCandidate = shallowRef<Candidate>()
const selectedClause = shallowRef<ClauseSnapshot>()
const catalog = shallowRef<ClauseEntry[]>([])
const catalogAfter = ref('')
const catalogError = ref('')
const catalogLoading = ref(false)
const requestID = writeIdentity()
let catalogRequest: AbortController | undefined
const keyword = ref('')
const matches = shallowRef<KnowledgeEntrySummary[]>([])
const hasMoreMatches = ref(false)
const knowledgeID = ref('')
const knowledge = shallowRef<KnowledgeEntry>()
const knowledgeNote = ref('')
const pinned = shallowRef<{ id: string; value?: KnowledgeRevisionDetail; error?: string }>()
const decision = ref<Decision | ''>('')
const comment = ref('')
const saving = ref(false)
const saveNotice = ref('')
const saveError = ref('')
let historyRequest: AbortController | undefined
let evidenceRequest: AbortController | undefined
let candidateRequest: AbortController | undefined
let lookupRequest: AbortController | undefined
let knowledgeRequest: AbortController | undefined
let pinnedRequest: AbortController | undefined
let writeRequest: AbortController | undefined

/**
 * 用途 读取最近留痕或追加下一页历史记录。
 * 参数 before 为更早历史的可选游标。
 * 返回值 历史读取任务。
 */
async function loadHistory(before = '') {
  historyRequest?.abort()
  const request = new AbortController()
  historyRequest = request
  loading.value = true
  historyError.value = ''
  try {
    const page = await api<AuditPage>(endpoint.value + params({ before }), { signal: request.signal })
    if (historyRequest !== request) return
    history.value = before ? [...history.value, ...page.items] : page.items
    nextBefore.value = page.nextBefore ?? ''
    permitted.value = page.canRecord
  } catch (error) {
    if (historyRequest === request && !isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === request) loading.value = false
  }
}

/**
 * 用途 读取选中正文的当前人工校对版本并重置证据选择。
 * 参数 无，使用监管员选择的实际报告附件标识。
 * 返回值 证据读取任务。
 */
async function loadEvidence() {
  evidenceRequest?.abort()
  extraction.value = undefined
  field.value = ''
  evidenceNote.value = ''
  if (!attachment.value) return
  const request = new AbortController()
  evidenceRequest = request
  try {
    const result = await api<Extraction>(props.path + '/attachments/' + encodeURIComponent(attachment.value) + '/extraction',
      { signal: request.signal })
    if (request.signal.aborted) return
    if (result.confirmedAt) extraction.value = result
    else evidenceNote.value = '请先在报告提取与校对中确认该附件。'
  } catch (error) {
    if (!isAbort(error)) evidenceNote.value = error instanceof ApiError && error.code === 'extraction_not_found'
      ? '该附件暂无提取结果。' : errorText(error)
  }
}

/**
 * 用途 查找所选校对栏目中明确引用的启用法规和标准。
 * 参数 无，使用当前附件、栏目与校对版本。
 * 返回值 候选查找任务。
 */
async function loadCandidates() {
  if (!extraction.value || !evidence.value) return
  candidateRequest?.abort()
  const request = new AbortController()
  candidateRequest = request
  candidates.value = undefined
  comparison.value = undefined
  candidateError.value = comparisonError.value = ''
  candidateLoading.value = true
  try {
    const query = params({ attachmentId: attachment.value,
      extractionVersion: extraction.value.version, fieldKey: evidence.value.key })
    const [documents, clauses] = await Promise.allSettled([
      api<CandidatePage>(endpoint.value + '/candidates' + query, { signal: request.signal }),
      api<CandidatePage>(endpoint.value + '/clause-candidates' + query, { signal: request.signal }),
    ])
    if (request.signal.aborted) return
    if (documents.status === 'fulfilled') candidates.value = documents.value
    else candidateError.value = errorText(documents.reason)
    if (clauses.status === 'fulfilled') comparison.value = clauses.value
    else comparisonError.value = errorText(clauses.reason)
  } finally {
    if (candidateRequest === request) candidateLoading.value = false
  }
}

/**
 * 用途 在栏目或校对结果变化时清除该份证据关联的候选。
 * 参数 无。
 * 返回值 无。
 */
function resetCandidates() {
  candidateRequest?.abort()
  candidateRequest = undefined
  candidateLoading.value = false
  candidates.value = comparison.value = undefined
  candidateError.value = comparisonError.value = ''
  selectedClause.value = undefined
  if (!selectedCandidate.value) return
  selectedCandidate.value = undefined
  knowledgeID.value = ''
  knowledge.value = undefined
  knowledgeNote.value = ''
}

/**
 * 用途 在人工选用引用候选后核对该条目的当前启用版本。
 * 参数 candidate 为用户点选的标题、来源和版本。
 * 返回值 无。
 */
function useCandidate(candidate: Candidate) {
  selectedClause.value = candidate.clause
  if (knowledgeID.value === candidate.id && selectedCandidate.value?.version === candidate.version) {
    void loadKnowledge()
    return
  }
  selectedCandidate.value = candidate
  knowledgeID.value = candidate.id
}

/**
 * 用途 搜索可作为人工判定依据的当前启用知识条目。
 * 参数 无，使用表单中的关键词。
 * 返回值 知识查询任务。
 */
async function searchKnowledge() {
  lookupRequest?.abort()
  const request = new AbortController()
  lookupRequest = request
  selectedCandidate.value = selectedClause.value = undefined
  knowledgeID.value = ''
  knowledgeNote.value = ''
  matches.value = []
  hasMoreMatches.value = false
  try {
    const page = await api<Page<KnowledgeEntrySummary>>(knowledgePath + params({
      q: keyword.value.trim(), status: 'active', limit: 20,
    }), { signal: request.signal })
    if (request.signal.aborted) return
    matches.value = page.items
    hasMoreMatches.value = Boolean(page.nextCursor)
    if (!page.items.length) knowledgeNote.value = '未找到符合条件的启用条目。'
  } catch (error) {
    if (!isAbort(error)) knowledgeNote.value = errorText(error)
  }
}

/**
 * 用途 读取所选知识正文，校验候选版本仍与用户看到的一致。
 * 参数 无，使用当前选中的知识条目标识。
 * 返回值 知识详情读取任务。
 */
async function loadKnowledge() {
  knowledgeRequest?.abort()
  knowledge.value = undefined
  if (!knowledgeID.value) return
  knowledgeNote.value = ''
  const request = new AbortController()
  knowledgeRequest = request
  const candidateVersion = selectedCandidate.value?.version
  try {
    const result = await api<KnowledgeEntry>(knowledgeRecord(knowledgeID.value), { signal: request.signal })
    if (request.signal.aborted) return
    if (result.inactiveAt) knowledgeNote.value = '条目已停用，请重新搜索。'
    else if (candidateVersion !== undefined && result.version !== candidateVersion) {
      knowledgeNote.value = '候选知识条目已更新，请重新查找引用的知识条目。'
    } else knowledge.value = result
  } catch (error) {
    if (!isAbort(error)) knowledgeNote.value = errorText(error)
  }
}

/**
 * 用途 读取当前所选法规或标准的条款，支持人工选取未在报告中明载的条款。
 * 参数 after 为可选的上一页末条款号。
 * 返回值 目录查询任务。
 */
async function loadCatalog(after = '') {
  catalogRequest?.abort()
  catalogError.value = ''
  catalogLoading.value = false
  if (!after) {
    catalog.value = []
    catalogAfter.value = ''
  }
  const entry = knowledge.value
  if (!entry || !['regulation', 'standard'].includes(entry.category)) return
  const request = new AbortController()
  catalogRequest = request
  catalogLoading.value = true
  try {
    const result = await clausePage(knowledgeRecord(entry.id), request.signal, after)
    if (request.signal.aborted) return
    if (result.knowledgeVersion !== entry.version) {
      selectedClause.value = undefined
      catalogError.value = '知识条目已更新，请重新选择。'
      return
    }
    const active = result.items.filter(item => item.active)
    catalog.value = after ? [...catalog.value, ...active] : active
    catalogAfter.value = result.nextAfter ?? ''
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) catalogError.value = errorText(error)
  } finally {
    if (catalogRequest === request) catalogLoading.value = false
  }
}

/**
 * 用途 核对历史记录引用的固定知识正文。
 * 参数 audit 为已保存的合规依据记录。
 * 返回值 固定版本读取或折叠任务。
 */
async function showPinned(audit: Audit) {
  pinnedRequest?.abort()
  if (pinned.value?.id === audit.id) {
    pinned.value = undefined
    return
  }
  pinned.value = { id: audit.id }
  const request = new AbortController()
  pinnedRequest = request
  try {
    const path = knowledgeRecord(audit.knowledgeId) + '/versions/' + audit.knowledgeVersion
    const value = await api<KnowledgeRevisionDetail>(path, { signal: request.signal })
    if (!request.signal.aborted) pinned.value = { id: audit.id, value }
  } catch (error) {
    if (!isAbort(error)) pinned.value = { id: audit.id, error: errorText(error) }
  }
}

/**
 * 用途 把人工选择的证据、当前知识版本和判定写入留痕。
 * 参数 无，使用表单已选择的版本与说明。
 * 返回值 保存及历史刷新任务。
 */
async function save() {
  if (!editable.value || saving.value || !extraction.value || !evidence.value || !knowledge.value ||
    !decision.value || !comment.value.trim()) return
  const request = new AbortController()
  writeRequest = request
  saving.value = true
  saveError.value = saveNotice.value = ''
  try {
    const clause = selectedClause.value
    const body = {
      expectedReportVersion: props.reportVersion, attachmentId: attachment.value,
      extractionVersion: extraction.value.version, fieldKey: evidence.value.key,
      knowledgeId: knowledge.value.id, knowledgeVersion: knowledge.value.version,
      decision: decision.value, comment: comment.value.trim(),
      ...(clause ? { clauseNumber: clause.number, clauseVersion: clause.version } : {}),
    }
    await api<Audit>(endpoint.value, { method: 'POST', signal: request.signal,
      body: { ...body, requestId: requestID({ report: props.path, ...body }) } })
    if (request.signal.aborted) return
    requestID()
    comment.value = ''
    decision.value = ''
    saveNotice.value = '合规依据已登记。'
    await loadHistory()
  } catch (error) {
    if (isAbort(error)) return
    saveError.value = errorText(error)
    if (error instanceof ApiError && error.code === 'source_conflict') {
      await Promise.all([loadEvidence(), loadKnowledge()])
    } else if (error instanceof ApiError && error.code === 'version_conflict') {
      emit('refresh')
      await loadHistory()
    }
  } finally {
    if (writeRequest === request) saving.value = false
  }
}

watch([endpoint, () => props.reportVersion], () => { void loadHistory() }, { immediate: true })
watch(files, (current) => {
  if (!current.some(item => item.id === attachment.value)) attachment.value = ''
})
watch(attachment, loadEvidence)
watch(evidence, resetCandidates)
watch([knowledgeID, () => selectedCandidate.value?.version], loadKnowledge)
watch(knowledge, () => { void loadCatalog() })
onBeforeUnmount(() => {
  for (const request of [historyRequest, evidenceRequest, candidateRequest, lookupRequest, knowledgeRequest,
    pinnedRequest, writeRequest, catalogRequest]) {
    request?.abort()
  }
})
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="compliance-title">
    <header class="panel-heading panel-compact"><h2 id="compliance-title">合规依据留痕</h2></header>
    <template v-if="editable">
      <p class="muted">依据真实报告段落和启用的知识条目登记人工判定。报告审批在审核操作中办理。</p>
      <p v-if="!files.length" class="muted">报告尚无可校对的 DOCX 或 PDF 正文。</p>
      <form v-else class="compliance-form" @submit.prevent="save">
        <label class="field">
          <span>报告正文</span>
          <select v-model="attachment" :disabled="saving">
            <option value="">选择报告正文</option>
            <option v-for="file in files" :key="file.id" :value="file.id">
              {{ file.fileName }} · {{ formatTime(file.uploadedAt) }}
            </option>
          </select>
        </label>
        <button v-if="attachment" class="text-action" type="button" :disabled="saving" @click="loadEvidence">刷新校对结果</button>
        <p v-if="evidenceNote" class="muted" role="status">{{ evidenceNote }}</p>
        <label v-if="extraction" class="field">
          <span>证据栏目 · 校对版本 {{ extraction.version }}</span>
          <select v-model="field" :disabled="saving">
            <option value="">选择证据栏目</option>
            <option v-for="item in extraction.fields" :key="item.key" :value="item.key">
              {{ fieldLabels[item.key] }}（{{ item.startPage ? '第 ' + item.startPage + '–' + item.endPage + ' 页，' : '' }}第
              {{ item.startParagraph }}–{{ item.endParagraph }} 段）
            </option>
          </select>
        </label>
        <details v-if="evidence">
          <summary>查看原文及校对值</summary>
          <p v-if="evidence.truncated" class="muted">抽取快照为节选，可下载报告核对完整内容。</p>
          <pre>{{ evidence.original }}</pre>
          <template v-if="evidence.value !== undefined"><p>校对值</p><pre>{{ evidence.value }}</pre></template>
        </details>

        <div v-if="evidence" class="candidate-results">
          <button class="button neutral" type="button" :disabled="saving || candidateLoading" @click="loadCandidates">
            {{ candidateLoading ? '正在查找引用…' : '查找引用的知识条目及条款' }}
          </button>
          <p v-if="comparisonError" class="form-message error-message" role="alert">{{ comparisonError }}</p>
          <template v-else-if="comparison">
            <p v-if="!comparison.items.length" class="muted" role="status">
              未匹配到已登记的明确条款引用，可从知识条目中人工选取。
            </p>
            <ul v-else class="candidate-list" aria-label="报告引用的条款候选">
              <li v-for="item in comparison.items" :key="item.id + ':' + item.clause?.number">
                <details>
                  <summary>《{{ item.title }}》 · {{ item.source }} · 条款 {{ item.clause?.number }}
                    · {{ item.inOriginal && item.inCorrected ? '原文及校对值' : item.inOriginal ? '原文' : '校对值' }}</summary>
                  <pre>{{ item.clause?.text }}</pre>
                </details>
                <button class="text-action" type="button" :disabled="saving" @click="useCandidate(item)">选用条款</button>
              </li>
            </ul>
            <p v-if="comparison.hasMore" class="muted">已显示前 20 项，其他条款可从知识目录选取。</p>
          </template>
          <p v-if="candidateError" class="form-message error-message" role="alert">{{ candidateError }}</p>
          <template v-else-if="candidates">
            <p v-if="!candidates.items.length" class="muted" role="status">
              未找到与本栏书名号内完整标题相同的启用法规或标准，可按关键词查找。
            </p>
            <ul v-else class="candidate-list" aria-label="报告引用的知识候选">
              <li v-for="item in candidates.items" :key="item.id">
                <span>《{{ item.title }}》 · 版本 {{ item.version }} · {{ item.source }}
                  · {{ item.inOriginal && item.inCorrected ? '原文及校对值' : item.inOriginal ? '原文' : '校对值' }}</span>
                <button class="text-action" type="button" :disabled="saving" @click="useCandidate(item)">选用</button>
              </li>
            </ul>
            <p v-if="candidates.hasMore" class="muted">当前显示前 20 项，可按知识标题或来源继续查找。</p>
          </template>
        </div>
        <div class="knowledge-search">
          <label class="field"><span>知识库关键词</span>
            <input v-model="keyword" type="search" maxlength="120" :disabled="saving" @keydown.enter.prevent="searchKnowledge">
          </label>
          <button class="button neutral" type="button" :disabled="saving" @click="searchKnowledge">查找启用条目</button>
        </div>
        <p v-if="knowledgeNote" class="muted" role="status">{{ knowledgeNote }}</p>
        <p v-if="hasMoreMatches" class="muted">当前显示前 20 项，可补充关键词缩小范围。</p>
        <label v-if="matches.length" class="field">
          <span>知识条目</span>
          <select v-model="knowledgeID" :disabled="saving" @change="selectedCandidate = selectedClause = undefined">
            <option value="">选择知识条目</option>
            <option v-for="item in matches" :key="item.id" :value="item.id">{{ item.title }} · {{ item.source }}</option>
          </select>
        </label>
        <details v-if="knowledge">
          <summary>{{ knowledge.title }} · 版本 {{ knowledge.version }}</summary>
          <p>{{ knowledge.source }}</p><pre>{{ knowledge.body }}</pre>
        </details>
        <div v-if="knowledge && ['regulation', 'standard'].includes(knowledge.category)">
          <details v-if="catalog.length || catalogAfter">
            <summary>从知识条目选取条款</summary>
            <ul class="candidate-list">
              <li v-for="item in catalog" :key="item.number">
                <button class="text-action" type="button" :disabled="saving" @click="selectedClause = item">
                  {{ item.number }} · 条款版本 {{ item.version }}
                </button>
              </li>
            </ul>
            <button v-if="catalogAfter" class="text-action" type="button" :disabled="catalogLoading || saving"
              @click="loadCatalog(catalogAfter)">加载更多条款</button>
          </details>
          <p v-if="catalogError" class="error-message" role="alert">{{ catalogError }}</p>
          <details v-if="selectedClause" open>
            <summary>所选条款 {{ selectedClause.number }} · 版本 {{ selectedClause.version }}</summary>
            <pre>{{ selectedClause.text }}</pre>
            <button class="text-action" type="button" :disabled="saving" @click="selectedClause = undefined">取消选用条款</button>
          </details>
        </div>
        <label class="field">
          <span>人工判定</span>
          <select v-model="decision" :disabled="saving" required>
            <option value="">选择人工判定</option>
            <option v-for="(label, key) in decisions" :key="key" :value="key">{{ label }}</option>
          </select>
        </label>
        <label class="field"><span>可复核说明</span>
          <textarea v-model="comment" rows="3" maxlength="1000" :disabled="saving" required />
        </label>
        <button class="button primary" type="submit" :disabled="!evidence || !knowledge || !decision || !comment.trim() || saving">
          {{ saving ? '正在登记…' : '登记合规依据' }}
        </button>
      </form>
    </template>
    <p v-else-if="!loading && !historyError" class="muted">待初审或待终审时可新增合规依据。</p>
    <p v-if="saveNotice" class="form-message" role="status">{{ saveNotice }}</p>
    <p v-if="saveError" class="form-message error-message" role="alert">{{ saveError }}</p>
    <p v-if="historyError" class="form-message error-message" role="alert">
      {{ historyError }} <button class="text-action" type="button" @click="loadHistory()">重试</button>
    </p>
    <p v-else-if="loading && !history.length" class="muted" role="status">正在读取登记记录…</p>
    <p v-else-if="!history.length" class="muted">暂无人工合规登记。</p>
    <ol v-if="history.length" class="compliance-history">
      <li v-for="item in history" :key="item.id">
        <strong>{{ decisions[item.decision] }}</strong> · {{ item.operator }} · {{ formatTime(item.createdAt) }}
        <p>{{ item.comment }}</p>
        <p>
          <a :href="attachmentURL(path, item.attachmentId)">{{ item.attachmentName }}</a>
          · {{ fieldLabels[item.field.key] }}
          <template v-if="item.field.startPage">第 {{ item.field.startPage }}–{{ item.field.endPage }} 页 ·</template>
          第 {{ item.field.startParagraph }}–{{ item.field.endParagraph }} 段
          · 校对版本 {{ item.extractionVersion }} · 报告版本 {{ item.reportVersion }}
        </p>
        <p>{{ item.knowledgeTitle }} · 版本 {{ item.knowledgeVersion }} · {{ item.knowledgeSource }}</p>
        <details v-if="item.clause">
          <summary>条款 {{ item.clause.number }} · 版本 {{ item.clause.version }}</summary>
          <pre>{{ item.clause.text }}</pre>
        </details>
        <button class="text-action" type="button" :aria-expanded="pinned?.id === item.id" @click="showPinned(item)">
          {{ pinned?.id === item.id ? '收起知识正文' : '查看固定知识版本' }}
        </button>
        <template v-if="pinned?.id === item.id">
          <pre v-if="pinned.value">{{ pinned.value.body }}</pre>
          <p v-else-if="pinned.error" class="error-message" role="alert">{{ pinned.error }}</p>
          <p v-else class="muted" role="status">正在读取固定版本…</p>
        </template>
        <details>
          <summary>查看证据快照</summary>
          <p v-if="item.field.truncated" class="muted">抽取快照为节选。</p>
          <pre>{{ item.field.original }}</pre>
          <template v-if="item.field.value !== undefined"><p>校对值</p><pre>{{ item.field.value }}</pre></template>
        </details>
      </li>
    </ol>
    <button v-if="nextBefore" class="button neutral" type="button" :disabled="loading" @click="loadHistory(nextBefore)">
      加载更早记录
    </button>
  </section>
</template>

<style scoped>
.compliance-form { display: grid; gap: 10px; align-items: start; margin-bottom: 16px; }
.compliance-form > button { justify-self: start; }
.knowledge-search { display: flex; flex-wrap: wrap; align-items: end; gap: 8px; }
.knowledge-search .field { flex: 1 1 180px; }
.candidate-list { display: grid; gap: 8px; padding-inline-start: 21px; }
.candidate-list li { overflow-wrap: anywhere; }
.candidate-list button { margin-inline-start: 8px; }
.compliance-history { display: grid; gap: 14px; padding-inline-start: 21px; }
.compliance-history li { padding: 10px 0; }
.compliance-history p { margin: 5px 0; overflow-wrap: anywhere; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; max-height: 16rem; overflow: auto; }
summary { cursor: pointer; }
</style>

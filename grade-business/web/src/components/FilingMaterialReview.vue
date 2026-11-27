<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, useId, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { ListChecks, Quote, RefreshCw } from '@lucide/vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import type { Attachment, FilingMaterialReview, FilingReviewItem, FilingReviewStatus,
  FilingRuleCheck, FilingRuleEvidence, FilingRuleReview } from '../lib/domain'
import { canPreviewWord, reviewRows, wordEvidenceTarget, wordParagraphKey,
  type WordEvidenceTarget } from '../lib/filingReviewEvidence'
import FilingReviewDocument from './FilingReviewDocument.vue'

const props = defineProps<{
  path: string; revision: string; attachments: Attachment[]
  checks?: FilingRuleCheck[]; source?: 'deepseek' | 'local_rules'
}>()
const emit = defineEmits<{ refresh: [] }>()
type Draft = { status: FilingReviewStatus; remark: string; baseVersion: number; saving: boolean; failure: string }
type Row = FilingReviewItem & { draft: Draft }
type ReviewPosition = { ruleId?: string; offset: number; scrollTop: number }
const form = ref<{ materialRevision: number; items: Row[] }>()
const pendingMaterial = shallowRef<FilingMaterialReview>()
const loading = ref(false)
const failure = ref('')
let requests = new AbortController()
const attachmentId = ref('')
const showAll = ref(false)
const target = shallowRef<WordEvidenceTarget>()
const activeAttachment = computed(() => props.attachments.find(file => file.id === attachmentId.value) ??
  props.attachments.find(file => canPreviewWord(file)) ?? props.attachments[0])
const checks = computed(() => new Map(props.checks?.map(check => [check.ruleId, check])))
const rows = computed(() => reviewRows(form.value?.items ?? [], checks.value,
  showAll.value ? undefined : activeAttachment.value))
const scopeKey = computed(() => showAll.value ? 'all' : 'attachment:' + (activeAttachment.value?.id ?? ''))
const positions = new Map<string, ReviewPosition>()
const saving = computed(() => form.value?.items.some(item => item.draft.saving))
const unsavedCount = computed(() => form.value?.items.filter(dirty).length ?? 0)
const hasUnsaved = computed(() => unsavedCount.value > 0 || Boolean(saving.value))
const checkLabels = { pass: '通过', fail: '不通过', manual: '待人工核实', not_applicable: '不适用' }
const reviewLabels = { pending: '未审核', pass: '通过', fail: '不通过' }
const reviewedCount = computed(() => form.value?.items.filter(item => item.review && item.review.status !== 'pending').length ?? 0)
const selection = shallowRef<{ rule: string; index: number; evidence: FilingRuleEvidence }>()
const workspace = ref<HTMLElement>()
const commentPane = ref<HTMLElement>()
const commentList = ref<HTMLOListElement>()
type VisibleCitation = {
  id: string; ruleId: string; source: DOMRect; x: number; y: number; active: boolean
}
type Connector = {
  id: string; ruleId: string; path: string; x: number; y: number; endX: number; endY: number
  active: boolean; outside?: 'above' | 'below'
}
const connectors = shallowRef<Connector[]>([])
const linkedAnchors = shallowRef<ReadonlySet<string>>(new Set())
const connectorColors = {
  pending: { start: '#087e9c', end: '#315cc6' },
  pass: { start: '#13814a', end: '#22853d' },
  fail: { start: '#c32f47', end: '#b52239' },
} satisfies Record<FilingReviewStatus, { start: string; end: string }>
const reviewStatuses = computed(() => new Map(
  form.value?.items.map(item => [item.ruleId, item.draft.status]),
))
const coloredConnectors = computed(() => connectors.value.map(link => {
  const status = reviewStatuses.value.get(link.ruleId) ?? 'pending'
  return { ...link, status, color: connectorColors[status] }
}))
const activeIndex = computed(() => form.value?.items.findIndex(item => item.ruleId === selection.value?.rule) ?? -1)
const connectorGradient = useId()
let frame = 0
let resize: ResizeObserver | undefined

/**
 * 用途 激活某条审核依据，并将相应附件交给原文预览。
 * 参数 rule、index、evidence 为审核项、该项内的依据顺序和实际出处。
 * 返回值 无。
 */
async function selectEvidence(rule: string, index: number, evidence: FilingRuleEvidence) {
  selection.value = { rule, index, evidence }
  target.value = wordEvidenceTarget(evidence, props.attachments)
  if (target.value) attachmentId.value = target.value.attachment.id
  await nextTick()
  workspace.value?.querySelector<HTMLElement>('[data-current-citation]')?.scrollIntoView({ block: 'nearest' })
  queueLink()
}

/**
 * 用途 清除此前的原文锚点、批注激活态与连线。
 * 参数 无。
 * 返回值 无。
 */
function clearCitation() {
  selection.value = target.value = undefined
  connectors.value = []
  linkedAnchors.value = new Set()
}

/**
 * 用途 为当前附件中可定位的审核依据生成段落标识。
 * 参数 evidence 为审核依据，包含附件标识和原始出处。
 * 返回值 当前预览可匹配的段落标识，其他附件或无段号时为空。
 */
function citationParagraph(evidence: FilingRuleEvidence) {
  const attachment = activeAttachment.value
  if (!attachment || attachment.id !== evidence.attachmentId) return
  const source = wordEvidenceTarget(evidence, [attachment])
  return wordParagraphKey(source?.member, source?.paragraph)
}

/**
 * 用途 让附件预览与批注范围切换到同一文件。
 * 参数 id 为用户选择的备案附件标识。
 * 返回值 恢复批注滚动位置后的异步任务。
 */
async function chooseAttachment(id: string) {
  const before = scopeKey.value
  rememberPosition()
  clearCitation()
  attachmentId.value = id
  const next = scopeKey.value
  await nextTick()
  if (next !== before && next === scopeKey.value) restorePosition(undefined, positions.get(next))
  queueLink()
}

/**
 * 用途 在附件批注与整份备案的审核表之间切换。
 * 参数 all 为是否查看整份备案的审核项。
 * 返回值 恢复批注滚动位置后的异步任务。
 */
async function changeScope(all: boolean) {
  if (showAll.value === all) return
  const previous = rememberPosition()
  showAll.value = all
  const next = scopeKey.value
  await nextTick()
  if (next !== scopeKey.value) return
  restorePosition(previous, positions.get(next))
  queueLink()
}

/**
 * 用途 记录当前阅读的审核项及其在滚动区内的位置。
 * 参数 无。
 * 返回值 当前作用域的位置，滚动区未挂载时为空。
 */
function rememberPosition(): ReviewPosition | undefined {
  const pane = commentPane.value
  if (!pane) return
  const bounds = pane.getBoundingClientRect()
  const visible = [...pane.querySelectorAll<HTMLElement>('[data-review-item]')]
    .filter(card => visibleMidpoint(card.getBoundingClientRect(), bounds) !== undefined)
  const anchor = visible.find(card => card.dataset.reviewItem === selection.value?.rule) ?? visible[0]
  const position = {
    ruleId: anchor?.dataset.reviewItem, offset: anchor ? anchor.getBoundingClientRect().top - bounds.top : 0,
    scrollTop: pane.scrollTop,
  }
  positions.set(scopeKey.value, position)
  return position
}

/**
 * 用途 尽量把相同审核项保留在原位置，找不到时恢复目标范围的阅读位置。
 * 参数 preferred、fallback 为优先和备用的滚动锚点。
 * 返回值 无。
 */
function restorePosition(preferred?: ReviewPosition, fallback?: ReviewPosition) {
  const pane = commentPane.value
  if (!pane) return
  const cards = [...pane.querySelectorAll<HTMLElement>('[data-review-item]')]
  for (const position of [preferred, fallback]) {
    if (!position?.ruleId) continue
    const card = cards.find(item => item.dataset.reviewItem === position.ruleId)
    if (!card) continue
    pane.scrollTop += card.getBoundingClientRect().top - pane.getBoundingClientRect().top - position.offset
    return
  }
  pane.scrollTop = fallback?.scrollTop ?? 0
}

/**
 * 用途 在窄屏上从文档回到正在核对的批注。
 * 参数 无。
 * 返回值 无。
 */
function returnToCitation() {
  const button = workspace.value?.querySelector<HTMLElement>('[data-current-citation]')
  button?.focus({ preventScroll: true })
  button?.scrollIntoView({ block: 'center' })
}

/**
 * 用途 将预览或批注的滚动与尺寸变化合并到下一次绘制。
 * 参数 无。
 * 返回值 无。
 */
function queueLink() {
  if (!frame) frame = requestAnimationFrame(drawCitations)
}

/**
 * 用途 计算段落或出处在可滚动面板内的可见位置。
 * 参数 bounds、pane 分别为内容和面板的边界。
 * 返回值 可见部分的垂直中心，完全不可见时为空。
 */
function visibleMidpoint(bounds: DOMRect, pane: DOMRect) {
  const top = Math.max(bounds.top, pane.top)
  const bottom = Math.min(bounds.bottom, pane.bottom)
  return bottom > top ? (top + bottom) / 2 : undefined
}

/**
 * 用途 收集可见依据的位置以及它们在原文预览中的段落标记。
 * 参数 preview、comments 为原文与批注的滚动区，pane、column 为各自边界。
 * 返回值 对应段落标记和可定位的依据坐标。
 */
function visibleCitations(preview: HTMLElement, comments: HTMLElement, pane: DOMRect, column: DOMRect) {
  const origins = new Map(Array.from(preview.querySelectorAll<HTMLElement>('[data-review-paragraph]'),
    paragraph => [paragraph.dataset.reviewParagraph, paragraph]))
  const anchors = new Set<string>()
  const citations: VisibleCitation[] = []
  for (const button of comments.querySelectorAll<HTMLElement>('[data-review-citation]')) {
    const anchor = button.dataset.reviewCitation
    const id = button.dataset.reviewCitationId
    const ruleId = button.dataset.reviewRule
    const origin = origins.get(anchor)
    if (!id || !ruleId || !anchor || !origin) continue
    const bounds = button.getBoundingClientRect()
    const y = visibleMidpoint(bounds, column)
    if (y === undefined) continue
    const source = origin.getBoundingClientRect()
    if (visibleMidpoint(source, pane) !== undefined) anchors.add(anchor)
    citations.push({ id, ruleId, source, x: bounds.left, y, active: button.hasAttribute('data-current-citation') })
  }
  return { anchors, citations }
}

/**
 * 用途 生成单条证据连线，原文滚出视口时将端点落在对应方向的边缘。
 * 参数 citation 为批注与原文坐标，pane、area 为预览面板与画布边界。
 * 返回值 曲线、端点及原文是否在可见范围。
 */
function makeConnector(citation: VisibleCitation, pane: DOMRect, area: DOMRect): Connector {
  const midpoint = visibleMidpoint(citation.source, pane)
  const outside = midpoint === undefined ? (citation.source.bottom <= pane.top ? 'above' : 'below') : undefined
  const x = citation.source.right - area.left
  const y = (midpoint ?? (outside === 'above' ? pane.top + 9 : pane.bottom - 9)) - area.top
  const endX = citation.x - area.left
  const endY = citation.y - area.top
  const bend = (endX - x) * 0.46
  const path = `M ${x} ${y} C ${x + bend} ${y}, ${endX - bend} ${endY}, ${endX} ${endY}`
  return { id: citation.id, ruleId: citation.ruleId, active: citation.active, path, x, y, endX, endY, outside }
}

/**
 * 用途 更新段落标记，为右侧可见依据自动绘制连线，选中连线置于顶层。
 * 参数 无。
 * 返回值 无，窄屏或不存在有效出处时清空画布。
 */
function drawCitations() {
  frame = 0
  const root = workspace.value
  const preview = root?.querySelector<HTMLElement>('[data-review-preview]')
  const comments = commentPane.value
  if (!root || !preview || !comments) return
  const pane = preview.getBoundingClientRect()
  const right = comments.getBoundingClientRect()
  const { anchors, citations } = visibleCitations(preview, comments, pane, right)
  if (linkedAnchors.value.size !== anchors.size || [...anchors].some(key => !linkedAnchors.value.has(key))) {
    linkedAnchors.value = anchors
  }
  if (pane.right > right.left) {
    connectors.value = []
    return
  }
  const area = root.getBoundingClientRect()
  connectors.value = citations
    .filter(citation => citation.source.right < citation.x)
    .sort((first, second) => Number(first.active) - Number(second.active))
    .map(citation => makeConnector(citation, pane, area))
}

/**
 * 用途 同时刷新人工审核记录与所属材料和预审状态。
 * 参数 无。
 * 返回值 无。
 */
function refresh() {
  emit('refresh')
  void load()
}

/**
 * 用途 从业务接口读取当前材料版本的 11 项和已保存的人审意见。
 * 参数 无。
 * 返回值 读取完成后的异步任务。
 */
async function load() {
  if (loading.value) return
  const operation = requests
  const path = props.path
  loading.value = true
  failure.value = ''
  try {
    const result = await api<FilingMaterialReview>(path + '/prechecks/review', { signal: operation.signal })
    if (requests !== operation) return
    applyReviews(result)
  } catch (error) {
    if (requests === operation && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (requests === operation) loading.value = false
  }
}

/**
 * 用途 应用服务端审核记录，同时保留需要继续核对的草稿。
 * 参数 result 为当前材料及其最新审核意见。
 * 返回值 无。
 */
function applyReviews(result: FilingMaterialReview) {
  const previous = form.value
  const sameMaterial = previous?.materialRevision === result.materialRevision
  if (previous && !sameMaterial && hasUnsaved.value) {
    pendingMaterial.value = result
    return
  }
  pendingMaterial.value = undefined
  const items = result.items.map(item => {
    const old = sameMaterial ? previous?.items.find(entry => entry.ruleId === item.ruleId) : undefined
    if (old && dirty(old) && !matchesReview(old.draft, item.review)) {
      return { ...item, draft: { ...old.draft, saving: false, failure: '' } }
    }
    const draft: Draft = {
      status: item.review?.status ?? 'pending', remark: item.review?.remark ?? '',
      baseVersion: item.review?.version ?? 0, saving: false, failure: '',
    }
    return { ...item, draft }
  })
  form.value = { materialRevision: result.materialRevision, items }
}

/**
 * 用途 在用户确认放弃旧草稿后应用新材料的审核记录。
 * 参数 无。
 * 返回值 无。
 */
function acceptNewMaterial() {
  const next = pendingMaterial.value
  if (!next) return
  form.value = undefined
  applyReviews(next)
}

/**
 * 用途 判断草稿内容是否已经与保存记录一致。
 * 参数 draft、review 为当前草稿和最新保存的意见。
 * 返回值 审核结论和备注均一致时为真。
 */
function matchesReview(draft: Draft, review?: FilingRuleReview) {
  return draft.status === (review?.status ?? 'pending') &&
    draft.remark.trim() === (review?.remark ?? '')
}

/**
 * 用途 判断当前项是否有待保存的审核修改。
 * 参数 item 是服务端当前保存的审核。
 * 返回值 结论或备注与保存值不同时为真。
 */
function dirty(item: Row) {
  return !matchesReview(item.draft, item.review)
}

/**
 * 用途 检查同一条草稿是否基于过期的人工审核意见。
 * 参数 item 为当前审核项及其草稿。
 * 返回值 有未保存修改且审核意见版本已变化时为真。
 */
function reviewConflict(item: Row) {
  return dirty(item) && item.draft.baseVersion !== (item.review?.version ?? 0)
}

/**
 * 用途 人工核对并发变更后，按所选意见解除冲突。
 * 参数 item 为冲突项，keepDraft 表示是否继续保留自己的草稿。
 * 返回值 无，后续由用户主动保存。
 */
function resolveReviewConflict(item: Row, keepDraft: boolean) {
  const latest = item.review
  if (!keepDraft) {
    item.draft.status = latest?.status ?? 'pending'
    item.draft.remark = latest?.remark ?? ''
  }
  item.draft.baseVersion = latest?.version ?? 0
  item.draft.failure = ''
}

/**
 * 用途 保存单项人工审核，同时核对材料版本和意见版本。
 * 参数 item 为当前编辑的规则行。
 * 返回值 保存完成后的异步任务。
 */
async function save(item: Row) {
  const draft = item.draft
  const current = form.value
  if (!current || loading.value || pendingMaterial.value || draft.saving ||
    !dirty(item) || reviewConflict(item)) return
  const operation = requests
  draft.saving = true
  draft.failure = ''
  try {
    const result = await api<FilingRuleReview>(props.path + '/prechecks/review/' + encodeURIComponent(item.ruleId), {
      method: 'PUT', signal: operation.signal,
      body: { status: draft.status, remark: draft.remark, materialRevision: current.materialRevision,
        expectedVersion: draft.baseVersion },
    })
    if (requests !== operation || form.value !== current) return
    item.review = result
    draft.status = result.status
    draft.remark = result.remark
    draft.baseVersion = result.version
  } catch (error) {
    if (requests !== operation || isAbort(error)) return
    draft.failure = error instanceof ApiError && error.status === 409
      ? '材料或审核意见已更新。请重新读取后核对再保存。'
      : errorText(error)
  } finally {
    draft.saving = false
  }
}

/**
 * 用途 在离开审阅页面前保护尚未保存的人工审核。
 * 参数 无。
 * 返回值 允许离开时为真。
 */
function confirmLeaving() {
  return !hasUnsaved.value || window.confirm('有未保存的人工审核修改。确定离开材料审阅？')
}

/**
 * 用途 在浏览器刷新、关闭或离开站点时请求原生确认。
 * 参数 event 为浏览器准备离开当前页面的事件。
 * 返回值 无。
 */
function protectBeforeUnload(event: BeforeUnloadEvent) {
  if (!hasUnsaved.value) return
  event.preventDefault()
  event.returnValue = ''
}

onBeforeRouteLeave(confirmLeaving)
onBeforeRouteUpdate((to, from) => to.params.id === from.params.id || confirmLeaving())
watch(hasUnsaved, enabled => {
  if (enabled) window.addEventListener('beforeunload', protectBeforeUnload)
  else window.removeEventListener('beforeunload', protectBeforeUnload)
})

watch(() => [props.path, props.revision] as const, ([path], before) => {
  clearCitation()
  requests.abort()
  requests = new AbortController()
  if (path !== before?.[0]) {
    form.value = pendingMaterial.value = undefined
    attachmentId.value = ''
    showAll.value = false
    positions.clear()
    if (commentPane.value) commentPane.value.scrollTop = 0
  }
  loading.value = false
  void load()
}, { immediate: true })
watch(rows, queueLink, { flush: 'post' })
watch(commentList, (list, previous) => {
  if (previous) resize?.unobserve(previous)
  if (list) resize?.observe(list)
})
watch(() => props.checks, () => {
  const chosen = selection.value
  if (!chosen) return
  const current = checks.value.get(chosen.rule)?.evidence?.[chosen.index]
  const { attachmentId, fileName, location, quote, kind } = chosen.evidence
  if (current?.attachmentId !== attachmentId || current.fileName !== fileName ||
    current.location !== location || current.quote !== quote || current.kind !== kind) clearCitation()
})
onMounted(() => {
  resize = new ResizeObserver(queueLink)
  if (workspace.value) resize.observe(workspace.value)
  if (commentList.value) resize.observe(commentList.value)
})
onBeforeUnmount(() => {
  requests.abort()
  resize?.disconnect()
  cancelAnimationFrame(frame)
  window.removeEventListener('beforeunload', protectBeforeUnload)
})
</script>

<template>
  <div class="material-review">
    <div class="review-heading">
      <h3>逐项审核</h3>
      <template v-if="form">
        <span class="muted">已保存审核 {{ reviewedCount }} / {{ form.items.length }} 项</span>
        <div class="review-progress" aria-hidden="true">
          <span v-for="item in form.items" :key="item.ruleId" :data-status="item.review?.status"
            :class="{ 'progress-current': item.ruleId === selection?.rule }" />
        </div>
        <span v-if="activeIndex >= 0" class="muted">当前第 {{ activeIndex + 1 }} 项</span>
        <span v-if="unsavedCount" class="review-unsaved" role="status">{{ unsavedCount }} 项未保存</span>
      </template>
      <button type="button" class="button neutral small" :disabled="loading || saving"
        @click="refresh"><RefreshCw :size="15" aria-hidden="true" />重新读取</button>
    </div>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <p v-if="loading && !form" class="hint-row" role="status">正在读取逐项审核…</p>
    <div v-if="pendingMaterial" class="form-message warning-message review-material-update" role="alert">
      <p>材料已更新。以下草稿属于上一个材料版本，请先核对或复制需要保留的内容。</p>
      <button type="button" class="button neutral small" :disabled="loading"
        @click="acceptNewMaterial">放弃旧草稿并读取新材料</button>
    </div>
    <div ref="workspace" class="review-workspace" @scroll.capture="queueLink">
      <FilingReviewDocument :key="revision" :path="path" :attachments="attachments"
        :attachment="activeAttachment" :target="target" :linked-anchors="linkedAnchors"
        @select-attachment="chooseAttachment" @return-to-citation="returnToCitation" @geometry="queueLink" />
      <section class="review-comments" aria-labelledby="review-comments-title">
        <header class="review-toolbar">
          <div class="review-toolbar-title">
            <ListChecks :size="19" aria-hidden="true" />
            <div>
              <h3 id="review-comments-title">审核批注</h3>
              <p v-if="form" class="muted" aria-live="polite">
                显示 {{ rows.length }} 项
              </p>
            </div>
          </div>
          <div v-if="form && activeAttachment" class="review-scope" role="group" aria-label="批注范围">
            <button type="button" :aria-pressed="!showAll" @click="changeScope(false)">当前附件</button>
            <button type="button" :aria-pressed="showAll" @click="changeScope(true)">全部审核项</button>
          </div>
        </header>
        <div ref="commentPane" class="review-scroll" data-review-scroll tabindex="0" aria-label="逐项审核批注">
          <p v-if="form && !rows.length" class="review-empty muted" role="status">当前附件暂无关联的审核批注。</p>
          <ol v-if="rows.length" ref="commentList" class="review-items">
            <li v-for="{ item, check, citations } in rows" :key="item.ruleId" class="review-item"
              :data-review-item="item.ruleId">
              <div class="review-row">
                <div class="review-main">
                  <div class="review-rule">
                    <div class="review-card-heading">
                      <h4>{{ item.title }}</h4>
                      <span v-if="dirty(item)" class="review-unsaved">{{ item.draft.saving ? '保存中' : '未保存' }}</span>
                    </div>
                    <div v-if="item.review" class="review-record">
                      <strong>已保存 · {{ reviewLabels[item.review.status] }}</strong>
                      <span>{{ item.review.reviewer }} · {{ formatTime(item.review.reviewedAt) }}</span>
                    </div>
                    <p class="muted">{{ item.method }}</p>
                  </div>
                  <div class="review-rule-result">
                    <template v-if="check">
                      <strong class="review-status" :data-status="check.status">
                        {{ source === 'deepseek' ? 'AI 预审' : '规则预审' }} · {{ checkLabels[check.status] }}
                      </strong>
                      <p>{{ check.message }}</p>
                      <ul v-if="citations.length" class="review-evidence" aria-label="审核依据">
                        <li v-for="{ evidence, index } in citations" :key="index">
                          <button type="button" class="evidence-button"
                            :data-review-citation="citationParagraph(evidence)"
                            :data-review-citation-id="item.ruleId + ':' + index"
                            :data-review-rule="item.ruleId"
                            :aria-pressed="selection?.rule === item.ruleId && selection?.index === index"
                            :data-current-citation="selection?.rule === item.ruleId && selection?.index === index
                              ? '' : undefined"
                            @click="selectEvidence(item.ruleId, index, evidence)">
                            <strong><Quote :size="14" aria-hidden="true" />{{ evidence.fileName }} · {{ evidence.location }}</strong>
                            <span>{{ evidence.quote }}</span>
                          </button>
                          <small v-if="selection?.rule === item.ruleId && selection?.index === index && !target"
                            class="muted" role="status">当前备案已无此附件，请核对预审记录。</small>
                        </li>
                      </ul>
                    </template>
                    <strong v-else class="review-status muted">待预审</strong>
                  </div>
                </div>
                <div class="review-input">
                  <div v-if="reviewConflict(item) && !pendingMaterial" class="review-conflict">
                    <p role="alert">该项审核意见已更新。请核对最新意见与你的草稿。</p>
                    <div class="review-conflict-comparison">
                      <div>
                        <strong>最新已保存 · {{ reviewLabels[item.review?.status ?? 'pending'] }}</strong>
                        <p>{{ item.review?.remark || '无备注' }}</p>
                      </div>
                      <div>
                        <strong>我的草稿 · {{ reviewLabels[item.draft.status] }}</strong>
                        <p>{{ item.draft.remark || '无备注' }}</p>
                      </div>
                    </div>
                    <div class="review-save">
                      <button type="button" class="button neutral small" :disabled="loading"
                        @click="resolveReviewConflict(item, false)">采用已保存意见</button>
                      <button type="button" class="button neutral small" :disabled="loading"
                        @click="resolveReviewConflict(item, true)">保留我的草稿</button>
                    </div>
                  </div>
                  <label>
                    <span>人工审核</span>
                    <select v-model="item.draft.status" :disabled="item.draft.saving || !!pendingMaterial">
                      <option value="pending">未审核</option>
                      <option value="pass">通过</option>
                      <option value="fail">不通过</option>
                    </select>
                  </label>
                  <label>
                    <span>人工备注</span>
                    <textarea v-model="item.draft.remark" rows="2" maxlength="2000"
                      :disabled="item.draft.saving" :readonly="!!pendingMaterial" />
                  </label>
                  <div class="review-save">
                    <button type="button" class="button neutral small"
                      :disabled="loading || !!pendingMaterial || item.draft.saving || !dirty(item) || reviewConflict(item)"
                      @click="save(item)">{{ item.draft.saving ? '保存中' : '保存本项' }}</button>
                  </div>
                  <p v-if="item.draft.failure" class="form-message error-message" role="alert">{{ item.draft.failure }}</p>
                </div>
              </div>
            </li>
          </ol>
        </div>
      </section>
      <svg v-if="connectors.length" class="review-connector" aria-hidden="true" focusable="false">
        <defs>
          <linearGradient v-for="(color, status) in connectorColors" :key="status"
            :id="connectorGradient + '-' + status" gradientUnits="userSpaceOnUse" x1="48%" x2="64%">
            <stop :stop-color="color.start" />
            <stop offset="100%" :stop-color="color.end" />
          </linearGradient>
        </defs>
        <g v-for="link in coloredConnectors" :key="link.id" :data-review-connection="link.id"
          :class="{ 'connector-active': link.active, 'connector-outside': link.outside }">
          <path :d="link.path" class="connector-track" />
          <path :d="link.path" class="connector-line" :stroke="'url(#' + connectorGradient + '-' + link.status + ')'" />
          <path v-if="link.outside" class="connector-direction" d="M -4 2 L 0 -2 L 4 2"
            :stroke="link.color.start"
            :transform="`translate(${link.x} ${link.y}) scale(1 ${link.outside === 'above' ? 1 : -1})`" />
          <circle v-else :cx="link.x" :cy="link.y" :r="link.active ? 4 : 3" :fill="link.color.start" />
          <circle :cx="link.endX" :cy="link.endY" :r="link.active ? 4 : 3" :fill="link.color.end" />
        </g>
      </svg>
    </div>
  </div>
</template>

<style scoped>
.material-review { margin-block-start: 1rem; --review-toolbar-height: 4rem;
  --review-pane-height: clamp(27rem, 68dvh, 56rem); --review-accent: #285bb5;
  --review-border: #cfdeec; --review-panel: #f8fbff; }
.review-heading, .review-save { display: flex; flex-wrap: wrap; align-items: center; gap: .65rem; }
.review-heading { min-height: 2.5rem; margin-block-end: .65rem; }
.review-heading h3, .review-rule h4, .review-item p { margin: 0; }
.review-heading button { margin-inline-start: auto; }
.review-progress { display: flex; gap: 3px; }
.review-progress span { width: .65rem; height: .35rem; border-radius: 2px; background: #b6c3d2; }
.review-progress [data-status="pass"] { background: #22853d; }
.review-progress [data-status="fail"] { background: #b52239; }
.review-progress .progress-current { outline: 1px solid var(--review-accent); outline-offset: 1px; }
.review-unsaved { color: #795014; background: #fff3d9; padding: .1rem .45rem; border-radius: 4px;
  font-size: .82rem; font-weight: 600; }
.review-material-update { display: flex; align-items: center; flex-wrap: wrap; gap: .6rem; }
.review-material-update p { margin: 0; }
.review-workspace { position: relative; display: grid; align-items: start; gap: 1.35rem; }
.review-comments { min-width: 0; border: 1px solid var(--review-border); border-radius: 14px;
  background: #eef4fb; overflow: hidden; box-shadow: 0 6px 18px #1734590d; }
.review-toolbar, .review-toolbar-title { display: flex; align-items: center; gap: .65rem; }
.review-toolbar { justify-content: space-between; flex-wrap: wrap; min-height: var(--review-toolbar-height);
  padding: .6rem .9rem; background: var(--review-panel); border-bottom: 1px solid var(--review-border); }
.review-toolbar-title > svg { color: var(--review-accent); flex: none; }
.review-toolbar h3, .review-toolbar p { margin: 0; }
.review-toolbar h3 { font-size: 1rem; }
.review-toolbar p { margin-block-start: .1rem; font-size: .8rem; }
.review-scope { display: inline-flex; padding: 2px; background: #ecf2fa;
  border: 1px solid var(--review-border); border-radius: 8px; }
.review-scope button { padding: .32rem .55rem; color: #314a69; border: 0; border-radius: 5px;
  background: transparent; white-space: nowrap; font-size: .84rem; cursor: pointer; }
.review-scope button[aria-pressed="true"] { color: #214f9f; background: #fff; font-weight: 600;
  box-shadow: 0 1px 3px #17345926; }
.review-scroll { height: var(--review-pane-height); overflow: auto; scrollbar-gutter: stable; }
.review-empty { margin: .85rem; padding: 1rem; border: 1px dashed var(--review-border); border-radius: 9px; }
.review-items { display: grid; gap: .7rem; list-style: none; margin: 0; padding: .8rem; }
.review-item { min-width: 0; padding: .9rem 1rem; background: var(--surface, #fff);
  border: 1px solid var(--review-border); border-radius: 11px; box-shadow: 0 2px 7px #1734590b; }
.review-item:has([data-current-citation]) { border-color: #80a7e4; box-shadow: inset 3px 0 #3771cd, 0 3px 12px #254b8720; }
.review-row { display: grid; gap: .8rem; }
.review-rule, .review-rule-result { min-width: 0; overflow-wrap: anywhere; }
.review-rule h4 { font-size: 1rem; }
.review-card-heading, .review-record { display: flex; flex-wrap: wrap; align-items: baseline; gap: .45rem; }
.review-record { margin-block-start: .35rem; color: #344d6b; font-size: .87rem; line-height: 1.45; }
.review-rule p { margin-block-start: .25rem; font-size: .88rem; line-height: 1.55; }
.review-rule-result { margin-block-start: .65rem; }
.review-rule-result > p { margin-block-start: .3rem; line-height: 1.55; }
.review-status { display: inline-block; padding: .16rem .45rem; border-radius: 5px;
  font-size: .8rem; background: #edf2f8; }
.review-status[data-status="fail"] { color: #9b2437; background: #fff0f0; }
.review-status[data-status="pass"] { color: #176346; background: #eaf7f1; }
.review-status[data-status="manual"] { color: #7f5319; background: #fdf3da; }
.review-input { display: grid; gap: .45rem; padding-block-start: .65rem; border-top: 1px solid #e2eaf3; }
.review-input label { display: grid; gap: .24rem; min-width: 0; }
.review-input label span { font-size: .86rem; font-weight: 600; }
.review-input select { max-width: 14rem; }
.review-input textarea { width: 100%; }
.review-conflict { display: grid; gap: .55rem; padding: .65rem; border: 1px solid #e9cb8e;
  border-radius: 7px; background: #fff9eb; overflow-wrap: anywhere; }
.review-conflict-comparison { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 12rem), 1fr));
  gap: .5rem; }
.review-conflict-comparison > div { padding: .5rem; background: #fff; border-radius: 5px; }
.review-conflict-comparison p { white-space: pre-wrap; }
.review-evidence { display: grid; list-style: none; gap: .45rem; margin: .55rem 0 0; padding: 0; }
.evidence-button { display: grid; width: 100%; gap: .24rem; padding: .5rem .6rem; text-align: start;
  color: #274b79; background: #f3f7fc; border: 1px solid #e0e9f3; border-radius: 7px; overflow-wrap: anywhere; }
.evidence-button strong { font-size: .86rem; }
.evidence-button svg { vertical-align: -.16em; margin-inline-end: .3rem; }
.evidence-button span { color: var(--muted, #41576a); }
.evidence-button:hover, .evidence-button[aria-pressed="true"] { background: #eaf2ff; border-color: #7fa9e8; }
.review-connector { position: absolute; inset: 0; width: 100%; height: 100%; pointer-events: none; z-index: 1; }
.review-connector path { fill: none; stroke-linecap: round; }
.connector-track { stroke: #f7fbff; stroke-width: 3.5; }
.connector-line { stroke-width: 1.75; }
.connector-active .connector-track { stroke-width: 5; }
.connector-active .connector-line { stroke-width: 3; }
.connector-outside { opacity: .72; }
.connector-outside .connector-line { stroke-dasharray: 4 4; }
.connector-direction { stroke-width: 2; stroke-linejoin: round; }
.review-connector circle { stroke: white; stroke-width: 1.7; }
@media (min-width: 861px) {
  .review-workspace { grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr); }
}
@media (max-width: 860px) {
  .material-review { --review-pane-height: 55dvh; }
  .review-connector { display: none; }
}
</style>

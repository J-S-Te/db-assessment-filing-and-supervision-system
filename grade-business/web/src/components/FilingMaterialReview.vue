<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { api, ApiError, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import type { Attachment, FilingMaterialReview, FilingReviewItem, FilingReviewStatus,
  FilingRuleCheck, FilingRuleEvidence, FilingRuleReview } from '../lib/domain'
import { wordEvidenceTarget, type WordEvidenceTarget } from '../lib/filingReviewEvidence'
import FilingReviewDocument from './FilingReviewDocument.vue'

const props = defineProps<{
  path: string; revision: string; attachments: Attachment[]
  checks?: FilingRuleCheck[]; source?: 'deepseek' | 'local_rules'
}>()
const emit = defineEmits<{ refresh: [] }>()
type Draft = { status: FilingReviewStatus; remark: string; saving: boolean; failure: string }
type Row = FilingReviewItem & { draft: Draft }
const form = ref<{ materialRevision: number; items: Row[] }>()
const loading = ref(false)
const failure = ref('')
let requests = new AbortController()
const checks = computed(() => new Map(props.checks?.map(check => [check.ruleId, check])))
const rows = computed(() => form.value?.items.map(item => ({ item, check: checks.value.get(item.ruleId) })) ?? [])
const saving = computed(() => form.value?.items.some(item => item.draft.saving))
const checkLabels = { pass: '通过', fail: '不通过', manual: '待人工核实', not_applicable: '不适用' }
const reviewedCount = computed(() => form.value?.items.filter(item => item.review && item.review.status !== 'pending').length ?? 0)
const selection = shallowRef<{ rule: string; index: number; evidence: FilingRuleEvidence }>()
const target = shallowRef<WordEvidenceTarget>()
const workspace = ref<HTMLElement>()
const connector = ref('')
let frame = 0
let resize: ResizeObserver | undefined

/**
 * 用途 激活某条审核依据，并将相应附件交给原文预览。
 * 参数 rule、index、evidence 为审核项、该项内的依据顺序和实际出处。
 * 返回值 无。
 */
function selectEvidence(rule: string, index: number, evidence: FilingRuleEvidence) {
  selection.value = { rule, index, evidence }
  target.value = wordEvidenceTarget(evidence, props.attachments)
  queueLink()
}

/**
 * 用途 清除此前的原文锚点、批注激活态与连线。
 * 参数 无。
 * 返回值 无。
 */
function clearCitation() {
  selection.value = target.value = undefined
  connector.value = ''
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
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(drawLink)
}

/**
 * 用途 根据当前可见的文字与批注位置绘制连线。
 * 参数 无。
 * 返回值 无，任一端滚出面板时清除连线。
 */
function drawLink() {
  connector.value = ''
  const root = workspace.value
  const origin = root?.querySelector('[data-review-anchor]')
  const destination = root?.querySelector('[data-current-citation]')
  if (!root || !origin || !destination) return
  const a = origin.getBoundingClientRect()
  const b = destination.getBoundingClientRect()
  if (!visibleInPane(origin, a) || !visibleInPane(destination, b) || a.right >= b.left) return
  const area = root.getBoundingClientRect()
  const x = a.right - area.left
  const y = a.top + a.height / 2 - area.top
  const endX = b.left - area.left
  const endY = b.top + b.height / 2 - area.top
  const middle = (x + endX) / 2
  connector.value = `M ${x} ${y} C ${middle} ${y}, ${middle} ${endY}, ${endX} ${endY}`
}

/**
 * 用途 核对连线端点是否仍在各自可滚动面板内。
 * 参数 node 是端点元素，bounds 是端点矩形。
 * 返回值 端点的垂直中心可见时为真。
 */
function visibleInPane(node: Element, bounds: DOMRect) {
  const pane = node.closest('[data-review-scroll]')?.getBoundingClientRect()
  const midpoint = bounds.top + bounds.height / 2
  return pane && midpoint >= pane.top && midpoint <= pane.bottom
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
 * 参数 preserve 表示是否在同一材料版本的刷新中保留用户编辑。
 * 返回值 读取完成后的异步任务。
 */
async function load(preserve = true) {
  if (loading.value) return
  const operation = requests
  const path = props.path
  loading.value = true
  failure.value = ''
  try {
    const result = await api<FilingMaterialReview>(path + '/prechecks/review', { signal: operation.signal })
    if (requests !== operation) return
    const previous = form.value
    const items = result.items.map(item => {
      const old = previous?.items.find(entry => entry.ruleId === item.ruleId)
      if (preserve && previous?.materialRevision === result.materialRevision && old && dirty(old)) {
        return { ...item, draft: { ...old.draft, failure: '' } }
      }
      const draft: Draft = {
        status: item.review?.status ?? 'pending', remark: item.review?.remark ?? '', saving: false, failure: '',
      }
      return { ...item, draft }
    })
    form.value = { materialRevision: result.materialRevision, items }
  } catch (error) {
    if (requests === operation && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (requests === operation) loading.value = false
  }
}

/**
 * 用途 判断当前项是否有待保存的审核修改。
 * 参数 item 是服务端当前保存的审核。
 * 返回值 结论或备注与保存值不同时为真。
 */
function dirty(item: Row) {
  return item.draft.status !== (item.review?.status ?? 'pending') ||
    item.draft.remark.trim() !== (item.review?.remark ?? '')
}

/**
 * 用途 保存单项人工审核，同时核对材料版本和意见版本。
 * 参数 item 为当前编辑的规则行。
 * 返回值 保存完成后的异步任务。
 */
async function save(item: Row) {
  const draft = item.draft
  const current = form.value
  if (!current || draft.saving || !dirty(item)) return
  const operation = requests
  draft.saving = true
  draft.failure = ''
  try {
    const result = await api<FilingRuleReview>(props.path + '/prechecks/review/' + encodeURIComponent(item.ruleId), {
      method: 'PUT', signal: operation.signal,
      body: { status: draft.status, remark: draft.remark, materialRevision: current.materialRevision,
        expectedVersion: item.review?.version ?? 0 },
    })
    if (requests !== operation || form.value !== current) return
    item.review = result
    draft.status = result.status
    draft.remark = result.remark
  } catch (error) {
    if (requests !== operation || isAbort(error)) return
    draft.failure = error instanceof ApiError && error.status === 409
      ? '材料或审核意见已更新。请重新读取后核对再保存。'
      : errorText(error)
  } finally {
    if (requests === operation) draft.saving = false
  }
}

watch(() => [props.path, props.revision], () => {
  clearCitation()
  requests.abort()
  requests = new AbortController()
  form.value = undefined
  loading.value = false
  void load(false)
}, { immediate: true })
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
})
onBeforeUnmount(() => {
  requests.abort()
  resize?.disconnect()
  cancelAnimationFrame(frame)
})
</script>

<template>
  <div class="material-review">
    <div class="review-heading">
      <h3>逐项审核</h3>
      <span v-if="form" class="muted">{{ reviewedCount }} / {{ form.items.length }} 项已人工审核</span>
      <button type="button" class="button neutral small" :disabled="loading || saving"
        @click="refresh">重新读取</button>
    </div>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <p v-if="loading && !form" class="hint-row" role="status">正在读取逐项审核…</p>
    <div ref="workspace" class="review-workspace" @scroll.capture="queueLink">
      <FilingReviewDocument :key="revision" :path="path" :attachments="attachments" :target="target"
        @clear-citation="clearCitation" @return-to-citation="returnToCitation" @geometry="queueLink" />
      <section class="review-comments" aria-labelledby="review-comments-title">
        <h3 id="review-comments-title">审核批注</h3>
        <ol v-if="form" class="review-items" data-review-scroll tabindex="0" aria-label="逐项审核批注">
          <li v-for="{ item, check } in rows" :key="item.ruleId" class="review-item">
            <div class="review-row">
              <div class="review-main">
                <div class="review-rule">
                  <h4>{{ item.title }}</h4>
                  <p class="muted">{{ item.method }}</p>
                </div>
                <div class="review-rule-result">
                  <template v-if="check">
                    <strong class="review-status" :data-status="check.status">
                      {{ source === 'deepseek' ? 'AI 预审' : '规则预审' }} · {{ checkLabels[check.status] }}
                    </strong>
                    <p>{{ check.message }}</p>
                    <ul v-if="check.evidence?.length" class="review-evidence" aria-label="审核依据">
                      <li v-for="(evidence, index) in check.evidence" :key="index">
                        <button type="button" class="evidence-button"
                          :aria-pressed="selection?.rule === item.ruleId && selection?.index === index"
                          :data-current-citation="selection?.rule === item.ruleId && selection?.index === index
                            ? '' : undefined"
                          @click="selectEvidence(item.ruleId, index, evidence)">
                          <strong>{{ evidence.fileName }} · {{ evidence.location }}</strong>
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
                <label>
                  <span>人工审核</span>
                  <select v-model="item.draft.status" :disabled="item.draft.saving">
                    <option value="pending">未审核</option>
                    <option value="pass">通过</option>
                    <option value="fail">不通过</option>
                  </select>
                </label>
                <label>
                  <span>人工备注</span>
                  <textarea v-model="item.draft.remark" rows="2" maxlength="2000" :disabled="item.draft.saving" />
                </label>
                <div class="review-save">
                  <button type="button" class="button neutral small"
                    :disabled="loading || item.draft.saving || !dirty(item)"
                    @click="save(item)">{{ item.draft.saving ? '保存中' : '保存本项' }}</button>
                  <small v-if="item.review" class="muted">
                    {{ item.review.reviewer }} · {{ formatTime(item.review.reviewedAt) }}
                  </small>
                </div>
                <p v-if="item.draft.failure" class="form-message error-message" role="alert">{{ item.draft.failure }}</p>
              </div>
            </div>
          </li>
        </ol>
      </section>
      <svg v-if="connector" class="review-connector" aria-hidden="true" focusable="false">
        <path :d="connector" />
      </svg>
    </div>
  </div>
</template>

<style scoped>
.material-review { margin-block-start: 1rem; --review-toolbar-height: 3.5rem;
  --review-pane-height: clamp(27rem, 68dvh, 56rem); }
.review-heading, .review-save { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem; }
.review-heading { margin-block-end: .5rem; }
.review-heading h3, .review-rule h4, .review-item p { margin: 0; }
.review-workspace { position: relative; display: grid; align-items: start; gap: 1.1rem; }
.review-comments { min-width: 0; border: 1px solid var(--line); border-radius: 12px; overflow: hidden; }
.review-comments > h3 { display: flex; align-items: center; min-height: var(--review-toolbar-height);
  margin: 0; padding: .65rem 1rem; font-size: 1rem; }
.review-items { margin: 0; padding: 0 .85rem .85rem 2.3rem; height: var(--review-pane-height); overflow: auto; }
.review-item { min-width: 0; padding: .85rem .75rem; margin-block-end: .7rem; background: var(--surface, #fff);
  border: 1px solid var(--line, #dbe3ed); border-radius: 8px; }
.review-item:has([data-current-citation]) { border-color: #a67624; }
.review-row { display: grid; gap: .75rem; }
.review-rule, .review-rule-result { min-width: 0; overflow-wrap: anywhere; }
.review-rule-result { margin-block: .5rem; }
.review-status[data-status="fail"] { color: var(--danger, #ab2634); }
.review-status[data-status="pass"] { color: var(--success, #16634a); }
.review-input { display: grid; gap: .5rem; }
.review-input label { display: grid; gap: .25rem; }
.review-input select, .review-input textarea { width: 100%; max-width: 38rem; }
.review-evidence { display: grid; list-style: none; gap: .45rem; margin: .5rem 0 0; padding: 0; }
.evidence-button { display: grid; width: 100%; gap: .28rem; padding: .45rem .65rem; text-align: start;
  color: inherit; background: transparent; border: 1px solid var(--line); border-radius: 6px; overflow-wrap: anywhere; }
.evidence-button span { color: var(--muted, #41576a); }
.evidence-button:hover, .evidence-button[aria-pressed="true"] { background: #fff7d7; color: #192536; border-color: #a67624; }
.review-connector { position: absolute; inset: 0; width: 100%; height: 100%; pointer-events: none; }
.review-connector path { fill: none; stroke: #aa7621; stroke-width: 2; }
@media (min-width: 861px) {
  .review-workspace { grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr); }
}
@media (max-width: 860px) {
  .material-review { --review-pane-height: 55dvh; }
  .review-connector { display: none; }
}
</style>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api, ApiError, attachmentURL, errorText, isAbort } from '../lib/api'
import { formatTime } from '../lib/domain'
import type { FilingMaterialReview, FilingReviewItem, FilingReviewStatus, FilingRuleCheck, FilingRuleReview } from '../lib/domain'

const props = defineProps<{
  path: string; revision: string; checks?: FilingRuleCheck[]; source?: 'deepseek' | 'local_rules'
}>()
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
  requests.abort()
  requests = new AbortController()
  form.value = undefined
  loading.value = false
  void load(false)
}, { immediate: true })
onBeforeUnmount(() => requests.abort())
</script>

<template>
  <div class="material-review">
    <div class="review-heading">
      <h3>逐项审核</h3>
      <span v-if="form" class="muted">{{ reviewedCount }} / {{ form.items.length }} 项已人工审核</span>
      <button type="button" class="button neutral small" :disabled="loading || saving"
        @click="load()">重新读取</button>
    </div>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <p v-if="loading && !form" class="hint-row" role="status">正在读取逐项审核…</p>
    <ol v-if="form" class="review-items">
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
            <details v-if="check.evidence?.length">
              <summary>查看依据</summary>
              <ul class="review-evidence">
                <li v-for="(source, index) in check.evidence" :key="index">
                  <a :href="attachmentURL(path, source.attachmentId)">{{ source.fileName }}</a>
                  <span class="muted"> · {{ source.location }}</span>
                  <blockquote>{{ source.quote }}</blockquote>
                </li>
              </ul>
            </details>
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
            <textarea v-model="item.draft.remark" rows="2" maxlength="2000"
              :disabled="item.draft.saving" />
          </label>
          <div class="review-save">
            <button type="button" class="button neutral small"
              :disabled="loading || item.draft.saving || !dirty(item)"
              @click="save(item)">{{ item.draft.saving ? '保存中' : '保存本项' }}</button>
            <small v-if="item.review" class="muted">{{ item.review.reviewer }} · {{ formatTime(item.review.reviewedAt) }}</small>
          </div>
          <p v-if="item.draft.failure" class="form-message error-message" role="alert">
            {{ item.draft.failure }}
          </p>
        </div>
        </div>
      </li>
    </ol>
  </div>
</template>

<style scoped>
.material-review { margin-block-start: 1rem; }
.review-heading, .review-save { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem; }
.review-heading { margin-block-end: .5rem; }
.review-heading h3, .review-rule h4, .review-item p { margin: 0; }
.review-items { display: grid; gap: .75rem; padding-inline-start: 1.5rem; }
.review-item { min-width: 0; padding-block: .75rem; border-bottom: 1px solid var(--line, #dbe3ed); }
.review-row { display: grid; gap: .75rem; }
.review-rule, .review-rule-result { min-width: 0; overflow-wrap: anywhere; }
.review-rule-result { margin-block: .5rem; }
.review-status[data-status="fail"] { color: var(--danger, #ab2634); }
.review-status[data-status="pass"] { color: var(--success, #16634a); }
.review-input { display: grid; gap: .5rem; }
.review-input label { display: grid; gap: .25rem; }
.review-input select, .review-input textarea { width: 100%; max-width: 38rem; }
.review-evidence { padding-inline-start: 1.25rem; }
.review-evidence blockquote { margin: .25rem 0 .5rem; }
@media (min-width: 1024px) {
  .review-row { grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); align-items: start; }
}
</style>

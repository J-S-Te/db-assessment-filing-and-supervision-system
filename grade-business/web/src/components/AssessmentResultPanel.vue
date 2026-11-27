<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { ApiError, api, attachmentURL, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { assessmentConclusions, type AssessmentConclusion, type AssessmentEditor, type AssessmentRevision,
  type AssessmentRevisionsPage } from '../lib/assessmentHistory'

const props = defineProps<{ path: string; reportVersion: number; reportStatus: string }>()
const fields = reactive({
  evidenceId: '',
  score: '' as number | '',
  conclusion: '' as AssessmentConclusion | '',
  high: '' as number | '',
  medium: '' as number | '',
  low: '' as number | '',
})
const editor = shallowRef<AssessmentEditor>()
const revisions = shallowRef<AssessmentRevisionsPage>()
const loading = ref(true)
const saving = ref(false)
const loadingHistory = ref(false)
const showHistory = ref(false)
const stale = ref(false)
const failure = ref('')
const saveError = ref('')
const historyError = ref('')
const notice = ref('')
const endpoint = computed(() => props.path + '/assessment-result')
const editable = computed(() => editor.value?.editable && props.reportStatus === 'approved')
const riskFields = { high: '高风险项', medium: '中风险项', low: '低风险项' } as const
const controller = new AbortController()

/**
 * 用途 从最近一次已保存的核验结果初始化编辑表单。
 * 参数 result 为当前登记版本，首次登记时为空。
 * 返回值 无。
 */
function populate(result?: AssessmentRevision) {
  const evidence = editor.value?.evidence ?? []
  const currentEvidence = evidence.find(item => item.id === result?.evidenceId)
  Object.assign(fields, {
    evidenceId: currentEvidence?.id ?? evidence[0]?.id ?? '',
    score: result?.score ?? '',
    conclusion: result?.conclusion ?? '',
    high: result?.high ?? '',
    medium: result?.medium ?? '',
    low: result?.low ?? '',
  })
}

/**
 * 用途 获取可用证据、登记版本和结论表单初值。
 * 参数 无。
 * 返回值 查询完成后的任务。
 */
async function load() {
  loading.value = true
  failure.value = ''
  saveError.value = ''
  notice.value = ''
  try {
    editor.value = await api<AssessmentEditor>(endpoint.value, { signal: controller.signal })
    populate(editor.value.result)
    stale.value = false
    if (showHistory.value) await loadHistory()
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    loading.value = false
  }
}

/**
 * 用途 将主管核对过的测评结果提交为一个可追溯版本。
 * 参数 无，使用已选证据、分数、结论和风险项数量。
 * 返回值 登记完成后的任务，版本冲突时提示重新读取。
 */
async function save() {
  if (!editable.value || !editor.value || loading.value || saving.value || stale.value) return
  saving.value = true
  saveError.value = ''
  notice.value = ''
  try {
    const body = {
      ...fields,
      expectedReportVersion: props.reportVersion,
      expectedResultVersion: editor.value.result?.version ?? 0,
    }
    const result = await api<AssessmentRevision>(endpoint.value, {
      method: 'POST', body, signal: controller.signal,
    })
    editor.value = { ...editor.value, result }
    populate(result)
    notice.value = '测评结论已保存，运营者可查看更新后的结果。'
    if (showHistory.value) await loadHistory()
  } catch (error) {
    if (!isAbort(error)) {
      saveError.value = errorText(error)
      stale.value = error instanceof ApiError && error.status === 409
    }
  } finally {
    saving.value = false
  }
}

/**
 * 用途 按版本倒序读取监管登记和证据留痕。
 * 参数 before 为下一页起始版本，未传时从最新版本读取。
 * 返回值 留痕读取任务。
 */
async function loadHistory(before?: number) {
  loadingHistory.value = true
  historyError.value = ''
  try {
    const page = await api<AssessmentRevisionsPage>(endpoint.value + '/history' + params({ before }), {
      signal: controller.signal,
    })
    revisions.value = {
      items: before ? [...(revisions.value?.items ?? []), ...page.items] : page.items,
      nextBefore: page.nextBefore,
    }
  } catch (error) {
    if (!isAbort(error)) historyError.value = errorText(error)
  } finally {
    loadingHistory.value = false
  }
}

/**
 * 用途 展开或收起本报告的测评结果登记留痕。
 * 参数 无。
 * 返回值 展开时返回最新留痕的查询任务。
 */
function toggleHistory() {
  showHistory.value = !showHistory.value
  if (showHistory.value) return loadHistory()
}

watch(() => props.reportStatus, load, { immediate: true })
onScopeDispose(() => controller.abort())
</script>

<template>
  <section class="panel result-panel" aria-label="测评结论登记">
    <h2>测评结论登记</h2>
    <AsyncState :loading="loading" :error="failure" @retry="load" />
    <template v-if="editor && !loading && !failure">
      <p v-if="editor.result" class="muted">
        已登记第 {{ editor.result.version }} 版 · {{ editor.result.actor }} · {{ formatTime(editor.result.recordedAt) }}
      </p>
      <p v-if="!editable" class="muted">
        {{ reportStatus !== 'approved' ? '报告终审通过后可登记测评结论。' : '报告缺少可核验的结论或正文附件。' }}
      </p>
      <template v-else>
        <p class="muted">请按已批准报告的材料核对评分、结论和风险项数量。运营者可查看登记后的摘要。</p>
        <form @submit.prevent="save">
          <fieldset :disabled="saving || stale">
            <label class="field"><span>核验材料</span>
              <select v-model="fields.evidenceId" required>
                <option disabled value="">请选择核验材料</option>
                <option v-for="item in editor.evidence" :key="item.id" :value="item.id">{{ item.fileName }}</option>
              </select>
            </label>
            <a v-if="fields.evidenceId" class="text-button evidence-link"
              :href="attachmentURL(path, fields.evidenceId)" download>下载核验材料</a>
            <div class="result-fields">
              <label class="field"><span>综合得分</span>
                <input v-model.number="fields.score" type="number" min="0" max="100" step="0.01" required>
              </label>
              <label class="field"><span>测评结论</span>
                <select v-model="fields.conclusion" required>
                  <option disabled value="">请选择结论</option>
                  <option v-for="(label, value) in assessmentConclusions" :key="value" :value="value">{{ label }}</option>
                </select>
              </label>
              <label v-for="(label, key) in riskFields" :key="key" class="field"><span>{{ label }}</span>
                <input v-model.number="fields[key]" type="number" min="0" max="100000" step="1" required>
              </label>
            </div>
            <button class="button primary" type="submit">{{ saving ? '保存中' : '保存测评结论' }}</button>
          </fieldset>
        </form>
      </template>
      <p v-if="saveError" class="form-message error-message" role="alert">{{ saveError }}</p>
      <button v-if="stale" class="button neutral" type="button" @click="load">重新读取登记结果</button>
      <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
      <button v-if="editor.result" class="button neutral" type="button" :aria-expanded="showHistory"
        :disabled="loadingHistory || saving" @click="toggleHistory">
        {{ showHistory ? '收起结果留痕' : '查看结果留痕' }}
      </button>
      <div v-if="showHistory" class="result-history">
        <AsyncState :loading="loadingHistory && !revisions" :error="historyError" @retry="loadHistory()" />
        <ol v-if="revisions?.items.length">
          <li v-for="item in revisions.items" :key="item.version">
            <strong>第 {{ item.version }} 版</strong> · {{ item.actor }} · {{ formatTime(item.recordedAt) }}
            <p>{{ item.score }} 分 · {{ assessmentConclusions[item.conclusion] }} ·
              高 {{ item.high }} / 中 {{ item.medium }} / 低 {{ item.low }}
              <a :href="attachmentURL(path, item.evidenceId)" download>证据附件</a>
            </p>
          </li>
        </ol>
        <button v-if="revisions?.nextBefore" class="button neutral" type="button" :disabled="loadingHistory"
          @click="loadHistory(revisions.nextBefore)">更多登记版本</button>
      </div>
    </template>
  </section>
</template>

<style scoped>
.result-panel { display: grid; gap: 14px; padding: 20px; }
.result-panel h2, .result-panel p { margin: 0; }
fieldset { display: grid; gap: 12px; margin: 0; padding: 0; border: 0; min-inline-size: 0; }
.result-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(130px, 1fr)); gap: 12px; }
.result-panel .button, .evidence-link { justify-self: start; }
.result-history { border-top: 1px solid var(--border); padding-top: 12px; }
.result-history ol { margin: 0; padding-inline-start: 18px; }
.result-history li { margin-bottom: 10px; }
</style>

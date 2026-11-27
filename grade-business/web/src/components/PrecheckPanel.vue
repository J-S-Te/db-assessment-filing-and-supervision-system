<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { CheckCircle2, Clock3, FileSearch, LoaderCircle } from '@lucide/vue'
import { api, ApiError, attachmentURL, errorText, isAbort } from '../lib/api'
import type { Precheck } from '../lib/domain'
import FilingMaterialReview from './FilingMaterialReview.vue'

const props = defineProps<{ path: string; revision: string }>()
const latest = shallowRef<Precheck>()
const loading = ref(false)
const starting = ref(false)
const failure = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let request: AbortController | undefined
const steps = ['排队', '读取材料', '检查', '完成']
const checkingMessages = {
  materials: '正在读取材料',
  deepseek: '正在等待 DeepSeek 返回检查结果',
  local_rules: '正在执行本地规则检查',
  fallback: 'DeepSeek 未完成，正在执行本地规则检查',
}
const progress = computed(() => {
  const job = latest.value
  if (job?.status === 'queued') return { step: 0, message: '已提交，等待后台检查' }
  if (job?.status !== 'running') return undefined
  if (!job.progressStage) return { step: -1, message: '正在检查' }
  return { step: job.progressStage === 'materials' ? 1 : 2, message: checkingMessages[job.progressStage] }
})
const adviceTitles = { correct: '建议补正', manual_review: '建议人工复核', pass: '建议通过材料审核' }

/**
 * 用途 停止前一项预审的查询和计时。
 * 参数 无。
 * 返回值 新请求控制器。
 */
function renewRequest(): AbortController {
  clearTimeout(timer)
  request?.abort()
  request = new AbortController()
  return request
}

/**
 * 用途 查询预审任务并只在执行中安排下一次刷新。
 * 参数 identifier 为任务标识或 latest，quiet 表示是否为后台刷新。
 * 返回值 查询任务。
 */
async function read(identifier = 'latest', quiet = false) {
  const next = renewRequest()
  const path = props.path
  if (!quiet) loading.value = true
  failure.value = ''
  try {
    const result = await api<Precheck>(path + '/prechecks/' + encodeURIComponent(identifier), { signal: next.signal })
    if (request === next) update(result)
  } catch (error) {
    if (request !== next || isAbort(error)) return
    if (identifier === 'latest' && error instanceof ApiError && error.status === 404) latest.value = undefined
    else failure.value = errorText(error)
  } finally {
    if (request === next) loading.value = false
  }
}

/**
 * 用途 将任务状态呈现在当前记录上，并在等待或运行中继续刷新。
 * 参数 result 为已经确认属于当前记录的任务。
 * 返回值 无。
 */
function update(result: Precheck) {
  latest.value = result
  if (result.status === 'queued' || result.status === 'running') {
    timer = setTimeout(() => void read(result.id, true), 700)
  }
}

/**
 * 用途 为当前备案发起一次真实材料规则检查。
 * 参数 无。
 * 返回值 发起请求任务，结果继续异步查询。
 */
async function start() {
  if (starting.value) return
  const current = renewRequest()
  const path = props.path
  starting.value = true
  failure.value = ''
  try {
    const result = await api<Precheck>(path + '/prechecks', { method: 'POST', signal: current.signal })
    if (request === current) update(result)
  } catch (error) {
    if (request === current && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (request === current) starting.value = false
  }
}

watch(() => [props.path, props.revision], () => {
  latest.value = undefined
  starting.value = false
  void read()
}, { immediate: true })
onBeforeUnmount(() => {
  clearTimeout(timer)
  request?.abort()
})
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="precheck-title">
    <header class="panel-heading panel-compact">
      <div class="heading-icon"><FileSearch :size="20" aria-hidden="true" /><h2 id="precheck-title">材料规则预审</h2></div>
      <button class="button neutral small" type="button"
        :disabled="starting || loading || latest?.status === 'queued' || latest?.status === 'running'" @click="start">
        <LoaderCircle v-if="starting" class="spinner" :size="16" aria-hidden="true" />发起预审
      </button>
    </header>
    <div class="precheck-body">
      <p v-if="failure" class="form-message error-message" role="alert">
        {{ failure }}
        <button class="text-action" type="button" @click="read(latest?.id ?? 'latest')">重新查询</button>
      </p>
      <p v-else-if="loading" class="hint-row" role="status">正在读取预审状态…</p>
      <p v-else-if="starting" class="hint-row" role="status">正在提交预审…</p>
      <p v-else-if="!latest" class="hint-row">发起预审后可查看 11 项检查的结论和原件依据。</p>
      <template v-else>
        <p v-if="latest.stale" class="form-message warning-message" role="status">材料或规则已更新，请重新预审。</p>
        <p v-if="!latest.stale && latest.fallbackReason" class="form-message warning-message" role="status">
          {{ latest.fallbackReason }}
        </p>
        <div v-if="progress" class="precheck-progress">
          <p class="hint-row" role="status">
            <Clock3 :size="16" aria-hidden="true" />{{ progress.message }}
          </p>
          <ol v-if="progress.step >= 0" class="precheck-steps" aria-label="预审进度">
            <li v-for="(label, index) in steps" :key="label" :aria-current="index === progress.step ? 'step' : undefined"
              :data-state="index < progress.step ? 'done' : undefined">
              <CheckCircle2 v-if="index < progress.step" :size="14" aria-label="已完成" />
              {{ label }}
            </li>
          </ol>
        </div>
        <p v-if="latest.status === 'failed'" class="form-message error-message" role="alert">
          {{ latest.failure || '本次预审失败，请重新发起。' }}
        </p>
        <template v-if="!latest.stale &&
          (latest.status === 'succeeded' || latest.status === 'failed' && latest.checkSource === 'local_rules')">
          <p v-if="latest.status === 'succeeded'" class="hint-row" role="status">
            <CheckCircle2 :size="17" aria-hidden="true" />
            {{ latest.checkSource === 'deepseek' ? 'DeepSeek 预审已完成' : '规则检查已完成' }}
            <span v-if="latest.checks?.length">· 已检查 {{ latest.checks.length }} / 11 项</span>
          </p>
          <details v-if="latest.findings?.length" class="material-checklist">
            <summary>必传材料检查</summary>
            <ul class="finding-list">
              <li v-for="(finding, index) in latest.findings" :key="finding.ruleId + ':' + index">
                <span>{{ finding.message }}</span>
                <a v-if="finding.attachmentId" class="text-action" :href="attachmentURL(path, finding.attachmentId)">查看材料</a>
              </li>
            </ul>
          </details>
          <section v-if="latest.recommendation" class="precheck-advice" aria-labelledby="precheck-advice-title">
            <h3 id="precheck-advice-title">审核建议</h3>
            <strong>{{ adviceTitles[latest.recommendation.action] }}</strong>
            <ul v-if="latest.recommendation.action !== 'pass'">
              <li v-if="latest.recommendation.missingMaterials?.length">
                补传 {{ latest.recommendation.missingMaterials.join('、') }}
              </li>
              <li v-if="latest.recommendation.failedChecks?.length">
                核对并修正 {{ latest.recommendation.failedChecks.join('、') }}
              </li>
              <li v-if="latest.recommendation.manualChecks?.length">
                人工复核 {{ latest.recommendation.manualChecks.join('、') }}
              </li>
              <li v-if="latest.recommendation.incomplete">检查结果未覆盖全部 11 项，请重新发起预审。</li>
            </ul>
            <p v-else>必传材料齐全，11 项检查均通过或不适用。</p>
            <small>供监管员复核参考。</small>
          </section>
          <p v-if="!latest.checks?.length" class="hint-row">请重新发起预审以获取 11 项内容规则结论。</p>
        </template>
      </template>
      <FilingMaterialReview :path="path" :revision="revision"
        :source="latest?.checkSource"
        :checks="latest && !latest.stale &&
          (latest.status === 'succeeded' || latest.status === 'failed') ? latest.checks : undefined" />
    </div>
  </section>
</template>

<style scoped>
.precheck-steps {
  display: flex;
  flex-wrap: wrap;
  gap: .5rem 1rem;
  list-style: none;
  margin: .5rem 0 1rem;
  padding: 0;
}
.precheck-steps li { display: inline-flex; align-items: center; gap: .25rem; color: var(--muted, #526476); }
.precheck-steps li[aria-current="step"] { color: var(--primary, #155f9e); font-weight: 700; }
.precheck-advice { border-block-start: 1px solid var(--line, #d8e3ed); margin-block: .75rem; padding-block: .75rem; }
.precheck-advice h3 { margin: 0 0 .35rem; font-size: 1rem; }
.precheck-advice p, .precheck-advice ul { margin-block: .35rem; }
.precheck-advice small { color: var(--muted, #526476); }
</style>

<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { Download, FileSearch } from '@lucide/vue'
import { api, ApiError, attachmentURL, errorText, isAbort, params } from '../lib/api'
import type { ImplementationPrecheck, ImplementationPrecheckFinding } from '../lib/implementations'

const props = defineProps<{
  path: string
  filingId: string
  version: number
  audience?: 'institution' | 'operator'
  scope?: string
  disabled?: boolean
}>()
const emit = defineEmits<{ refresh: [] }>()
const result = shallowRef<ImplementationPrecheck>()
const running = ref(false)
const stale = ref(false)
const error = ref('')
let request: AbortController | undefined

const scopeQuery = computed(() => props.audience === 'institution' ? { institution: props.scope }
  : props.audience === 'operator' ? { organization: props.scope } : {})
const filingPath = computed(() => (props.audience ? '/' + props.audience : '')
  + '/filings/' + encodeURIComponent(props.filingId))
const findingsLabels = {
  pass: '通过',
  warning: '待核对',
  unavailable: '无法核对',
} satisfies Record<ImplementationPrecheckFinding['status'], string>
const statusStyles = {
  pass: 'status-approved',
  warning: 'status-returned',
  unavailable: 'status-draft',
} satisfies Record<ImplementationPrecheckFinding['status'], string>

/**
 * 用途 运行当前报备版本的预检并展示带版本的结果。
 * 参数 无，使用当前详情的业务标识、经办范围和报备版本。
 * 返回值 完成本次读取的异步任务。
 */
async function run() {
  if (props.disabled || running.value) return
  const current = new AbortController()
  request = current
  const version = props.version
  running.value = true
  stale.value = false
  error.value = ''
  result.value = undefined
  try {
    const query = params({ ...scopeQuery.value, expectedVersion: version })
    const response = await api<ImplementationPrecheck>(props.path + '/precheck' + query, { signal: current.signal })
    if (!current.signal.aborted && response.reportVersion === props.version) result.value = response
  } catch (failure) {
    if (current.signal.aborted || isAbort(failure)) return
    stale.value = failure instanceof ApiError && failure.code === 'stale_version'
    error.value = errorText(failure)
  } finally {
    if (request === current) running.value = false
  }
}

/**
 * 用途 取消旧详情的预检请求并清理旧结果。
 * 参数 无。
 * 返回值 无。
 */
function reset() {
  request?.abort()
  request = undefined
  running.value = false
  stale.value = false
  error.value = ''
  result.value = undefined
}

/**
 * 用途 为预检证据生成对应经办范围的同源备案材料下载地址。
 * 参数 attachmentId 为服务端校验归属后返回的附件标识。
 * 返回值 重新执行归属校验的下载地址。
 */
function evidenceURL(attachmentId: string): string {
  return attachmentURL(filingPath.value, attachmentId) + params(scopeQuery.value)
}

watch(() => [props.path, props.filingId, props.version, props.audience, props.scope], reset, { flush: 'sync' })
onScopeDispose(reset)
</script>

<template>
  <section class="panel detail-panel implementation-precheck" aria-labelledby="implementation-precheck-title">
    <header class="panel-heading panel-compact">
      <div class="heading-icon">
        <FileSearch :size="20" aria-hidden="true" />
        <h2 id="implementation-precheck-title">实施报备材料预检</h2>
      </div>
      <button type="button" class="button neutral small" :disabled="disabled || running || stale" @click="run">
        {{ running ? '正在预检…' : '运行材料预检' }}
      </button>
    </header>
    <p class="muted">核对已保存的报备、当前备案及材料，结果供人工审核参考。</p>
    <p v-if="running" class="muted" role="status">正在核对报备与关联备案材料…</p>
    <p v-if="error" class="form-message error-message" role="alert">
      {{ error }}
      <button v-if="stale" class="text-action" type="button" :disabled="disabled" @click="emit('refresh')">刷新报备详情</button>
    </p>
    <template v-if="result">
      <p class="muted" role="status">
        本次核对报备版本 {{ result.reportVersion }}<template v-if="result.filingVersion !== undefined">
          · 备案版本 {{ result.filingVersion }} · 材料版本 {{ result.materialRevision }}
        </template>
      </p>
      <ul class="precheck-findings">
        <li v-for="finding in result.findings" :key="finding.ruleId">
          <span class="status-badge" :class="statusStyles[finding.status]">{{ findingsLabels[finding.status] }}</span>
          <div class="finding-text">
            <p>{{ finding.message }}</p>
            <a v-if="finding.evidence" class="text-action" :href="evidenceURL(finding.evidence.attachmentId)" download>
              <Download :size="14" aria-hidden="true" />{{ finding.evidence.fileName }}
            </a>
          </div>
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.implementation-precheck > :not(header) { margin-inline: 16px; }
.implementation-precheck > p.muted { margin-top: 8px; }
.precheck-findings { display: grid; gap: .8rem; padding: 0; list-style: none; }
.precheck-findings li { display: flex; gap: .65rem; align-items: flex-start; }
.precheck-findings .status-badge { flex-shrink: 0; }
.finding-text { min-width: 0; overflow-wrap: anywhere; }
.finding-text p { margin: 0; }
.finding-text a { margin-top: .2rem; }
</style>

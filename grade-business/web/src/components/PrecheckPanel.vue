<script setup lang="ts">
import { onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { CheckCircle2, Clock3, FileSearch, LoaderCircle } from '@lucide/vue'
import { api, ApiError, attachmentURL, errorText, isAbort } from '../lib/api'
import type { Precheck } from '../lib/domain'

const props = defineProps<{ path: string; revision: string }>()
const latest = shallowRef<Precheck>()
const loading = ref(false)
const starting = ref(false)
const failure = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let request: AbortController | undefined

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
      <p v-else-if="!latest" class="hint-row">上传材料后可执行规则检查，并查看命中依据。</p>
      <template v-else>
        <p v-if="latest.stale" class="form-message warning-message" role="status">材料已更新，请重新预审。</p>
        <p v-if="latest.status === 'queued' || latest.status === 'running'" class="hint-row" role="status">
          <Clock3 :size="16" aria-hidden="true" />{{ latest.status === 'queued' ? '等待检查' : '正在检查' }}
        </p>
        <p v-else-if="latest.status === 'failed'" class="form-message error-message" role="alert">
          {{ latest.failure || '本次预审失败，请重新发起。' }}
        </p>
        <template v-else>
          <p class="hint-row" role="status"><CheckCircle2 :size="17" aria-hidden="true" />规则检查已完成</p>
          <ul v-if="latest.findings?.length" class="finding-list">
            <li v-for="(finding, index) in latest.findings" :key="finding.ruleId + ':' + index">
              <span>{{ finding.message }}</span>
              <a v-if="finding.attachmentId" class="text-action" :href="attachmentURL(path, finding.attachmentId)">查看材料</a>
            </li>
          </ul>
          <p v-else class="hint-row">当前无规则疑点。</p>
        </template>
      </template>
    </div>
  </section>
</template>

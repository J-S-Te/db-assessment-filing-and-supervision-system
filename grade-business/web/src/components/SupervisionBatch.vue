<script setup lang="ts">
import { computed, onMounted, onScopeDispose, reactive, ref, shallowRef, watch } from 'vue'
import { api, errorText, isAbort } from '../lib/api'
import { nextBusinessDate, supervisionPath } from '../lib/supervision'

interface TemplateOption { id: string; title: string; version: number; templateNames: string[] }
interface Recipient {
  filingId: string
  number: string
  systemName: string
  organizationName: string
  status: 'eligible' | 'existing'
  existingTaskId?: string
}
interface Preview { items: Recipient[]; totalEligible: number; token?: string }
interface Dispatch {
  id: string
  items: { filingId: string; taskId: string; status: 'published' | 'existing' }[]
  publishedCount: number
}

const emit = defineEmits<{ close: []; published: [] }>()
const path = supervisionPath + '/batches'
const form = reactive({ district: '', levels: [] as string[], title: '', dueOn: nextBusinessDate, source: '' })
const templates = shallowRef<TemplateOption[]>([])
const preview = shallowRef<Preview>()
const result = shallowRef<Dispatch>()
const busy = ref('')
const error = ref('')
const source = computed(() => templates.value.find(item => item.id === form.source))
const payload = computed(() => source.value && ({
  district: form.district.trim() || undefined,
  levels: form.levels.length ? form.levels : undefined,
  title: form.title.trim(), dueOn: form.dueOn,
  templateTaskId: source.value.id, templateVersion: source.value.version,
}))
const hasScope = computed(() => Boolean(form.district.trim() || form.levels.length))
const published = computed(() => new Map(result.value?.items.map(item => [item.filingId, item])))
let active: AbortController | undefined

/**
 * 用途 串行执行当前预览或下发请求，并在退出页面时清理。
 * 参数 stage 为操作名称，run 为接收可取消信号的请求。
 * 返回值 请求成功的值，或已经展示的错误。
 */
async function runRequest<T>(stage: string, run: (signal: AbortSignal) => Promise<T>) {
  if (busy.value) return
  const request = new AbortController()
  active = request
  error.value = ''
  busy.value = stage
  try {
    const data = await run(request.signal)
    if (!request.signal.aborted) return data
  } catch (reason) {
    if (!isAbort(reason)) error.value = errorText(reason)
  } finally {
    if (active === request) busy.value = ''
  }
}

/**
 * 用途 读取已具备两份有效模板的真实样板任务。
 * 参数 无。
 * 返回值 当前样板选项加载任务。
 */
async function loadTemplates() {
  const page = await runRequest('templates', signal => api<{items: TemplateOption[]}>(path + '/template-options', { signal }))
  if (page) templates.value = page.items
}

/**
 * 用途 查询区县及保护等级对应的实际收件名单。
 * 参数 无，读取填表条件与样板版本。
 * 返回值 本次收件范围核对任务。
 */
async function inspect() {
  const body = payload.value
  if (!body || !hasScope.value) return
  preview.value = undefined
  result.value = undefined
  preview.value = await runRequest('preview', signal => api<Preview>(path + '/preview', { method: 'POST', body, signal }))
}

/**
 * 用途 按已确认的名单及模板版本下发全部指定任务。
 * 参数 无，读取上次预览生成的确切摘要。
 * 返回值 下发任务及回执更新。
 */
async function dispatch() {
  const criteria = payload.value
  const token = preview.value?.token
  if (!criteria || !token) return
  const received = await runRequest('submit', signal => api<Dispatch>(path, {
    method: 'POST', body: { ...criteria, token }, signal,
  }))
  if (!received) return
  result.value = received
  emit('published')
}

watch(() => JSON.stringify(payload.value), () => { preview.value = result.value = undefined })
onMounted(loadTemplates)
onScopeDispose(() => active?.abort())
</script>

<template>
  <section class="panel batch-panel" aria-labelledby="batch-supervision-title">
    <h2 id="batch-supervision-title">批量下发年度自查</h2>
    <form class="batch-form" @submit.prevent="inspect">
      <fieldset :disabled="!!busy">
        <div class="batch-fields">
          <label class="field"><span>任务标题</span><input v-model="form.title" maxlength="200" required></label>
          <label class="field"><span>自查截止日期</span>
            <input v-model="form.dueOn" type="date" :min="nextBusinessDate" required>
          </label>
          <label class="field"><span>收件区县</span>
            <input v-model="form.district" placeholder="输入名录中的区县名称" maxlength="120">
          </label>
        </div>
        <fieldset class="level-choices">
          <legend>保护等级</legend>
          <label v-for="level in ['1','2','3','4','5']" :key="level">
            <input v-model="form.levels" type="checkbox" :value="level">第 {{ level }} 级
          </label>
        </fieldset>
        <label class="field"><span>模板来源任务</span><select v-model="form.source" required>
          <option value="">请选择已有两份模板的任务</option>
          <option v-for="item in templates" :key="item.id" :value="item.id">{{ item.title }} · 版本 {{ item.version }}</option>
        </select></label>
        <p v-if="source" class="muted">{{ source.templateNames.join('、') }}</p>
        <p v-if="!templates.length && !busy" class="muted">可先建单件监督检查草稿并上传两份模板。</p>
        <div class="batch-actions">
          <button type="button" class="button neutral" @click="loadTemplates">刷新样板</button>
          <button type="submit" class="button primary" :disabled="!hasScope || !source">预览收件范围</button>
          <button type="button" class="button neutral" @click="$emit('close')">关闭批量面板</button>
        </div>
      </fieldset>
    </form>
    <p v-if="busy" class="muted" role="status">{{ busy === 'submit' ? '正在下发任务…' : '正在读取…' }}</p>
    <p v-if="error" class="form-message error-message" role="alert">{{ error }}</p>
    <div v-if="preview" class="batch-result">
      <p v-if="result" class="form-message success-message" role="status">
        已下发 {{ result.publishedCount }} 个备案任务。批次编号 {{ result.id }}
      </p>
      <p v-else role="status">匹配 {{ preview.items.length }} 个备案，其中 {{ preview.totalEligible }} 个可下发。</p>
      <div v-if="preview.items.length" class="table-wrap">
        <table class="data-table">
          <thead><tr><th>备案编号</th><th>系统</th><th>收件单位</th><th>结果</th></tr></thead>
          <tbody><tr v-for="item in preview.items" :key="item.filingId">
            <td>{{ item.number }}</td><td>{{ item.systemName }}</td><td>{{ item.organizationName }}</td>
            <td>
              <RouterLink v-if="published.get(item.filingId)?.status === 'published'"
                :to="'/supervisions/' + published.get(item.filingId)?.taskId" class="text-action">已下发</RouterLink>
              <RouterLink v-else-if="item.existingTaskId" :to="'/supervisions/' + item.existingTaskId"
                class="text-action">已有年度任务</RouterLink>
              <span v-else>待下发</span>
            </td>
          </tr></tbody>
        </table>
      </div>
      <button v-if="preview.token && !result" class="button primary" type="button" :disabled="!!busy"
        @click="dispatch">确认向 {{ preview.totalEligible }} 个备案批量下发</button>
    </div>
  </section>
</template>

<style scoped>
.batch-panel,.batch-form > fieldset,.batch-result { display:grid; gap:12px; }
.batch-panel { padding:16px; }
.batch-panel h2,.batch-panel p { margin:0; }
.batch-form > fieldset { border:0; padding:0; min-width:0; }
.batch-fields,.batch-actions,.level-choices { display:flex; flex-wrap:wrap; align-items:end; gap:10px; }
.batch-fields .field { flex:1 1 155px; }
.level-choices { border:1px solid #e0e7ef; border-radius:5px; padding:8px 12px; }
.level-choices label { display:flex; align-items:center; white-space:nowrap; gap:5px; }
.level-choices input[type="checkbox"] { flex:none; width:16px; min-height:0; height:16px; padding:0; }
.batch-result > button { justify-self:start; }
</style>

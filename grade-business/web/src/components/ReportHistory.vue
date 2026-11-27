<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { Download, RefreshCw } from '@lucide/vue'
import AsyncState from './AsyncState.vue'
import { api, attachmentURL, errorText, isAbort, params } from '../lib/api'
import { formatTime, risks, type Attachment, type Page, type Risk } from '../lib/domain'

interface ArchivedReport {
  id: string
  number: string
  systemName: string
  assessmentOrg: string
  status: 'approved'
  risk: Risk
  createdAt: string
  submittedAt?: string
  approvedAt?: string
  reportFile?: Attachment
}

interface Evidence {
  attachmentId: string
  fileName: string
  version: number
  confirmedAt: string
  confirmedBy?: string
}

interface Period {
  report: ArchivedReport
  evidence?: Evidence
}

interface FieldValue {
  value: string
  startParagraph: number
  endParagraph: number
  truncated?: boolean
}

const fieldLabels = { assets: '资产清单', findings: '问题记录', risk: '报告风险描述', recommendations: '整改建议' }
const fieldStates = { same: '一致', changed: '有变化', insufficient: '资料不足' }
const riskChanges = { increased: '风险级别升高', decreased: '风险级别降低', same: '风险级别一致' }
type FieldState = keyof typeof fieldStates

interface Comparison {
  from: Period
  to: Period
  riskChange: keyof typeof riskChanges
  fields: Array<{ key: keyof typeof fieldLabels; state: FieldState; from?: FieldValue; to?: FieldValue }>
  findings?: { onlyFrom: string[]; onlyTo: string[]; common: string[] }
}

const findingsLabels = { onlyTo: '仅后期记录', onlyFrom: '仅前期记录', common: '两期均记录' } as const
const props = defineProps<{ path: string }>()
const archive = shallowRef<Page<ArchivedReport>>({ items: [] })
const comparison = shallowRef<Comparison>()
const from = ref('')
const to = ref('')
const archiveLoading = ref(false)
const compareLoading = ref(false)
const archiveFailure = ref('')
const compareFailure = ref('')
const periods = computed(() => comparison.value ? [comparison.value.from, comparison.value.to] : [])
let archiveRequest: AbortController | undefined
let compareRequest: AbortController | undefined

/**
 * 用途 读取同一备案的已通过报告，并在首页默认比较相邻两期。
 * 参数 cursor 为服务端返回的下一页游标，缺省时重新读取首页。
 * 返回值 归档加载任务，首页有效时启动对比。
 */
async function loadArchive(cursor?: string) {
  archiveRequest?.abort()
  if (!cursor) clearComparison()
  const request = new AbortController()
  archiveRequest = request
  archiveLoading.value = true
  archiveFailure.value = ''
  try {
    const query = params({ cursor })
    const page = await api<Page<ArchivedReport>>(props.path + '/history-reports' + query, { signal: request.signal })
    if (request.signal.aborted) return
    archive.value = cursor ? { ...page, items: [...archive.value.items, ...page.items] } : page
    if (!cursor) {
      from.value = page.items[1]?.id ?? ''
      to.value = page.items[0]?.id ?? ''
      if (from.value && to.value) void compare()
    }
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) archiveFailure.value = errorText(error)
  } finally {
    if (archiveRequest === request) archiveLoading.value = false
  }
}

/**
 * 用途 清除上一次对比并取消尚未结束的请求。
 * 参数 无。
 * 返回值 无。
 */
function clearComparison() {
  compareRequest?.abort()
  compareRequest = undefined
  comparison.value = undefined
  compareLoading.value = false
  compareFailure.value = ''
}

/**
 * 用途 查询选定的两期报告及已确认字段的对比结果。
 * 参数 无，读取当前归档列表与前后期选项。
 * 返回值 对比加载任务，选择不合规时显示可修正提示。
 */
async function compare() {
  clearComparison()
  const oldIndex = archive.value.items.findIndex(item => item.id === from.value)
  const newIndex = archive.value.items.findIndex(item => item.id === to.value)
  if (newIndex < 0 || oldIndex <= newIndex) {
    compareFailure.value = '请选择两份不同的报告，并将较早报告设为前期。'
    return
  }
  const request = new AbortController()
  compareRequest = request
  compareLoading.value = true
  try {
    const query = params({ from: from.value, to: to.value })
    const result = await api<Comparison>(props.path + '/history-comparison' + query, { signal: request.signal })
    if (!request.signal.aborted) comparison.value = result
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) compareFailure.value = errorText(error)
  } finally {
    if (compareRequest === request) compareLoading.value = false
  }
}

/**
 * 用途 生成证据或报告原件的既有归属校验下载地址。
 * 参数 report 为报告归档记录，attachmentId 为该报告登记的附件标识。
 * 返回值 同源材料下载地址。
 */
function downloadReport(report: ArchivedReport, attachmentId: string): string {
  return attachmentURL('/reports/' + encodeURIComponent(report.id), attachmentId)
}

/**
 * 用途 显示校对字段在报告正文中的段落范围。
 * 参数 field 为持久化的字段文本及段落元数据。
 * 返回值 唯一段落或起止段落的中文说明。
 */
function paragraphs(field: FieldValue): string {
  const end = field.startParagraph === field.endParagraph ? '' : '–' + field.endParagraph
  return '正文第 ' + field.startParagraph + end + ' 段'
}

watch(() => props.path, () => {
  archive.value = { items: [] }
  from.value = to.value = ''
  void loadArchive()
}, { immediate: true })
onScopeDispose(() => {
  archiveRequest?.abort()
  compareRequest?.abort()
})
</script>

<template>
  <section class="panel detail-panel report-history" aria-labelledby="report-history-title">
    <header class="panel-heading panel-compact">
      <h2 id="report-history-title">历史报告对比</h2>
      <button class="button neutral small" type="button" :disabled="archiveLoading" @click="loadArchive()">
        <RefreshCw :size="15" aria-hidden="true" />刷新归档
      </button>
    </header>

    <AsyncState :loading="archiveLoading && !archive.items.length" :error="archive.items.length ? '' : archiveFailure"
      :empty="!archive.items.length" empty-text="同一备案下暂无已通过的历史报告。" @retry="loadArchive()">
      <form v-if="archive.items.length >= 2" class="comparison-picker" @submit.prevent="compare">
        <label class="field"><span>前期报告</span>
          <select v-model="from" required :disabled="archiveLoading" @change="clearComparison">
            <option v-for="item in archive.items" :key="item.id" :value="item.id">
              {{ item.number }} · {{ formatTime(item.submittedAt ?? item.createdAt) }}
            </option>
          </select>
        </label>
        <label class="field"><span>后期报告</span>
          <select v-model="to" required :disabled="archiveLoading" @change="clearComparison">
            <option v-for="item in archive.items" :key="item.id" :value="item.id">
              {{ item.number }} · {{ formatTime(item.submittedAt ?? item.createdAt) }}
            </option>
          </select>
        </label>
        <button class="button primary" type="submit" :disabled="archiveLoading || compareLoading">对比</button>
      </form>
      <p v-else class="report-history-note muted">同一备案至少需要两份已通过的报告。</p>
      <p v-if="archiveFailure" class="form-message error-message report-history-note" role="alert">{{ archiveFailure }}</p>

      <AsyncState v-if="compareLoading || compareFailure || comparison" :loading="compareLoading"
        :error="compareFailure" @retry="compare">
        <template v-if="comparison">
          <div class="history-periods">
            <section v-for="(period, index) in periods" :key="period.report.id" class="history-period">
              <p class="muted">{{ index === 0 ? '前期' : '后期' }}</p>
              <h3>{{ period.report.number }}</h3>
              <p>{{ period.report.assessmentOrg }} · {{ risks[period.report.risk] }}</p>
              <p class="muted">提交 {{ formatTime(period.report.submittedAt) }}</p>
              <p v-if="period.evidence" class="evidence-note">
                <a class="text-action" :href="downloadReport(period.report, period.evidence.attachmentId)">
                  {{ period.evidence.fileName }}
                </a>
                <small class="muted">
                  校对版本 {{ period.evidence.version }} · {{ formatTime(period.evidence.confirmedAt) }}
                </small>
              </p>
              <p v-else class="muted evidence-note">当前正文暂无已确认的校对记录。</p>
            </section>
          </div>

          <div class="table-wrap history-content">
            <table class="data-table">
              <thead><tr><th>比较维度</th><th>前期</th><th>后期</th><th>变化</th></tr></thead>
              <tbody>
                <tr>
                  <th scope="row">测评机构</th>
                  <td>{{ comparison.from.report.assessmentOrg }}</td><td>{{ comparison.to.report.assessmentOrg }}</td>
                  <td>{{ comparison.from.report.assessmentOrg === comparison.to.report.assessmentOrg ? '一致' : '机构变更' }}</td>
                </tr>
                <tr>
                  <th scope="row">登记风险等级</th>
                  <td>{{ risks[comparison.from.report.risk] }}</td><td>{{ risks[comparison.to.report.risk] }}</td>
                  <td>{{ riskChanges[comparison.riskChange] }}</td>
                </tr>
                <tr v-for="field in comparison.fields" :key="field.key">
                  <th scope="row">{{ fieldLabels[field.key] }}</th>
                  <td v-for="side in ['from', 'to'] as const" :key="side">
                    <details v-if="field[side]" class="history-field">
                      <summary>{{ paragraphs(field[side]) }}</summary>
                      <p>{{ field[side].value || '校对内容为空' }}</p>
                      <small v-if="field[side].truncated" class="muted">提取文本有截断</small>
                    </details>
                    <span v-else class="muted">暂无校对字段</span>
                  </td>
                  <td>{{ fieldStates[field.state] }}</td>
                </tr>
              </tbody>
            </table>
          </div>

          <section v-if="comparison.findings" class="history-findings" aria-labelledby="history-findings-title">
            <h3 id="history-findings-title">问题记录变化</h3>
            <p class="muted">按两期已校对正文的记录逐行核对。</p>
            <details v-for="(label, group) in findingsLabels" :key="group">
              <summary>{{ label }} · {{ comparison.findings[group].length }} 条</summary>
              <ul v-if="comparison.findings[group].length">
                <li v-for="line in comparison.findings[group]" :key="line">{{ line }}</li>
              </ul>
              <p v-else class="muted">无匹配记录。</p>
            </details>
          </section>
        </template>
      </AsyncState>

      <section aria-labelledby="archive-report-title" class="archive-reports">
        <header class="panel-heading panel-compact"><h3 id="archive-report-title">历史报告归档</h3></header>
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr><th>报告编号</th><th>系统</th><th>测评机构</th><th>提交时间</th><th>终审通过</th><th>风险等级</th><th>报告</th></tr>
            </thead>
            <tbody>
              <tr v-for="report in archive.items" :key="report.id">
                <td>{{ report.number }}</td><td>{{ report.systemName }}</td><td>{{ report.assessmentOrg }}</td>
                <td>{{ formatTime(report.submittedAt) }}</td><td>{{ formatTime(report.approvedAt) }}</td>
                <td :class="'risk-' + report.risk">{{ risks[report.risk] }}</td>
                <td>
                  <a v-if="report.reportFile" class="text-action" :href="downloadReport(report, report.reportFile.id)"
                    :aria-label="'下载 ' + report.number + ' · ' + report.reportFile.fileName">
                    <Download :size="15" aria-hidden="true" />下载
                  </a>
                  <span v-else class="muted">暂无正文</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <footer v-if="archive.nextCursor" class="panel-footer">
          <button class="button neutral small" type="button" :disabled="archiveLoading"
            @click="loadArchive(archive.nextCursor)">{{ archiveLoading ? '加载中…' : '加载更早报告' }}</button>
        </footer>
      </section>
    </AsyncState>
  </section>
</template>

<style scoped>
.report-history .panel-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.comparison-picker { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 12px; padding: 14px 16px; }
.comparison-picker .field { flex: 1 1 190px; min-width: 0; }
.history-periods { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; padding: 4px 16px 16px; }
.history-period { padding: 12px; border: 1px solid var(--line); border-radius: 8px; overflow-wrap: anywhere; }
.history-period h3, .evidence-note { margin-top: 3px; }
.evidence-note small { display: block; }
.history-content .data-table td { vertical-align: top; min-width: 125px; }
.history-content .data-table th[scope] { text-align: left; }
.history-field summary, .history-findings summary { color: var(--primary); cursor: pointer; }
.history-field p { max-width: 23rem; white-space: pre-wrap; overflow-wrap: anywhere; }
.history-findings { padding: 14px 16px; }
.history-findings details { margin-top: 8px; }
.history-findings li { overflow-wrap: anywhere; }
.archive-reports { border-top: 1px solid var(--line); }
.report-history-note { margin: 12px 16px; }
@media (max-width: 540px) { .history-periods { grid-template-columns: 1fr; } }
</style>

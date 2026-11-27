<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { ChevronLeft, ChevronRight, Plus } from '@lucide/vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { queryValue, usePagedList } from '../lib/paging'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'
import {
  remediationBadge, remediationDue, remediationPath, remediationRecordPath, remediationStatuses,
  type RemediationStatistics, type RemediationSummary,
} from '../lib/remediations'

const route = useRoute()
type RemindableSummary = RemediationSummary & { reminderEligible: boolean }
type ReminderResult = { status: 'sent' | 'already_sent' | 'ineligible' }
const { keyword, filter, extraFilter, page, loading, failure, load, refresh, search, reset, nextPage, previousPage }
  = usePagedList<RemindableSummary>(() => remediationPath, 'status', () => 'due')
const stats = ref<RemediationStatistics>()
const statsError = ref('')
const selected = ref<string[]>([])
const sending = ref(false)
const reminderResult = ref('')
const reminderError = ref('')
const exportURL = computed(() => '/api/v1' + remediationPath + '/export' + params({
  q: queryValue(route.query.q), status: queryValue(route.query.status), due: queryValue(route.query.due),
}))
let statsRequest: AbortController | undefined
let reminderRequest: AbortController | undefined

watch(page, () => { selected.value = [] })

/**
 * 用途 分别加载跨所有规划的即时统计。
 * 参数 无，指标窗口由业务统计接口统一计算。
 * 返回值 统计获取任务，失败不影响列表操作。
 */
async function loadStats() {
  statsRequest?.abort()
  const current = new AbortController()
  statsRequest = current
  statsError.value = ''
  try {
    const result = await api<RemediationStatistics>(remediationPath + '/statistics', { signal: current.signal })
    if (statsRequest === current) stats.value = result
  } catch (error) {
    if (statsRequest === current && !isAbort(error)) statsError.value = errorText(error)
  }
}

/**
 * 用途 催办当前页明确选择的临期规划，并报告服务端实际送达结果。
 * 参数 无，使用监管员在列表中选中的规划标识。
 * 返回值 催办及列表刷新任务。
 */
async function sendReminders() {
  if (!selected.value.length || sending.value || loading.value || failure.value) return
  const current = new AbortController()
  reminderRequest = current
  sending.value = true
  reminderResult.value = ''
  reminderError.value = ''
  try {
    const result = await api<{ items: ReminderResult[] }>(remediationPath + '/reminders', {
      method: 'POST', body: { planIds: selected.value }, signal: current.signal,
    })
    const sent = result.items.filter(item => item.status === 'sent').length
    const repeated = result.items.filter(item => item.status === 'already_sent').length
    const changed = result.items.filter(item => item.status === 'ineligible').length
    reminderResult.value = `已催办 ${sent} 条，今日已催办 ${repeated} 条，当前不满足催办条件 ${changed} 条。`
    await load()
  } catch (error) {
    if (!isAbort(error)) reminderError.value = errorText(error)
  } finally {
    sending.value = false
    reminderRequest = undefined
  }
}

void loadStats()
refreshBusinessOnFocus(() => {
  if (route.path === remediationPath && !selected.value.length && !sending.value) {
    void refresh()
    void loadStats()
  }
}, 10_000)
onScopeDispose(() => {
  statsRequest?.abort()
  reminderRequest?.abort()
})
</script>

<template>
  <section class="v2-list" aria-label="整改规划审核">
    <div class="metrics" aria-label="整改规划统计">
      <div class="panel metric metric-amber">
        <strong>{{ stats?.pending ?? '—' }}</strong><p>待审核</p>
        <small>已提交的整改规划</small>
      </div>
      <div class="panel metric metric-purple">
        <strong>{{ stats?.returned ?? '—' }}</strong><p>已退回补正</p>
        <small>待运营者重新提交</small>
      </div>
      <div class="panel metric metric-blue">
        <strong>{{ stats?.dueWithin10Days ?? '—' }}</strong><p>10 日内到期</p>
        <small>整改期限临期提醒</small>
      </div>
      <div class="panel metric metric-green">
        <strong>
          {{ !stats ? '—' : stats.firstPassRate === null ? '暂无样本' : stats.firstPassRate + '%' }}
        </strong>
        <p>一次通过率</p><small>近 90 日整改规划</small>
      </div>
    </div>
    <p v-if="statsError" class="form-message error-message" role="alert">
      {{ statsError }} <button class="text-action" type="button" @click="loadStats">重试统计</button>
    </p>

    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <RouterLink class="button primary" :to="remediationPath + '/new'">
          <Plus :size="16" aria-hidden="true" />新建整改规划
        </RouterLink>
        <button class="button neutral" type="button" :disabled="!selected.length || loading || !!failure || sending"
          @click="sendReminders">
          {{ sending ? '正在催办…' : '批量催办' + (selected.length ? `（${selected.length}）` : '') }}
        </button>
        <a v-if="!failure" class="button neutral" :href="exportURL" title="导出已应用筛选的全部规划">导出台账</a>
        <button v-else class="button neutral" type="button" disabled>导出台账</button>
        <span class="v2-toolbar-spacer"></span>
        <label class="field">
          <span>审核状态</span>
          <select v-model="filter" @change="search">
            <option value="">全部状态</option>
            <option v-for="(name, value) in remediationStatuses" :key="value" :value="value">{{ name }}</option>
          </select>
        </label>
        <label class="field">
          <span>整改期限</span>
          <select v-model="extraFilter" @change="search">
            <option value="">全部整改期限</option>
            <option v-for="(name, value) in remediationDue" :key="value" :value="value">{{ name }}</option>
          </select>
        </label>
        <label class="field filter-keyword">
          <span>关键词</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="系统 / 单位 / 备案编号">
        </label>
        <button class="button primary" type="submit">查询</button>
        <button class="button neutral" type="button" @click="reset">重置</button>
      </div>
    </form>
    <p class="muted reminder-window">可催办待运营者补正、今日至未来 10 日内到期的规划。</p>
    <p v-if="reminderResult" class="form-message" role="status">{{ reminderResult }}</p>
    <p v-if="reminderError" class="form-message error-message" role="alert">{{ reminderError }}</p>
    <div class="panel v2-list-table">
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="未找到符合条件的整改规划。" @retry="load">
        <div class="table-wrap">
          <table class="data-table">
            <thead>
              <tr>
                <th scope="col">催办</th>
                <th scope="col">备案编号</th><th scope="col">系统 / 单位</th>
                <th scope="col">版本 · 提交时间</th><th scope="col">风险项</th>
                <th scope="col">整改期限</th><th scope="col">审核状态</th><th scope="col">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in page.items" :key="item.id">
                <td>
                  <input v-if="item.reminderEligible" v-model="selected" type="checkbox" :value="item.id"
                    :aria-label="'选择催办 ' + item.reportNumber" :disabled="sending">
                  <span v-else class="muted" aria-label="当前不可催办">—</span>
                </td>
                <td class="code">{{ item.filingNumber }}</td>
                <td class="emphasis">{{ item.systemName }}<small class="muted cell-note">{{ item.organizationName }}</small></td>
                <td>{{ item.submission ? 'V' + item.submission + '.0' : '未提交' }}
                  <small v-if="item.submittedAt" class="muted cell-note">{{ formatTime(item.submittedAt) }}</small>
                </td>
                <td><div class="risk-counts">
                  <span v-if="item.riskCounts.high" class="risk-high">高 {{ item.riskCounts.high }}</span>
                  <span v-if="item.riskCounts.medium" class="risk-medium">中 {{ item.riskCounts.medium }}</span>
                  <span v-if="item.riskCounts.low" class="risk-low">低 {{ item.riskCounts.low }}</span>
                </div></td>
                <td class="deadline">{{ item.deadline }}</td>
                <td><span class="status-badge" :class="remediationBadge(item.status)">
                  <span aria-hidden="true"></span>{{ remediationStatuses[item.status] }}
                </span></td>
                <td><RouterLink class="text-action" :to="{ path: remediationRecordPath(item.id), query: route.query }"
                  :aria-label="'查看整改规划 ' + item.reportNumber">详情</RouterLink></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>

      <footer v-if="!loading && !failure" class="panel-footer pager">
        <span>本页 {{ page.items.length }} 条</span>
        <div>
          <button class="button neutral small" type="button" :disabled="!route.query.cursor" @click="previousPage">
            <ChevronLeft :size="16" aria-hidden="true" />上一页
          </button>
          <button class="button neutral small" type="button" :disabled="!page.nextCursor" @click="nextPage">
            下一页<ChevronRight :size="16" aria-hidden="true" />
          </button>
        </div>
      </footer>
    </div>
  </section>
</template>

<style scoped>
.metrics { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 13px; margin-bottom: 17px; }
.metric { position: relative; overflow: hidden; padding: 14px 16px; }
.metric::before { content: ''; position: absolute; inset: 0 auto 0 0; width: 3px; background: var(--accent); }
.metric strong { color: var(--c-primary-700); font-size: 26px; line-height: 1.1; }
.metric p { margin: 4px 0 0; color: var(--muted); font-size: 13px; }
.metric small { display: block; margin-top: 8px; color: var(--c-text-3); }
.metric-blue { --accent: #185fa5; }
.metric-amber { --accent: #e69127; }
.metric-purple { --accent: #7e57c2; }
.metric-green { --accent: #0d9488; }
.cell-note { display: block; font-weight: 400; margin-top: 3px; }
.risk-counts { display: flex; flex-wrap: wrap; gap: 4px 7px; }
.risk-counts span, .deadline { white-space: nowrap; }
.reminder-window { margin: 0 0 10px; font-size: 13px; }
@media (max-width: 780px) { .metrics { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 9px; } }
</style>

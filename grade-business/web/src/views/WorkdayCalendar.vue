<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { ApiError, api, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { refreshWorkbench } from '../lib/workbench'

interface Day {
  day: string
  isWorkday: boolean
  override: boolean
  version: number
}
type Mode = 'workday' | 'restday' | 'default'
interface CalendarChange extends Day {
  mode: Mode
  reason: string
  operator: string
  changedAt: string
  previous: Pick<Day, 'isWorkday' | 'override'>
}
interface HistoryPage { items: CalendarChange[]; nextBefore?: string }

const endpoint = '/workday-calendar'
const modes: Record<Mode, string> = { workday: '设置为工作日', restday: '设置为休息日', default: '恢复默认日历' }
const defaultMonth = new Intl.DateTimeFormat('sv-SE', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit',
}).format()
const weekdayFormat = new Intl.DateTimeFormat('zh-CN', { weekday: 'short', timeZone: 'UTC' })
const route = useRoute()
const monthInput = ref(defaultMonth)
const days = shallowRef<Day[]>([])
const loadedMonth = ref('')
const monthLoading = ref(false)
const monthError = ref('')
const historyDay = ref('')
const history = shallowRef<HistoryPage>()
const historyLoading = ref(false)
const historyError = ref('')
const notice = ref('')
const saveError = ref('')
const dialog = ref<HTMLDialogElement>()
const editing = shallowRef<Day>()
const mode = ref<Mode>('workday')
const reason = ref('')
const saving = ref(false)
let monthRequest: AbortController | undefined
let historyRequest: AbortController | undefined
let saveRequest: AbortController | undefined
let retry: { fingerprint: string; id: string } | undefined

const canSave = computed(() => editing.value && reason.value.trim().length > 0 &&
  (mode.value === 'default' ? editing.value.override
    : !editing.value.override || editing.value.isWorkday !== (mode.value === 'workday')))

/**
 * 用途 获取选定自然月的服务端日历，取消上一次月份读取。
 * 参数 selected 为当前选择的月份。
 * 返回值 日期与版本的读取任务。
 */
async function loadMonth(selected = monthInput.value) {
  if (!selected) return
  monthRequest?.abort()
  const active = new AbortController()
  monthRequest = active
  monthLoading.value = true
  monthError.value = ''
  try {
    const result = await api<{ month: string; items: Day[] }>(endpoint + params({ month: selected }),
      { signal: active.signal })
    if (!active.signal.aborted) {
      loadedMonth.value = result.month
      days.value = result.items
    }
  } catch (error) {
    if (!isAbort(error)) monthError.value = errorText(error)
  } finally {
    if (monthRequest === active) monthLoading.value = false
  }
}

/**
 * 用途 选择一个日期的最新留痕，或接续当前日期的下一页。
 * 参数 day 为选中自然日，before 为历史续页版本。
 * 返回值 可取消的审计读取任务。
 */
async function loadHistory(day: string, before?: string) {
  historyRequest?.abort()
  const active = new AbortController()
  historyRequest = active
  historyDay.value = day
  if (!before) history.value = undefined
  historyLoading.value = true
  historyError.value = ''
  try {
    const page = await api<HistoryPage>(endpoint + '/' + day + '/history' + params({ before }),
      { signal: active.signal })
    if (!active.signal.aborted) {
      history.value = before ? { ...page, items: [...(history.value?.items ?? []), ...page.items] } : page
    }
  } catch (error) {
    if (!isAbort(error)) historyError.value = errorText(error)
  } finally {
    if (historyRequest === active) historyLoading.value = false
  }
}

/**
 * 用途 按当前列表看到的版本打开日历设置。
 * 参数 item 为需要变更的自然日与现有覆盖状态。
 * 返回值 无。
 */
function openEditor(item: Day) {
  editing.value = item
  mode.value = item.isWorkday ? 'restday' : 'workday'
  reason.value = ''
  retry = undefined
  saveError.value = ''
  notice.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 保存确认前锁定的日期版本，刷新工作台和日历实际结果。
 * 参数 无，使用确认框中业务输入。
 * 返回值 写入及读取后的任务。
 */
async function save() {
  const item = editing.value
  if (!item || saving.value || !canSave.value) return
  const input = { expectedVersion: item.version, mode: mode.value, reason: reason.value.trim() }
  const fingerprint = JSON.stringify(input)
  if (!retry || retry.fingerprint !== fingerprint) retry = { fingerprint, id: crypto.randomUUID() }
  const active = new AbortController()
  saveRequest = active
  saving.value = true
  saveError.value = ''
  try {
    await api<CalendarChange>(endpoint + '/' + item.day,
      { method: 'PATCH', body: { requestId: retry.id, ...input }, signal: active.signal })
    if (active.signal.aborted) return
    dialog.value?.close()
    notice.value = item.day + ' 的日历设置已保存。'
    void refreshWorkbench()
    await Promise.all([loadMonth(loadedMonth.value), loadHistory(item.day)])
  } catch (error) {
    if (isAbort(error)) return
    if (error instanceof ApiError && error.status === 409) {
      dialog.value?.close()
      notice.value = errorText(error)
      await loadMonth(loadedMonth.value)
    } else {
      saveError.value = errorText(error)
    }
  } finally {
    if (saveRequest === active) saving.value = false
  }
}

/**
 * 用途 生成自然日对应的周内名称。
 * 参数 date 为 API 返回的标准日期。
 * 返回值 当前行显示的星期。
 */
function weekday(date: string) {
  return weekdayFormat.format(new Date(date + 'T00:00:00Z'))
}

/**
 * 用途 为当前状态和变更前状态共用工作日标签。
 * 参数 day 为有效工作状态及数据来源。
 * 返回值 工作日或休息日以及覆盖来源。
 */
function dayLabel(day: Pick<Day, 'isWorkday' | 'override'>) {
  return (day.isWorkday ? '工作日' : '休息日') + (day.override ? '（已覆盖）' : '（默认）')
}

/**
 * 用途 提交月份查询并清理上一月份的审计内容。
 * 参数 无，月份来自工具栏。
 * 返回值 月份读取任务。
 */
async function goMonth() {
  historyRequest?.abort()
  historyDay.value = ''
  history.value = undefined
  await loadMonth()
}

watch(() => queryValue(route.query.day), raw => {
  if (/^\d{4}-\d{2}-\d{2}$/.test(raw)) {
    monthInput.value = raw.slice(0, 7)
    void loadMonth()
    void loadHistory(raw)
    return
  }
  void loadMonth()
}, { immediate: true })

onScopeDispose(() => { monthRequest?.abort(); historyRequest?.abort(); saveRequest?.abort() })
</script>

<template>
  <section class="v2-list" aria-label="工作日历">
    <header class="v2-page-head">
      <div><h1>工作日历</h1><p>设置休息日或补班。变更生效后，在办审核期限和站内提醒使用该日历。</p></div>
    </header>
    <form class="v2-list-toolbar" @submit.prevent="goMonth">
      <div class="v2-toolbar-row">
        <label class="field"><span>月份</span><input v-model="monthInput" type="month" required></label>
        <button class="button primary" type="submit" :disabled="monthLoading || saving">查询日历</button>
      </div>
    </form>
    <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
    <AsyncState :loading="monthLoading" :error="monthError" :empty="!days.length" @retry="loadMonth()">
      <div class="table-wrap panel">
        <table class="data-table">
          <thead><tr><th>日期</th><th>星期</th><th>有效设置</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="item in days" :key="item.day">
              <td>{{ item.day }}</td><td>{{ weekday(item.day) }}</td><td>{{ dayLabel(item) }}</td>
              <td class="calendar-actions">
                <button class="text-action" type="button" @click="openEditor(item)">设置</button>
                <button class="text-action" type="button" @click="loadHistory(item.day)">变更记录</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <section v-if="historyDay" class="panel calendar-history" aria-label="日历变更记录">
      <header><h2>{{ historyDay }} 变更记录</h2>
        <RouterLink class="text-action" :to="{ path: '/audit', query: { resource: 'workday-calendar', q: historyDay } }">查看操作日志</RouterLink>
      </header>
      <AsyncState :loading="historyLoading && !history" :error="historyError" :empty="!history?.items.length"
        empty-text="该日期暂无变更记录。" @retry="loadHistory(historyDay)">
        <ol>
          <li v-for="change in history?.items" :key="change.version">
            <strong>{{ modes[change.mode] }}</strong> · {{ change.operator }} · {{ formatTime(change.changedAt) }}
            <p>{{ dayLabel(change.previous) }} → {{ dayLabel(change) }}。{{ change.reason }}</p>
          </li>
        </ol>
        <button v-if="history?.nextBefore" class="button neutral" type="button" :disabled="historyLoading"
          @click="loadHistory(historyDay, history.nextBefore)">加载更早记录</button>
      </AsyncState>
    </section>
    <dialog ref="dialog" class="decision-dialog" aria-labelledby="calendar-confirmation"
      @cancel.prevent="!saving && dialog?.close()">
      <form v-if="editing" class="calendar-form" @submit.prevent="save">
        <h2 id="calendar-confirmation">设置 {{ editing.day }}</h2>
        <p>当前为{{ dayLabel(editing) }}。</p>
        <label class="field"><span>生效设置</span><select v-model="mode" :disabled="saving">
          <option v-for="(label, key) in modes" :key="key" :value="key"
            :disabled="key === 'default' && !editing.override">{{ label }}</option>
        </select></label>
        <label class="field"><span>变更理由</span>
          <textarea v-model="reason" rows="3" maxlength="300" required :disabled="saving"></textarea>
        </label>
        <p v-if="saveError" class="form-message error-message" role="alert">{{ saveError }}</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="saving" @click="dialog?.close()">取消</button>
          <button class="button primary" type="submit" :disabled="saving || !canSave">确认保存</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.calendar-form, .calendar-history { display: grid; gap: 12px; }
.calendar-form h2, .calendar-form p, .calendar-history h2, .calendar-history p { margin: 0; }
.calendar-actions { display: flex; gap: 14px; }
.calendar-history { padding: 16px; margin-top: 14px; }
.calendar-history header { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; }
.calendar-history ol { display: grid; gap: 10px; padding-inline-start: 20px; }
</style>


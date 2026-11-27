<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import DirectoryPicker from './DirectoryPicker.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { formatTime, levels } from '../lib/domain'
import { createRequestId } from '../lib/requestId'

const endpoint = '/alerts/rules'
const triggers = { high_risk: '未销项高风险隐患', overdue: '整改逾期', filing_review_overdue: '备案审核超期' }
const audiences = { regulator: '监管端', institution: '对应测评机构', operator: '对应运营单位' }
type RulePolicy = {
  name: string; trigger: keyof typeof triggers; audience: keyof typeof audiences
  district?: string; level?: string; unitId?: string; all?: boolean; enabled: boolean
}
type Rule = RulePolicy & { id: string; version: number; actor: string; updatedAt: string }
type Hit = { resource: 'alerts' | 'filings'; resourceId: string; reference: string; systemName: string
  organizationName: string; audience: keyof typeof audiences; recipient: string; deadline: string }
type Match = Hit & { ruleId: string; ruleName: string; ruleVersion: number; matchedAt: string }
type Page<T> = {items: T[]; nextBefore?: string}
const tab = ref<'rules' | 'matches'>('rules')
const rules = shallowRef<Page<Rule>>({ items: [] })
const matches = shallowRef<Page<Match>>({ items: [] })
const dialog = ref<HTMLDialogElement>()
const editing = shallowRef<Rule>()
const fields = ref({ name: '', trigger: 'high_risk' as RulePolicy['trigger'], audience: 'operator' as RulePolicy['audience'],
  district: '', level: '', unitId: '', all: false, enabled: true })
const policy = computed<RulePolicy>(() => ({
  name: fields.value.name.trim(), trigger: fields.value.trigger, audience: fields.value.audience,
  ...(!fields.value.all ? {
    district: fields.value.district.trim() || undefined, level: fields.value.level || undefined,
    unitId: fields.value.unitId || undefined,
  } : { all: true }), enabled: fields.value.enabled,
}))
const isComplete = computed(() => Boolean(policy.value.name &&
  (policy.value.all || policy.value.district || policy.value.unitId || policy.value.level)))
const requestId = ref('')
const loading = ref(false)
const working = ref(false)
const workError = ref('')
const listError = ref('')
const succeeded = ref('')
const preview = shallowRef<{items: Hit[]; hasMore?: boolean}>()
let query: AbortController | undefined
let write: AbortController | undefined

/**
 * 用途 读取当前选中的规则或命中留痕页面。
 * 参数 before 为续页位置，缺省时重新读取第一页。
 * 返回值 当前列表加载任务。
 */
async function load(before?: string) {
  query?.abort()
  const request = new AbortController()
  query = request
  const current = tab.value
  listError.value = ''
  loading.value = true
  try {
    const filter = params({ before })
    if (current === 'rules') {
      const next = await api<Page<Rule>>(endpoint + filter, { signal: request.signal })
      if (!request.signal.aborted) rules.value = { ...next, items: [...(before ? rules.value.items : []), ...next.items] }
    } else {
      const next = await api<Page<Match>>(endpoint + '/matches' + filter, { signal: request.signal })
      if (!request.signal.aborted) matches.value = { ...next, items: [...(before ? matches.value.items : []), ...next.items] }
    }
  } catch (error) {
    if (!request.signal.aborted && !isAbort(error)) listError.value = errorText(error)
  } finally {
    if (query === request) loading.value = false
  }
}

/**
 * 用途 打开规则表单并加载当前版本或初始配置。
 * 参数 known 为正在修改的已保存规则，缺省时创建。
 * 返回值 无。
 */
function open(known?: Rule) {
  editing.value = known
  fields.value = {
    name: known?.name ?? '', trigger: known?.trigger ?? 'high_risk', audience: known?.audience ?? 'operator',
    district: known?.district ?? '', level: known?.level ?? '', unitId: known?.unitId ?? '',
    all: known?.all ?? false, enabled: known?.enabled ?? true,
  }
  succeeded.value = workError.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 根据当前配置预览命中、保存规则或立即扫描。
 * 参数 mode 为当前按钮选择的操作。
 * 返回值 本次操作及刷新任务。
 */
async function run(mode: 'preview' | 'save' | 'sync') {
  if (working.value || (mode !== 'sync' && !isComplete.value)) return
  const request = new AbortController()
  write = request
  workError.value = ''
  working.value = true
  try {
    if (mode === 'preview') {
      preview.value = await api(endpoint + '/preview', { method: 'POST', body: policy.value, signal: request.signal })
      return
    }
    if (mode === 'sync') {
      const result = await api<{matched: number; notified: number}>(endpoint + '/sync', {
        method: 'POST', signal: request.signal,
      })
      succeeded.value = `新增命中 ${result.matched} 条，首次送达 ${result.notified} 条。`
      if (tab.value === 'matches') await load()
      else tab.value = 'matches'
      return
    }
    const current = editing.value
    if (!current && !requestId.value) requestId.value = createRequestId()
    await api(current ? endpoint + '/' + current.id : endpoint, {
      method: current ? 'PATCH' : 'POST',
      body: { ...policy.value, ...(current ? { expectedVersion: current.version } : { requestId: requestId.value }) },
      signal: request.signal,
    })
    succeeded.value = '预警规则已保存。'
    dialog.value?.close()
    await load()
  } catch (error) {
    if (!isAbort(error)) {
      workError.value = errorText(error)
      if (error instanceof ApiError && error.status === 409 && editing.value) void load()
    }
  } finally {
    if (write === request) working.value = false
  }
}

/**
 * 用途 显示多个区县、等级或单位条件的交集说明。
 * 参数 record 为读取到的规则配置。
 * 返回值 可读的范围文本。
 */
function locationLabel(record: RulePolicy) {
  return record.all ? '全部备案范围' :
    [record.district, record.level && (levels[record.level] ?? record.level + '级'), record.unitId && '指定单位']
      .filter(Boolean).join(' · ')
}

watch(policy, () => {
  requestId.value = ''
  preview.value = undefined
})
watch(tab, () => void load())
onMounted(() => void load())
onScopeDispose(() => { query?.abort(); write?.abort() })
</script>

<template>
  <section class="panel rule-panel" aria-label="预警规则">
    <div class="v2-toolbar-row">
      <button class="button" :class="tab === 'rules' ? 'primary' : 'neutral'" @click="tab = 'rules'">规则列表</button>
      <button class="button" :class="tab === 'matches' ? 'primary' : 'neutral'" @click="tab = 'matches'">命中留痕</button>
      <button class="button neutral" :disabled="working" @click="run('sync')">立即扫描规则</button>
      <button v-if="tab === 'rules'" class="button neutral" :disabled="working" @click="open()">新建预警规则</button>
    </div>
    <p v-if="succeeded" class="form-message success-message" role="status">{{ succeeded }}</p>
    <p v-if="workError && !dialog?.open" class="form-message error-message" role="alert">{{ workError }}</p>
    <AsyncState :loading="loading" :error="listError" :empty="!(tab === 'rules' ? rules.items : matches.items).length"
      :empty-text="tab === 'rules' ? '暂无自定义预警规则。' : '暂无规则命中记录。'" @retry="load()">
      <div class="table-wrap">
        <table v-if="tab === 'rules'" class="data-table">
          <thead><tr><th>名称</th><th>触发条件</th><th>接收角色</th><th>筛选范围</th><th>状态</th><th>操作</th></tr></thead>
          <tbody><tr v-for="row in rules.items" :key="row.id">
            <td>{{ row.name }}</td><td>{{ triggers[row.trigger] }}</td><td>{{ audiences[row.audience] }}</td>
            <td>{{ locationLabel(row) }}</td><td>{{ row.enabled ? '已启用' : '已停用' }}</td>
            <td><button type="button" class="text-action" @click="open(row)">编辑</button></td>
          </tr></tbody>
        </table>
        <table v-else class="data-table">
          <thead><tr><th>规则</th><th>系统与单位</th><th>接收方</th><th>截止日期</th><th>命中时间</th><th>操作</th></tr></thead>
          <tbody><tr v-for="row in matches.items" :key="row.ruleId + ':' + row.resourceId + ':' + row.matchedAt">
            <td>{{ row.ruleName }}<br><small class="muted">版本 {{ row.ruleVersion }}</small></td>
            <td>{{ row.systemName }}<br><small>{{ row.organizationName }}</small></td>
            <td>{{ row.recipient }}</td><td>{{ row.deadline }}</td><td>{{ formatTime(row.matchedAt) }}</td>
            <td><RouterLink class="text-action"
              :to="row.resource === 'filings' ? '/filings/' + row.resourceId
                : { path: '/alerts', query: { alert: row.resourceId } }">查看业务</RouterLink></td>
          </tr></tbody>
        </table>
      </div>
    </AsyncState>
    <button v-if="tab === 'rules' ? rules.nextBefore : matches.nextBefore" class="button neutral" type="button"
      :disabled="loading" @click="load(tab === 'rules' ? rules.nextBefore : matches.nextBefore)">加载更多</button>

    <dialog ref="dialog" class="decision-dialog rule-dialog" aria-labelledby="rule-editor-heading"
      @cancel.prevent="!working && dialog?.close()">
      <form class="rule-panel" @submit.prevent="run('save')">
        <h2 id="rule-editor-heading">{{ editing ? '编辑预警规则' : '新建预警规则' }}</h2>
        <fieldset :disabled="working" class="rule-grid">
          <label class="field"><span>规则名称</span><input v-model="fields.name" required maxlength="100"></label>
          <label class="field"><span>触发条件</span><select v-model="fields.trigger">
            <option v-for="(label, key) in triggers" :key="key" :value="key">{{ label }}</option>
          </select></label>
          <label class="field"><span>接收角色</span><select v-model="fields.audience">
            <option v-for="(label, key) in audiences" :key="key" :value="key">{{ label }}</option>
          </select></label>
          <label class="rule-checkbox"><input v-model="fields.all" type="checkbox">明确选择全部备案范围</label>
          <template v-if="!fields.all">
            <label class="field"><span>所属区县</span><input v-model="fields.district" maxlength="80" placeholder="填写名录中的区县名称"></label>
            <label class="field"><span>保护等级</span><select v-model="fields.level">
              <option value="">全部等级</option>
              <option v-for="(label, key) in levels" :key="key" :value="key">{{ label }}</option>
            </select></label>
            <DirectoryPicker v-model="fields.unitId" kind="unit" label="指定单位" use-id />
          </template>
          <label class="rule-checkbox"><input v-model="fields.enabled" type="checkbox">启用规则</label>
        </fieldset>
        <div v-if="preview" class="rule-preview" aria-label="命中预览">
          <strong>{{ preview.items.length ? '当前命中样本' : '当前没有符合范围的事项' }}</strong>
          <ul v-if="preview.items.length"><li v-for="item in preview.items" :key="item.resourceId">
            {{ item.systemName }} · {{ item.organizationName }} → {{ item.recipient }}
          </li></ul>
          <p v-if="preview.hasMore">显示前 20 条。</p>
        </div>
        <p v-if="workError" class="form-message error-message" role="alert">{{ workError }}</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="working" @click="dialog?.close()">关闭</button>
          <button class="button neutral" type="button" :disabled="working || !isComplete" @click="run('preview')">预览收件范围</button>
          <button class="button primary" type="submit" :disabled="working || !isComplete">保存预警规则</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.rule-panel { display: grid; gap: 12px; padding: 14px; }
.rule-dialog { width: min(730px, 94vw); max-height: 90vh; overflow: auto; }
.rule-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 12px; border: 0; padding: 0; margin: 0; }
.rule-checkbox { display: flex; align-items: center; gap: 6px; }
.rule-checkbox input[type="checkbox"] { flex: none; width: 16px; min-height: 0; height: 16px; padding: 0; }
.rule-preview { border: 1px solid #dbe6f2; border-radius: 6px; padding: 10px; max-height: 180px; overflow: auto; }
</style>

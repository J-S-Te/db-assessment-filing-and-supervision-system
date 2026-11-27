<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import BusinessReceiptDialog from '../components/BusinessReceiptDialog.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { writeIdentity } from '../lib/clauses'
import { formatTime } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { assessmentDueLabels, businessBadge, businessScopes, type BusinessAudience } from '../lib/scopedBusiness'

interface DueEvent {
  action: 'register' | 'revise' | 'close'
  version: number
  dueOn: string
  warningDays: number
  evidence: string
  actor: string
  createdAt: string
}
interface DueRecord {
  id: string
  filingId: string
  filingNumber: string
  systemName: string
  organizationName: string
  institutionName?: string
  dueOn: string
  warningDays: number
  status: string
  version: number
  evidence?: string
  history?: DueEvent[]
}
interface DuePage { items: DueRecord[]; nextBefore?: string }

const props = defineProps<{ audience?: BusinessAudience }>()
const route = useRoute()
const router = useRouter()
const base = computed(() => (props.audience ? '/' + props.audience : '') + '/assessment-dues')
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const scopeSettings = computed(() => props.audience ? businessScopes[props.audience] : undefined)
const scopeName = computed(() => scopeSettings.value ? queryValue(route.query[scopeSettings.value.key]).trim() : '')
const scopeQuery = computed(() => scopeSettings.value ? { [scopeSettings.value.key]: scopeName.value } : {})
const scopeDraft = ref('')
const filingId = computed(() => queryValue(route.query.filingId))
const status = computed(() => queryValue(route.query.status))
const before = computed(() => queryValue(route.query.before))
const listQuery = computed(() => ({ ...scopeQuery.value, filingId: filingId.value || undefined,
  status: status.value || undefined }))
const endpoint = computed(() => id.value ? base.value + '/' + encodeURIComponent(id.value) + params(scopeQuery.value)
  : base.value + params({ ...listQuery.value, before: before.value || undefined }))
const available = computed(() => !scopeSettings.value || Boolean(scopeName.value))
const page = shallowRef<DuePage>()
const detail = shallowRef<DueRecord>()
const loading = ref(false)
const failure = ref('')
const actionError = ref('')
const notice = ref('')
const editor = ref<HTMLDialogElement>()
const action = ref<DueEvent['action']>('register')
const dueOn = ref('')
const warningDays = ref<number | ''>(7)
const evidence = ref('')
const saving = ref(false)
const actionLabels = { register: '登记', revise: '修订', close: '办结' }
let readRequest: AbortController | undefined
let writeRequest: AbortController | undefined
const registrationId = writeIdentity()

/**
 * 用途 读取当前范围的台账列表或单条台账。
 * 参数 无，范围取自当前路由。
 * 返回值 请求完成后同步台账数据与错误提示。
 */
async function load() {
  readRequest?.abort()
  if (!available.value) return
  const active = new AbortController()
  readRequest = active
  loading.value = true
  failure.value = ''
  try {
    if (id.value) detail.value = await api<DueRecord>(endpoint.value, { signal: active.signal })
    else page.value = await api<DuePage>(endpoint.value, { signal: active.signal })
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (readRequest === active) loading.value = false
  }
}

/**
 * 用途 打开到期登记、日期修订或办结表单。
 * 参数 nextAction 是本次准备执行的台账操作。
 * 返回值 无。
 */
function openEditor(nextAction: DueEvent['action']) {
  action.value = nextAction
  dueOn.value = detail.value?.dueOn ?? ''
  warningDays.value = detail.value?.warningDays ?? 7
  evidence.value = ''
  actionError.value = ''
  registrationId()
  editor.value?.showModal()
}

/**
 * 用途 提交到期登记、变更依据或办结依据，并读取最新台账。
 * 参数 无，读取用户填写的日期、预警窗口和依据。
 * 返回值 保存并同步页面的任务。
 */
async function save() {
  if (saving.value || !evidence.value.trim()) return
  const registering = action.value === 'register'
  const record = detail.value
  if (registering ? !filingId.value : !record) return
  const active = new AbortController()
  writeRequest = active
  saving.value = true
  actionError.value = ''
  try {
    const dateFields = { dueOn: dueOn.value, warningDays: warningDays.value }
    const registration = { filingId: filingId.value, ...dateFields, evidence: evidence.value.trim() }
    const body = registering ? { ...registration, requestId: registrationId(registration) }
      : { action: action.value, expectedVersion: record?.version,
      ...(action.value === 'revise' ? dateFields : {}), evidence: evidence.value.trim() }
    const saved = await api<DueRecord>(registering ? base.value : endpoint.value,
      { method: registering ? 'POST' : 'PATCH', body, signal: active.signal })
    editor.value?.close()
    if (registering) await router.replace({ path: base.value + '/' + saved.id, query: listQuery.value })
    else await load()
    notice.value = '已' + actionLabels[action.value] + '。'
  } catch (error) {
    if (!isAbort(error)) actionError.value = errorText(error)
  } finally {
    if (writeRequest === active) saving.value = false
  }
}

/**
 * 用途 触发一次站内测评到期通知计算。
 * 参数 无。
 * 返回值 更新本次送达数量的任务。
 */
async function sweep() {
  if (saving.value) return
  const active = new AbortController()
  writeRequest = active
  saving.value = true
  failure.value = ''
  try {
    const result = await api<{ notices: number }>(base.value + '/sweep', { method: 'POST', signal: active.signal })
    await load()
    notice.value = `本次新增 ${result.notices} 条站内通知。`
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (writeRequest === active) saving.value = false
  }
}

watch(endpoint, () => {
  detail.value = undefined
  page.value = undefined
  scopeDraft.value = scopeName.value
  notice.value = ''
  void load()
}, { immediate: true })
onScopeDispose(() => { readRequest?.abort(); writeRequest?.abort() })
</script>

<template>
  <section>
    <RouterLink class="back-link" :to="id ? { path: base, query: listQuery }
      : { path: (audience ? '/' + audience : '') + '/alerts', query: scopeQuery }">
      {{ id ? '返回到期台账' : '返回预警' }}
    </RouterLink>
    <header class="page-header">
      <h1>测评到期台账</h1>
      <div v-if="!audience" class="page-actions">
        <button v-if="!id && filingId" type="button" class="button primary" :disabled="loading || saving"
          @click="openEditor('register')">登记测评到期</button>
        <button v-if="!id" type="button" class="button neutral" :disabled="loading || saving"
          @click="sweep">检查到期通知</button>
      </div>
    </header>
    <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
    <section class="panel">
      <form v-if="scopeSettings" class="filter-bar" @submit.prevent="router.push({ path: base,
        query: { [scopeSettings.key]: scopeDraft.trim() || undefined } })">
        <label class="field"><span>{{ scopeSettings.label }}</span><input v-model="scopeDraft" required></label>
        <button class="button primary" type="submit">查看</button>
      </form>
      <p v-if="!available" class="muted">请选择办理范围。</p>
      <AsyncState v-else :loading="loading" :error="failure" @retry="load">
        <template v-if="detail">
          <header class="page-header">
            <h2>{{ detail.systemName }}</h2>
            <div v-if="!audience" class="page-actions">
              <BusinessReceiptDialog resource="assessment-dues" :record-id="detail.id" />
              <template v-if="detail.status !== 'closed'">
                <button type="button" class="button neutral" @click="openEditor('revise')">修订日期</button>
                <button type="button" class="button neutral" @click="openEditor('close')">办结</button>
              </template>
            </div>
          </header>
          <dl class="detail-grid">
            <div><dt>备案编号</dt><dd>{{ detail.filingNumber }}</dd></div>
            <div><dt>运营单位</dt><dd>{{ detail.organizationName }}</dd></div>
            <div><dt>测评机构</dt><dd>{{ detail.institutionName || '—' }}</dd></div>
            <div><dt>到期日期</dt><dd>{{ detail.dueOn }}</dd></div>
            <div><dt>提前提醒</dt><dd>{{ detail.warningDays }} 天</dd></div>
            <div><dt>当前状态</dt><dd>{{ assessmentDueLabels[detail.status] }}</dd></div>
            <div v-if="detail.evidence"><dt>办理依据</dt><dd>{{ detail.evidence }}</dd></div>
          </dl>
          <template v-if="detail.history?.length">
            <h3>办理记录</h3>
            <ul><li v-for="event in detail.history" :key="event.version">
              {{ formatTime(event.createdAt) }} · {{ actionLabels[event.action] }} · {{ event.actor }} · {{ event.evidence }}
            </li></ul>
          </template>
        </template>
        <template v-else-if="page">
          <p v-if="!audience && !filingId" class="muted">登记时从已生效的备案详情打开“系统测评到期”。</p>
          <div class="filter-bar">
            <label class="field"><span>状态</span><select :value="status" @change="router.push({ path: base,
              query: { ...listQuery, status: ($event.target as HTMLSelectElement).value || undefined } })">
              <option value="">全部状态</option>
              <option v-for="(label, state) in assessmentDueLabels" :key="state" :value="state">{{ label }}</option>
            </select></label>
            <RouterLink v-if="filingId" class="text-action" :to="{ path: base, query: scopeQuery }">查看全部系统</RouterLink>
          </div>
          <p v-if="!page.items.length" class="muted">暂无测评到期记录。</p>
          <div v-else class="table-shell">
            <table>
              <thead><tr><th>系统及备案编号</th><th>运营单位</th><th>到期日期</th><th>状态</th></tr></thead>
              <tbody>
                <tr v-for="item in page.items" :key="item.id">
                  <td><RouterLink class="text-action" :to="{ path: base + '/' + item.id, query: listQuery }">
                    {{ item.systemName }}
                  </RouterLink><br><small class="muted">{{ item.filingNumber }}</small></td>
                  <td>{{ item.organizationName }}</td><td>{{ item.dueOn }}</td>
                  <td><span class="status-badge" :class="businessBadge(item.status)">
                    {{ assessmentDueLabels[item.status] }}
                  </span></td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="page-actions">
            <RouterLink v-if="before" class="button neutral" :to="{ path: base, query: listQuery }">返回首页</RouterLink>
            <RouterLink v-if="page.nextBefore" class="button neutral" :to="{ path: base,
              query: { ...listQuery, before: page.nextBefore } }">下一页</RouterLink>
          </div>
        </template>
      </AsyncState>
    </section>
    <dialog v-if="!audience" ref="editor" class="decision-dialog" aria-labelledby="assessment-due-editor"
      @cancel="saving && $event.preventDefault()">
      <form @submit.prevent="save">
        <h2 id="assessment-due-editor">{{ actionLabels[action] }}测评到期</h2>
        <template v-if="action !== 'close'">
          <label class="field"><span>到期日期（北京时间）</span><input v-model="dueOn" type="date" required></label>
          <label class="field"><span>提前提醒天数</span><input v-model="warningDays" type="number" min="0" max="90" required></label>
        </template>
        <label class="field"><span>办理依据</span><textarea v-model="evidence" rows="3" maxlength="1000" required></textarea></label>
        <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
        <div class="page-actions">
          <button type="button" class="button neutral" :disabled="saving" @click="editor?.close()">取消</button>
          <button type="submit" class="button primary" :disabled="saving">{{ saving ? '保存中…' : '确认' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

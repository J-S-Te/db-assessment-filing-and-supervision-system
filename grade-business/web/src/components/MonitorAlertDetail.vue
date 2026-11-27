<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import {
  alertActionNotices, alertActionPayload, alertActions, alertBadgeStyles,
  alertDispositionStatuses, alertDispositionStyles, alertPath, alertStatuses, alertTypes,
  institutionAlertPath, operatorAlertPath,
  type AlertAction, type AlertDetail, type AlertMutation,
} from '../lib/alerts'
import { formatTime, risks } from '../lib/domain'
import { hazardPath, hazardStatuses } from '../lib/hazards'
import { businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'

const props = defineProps<{ id: string; initialAction?: AlertAction; audience?: BusinessAudience; scope: string }>()
const emit = defineEmits<{ close: []; changed: [] }>()
const path = computed(() => {
  const collection = props.audience === 'institution' ? institutionAlertPath
    : props.audience === 'operator' ? operatorAlertPath : alertPath
  return collection + '/' + encodeURIComponent(props.id)
})
const scopeQuery = computed(() => props.audience ? { [businessScopes[props.audience].key]: props.scope } : {})
const query = computed(() => params(scopeQuery.value))
const dialog = ref<HTMLDialogElement>()
const detail = shallowRef<AlertDetail>()
const action = ref<AlertAction>(props.initialAction ?? 'process')
const comment = ref('')
const requestedDeadline = ref('')
const loading = ref(false)
const saving = ref(false)
const failure = ref('')
const actionFailure = ref('')
const notice = ref('')
const convertedHazard = ref('')
const disposition = computed(() => detail.value?.disposition)
const reviewing = computed(() => !props.audience && disposition.value?.status === 'pending')
const body = computed(() => {
  const item = detail.value
  if (item?.allowedActions.includes(action.value)) {
    return alertActionPayload(action.value, item.version, comment.value, requestedDeadline.value)
  }
})
let readRequest: AbortController | undefined
let writeRequest: AbortController | undefined

/**
 * 用途 生成对应办理端的整改单链接并保留收件范围。
 * 参数 id 为详情或处置接口返回的真实隐患标识。
 * 返回值 对应办理端的整改单路由。
 */
function linkedHazard(id: string) {
  const prefix = props.audience ? '/' + props.audience : ''
  return { path: prefix + hazardPath(id), query: scopeQuery.value }
}

/**
 * 用途 读取预警当前状态、来源隐患和处置留痕。
 * 参数 quiet 表示后台读取时保留当前显示的详情。
 * 返回值 详情读取或错误处理任务。
 */
async function load(quiet = false) {
  readRequest?.abort()
  const request = new AbortController()
  readRequest = request
  loading.value = !quiet
  failure.value = ''
  try {
    const result = await api<AlertDetail>(path.value + query.value, { signal: request.signal })
    if (request.signal.aborted) return
    detail.value = result
    if (!result.allowedActions.includes(action.value)) {
      comment.value = ''
      requestedDeadline.value = ''
      action.value = result.allowedActions[0] ?? 'close'
    }
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    failure.value = errorText(error)
    if (error instanceof ApiError && [403, 404].includes(error.status)) detail.value = undefined
  } finally {
    if (readRequest === request) {
      readRequest = undefined
      loading.value = false
    }
  }
}

/**
 * 用途 按已查看版本登记一次预警处置并刷新留痕。
 * 参数 无，使用选择的动作和用户填写的备注。
 * 返回值 保存及状态同步任务。
 */
async function save() {
  const input = body.value
  if (!input || saving.value || loading.value || failure.value) return
  readRequest?.abort()
  const request = new AbortController()
  writeRequest = request
  saving.value = true
  actionFailure.value = notice.value = ''
  convertedHazard.value = ''
  try {
    const result = await api<AlertMutation>(path.value + '/actions' + query.value, {
      method: 'POST', body: input, signal: request.signal,
    })
    if (request.signal.aborted) return
    comment.value = ''
    requestedDeadline.value = ''
    notice.value = alertActionNotices[input.action]
    convertedHazard.value = input.action === 'convert' ? result.hazardId ?? '' : ''
    emit('changed')
    await load()
  } catch (error) {
    if (request.signal.aborted || isAbort(error)) return
    actionFailure.value = errorText(error)
    if (error instanceof ApiError && error.status === 409) await load()
  } finally {
    if (writeRequest === request) {
      writeRequest = undefined
      saving.value = false
    }
  }
}

/**
 * 用途 关闭预警窗口并把筛选返回权交给列表。
 * 参数 无。
 * 返回值 无。
 */
function dismiss() {
  if (!saving.value) emit('close')
}

onMounted(() => {
  dialog.value?.showModal()
  void load()
})
watch(action, () => {
  comment.value = ''
  requestedDeadline.value = ''
})
refreshBusinessOnFocus(() => {
  if (!readRequest && !writeRequest && !comment.value && !requestedDeadline.value) void load(true)
}, 10_000)
onScopeDispose(() => {
  readRequest?.abort()
  writeRequest?.abort()
})
</script>

<template>
  <dialog ref="dialog" class="decision-dialog alert-detail" aria-labelledby="alert-detail-title" @cancel.prevent="dismiss">
    <header class="alert-detail-heading">
      <h2 id="alert-detail-title">{{ detail ? alertTypes[detail.type] : '预警详情' }}</h2>
      <button type="button" class="v2-route-close" aria-label="关闭预警详情窗口" :disabled="saving" @click="dismiss">×</button>
    </header>
    <p v-if="notice" class="form-message success-message" role="status">
      {{ notice }} <RouterLink v-if="convertedHazard" class="text-action" :to="linkedHazard(convertedHazard)">
        办理整改单
      </RouterLink>
    </p>
    <p v-if="actionFailure" class="form-message error-message" role="alert">{{ actionFailure }}</p>
    <AsyncState :loading="loading && !detail" :error="failure" @retry="load()">
      <template v-if="detail">
        <component :is="reviewing ? 'details' : 'section'" class="panel" aria-label="预警信息">
          <summary v-if="reviewing" class="alert-source-summary">
            <strong>{{ detail.systemName }}</strong> · 当前整改期限 {{ detail.deadline }}
            <span class="muted"> · 查看预警信息</span>
          </summary>
          <header v-else class="panel-heading panel-compact">
            <strong>{{ detail.systemName }}</strong>
            <span class="status-badge" :class="alertBadgeStyles[detail.status]">
              <span aria-hidden="true"></span>{{ alertStatuses[detail.status] }}
            </span>
          </header>
          <dl class="facts">
            <div><dt>所属单位</dt><dd>{{ detail.organizationName }}</dd></div>
            <div><dt>风险级别</dt><dd :class="'risk-' + detail.severity">{{ risks[detail.severity] }}</dd></div>
            <div><dt>触发时间</dt><dd>{{ formatTime(detail.triggeredAt) }}</dd></div>
            <div><dt>当前整改期限</dt><dd>{{ detail.deadline }}</dd></div>
            <div v-if="detail.triggeredDeadline"><dt>触发时整改期限</dt><dd>{{ detail.triggeredDeadline }}</dd></div>
            <div v-if="detail.type === 'overdue' && detail.currentlyOverdue === false">
              <dt>当前逾期情况</dt><dd>当前未逾期</dd>
            </div>
            <div><dt>隐患当前状态</dt><dd>{{ hazardStatuses[detail.hazardStatus] }}</dd></div>
            <div v-if="detail.closedAt"><dt>预警关闭时间</dt><dd>{{ formatTime(detail.closedAt) }}</dd></div>
            <div class="alert-description"><dt>台账说明</dt><dd>{{ detail.description }}</dd></div>
          </dl>
          <footer class="panel-footer">
            <RouterLink class="text-action" :to="linkedHazard(detail.hazardId)">
              {{ audience ? '查看整改单' : '查看来源隐患' }}
            </RouterLink>
          </footer>
        </component>

        <section v-if="disposition" class="panel alert-history"
          :class="{ 'alert-pending-review': disposition.status === 'pending' }" aria-label="预警申请进度">
          <header class="panel-heading panel-compact">
            <h3>{{ alertActions[disposition.action] }}</h3>
            <span class="status-badge" :class="alertDispositionStyles[disposition.status]">
              <span aria-hidden="true"></span>{{ alertDispositionStatuses[disposition.status] }}
            </span>
          </header>
          <dl class="facts">
            <div><dt>办理时间</dt><dd>{{ formatTime(disposition.requestedAt) }}</dd></div>
            <div><dt>{{ disposition.requesterAudience === 'institution' ? '反馈机构' : '经办人' }}</dt>
              <dd>{{ disposition.requestedBy }}</dd></div>
            <div v-if="disposition.requestedDeadline"><dt>申请延期至</dt><dd>{{ disposition.requestedDeadline }}</dd></div>
            <div v-if="disposition.comment" class="alert-description">
              <dt>{{ disposition.action === 'convert' ? '办理备注'
                : disposition.action === 'submit_feedback' ? '处置反馈' : '申请说明' }}</dt>
              <dd>{{ disposition.comment }}</dd>
            </div>
            <div v-if="disposition.decidedAt"><dt>审核时间</dt><dd>{{ formatTime(disposition.decidedAt) }}</dd></div>
            <div v-if="disposition.decidedBy"><dt>审核人</dt><dd>{{ disposition.decidedBy }}</dd></div>
            <div v-if="disposition.decisionComment" class="alert-description">
              <dt>审核意见</dt><dd>{{ disposition.decisionComment }}</dd>
            </div>
          </dl>
        </section>

        <form v-if="detail.allowedActions.length" class="alert-decision" @submit.prevent="save">
          <h3 v-if="reviewing">{{ disposition?.action === 'submit_feedback' ? '审核机构处置反馈' : '审核预警申请' }}</h3>
          <p v-if="detail.approvalBlockedReason" class="form-message warning-message" role="status">
            {{ detail.approvalBlockedReason }}
          </p>
          <fieldset class="form-grid" :disabled="loading || saving">
            <label class="field">
              <span>处置动作</span>
              <select v-model="action">
                <option v-for="choice in detail.allowedActions" :key="choice" :value="choice">{{ alertActions[choice] }}</option>
              </select>
            </label>
            <label v-if="action === 'request_extension'" class="field">
              <span>申请延期至</span>
              <input v-model="requestedDeadline" type="date" min="0001-01-01" max="9999-12-31" required>
            </label>
            <label class="field wide">
              <span>{{ action === 'approve' || action === 'reject' ? '审核意见'
                : action === 'convert' ? '办理备注（选填）'
                : action === 'submit_feedback' ? '处置反馈'
                : audience === 'operator' ? '申请说明' : '处置说明' }}</span>
              <textarea v-model="comment" maxlength="1000" rows="3" :required="action !== 'convert'" />
            </label>
          </fieldset>
          <div class="form-actions">
            <button class="button primary" type="submit" :disabled="loading || saving || !body">
              {{ saving ? '保存中…' : '确认' + alertActions[action] }}
            </button>
          </div>
        </form>

        <section class="panel alert-history" aria-labelledby="alert-history-title">
          <header class="panel-heading panel-compact"><h3 id="alert-history-title">处置留痕</h3></header>
          <ol v-if="detail.history.length" class="timeline">
            <li v-for="(item, index) in detail.history" :key="index">
              <span class="timeline-dot" aria-hidden="true"></span>
              <strong>{{ alertActions[item.action] }}</strong>
              <span><template v-if="item.fromStatus">{{ alertStatuses[item.fromStatus] }} → </template>
                {{ alertStatuses[item.toStatus] }}
              </span>
              <span v-if="item.dispositionAction && item.dispositionAction !== item.action">
                {{ alertActions[item.dispositionAction] }}
              </span>
              <span v-if="item.requestedDeadline">申请延期至 {{ item.requestedDeadline }}</span>
              <p v-if="item.comment">{{ item.comment }}</p>
              <small>{{ item.operator }} · {{ formatTime(item.occurredAt) }}</small>
            </li>
          </ol>
          <p v-else class="panel-footer muted">暂无处置记录。</p>
        </section>
      </template>
    </AsyncState>

    <footer class="form-actions">
      <button type="button" class="button neutral" :disabled="saving" @click="dismiss">返回列表</button>
    </footer>
  </dialog>
</template>

<style scoped>
.alert-detail { width: min(690px, calc(100vw - 32px)); max-height: calc(100vh - 40px); overflow: auto; }
.alert-detail-heading { display: flex; justify-content: space-between; align-items: center; gap: 12px; margin-bottom: 14px; }
.alert-detail-heading h2 { margin: 0; }
.alert-detail .facts { margin: 0; padding: 10px 16px; gap: 8px 16px; }
.alert-detail .panel-footer { min-height: 36px; padding: 6px 16px; }
.alert-source-summary { padding: 12px 16px; cursor: pointer; overflow-wrap: anywhere; }
.alert-description { grid-column: 1 / -1; white-space: pre-wrap; }
.alert-decision, .alert-history { margin-top: 12px; }
.alert-pending-review { border-left: 3px solid var(--c-warn); }
.alert-decision h3 { margin: 0 0 12px; }
.alert-decision .form-grid { gap: 10px 16px; }
.alert-decision .form-actions { margin-top: 12px; }
.timeline p { white-space: pre-wrap; }
</style>

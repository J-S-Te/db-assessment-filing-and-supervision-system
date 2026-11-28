<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import { useRoute } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import { api, ApiError, errorText } from '../lib/api'
import { formatTime } from '../lib/domain'
import { reviewRegistrationPath, registrationStatuses } from '../lib/operatorRegistration'
import type { RegistrationItem } from '../lib/operatorRegistration'
import { queryValue, usePagedList } from '../lib/paging'
import { hasPermission } from '../lib/session'

const route = useRoute()
const creditCode = computed(() => queryValue(route.query.creditCode))
const { keyword, filter, page, loading, failure, load, search, nextPage, previousPage } =
  usePagedList<RegistrationItem>(() => reviewRegistrationPath, 'status', () => 'creditCode')
const dialog = ref<HTMLDialogElement>()
const item = shallowRef<RegistrationItem>()
const decision = ref<'approve' | 'reject'>('approve')
const verified = ref(false)
const reason = ref('')
const notice = ref('')
const actionError = ref('')
const saving = ref(false)

/**
 * 用途 打开单条申请的审核确认框。
 * 参数 row 为当前列表申请，action 为所选择的审核动作。
 * 返回值 无。
 */
function openReview(row: RegistrationItem, action: 'approve' | 'reject') {
  item.value = row
  decision.value = action
  reason.value = ''
  verified.value = false
  actionError.value = notice.value = ''
  dialog.value?.showModal()
}

/**
 * 用途 保存真实审核结果，并刷新当前范围的申请列表。
 * 参数 无，读取当前单据、审核决定及填写的核验结果。
 * 返回值 审批与列表同步任务。
 */
async function saveReview() {
  if (!item.value || saving.value) return
  saving.value = true
  actionError.value = ''
  try {
    const reviewed = await api<RegistrationItem>(reviewRegistrationPath + '/' + item.value.id + '/review-actions',
      { method: 'POST', body: { decision: decision.value, verified: verified.value, reason: reason.value } })
    notice.value = reviewed.status === 'approved' ? '审核通过，运营单位账号已开通。' : '申请已驳回。'
    dialog.value?.close()
    await load()
  } catch (error) {
    actionError.value = errorText(error)
    if (error instanceof ApiError && (error.code === 'registration_reviewed' || error.status === 404)) await load()
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <section class="v2-list" aria-labelledby="registration-review-heading">
    <header class="page-header">
      <div><h2 id="registration-review-heading">注册审核</h2></div>
      <RouterLink v-if="creditCode" class="button neutral" :to="{ query: {} }">返回待审单位列表</RouterLink>
    </header>
    <form class="v2-list-toolbar" @submit.prevent="search">
      <div class="v2-toolbar-row">
        <label class="field"><span>申请状态</span><select v-model="filter">
          <option value="">待审核</option>
          <option value="approved">已通过</option>
          <option value="rejected">已驳回</option>
        </select></label>
        <label class="field filter-keyword"><span>关键字</span>
          <input v-model="keyword" type="search" maxlength="120" placeholder="单位名称、信用代码或账号">
        </label>
        <button type="submit" class="button primary" :disabled="loading">查询</button>
        <span v-if="creditCode" class="muted">信用代码 {{ creditCode }}</span>
      </div>
    </form>
    <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
    <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
      empty-text="当前筛选范围内没有注册申请。" @retry="load">
      <div class="table-wrap v2-list-table">
        <table class="data-table">
          <thead><tr><th>申请单位</th><th>监管辖区</th><th>管理员</th><th>申请状态</th><th>审核记录 / 操作</th></tr></thead>
          <tbody>
            <tr v-for="row in page.items" :key="row.id">
              <td><strong>{{ row.unitName }}</strong><br><span class="muted">{{ row.creditCode }}</span></td>
              <td>
                {{ row.district }}
                <small v-if="row.requestedDistrict && row.requestedDistrict !== row.district"
                  class="muted requested-district">
                  申请填写 {{ row.requestedDistrict }}
                </small>
              </td>
              <td>{{ row.contactName }} · {{ row.phone }}<br><span class="muted">{{ row.username }}</span></td>
              <td>
                <span class="status-badge" :class="'status-' + (row.status === 'pending' ? 'pending_initial' : row.status)">
                  {{ registrationStatuses[row.status] }}</span><br>
                <span class="muted">{{ formatTime(row.createdAt) }}</span>
              </td>
              <td>
                <RouterLink v-if="(row.pendingCount ?? 0) > 1" class="text-action"
                  :to="{ query: { creditCode: row.creditCode } }">
                  查看 {{ row.pendingCount }} 份申请
                </RouterLink>
                <div v-else-if="row.status === 'pending' && hasPermission('admin.sys.user.audit')"
                  class="registration-actions">
                  <button type="button" class="text-action" @click="openReview(row, 'approve')">通过</button>
                  <button type="button" class="text-action" @click="openReview(row, 'reject')">驳回</button>
                </div>
                <template v-else-if="row.reviewedAt">
                  {{ row.reviewer }} · {{ row.reviewerRole === 'ROLE_SYS_ADMIN' ? '系统管理员' : '监管员' }}<br>
                  <span class="muted">{{ formatTime(row.reviewedAt) }}</span>
                  <p v-if="row.reason">{{ row.reason }}</p>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <footer v-if="queryValue(route.query.cursor) || page.nextCursor" class="panel-footer pager">
      <span>本页 {{ page.items.length }} 条</span>
      <div>
        <button type="button" class="button neutral small" :disabled="loading || !queryValue(route.query.cursor)"
          @click="previousPage">上一页</button>
        <button type="button" class="button neutral small" :disabled="loading || !page.nextCursor" @click="nextPage">下一页</button>
      </div>
    </footer>
    <dialog ref="dialog" class="decision-dialog" aria-labelledby="registration-review-confirmation"
      @cancel.prevent="!saving && dialog?.close()">
      <form @submit.prevent="saveReview">
        <h2 id="registration-review-confirmation">{{ decision === 'approve' ? '通过注册申请' : '驳回注册申请' }}</h2>
        <p v-if="item">{{ item.unitName }}（{{ item.creditCode }}）</p>
        <label v-if="decision === 'approve'" class="registration-verification">
          <input v-model="verified" type="checkbox" required :disabled="saving">
          <span>已核实单位资料、行政归属及联系人授权。确认通过后将立即开通该单位账号。</span>
        </label>
        <label class="field"><span>{{ decision === 'approve' ? '审核意见（选填）' : '驳回原因（仅审核端可见）' }}</span>
          <textarea v-model.trim="reason" rows="3" maxlength="500" :required="decision === 'reject'" :disabled="saving" />
        </label>
        <p v-if="actionError" class="form-message error-message" role="alert">{{ actionError }}</p>
        <div class="form-actions">
          <button type="button" class="button neutral" :disabled="saving" @click="dialog?.close()">取消</button>
          <button type="submit" class="button primary" :disabled="saving">{{ saving ? '保存中…' : '确认审核' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.registration-actions { display: flex; gap: 12px; }
.requested-district { display: block; }
.registration-verification { display: flex; gap: 8px; align-items: start; margin: 14px 0; }
.registration-verification input { margin-top: 4px; }
.decision-dialog .field { margin: 14px 0; }
</style>

<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AsyncState from '../components/AsyncState.vue'
import AttachmentPanel from '../components/AttachmentPanel.vue'
import ScopedReviewDeadline from '../components/ScopedReviewDeadline.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { directorySnapshotLabel } from '../lib/directory'
import { reportSubmissionMaterials, risks, type CaseMutation } from '../lib/domain'
import { queryValue } from '../lib/paging'
import { institutionReportPath, type InstitutionReport } from '../lib/reportFeedback'

const route = useRoute()
const router = useRouter()
const scope = computed(() => queryValue(route.query.institution).trim())
const path = computed(() => institutionReportPath(String(route.params.id ?? '')))
const query = computed(() => params({ institution: scope.value }))
const record = shallowRef<InstitutionReport>()
const loading = ref(false)
const submitting = ref(false)
const uploading = ref(false)
const failure = ref('')
const actionError = ref('')
const materialBlocked = ref(false)
const notice = ref('')
const confirmDialog = ref<HTMLDialogElement>()
const deleteDialog = ref<HTMLDialogElement>()
const canEdit = computed(() => record.value?.allowedActions?.includes('edit'))
const canDelete = computed(() => record.value?.allowedActions?.includes('delete'))
const draft = computed(() => record.value?.status === 'draft')
const missing = computed(() => (record.value?.requiredMaterials ?? ['report_file'])
  .filter(kind => !record.value?.attachments.some(file => file.kind === kind)))
const locked = computed(() => submitting.value || uploading.value || loading.value || Boolean(failure.value))
let request: AbortController | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined
let active = true

/**
 * 用途 读取当前机构报告的附件、状态、版本和退回意见。
 * 参数 quiet 为是否保留当前屏幕内容的定时刷新。
 * 返回值 当前报告的加载任务。
 */
async function load(quiet = false) {
  request?.abort()
  if (!active || !scope.value) return
  const current = new AbortController()
  request = current
  loading.value = !quiet
  failure.value = ''
  try {
    const item = await api<InstitutionReport>(path.value + query.value, { signal: current.signal })
    if (request === current && !current.signal.aborted) record.value = item
  } catch (error) {
    if (request === current && !isAbort(error)) {
      failure.value = errorText(error)
      if (error instanceof ApiError && error.status === 404) record.value = undefined
    }
  } finally {
    if (request === current) loading.value = false
  }
}

/**
 * 用途 以当前报告版本进行首次提交或补正重提，进入初审。
 * 参数 无，报告版本由详情接口提供。
 * 返回值 持久化后的状态更新任务。
 */
async function submit() {
  const item = record.value
  if (!item?.canCorrect || locked.value || missing.value.length) return
  const scopeAtStart = path.value + query.value
  submitting.value = true
  actionError.value = notice.value = ''
  materialBlocked.value = false
  try {
    const result = await api<CaseMutation>(path.value + '/submit' + query.value, {
      method: 'POST', body: { expectedVersion: item.version },
    })
    if (!active || scopeAtStart !== path.value + query.value) return
    record.value = { ...item, status: result.status, version: result.version, canCorrect: false, allowedActions: [] }
    notice.value = item.status === 'draft' ? '报告已提交，等待初审。' : '补正已重新提交，等待初审。'
    confirmDialog.value?.close()
    await load()
  } catch (error) {
    if (!active || scopeAtStart !== path.value + query.value) return
    actionError.value = errorText(error)
    materialBlocked.value = error instanceof ApiError && error.code === 'material_issues_open'
    confirmDialog.value?.close()
    if (error instanceof ApiError && error.status === 409) await load()
  } finally {
    submitting.value = false
  }
}

/**
 * 用途 按当前报告版本删除尚未提交的草稿。
 * 参数 无，当前记录和机构范围取自详情页。
 * 返回值 删除完成后的列表导航或错误反馈任务。
 */
async function removeDraft() {
  const item = record.value
  if (!item || !canDelete.value || locked.value) return
  const scopeAtStart = path.value + query.value
  submitting.value = true
  actionError.value = notice.value = ''
  materialBlocked.value = false
  try {
    await api<void>(scopeAtStart, { method: 'DELETE', body: { expectedVersion: item.version } })
    if (!active || scopeAtStart !== path.value + query.value) return
    deleteDialog.value?.close()
    await router.push({ path: '/institution/reports', query: { institution: scope.value } })
  } catch (error) {
    if (!active || scopeAtStart !== path.value + query.value) return
    actionError.value = errorText(error)
    deleteDialog.value?.close()
    if (error instanceof ApiError && error.status === 409) await load()
  } finally {
    submitting.value = false
  }
}

/**
 * 用途 在可见的机构报告页刷新服务端状态，保持审核结果同步。
 * 参数 无。
 * 返回值 无，办理及确认期间由当前操作控制刷新。
 */
function refreshVisible() {
  if (document.visibilityState === 'visible' && !locked.value &&
    !confirmDialog.value?.open && !deleteDialog.value?.open) {
    void load(true)
  }
}

watch([path, query], () => {
  confirmDialog.value?.close()
  deleteDialog.value?.close()
  record.value = undefined
  actionError.value = notice.value = ''
  materialBlocked.value = false
  void load()
}, { immediate: true })
onMounted(() => {
  refreshTimer = setInterval(refreshVisible, 10_000)
  document.addEventListener('visibilitychange', refreshVisible)
})
onScopeDispose(() => {
  active = false
  request?.abort()
  clearInterval(refreshTimer)
  document.removeEventListener('visibilitychange', refreshVisible)
})
</script>

<template>
  <section aria-label="机构测评报告">
    <RouterLink class="back-link" :to="{ path: '/institution/reports', query: { institution: scope || undefined } }">
      ‹ 返回测评报告
    </RouterLink>
    <p v-if="!scope" class="form-message warning-message">请从审核意见查收页选择机构后打开报告。</p>
    <AsyncState v-else :loading="loading && !record" :error="record ? '' : failure" @retry="load()">
      <template v-if="record">
        <header class="page-header">
          <div><h1>{{ record.systemName }}</h1><p>测评报告 {{ record.number }} · {{ risks[record.risk] }}</p>
            <p v-if="directorySnapshotLabel(record.directorySnapshot)">{{ directorySnapshotLabel(record.directorySnapshot) }}</p>
          </div>
          <div class="page-actions">
            <StatusBadge :status="record.status" />
            <RouterLink v-if="canEdit && !locked" class="button neutral"
              :to="{ path: path + '/edit', query: { institution: scope } }">
              {{ draft ? '编辑草稿' : '编辑补正' }}
            </RouterLink>
            <button v-if="canDelete" class="button neutral" type="button" :disabled="locked"
              @click="deleteDialog?.showModal()">删除草稿</button>
          </div>
        </header>
        <ScopedReviewDeadline audience="institution" :scope="scope" resource="reports"
          :id="record.id" :version="record.version" />
        <p v-if="failure" class="form-message warning-message" role="alert">
          {{ failure }}<button class="text-action" type="button" @click="load()">刷新报告</button>
        </p>
        <p v-if="actionError" class="form-message error-message" role="alert">
          {{ actionError }}
          <RouterLink v-if="materialBlocked" class="text-action"
            :to="{ path: '/institution/reports', query: { institution: scope, view: 'failed' } }">查看未成功列表</RouterLink>
        </p>
        <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
        <div v-if="record.returnComment" class="form-message warning-message">
          <strong>退回意见</strong>
          <div class="institution-comment">{{ record.returnComment }}</div>
        </div>
        <p v-if="record.canCorrect && missing.length" class="form-message warning-message">
          待上传 {{ missing.map(kind => reportSubmissionMaterials[kind] ?? kind).join('、') }}
        </p>
        <AttachmentPanel :key="path + query" :path="path" :query="query" kind="reports"
          :attachments="record.attachments" :required="record.requiredMaterials"
          :editable="record.canCorrect && !submitting && !failure" @retry="load()" @uploaded="load()"
          @busy="uploading = $event" />
        <div v-if="record.canCorrect" class="form-actions">
          <button class="button primary" type="button" :disabled="locked || Boolean(missing.length)"
            @click="confirmDialog?.showModal()">{{ draft ? '提交初审' : '重新提交初审' }}</button>
        </div>
      </template>
    </AsyncState>
    <dialog ref="confirmDialog" class="decision-dialog" aria-labelledby="institution-submit-title"
      @cancel.prevent="!submitting && confirmDialog?.close()">
      <form @submit.prevent="submit">
        <h2 id="institution-submit-title">{{ draft ? '提交初审' : '重新提交初审' }}</h2>
        <p>已登记的材料与历次审核意见会随报告保留。</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="submitting" @click="confirmDialog?.close()">取消</button>
          <button class="button primary" type="submit" :disabled="locked">确认提交</button>
        </div>
      </form>
    </dialog>
    <dialog ref="deleteDialog" class="decision-dialog" aria-labelledby="institution-delete-title"
      @cancel.prevent="!submitting && deleteDialog?.close()">
      <form @submit.prevent="removeDraft">
        <h2 id="institution-delete-title">删除未提交的报告草稿</h2>
        <p>删除后报告将退出办理列表，关联异常的关闭结果仍可查阅。</p>
        <div class="form-actions">
          <button class="button neutral" type="button" :disabled="submitting" @click="deleteDialog?.close()">取消</button>
          <button class="button primary" type="submit" :disabled="locked">{{ submitting ? '删除中…' : '确认删除' }}</button>
        </div>
      </form>
    </dialog>
  </section>
</template>

<style scoped>
.institution-comment { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>

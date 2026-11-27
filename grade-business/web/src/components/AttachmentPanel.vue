<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Download, FileText, Upload } from '@lucide/vue'
import AsyncState from './AsyncState.vue'
import FilingWordPreview from './FilingWordPreview.vue'
import { api, ApiError, attachmentURL, errorText } from '../lib/api'
import { formatSize, formatTime, materials, reportSubmissionMaterials, type Attachment, type CaseKind } from '../lib/domain'
import { canPreviewFilingWord, filingPackageKind, isFilingWordKind, materialRecoveryErrors, missingCaseMaterials } from '../lib/materialIssues'

const props = defineProps<{
  path: string
  kind: CaseKind
  attachments: Attachment[]
  editable: boolean
  loading?: boolean
  error?: string
  query?: string
  required?: string[]
}>()
const emit = defineEmits<{ uploaded: []; retry: []; busy: [value: boolean] }>()
const category = ref('')
const legacyWord = computed(() => props.kind === 'filings' && isFilingWordKind(category.value))
const picked = ref<File>()
const input = ref<HTMLInputElement>()
const saving = ref(false)
const failure = ref('')
const showRecovery = ref(false)
const notice = ref('')
const recoveryScope = computed(() => new URLSearchParams(props.query).get('institution'))
const requiredKinds = {
  filings: ['filing_form', 'classification_report'],
  reports: ['report_file'],
}
const materialLabels = computed(() => props.kind === 'reports' && (props.required?.length ?? 0) > 1
  ? reportSubmissionMaterials : materials[props.kind])
const categoryOptions = computed(() => Object.entries(materialLabels.value)
  .filter(([material]) => material !== filingPackageKind))
const missingKinds = computed(() => missingCaseMaterials(props.required ?? requiredKinds[props.kind], props.attachments))
const groups = computed(() => Object.entries(materialLabels.value)
  .map(([key, label]) => ({
    key, label, required: (props.required ?? requiredKinds[props.kind]).includes(key),
    files: props.attachments.filter(file => file.kind === key),
  }))
  .filter(group => group.key !== filingPackageKind || group.files.length))

/**
 * 用途 从本机文件选择器读取本次材料。
 * 参数 event 为文件输入变更事件。
 * 返回值 无。
 */
function pick(event: Event) {
  const target = event.target as HTMLInputElement
  picked.value = target.files?.[0]
  failure.value = ''
  showRecovery.value = false
  notice.value = ''
}

/**
 * 用途 向当前记录上传一个材料及对应类别。
 * 参数 无，类别和文件来自表单。
 * 返回值 上传完成后的任务。
 */
async function upload() {
  if (saving.value || !props.editable) return
  const file = picked.value
  const path = props.path
  const query = props.query ?? ''
  const institutionUpload = path.startsWith('/institution/')
  showRecovery.value = false
  if (!file || !category.value || !Object.hasOwn(materialLabels.value, category.value)) {
    failure.value = '请选择材料类别和文件。'
    return
  }
  const accepted = /\.(pdf|docx|xlsx|zip)$/i.test(file.name) || legacyWord.value && /\.doc$/i.test(file.name)
  if (!institutionUpload && (!file.size || !accepted)) {
    failure.value = legacyWord.value ? '请选择非空的 PDF、DOC 或 DOCX 备案材料。'
      : '请选择非空的 PDF、DOCX、XLSX 或 ZIP 文件。'
    return
  }
  const body = new FormData()
  body.append('file', file)
  body.append('kind', category.value)
  saving.value = true
  emit('busy', true)
  failure.value = ''
  notice.value = ''
  try {
    await api<Attachment>(path + '/attachments' + query, {
      method: 'POST', body, materialKind: institutionUpload ? category.value : undefined,
    })
    if (props.path !== path || (props.query ?? '') !== query) return
    if (input.value) input.value.value = ''
    picked.value = undefined
    notice.value = '材料已上传。'
    emit('uploaded')
  } catch (error) {
    if (props.path === path && (props.query ?? '') === query) {
      failure.value = errorText(error)
      showRecovery.value = institutionUpload && props.kind === 'reports' &&
        error instanceof ApiError && materialRecoveryErrors.has(error.code)
    }
  } finally {
    saving.value = false
    emit('busy', false)
  }
}

watch(() => [props.path, props.query], () => {
  category.value = ''
  picked.value = undefined
  failure.value = ''
  showRecovery.value = false
  notice.value = ''
  if (input.value) input.value.value = ''
})
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="attachments-title">
    <header class="panel-heading panel-compact">
      <div class="heading-icon"><FileText :size="20" aria-hidden="true" /><h2 id="attachments-title">业务材料</h2></div>
    </header>
    <div class="attachment-body v2-attachment-body">
      <form v-if="editable" class="attachment-form" @submit.prevent="upload">
        <label class="field">
          <span>材料类别</span>
          <select v-model="category" required :disabled="saving">
            <option value="" disabled>请选择类别</option>
            <option v-for="[key, name] in categoryOptions" :key="key" :value="key">{{ name }}</option>
          </select>
        </label>
        <label class="field attachment-chooser">
          <span>选择文件</span>
          <input ref="input" type="file" :accept="legacyWord ? '.pdf,.doc,.docx,.xlsx,.zip' : '.pdf,.docx,.xlsx,.zip'"
            required :disabled="saving" @change="pick">
        </label>
        <button class="button primary" type="submit" :disabled="saving">
          <Upload :size="16" aria-hidden="true" />{{ saving ? '上传中…' : '上传材料' }}
        </button>
      </form>
      <p v-if="failure" class="form-message error-message" role="alert">
        {{ failure }}
        <RouterLink v-if="showRecovery" class="text-action"
          :to="{ path: '/institution/reports', query: { institution: recoveryScope, view: 'failed' } }">
          查看未成功列表
        </RouterLink>
      </p>
      <p v-if="notice" class="form-message success-message" role="status">{{ notice }}</p>
      <AsyncState :loading="loading" :error="error" @retry="$emit('retry')">
        <div class="v2-upload-grid">
          <section v-for="group in groups" :key="group.key" class="v2-upload-card">
            <header><h3>{{ group.label }}</h3><span v-if="group.required" aria-label="必需材料">*</span></header>
            <p v-if="!group.files.length" class="v2-upload-row muted">
              {{ group.required && !missingKinds.includes(group.key) ? '已包含在校验通过的备案材料包中' : '暂未上传' }}
            </p>
            <div v-for="file in group.files" :key="file.id" class="v2-upload-row">
              <span class="file-info"><strong>{{ file.fileName }}</strong>
                <small>{{ formatSize(file.size) }} · {{ formatTime(file.uploadedAt) }}</small>
              </span>
              <FilingWordPreview v-if="kind === 'filings' && canPreviewFilingWord(file)"
                :file="file" :path="path" :query="query" />
              <a class="text-action" :href="attachmentURL(path, file.id) + (query ?? '')" :aria-label="'下载 ' + file.fileName">
                <Download :size="15" aria-hidden="true" />下载
              </a>
            </div>
          </section>
        </div>
      </AsyncState>
    </div>
  </section>
</template>

<style scoped>
.v2-attachment-body { padding-block: 12px; }
.v2-upload-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
.v2-upload-card { border: 1px solid var(--line); border-radius: 8px; padding: 12px; }
.v2-upload-card header { display: flex; justify-content: space-between; gap: 8px; margin-bottom: 8px; }
.v2-upload-card header h3 { font-size: 13px; font-weight: 500; }
.v2-upload-card header span { color: var(--c-danger); }
.v2-upload-row {
  display: flex; justify-content: space-between; align-items: center; gap: 8px; padding: 7px 10px;
  font-size: 12px; border: 1px solid var(--line); background: var(--c-surface-2); border-radius: 6px;
}
.v2-upload-row + .v2-upload-row { margin-top: 6px; }
.v2-upload-row .text-action { flex: none; }
@media (max-width: 560px) { .v2-upload-grid { grid-template-columns: 1fr; } }
</style>

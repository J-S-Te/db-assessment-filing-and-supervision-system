<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import AsyncState from './AsyncState.vue'
import V2RouteDialog from './V2RouteDialog.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { operatorAssessmentPath, type AssessmentPoint } from '../lib/assessmentHistory'
import { formatSize, formatTime, risks, type Attachment, type Risk } from '../lib/domain'

interface Requirement {
  risk: Risk
  description: string
  measure: string
  deadline: string
}

interface ReportMaterials {
  items: (Attachment & { kind: 'report_file' | 'remediation_plan' })[]
  requirements?: { deadline: string; items: Requirement[] }
}

const props = defineProps<{ report: AssessmentPoint; filingId: string; organization: string }>()
defineEmits<{ close: [] }>()
const data = shallowRef<ReportMaterials>()
const loading = ref(false)
const failure = ref('')
const path = computed(() => operatorAssessmentPath + '/' + encodeURIComponent(props.filingId)
  + '/reports/' + encodeURIComponent(props.report.id) + '/materials')
const scope = computed(() => params({ organization: props.organization }))
const categories = { report_file: '终审测评报告', remediation_plan: '报告随附整改规划' }
let current: AbortController | undefined

/**
 * 用途 读取该单位对指定终审测评报告可查看的归档材料及整改要求。
 * 参数 无，当前单位、备案及报告来自打开该面板的记录。
 * 返回值 本次有界只读请求完成后的任务。
 */
async function load() {
  current?.abort()
  const controller = new AbortController()
  current = controller
  loading.value = true
  failure.value = ''
  try {
    const result = await api<ReportMaterials>(path.value + scope.value, { signal: controller.signal })
    if (!controller.signal.aborted) data.value = result
  } catch (error) {
    if (!isAbort(error) && !controller.signal.aborted) failure.value = errorText(error)
  } finally {
    if (current === controller) loading.value = false
  }
}

onMounted(load)
onScopeDispose(() => current?.abort())
</script>

<template>
  <V2RouteDialog label="归档报告与整改要求" :width="760" @close="$emit('close')">
    <section class="assessment-delivery">
      <header class="page-header">
        <div>
          <h2>归档报告与整改要求</h2>
          <p class="muted">{{ report.number }}</p>
        </div>
      </header>
      <AsyncState :loading="loading" :error="failure" @retry="load">
        <template v-if="data">
          <section class="panel delivery-panel" aria-label="归档材料">
            <h3>归档材料</h3>
            <p v-if="!data.items.length" class="muted">本报告暂无可下载的归档材料。</p>
            <ul v-else class="delivery-files">
              <li v-for="file in data.items" :key="file.id">
                <div>
                  <strong>{{ categories[file.kind] }}</strong>
                  <span>{{ file.fileName }}</span>
                  <small class="muted">{{ formatSize(file.size) }} · {{ formatTime(file.uploadedAt) }}</small>
                </div>
                <a class="button neutral small" :aria-label="'下载' + categories[file.kind]"
                  :href="'/api/v1' + path + '/' + encodeURIComponent(file.id) + scope" download>下载</a>
              </li>
            </ul>
          </section>
          <section class="panel delivery-panel" aria-label="整改要求">
            <h3>整改要求</h3>
            <p v-if="!data.requirements" class="muted">本报告暂无监管审核通过的结构化整改要求。</p>
            <template v-else>
              <p>整改总期限 {{ data.requirements.deadline }}</p>
              <ol class="delivery-requirements">
                <li v-for="(item, index) in data.requirements.items" :key="index">
                  <strong>{{ risks[item.risk] }} · {{ item.description }}</strong>
                  <p>{{ item.measure }}</p>
                  <small class="muted">整改期限 {{ item.deadline }}</small>
                </li>
              </ol>
            </template>
          </section>
        </template>
      </AsyncState>
    </section>
  </V2RouteDialog>
</template>

<style scoped>
.assessment-delivery { padding: 20px; }
.assessment-delivery h2, .delivery-panel h3, .delivery-panel p { margin: 0; }
.delivery-panel { padding: 14px; margin-block-start: 10px; }
.delivery-panel h3 { margin-block-end: 10px; }
.delivery-files, .delivery-requirements { margin: 0; padding: 0; list-style: none; }
.delivery-files li { display: flex; justify-content: space-between; align-items: start; gap: 10px; }
.delivery-files li + li, .delivery-requirements li + li { border-top: 1px solid #d8e1ee; }
.delivery-files li, .delivery-requirements li { padding: 10px 0; overflow-wrap: anywhere; }
.delivery-files span, .delivery-files small { display: block; margin-top: 3px; }
.delivery-files a { flex: 0 0 auto; }
.delivery-requirements p { white-space: pre-line; }
</style>

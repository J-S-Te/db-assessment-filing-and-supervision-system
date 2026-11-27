<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { operatorAssessmentPath, type AnnualAssessment } from '../lib/assessmentHistory'

const props = defineProps<{ filingId: string; organization: string }>()
const items = shallowRef<AnnualAssessment[]>([])
const failure = ref('')
const loading = ref(true)
let request: AbortController | undefined

/**
 * 用途 读取同一备案可交付的年度记录，取消上一系统的查询。
 * 参数 无，当前备案和单位由页面提供。
 * 返回值 年度趋势查询任务。
 */
async function load() {
  request?.abort()
  const controller = new AbortController()
  request = controller
  items.value = []
  failure.value = ''
  loading.value = true
  try {
    const path = operatorAssessmentPath + '/' + encodeURIComponent(props.filingId) + '/trend'
    const result = await api<{ items: AnnualAssessment[] }>(
      path + params({ organization: props.organization }), { signal: controller.signal },
    )
    if (!controller.signal.aborted) items.value = result.items
  } catch (error) {
    if (!controller.signal.aborted && !isAbort(error)) failure.value = errorText(error)
  } finally {
    if (controller === request) loading.value = false
  }
}

watch([() => props.filingId, () => props.organization], load, { immediate: true, flush: 'sync' })
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="panel annual-assessment" aria-labelledby="annual-assessment-title">
    <h2 id="annual-assessment-title">年度测评得分</h2>
    <p class="muted">按上海自然年取每年最新一份可交付终审报告，未登记得分的年份保留空位。</p>
    <AsyncState :loading="loading" :error="failure" :empty="!items.length"
      empty-text="没有可用于年度趋势的测评报告。" @retry="load">
      <ol class="annual-trend">
        <li v-for="point in items" :key="point.year">
          <strong>{{ point.year }} 年</strong>
          <div class="annual-value">
            <meter v-if="point.score !== undefined" min="0" max="100" :value="point.score"
              :aria-label="point.year + ' 年测评得分'">{{ point.score }} 分</meter>
            <span v-if="point.score !== undefined">{{ point.score }} 分</span>
            <span v-else class="muted">{{ point.reportNumber ? '尚未登记得分' : '暂无归档报告' }}</span>
          </div>
          <small v-if="point.reportNumber" class="muted">{{ point.reportNumber }}</small>
        </li>
      </ol>
    </AsyncState>
  </section>
</template>

<style scoped>
.annual-assessment { display:grid; padding:18px; gap:12px; }
.annual-assessment h2, .annual-assessment p { margin:0; }
.annual-trend { list-style:none; margin:0; padding:0; display:grid; gap:10px; }
.annual-trend li { display:grid; grid-template-columns: 5.25rem minmax(0,1fr) minmax(8rem,.55fr); gap:12px; align-items:center; }
.annual-value { display:flex; align-items:center; gap:8px; }
.annual-value meter { flex:1 1 auto; min-width:0; height:1rem; accent-color:var(--c-primary,#244c88); }
.annual-value span { flex:none; }
.annual-trend small { overflow-wrap:anywhere; }
@media (max-width: 520px) {
  .annual-trend li { grid-template-columns: 4.4rem minmax(0,1fr); gap:4px 8px; }
  .annual-trend small { grid-column:2; }
}
</style>

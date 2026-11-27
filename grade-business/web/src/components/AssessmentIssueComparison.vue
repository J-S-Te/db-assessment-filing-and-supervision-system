<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, ApiError, errorText, isAbort, params } from '../lib/api'
import { operatorAssessmentPath, type IssueComparison } from '../lib/assessmentHistory'
import { risks } from '../lib/domain'

const props = defineProps<{ filingId: string; organization: string; from: string; to: string }>()
const comparison = shallowRef<IssueComparison>()
const loading = ref(true)
const failure = ref('')
const unavailable = ref(false)
const labels = { recurring: '重复出现', added: '本期新增', not_listed: '本期未列出' } as const
let request: AbortController | undefined

/**
 * 用途 查询两期可交付资料的已批整改项目差异。
 * 参数 无，备案、单位及前后报告由对比页面提供。
 * 返回值 问题清单读取任务。
 */
async function load() {
  request?.abort()
  const controller = new AbortController()
  request = controller
  comparison.value = undefined
  unavailable.value = false
  failure.value = ''
  loading.value = true
  try {
    const path = operatorAssessmentPath + '/' + encodeURIComponent(props.filingId) + '/issue-comparison'
    const query = params({ organization: props.organization, from: props.from, to: props.to })
    const data = await api<IssueComparison>(path + query, { signal: controller.signal })
    if (!controller.signal.aborted) comparison.value = data
  } catch (error) {
    if (controller.signal.aborted || isAbort(error)) return
    if (error instanceof ApiError && error.status === 404) unavailable.value = true
    else failure.value = errorText(error)
  } finally {
    if (request === controller) loading.value = false
  }
}

watch([() => props.filingId, () => props.organization, () => props.from, () => props.to], load,
  { immediate: true, flush: 'sync' })
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="issue-comparison" aria-labelledby="assessment-issue-title">
    <h3 id="assessment-issue-title">前后期整改问题</h3>
    <AsyncState :loading="loading" :error="failure" @retry="load">
      <p v-if="unavailable" class="muted">所选报告的整改问题暂无法在当前单位范围查看。</p>
      <template v-else-if="comparison">
        <p v-if="!comparison.fromAvailable || !comparison.toAvailable" class="muted">
          {{ !comparison.fromAvailable && !comparison.toAvailable
            ? '前后两期均暂无' : !comparison.fromAvailable ? '前期暂无' : '后期暂无' }}已批准的整改清单。
        </p>
        <p v-else-if="!comparison.items.length" class="muted">没有可比对的整改问题。</p>
        <template v-else>
          <p class="muted">按两期已批准清单中的问题描述匹配。本期未列出的问题可到整改台账核对状态。</p>
          <div class="issue-table">
            <table class="v2-table">
              <thead><tr><th scope="col">整改问题</th><th scope="col">清单变化</th>
                <th scope="col">前期风险</th><th scope="col">本期风险</th></tr></thead>
              <tbody>
                <tr v-for="item in comparison.items" :key="item.change + ':' + item.description">
                  <td>{{ item.description }}</td><td>{{ labels[item.change] }}</td>
                  <td>{{ item.fromRisk ? risks[item.fromRisk] : '—' }}</td>
                  <td>{{ item.toRisk ? risks[item.toRisk] : '—' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </template>
      </template>
    </AsyncState>
  </section>
</template>

<style scoped>
.issue-comparison { display:grid; gap:10px; }
.issue-comparison h3, .issue-comparison p { margin:0; }
.issue-table { overflow-x:auto; }
.issue-table table { width:100%; min-width:480px; text-align:left; }
.issue-table td:first-child { overflow-wrap:anywhere; }
</style>

<script setup lang="ts">
import { onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { formatTime } from '../lib/domain'
import { refreshBusinessOnFocus } from '../lib/scopedBusiness'

interface Lead {
  alertId: string; filingNumber: string; systemName: string; organizationName: string
  deadline: string; admittedAt: string; active: boolean
}

const page = shallowRef<{items: Lead[]; nextBefore?: string}>()
const filter = ref('active')
const loading = ref(false)
const failure = ref('')
let active: AbortController | undefined

/**
 * 用途 查询监管监督检查线索及原入册信息。
 * 参数 before 为接口给出的续页位置，keepVisible 在后台更新时保留已显示的线索。
 * 返回值 线索读取任务。
 */
async function load(before = '', keepVisible = false) {
  active?.abort()
  const request = new AbortController()
  active = request
  failure.value = ''
  loading.value = true
  if (!before && !keepVisible) page.value = undefined
  try {
    const result = await api<{items: Lead[]; nextBefore?: string}>(
      '/alerts/supervision-leads' + params({ status: filter.value, before }), { signal: request.signal },
    )
    if (!request.signal.aborted) {
      page.value = { ...result, items: [...(before ? page.value?.items ?? [] : []), ...result.items] }
    }
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) loading.value = false
  }
}

watch(filter, () => void load(), { immediate: true })
refreshBusinessOnFocus(() => {
  if (!loading.value && (page.value?.items.length ?? 0) <= 20) void load('', true)
}, 30_000)
onScopeDispose(() => active?.abort())
</script>

<template>
  <section class="panel" aria-label="监督检查线索">
    <div class="v2-toolbar-row">
      <label class="field"><span>跟踪状态</span>
        <select v-model="filter">
          <option value="active">跟踪中</option><option value="all">全部线索</option>
        </select>
      </label>
      <button class="button neutral" type="button" @click="load()">刷新</button>
    </div>
    <AsyncState :loading="loading && !page" :error="failure" :empty="!page?.items.length"
      empty-text="暂无符合条件的监督检查线索。" @retry="load()">
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>系统 / 编号</th><th>入册单位</th><th>超期期限</th><th>入册时间</th><th>跟踪状态</th><th>操作</th></tr></thead>
          <tbody><tr v-for="item in page?.items" :key="item.alertId">
            <td>{{ item.systemName }}<br><small class="muted">{{ item.filingNumber }}</small></td>
            <td>{{ item.organizationName }}</td><td>{{ item.deadline }}</td><td>{{ formatTime(item.admittedAt) }}</td>
            <td>{{ item.active ? '跟踪中' : '留痕' }}</td>
            <td>
              <RouterLink class="text-action" :to="{ path: '/alerts', query: { alert: item.alertId } }">查看预警</RouterLink>
              <RouterLink v-if="item.active" class="text-action"
                :to="{ path: '/supervisions', query: { leadAlertId: item.alertId } }">发起检查</RouterLink>
            </td>
          </tr></tbody>
        </table>
      </div>
    </AsyncState>
    <button v-if="page?.nextBefore" class="button neutral" type="button"
      :disabled="loading" @click="load(page.nextBefore)">加载更多线索</button>
  </section>
</template>

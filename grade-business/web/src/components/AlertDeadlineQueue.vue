<script setup lang="ts">
import { computed, onScopeDispose, ref, shallowRef, watch } from 'vue'
import AsyncState from './AsyncState.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { businessScopes, refreshBusinessOnFocus, type BusinessAudience } from '../lib/scopedBusiness'
import { describeDeadline, reviewAssignee, workbenchTaskLink, type WorkbenchTask } from '../lib/workbenchItems'

const props = defineProps<{ audience?: BusinessAudience; scope: string }>()
type DeadlinePage = {items: WorkbenchTask[]; nextOffset?: number}
const filter = ref('')
const page = shallowRef<DeadlinePage>()
const failure = ref('')
const loading = ref(false)
const scopeQuery = computed(() => props.audience ? { [businessScopes[props.audience].key]: props.scope } : {})
const context = computed(() => props.audience ? { audience: props.audience, query: scopeQuery.value } : undefined)
const endpoint = computed(() => (props.audience ? '/' + props.audience : '') + '/workbench/deadlines')
let active: AbortController | undefined

/**
 * 用途 读取当前归属的办理时限，并按截止日期续页。
 * 参数 offset 为接口返回的分页位置，keepVisible 在后台更新时保留已展示的内容。
 * 返回值 当前页的读取任务。
 */
async function load(offset = 0, keepVisible = false) {
  active?.abort()
  const request = new AbortController()
  active = request
  failure.value = ''
  loading.value = true
  if (!offset && !keepVisible) page.value = undefined
  try {
    const result = await api<DeadlinePage>(endpoint.value + params({
      ...scopeQuery.value, offset: offset || undefined, urgency: filter.value,
    }), { signal: request.signal })
    if (!request.signal.aborted) {
      page.value = { ...result, items: [...(offset ? page.value?.items ?? [] : []), ...result.items] }
    }
  } catch (error) {
    if (!isAbort(error)) failure.value = errorText(error)
  } finally {
    if (active === request) loading.value = false
  }
}

watch(() => [props.scope, props.audience, filter.value], () => void load(), { immediate: true })
refreshBusinessOnFocus(() => {
  if (!loading.value && (page.value?.items.length ?? 0) <= 20) void load(0, true)
}, 30_000)
onScopeDispose(() => active?.abort())
</script>

<template>
  <section class="panel" aria-label="业务时限">
    <div class="v2-toolbar-row">
      <label class="field"><span>时限状态</span>
        <select v-model="filter">
          <option value="">全部时限</option><option value="overdue">已超期</option>
          <option value="near">临近截止</option><option value="upcoming">后续截止</option>
        </select>
      </label>
      <button class="button neutral" type="button" @click="load()">刷新</button>
    </div>
    <AsyncState :loading="loading && !page" :error="failure" :empty="!page?.items.length"
      empty-text="暂无符合条件的办理时限。" @retry="load()">
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>系统 / 编号</th><th>办理环节</th><th>截止日期</th><th>操作</th></tr></thead>
          <tbody><tr v-for="item in page?.items" :key="item.resource+':'+item.id">
            <td>{{ item.systemName }}<br><small class="muted">{{ item.number }}</small></td>
            <td>{{ item.label }}<template v-if="item.waitingFor">
              <br><small class="muted">{{ reviewAssignee(item.waitingFor) }}</small>
            </template></td>
            <td>{{ item.deadline }}<br><small>{{ describeDeadline(item.deadline).text }}</small></td>
            <td><RouterLink class="text-action" :to="workbenchTaskLink(item, context)">查看业务</RouterLink></td>
          </tr></tbody>
        </table>
      </div>
    </AsyncState>
    <button v-if="page?.nextOffset" class="button neutral" type="button"
      :disabled="loading" @click="load(page.nextOffset)">加载更多时限</button>
  </section>
</template>

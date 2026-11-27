<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, shallowRef, watch } from 'vue'
import { ArrowLeft, Search } from '@lucide/vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { archiveRecordPath, archiveSystemStatuses } from '../lib/archives'
import {
  archiveSupplierLocation, supplierArchivePath, useArchiveInventory, type SupplierMaster, type SupplierProfile,
} from '../lib/archiveInventory'
import { formatTime } from '../lib/domain'
import ArchiveInventoryPager from './ArchiveInventoryPager.vue'
import AsyncState from './AsyncState.vue'
import DirectoryPicker from './DirectoryPicker.vue'

const props = defineProps<{ selected: string }>()
const path = computed(() => supplierArchivePath)
const keyword = ref('')
const status = ref('')
const filters = shallowRef({ q: '', active: '' })
const { page, cursors, loading, saving, failure, mutationFailure, load, save } =
  useArchiveInventory<SupplierMaster>(path, filters)
const editing = shallowRef<SupplierMaster | null>()
const form = reactive({
  name: '', serviceType: '', unitId: '', startedOn: '', contactName: '', contactPhone: '', source: '', active: true,
})
const profile = shallowRef<SupplierProfile>()
const profileCursors = ref([''])
const profileLoading = ref(false)
const profileError = ref('')
let profileRequest: AbortController | undefined

/**
 * 用途 应用供应商名称、服务类型和合作状态查询条件。
 * 参数 无，使用当前工具栏字段。
 * 返回值 无。
 */
function search() {
  filters.value = { q: keyword.value.trim(), active: status.value }
}

/**
 * 用途 打开登记或填充现有供应商资料和编辑版本。
 * 参数 row 为要编辑的主档，留空时开启登记。
 * 返回值 无。
 */
function edit(row?: SupplierMaster) {
  mutationFailure.value = ''
  editing.value = row ?? null
  Object.assign(form, {
    name: row?.name ?? '', serviceType: row?.serviceType ?? '', unitId: row?.unitId ?? '',
    startedOn: row?.startedOn ?? '', contactName: row?.contactName ?? '', contactPhone: row?.contactPhone ?? '',
    source: row?.source ?? '', active: row?.active ?? true,
  })
}

/**
 * 用途 读取所选主档与分页系统关联，切换档案时取消旧请求。
 * 参数 positions 为当前关联分页的游标路径。
 * 返回值 详情读取任务。
 */
async function loadProfile(positions = profileCursors.value) {
  profileRequest?.abort()
  const request = new AbortController()
  profileRequest = request
  profileError.value = ''
  if (!props.selected) return
  profileLoading.value = true
  try {
    const url = supplierArchivePath + '/' + encodeURIComponent(props.selected)
    const result = await api<SupplierProfile>(url + params({ cursor: positions.at(-1) }), { signal: request.signal })
    if (profileRequest === request) {
      profile.value = result
      profileCursors.value = positions
    }
  } catch (error) {
    if (profileRequest === request && !isAbort(error)) profileError.value = errorText(error)
  } finally {
    if (profileRequest === request) profileLoading.value = false
  }
}

/**
 * 用途 保存当前供应商资料或状态并刷新所见详情。
 * 参数 无，使用表单字段及所选记录的版本。
 * 返回值 已确认的写入任务。
 */
async function submit() {
  const result = await save({ ...form }, editing.value ?? undefined)
  if (!result) return
  editing.value = undefined
  if (props.selected === result.id) await loadProfile()
}

watch(() => props.selected, () => {
  editing.value = undefined
  profile.value = undefined
  profileLoading.value = false
  profileCursors.value = ['']
  void loadProfile()
}, { immediate: true })
onScopeDispose(() => { profileRequest?.abort(); profileRequest = undefined })
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="archive-suppliers">
    <header class="panel-heading">
      <h2 id="archive-suppliers">{{ selected ? '供应商详情' : '供应商主档' }}</h2>
      <div class="archive-inventory-actions">
        <template v-if="selected">
          <RouterLink :to="archiveSupplierLocation()" class="button neutral small">
            <ArrowLeft :size="15" aria-hidden="true" />返回供应商主档
          </RouterLink>
          <button v-if="profile && editing === undefined" class="button primary small" type="button"
            :disabled="saving || profileLoading" @click="edit(profile.supplier)">编辑主档</button>
        </template>
        <button v-else-if="editing === undefined" class="button primary small" type="button"
          :disabled="saving" @click="edit()">登记供应商</button>
      </div>
    </header>

    <form v-if="editing !== undefined" class="archive-inventory-form" @submit.prevent="submit">
      <h3>{{ editing ? '编辑供应商资料' : '新增供应商主档' }}</h3>
      <fieldset :disabled="saving" class="archive-inventory-fields">
        <label class="field"><span>供应商名称</span><input v-model="form.name" maxlength="200" required></label>
        <label class="field"><span>服务类型</span><input v-model="form.serviceType" maxlength="120" required placeholder="例如安全运维"></label>
        <DirectoryPicker :key="editing?.id ?? 'new'" v-model="form.unitId" kind="unit" label="服务单位"
          :initial-label="editing?.unitName" use-id />
        <label class="field"><span>合作起始</span><input v-model="form.startedOn" type="date" max="9999-12-31"></label>
        <label class="field">
          <span>合作状态</span>
          <select v-model="form.active"><option :value="true">合作中</option><option :value="false">已终止</option></select>
        </label>
        <label class="field"><span>联系人</span><input v-model="form.contactName" maxlength="120"></label>
        <label class="field"><span>联系电话</span><input v-model="form.contactPhone" type="tel" maxlength="80"></label>
        <label class="field archive-inventory-wide">
          <span>来源或凭据编号</span>
          <input v-model="form.source" maxlength="300" required placeholder="例如供应商资质档案编号或合作协议编号">
        </label>
      </fieldset>
      <p v-if="mutationFailure" class="form-message error-message" role="alert">{{ mutationFailure }}</p>
      <div class="archive-inventory-actions">
        <button class="button primary small" type="submit" :disabled="saving">保存主档</button>
        <button class="button neutral small" type="button" :disabled="saving" @click="editing = undefined">取消</button>
      </div>
    </form>

    <template v-if="selected">
      <AsyncState :loading="profileLoading" :error="profileError" @retry="loadProfile()">
        <template v-if="profile">
          <dl class="facts supplier-facts">
            <div><dt>供应商名称</dt><dd>{{ profile.supplier.name }}</dd></div>
            <div><dt>服务类型</dt><dd>{{ profile.supplier.serviceType }}</dd></div>
            <div><dt>服务单位</dt><dd>{{ profile.supplier.unitName || '—' }}</dd></div>
            <div><dt>合作起始</dt><dd>{{ profile.supplier.startedOn || '—' }}</dd></div>
            <div><dt>合作状态</dt><dd>{{ profile.supplier.active ? '合作中' : '已终止' }}</dd></div>
            <div><dt>联系人</dt><dd>{{ profile.supplier.contactName || '—' }}</dd></div>
            <div><dt>联系电话</dt><dd>{{ profile.supplier.contactPhone || '—' }}</dd></div>
            <div><dt>来源或凭据编号</dt><dd>{{ profile.supplier.source }}</dd></div>
            <div><dt>更新时间</dt><dd>{{ formatTime(profile.supplier.updatedAt) }}</dd></div>
          </dl>
          <h3 class="supplier-links-title">关联备案系统</h3>
          <div v-if="profile.systems.items.length" class="table-wrap">
            <table class="data-table">
              <thead><tr><th scope="col">备案系统</th><th scope="col">所属单位</th><th scope="col">备案状态</th>
                <th scope="col">关联来源</th><th scope="col">关联状态</th></tr></thead>
              <tbody>
                <tr v-for="row in profile.systems.items" :key="row.id">
                  <td><RouterLink class="text-action" :to="archiveRecordPath('systems', row.filingId)">
                    {{ row.systemName }}
                  </RouterLink><br><small class="code">{{ row.filingNumber }}</small></td>
                  <td>{{ row.organizationName }}</td>
                  <td>{{ archiveSystemStatuses[row.filingStatus] }}</td>
                  <td class="archive-inventory-identity">{{ row.source }}
                    <div v-if="row.description"><small>{{ row.description }}</small></div>
                  </td>
                  <td>{{ row.active ? '在用' : '停用' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-else class="empty-state muted">暂无关联备案系统。可在系统详情的资产与安全档案中登记关联。</p>
          <ArchiveInventoryPager :previous="profileCursors.length > 1" :next="!!profile.systems.nextCursor"
            :loading="profileLoading || saving" @previous="loadProfile(profileCursors.slice(0, -1))"
            @next="profile.systems.nextCursor && loadProfile([...profileCursors, profile.systems.nextCursor])" />
        </template>
      </AsyncState>
    </template>

    <template v-else>
      <form class="archive-inventory-form supplier-filters" @submit.prevent="search">
        <div class="v2-toolbar-row">
          <label class="field filter-keyword">
            <span>关键词</span><input v-model="keyword" type="search" maxlength="120" placeholder="供应商名称 / 服务类型">
          </label>
          <label class="field">
            <span>合作状态</span>
            <select v-model="status"><option value="">全部状态</option><option value="true">合作中</option>
              <option value="false">已终止</option></select>
          </label>
          <button class="button primary" :disabled="saving" type="submit"><Search :size="16" aria-hidden="true" />查询</button>
          <button class="button neutral" :disabled="saving" type="button" @click="keyword = ''; status = ''; search()">重置</button>
        </div>
      </form>
      <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
        empty-text="暂无符合条件的供应商主档。" @retry="load()">
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th scope="col">供应商名称</th><th scope="col">服务类型</th><th scope="col">服务单位</th>
              <th scope="col">合作起始</th><th scope="col">状态</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="row in page.items" :key="row.id">
                <td class="archive-inventory-identity"><strong>{{ row.name }}</strong></td>
                <td>{{ row.serviceType }}</td><td>{{ row.unitName || '—' }}</td><td>{{ row.startedOn || '—' }}</td>
                <td><span class="status-badge" :class="row.active ? 'status-approved' : 'status-draft'">
                  {{ row.active ? '合作中' : '已终止' }}
                </span></td>
                <td><div class="archive-inventory-actions">
                  <RouterLink class="text-action" :to="archiveSupplierLocation(row.id)">详情</RouterLink>
                  <button class="text-action" type="button" :disabled="saving" @click="edit(row)">编辑</button>
                </div></td>
              </tr>
            </tbody>
          </table>
        </div>
      </AsyncState>
      <ArchiveInventoryPager :previous="cursors.length > 1" :next="!!page.nextCursor" :loading="loading || saving"
        @previous="load(cursors.slice(0, -1))" @next="page.nextCursor && load([...cursors, page.nextCursor])" />
    </template>
  </section>
</template>

<style src="./archiveInventory.css"></style>
<style scoped>
.supplier-filters { border-top: 1px solid var(--line, #dce4ee); }
.supplier-facts dd { overflow-wrap: anywhere; }
.supplier-links-title { font-size: 1rem; margin: 16px; }
</style>

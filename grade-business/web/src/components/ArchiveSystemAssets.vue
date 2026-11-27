<script setup lang="ts">
import { computed, defineAsyncComponent, reactive, ref, shallowRef, watch } from 'vue'
import ArchiveInventoryPager from './ArchiveInventoryPager.vue'
import ArchiveSupplierPicker from './ArchiveSupplierPicker.vue'
import AsyncState from './AsyncState.vue'
import { formatTime } from '../lib/domain'
import {
  archiveSupplierLocation, systemArchivePath, systemAssetKinds, useArchiveInventory,
  type SystemAsset, type SystemAssetKind,
} from '../lib/archiveInventory'

const ArchiveSystemTopology = defineAsyncComponent(() => import('./ArchiveSystemTopology.vue'))
const props = defineProps<{ filingId: string; editable: boolean }>()
const path = computed(() => systemArchivePath + '/' + encodeURIComponent(props.filingId) + '/assets')
const { page, cursors, loading, saving, failure, mutationFailure, load, save } = useArchiveInventory<SystemAsset>(path)
const editing = shallowRef<SystemAsset | null>()
const topologyRevision = ref(0)
const form = reactive({
  kind: 'ip' as SystemAssetKind, identifier: '', relatedEndpoint: '', label: '', source: '', description: '',
  occurredOn: '', supplierId: '', active: true,
})
const identifierLabel = computed(() => ({
  ip: 'IP 地址', domain: '域名', topology: '起点', supplier: '供应商标识',
  security_event: '事件编号', cloud: '云资产标识', big_data: '大数据资产标识',
})[form.kind])

/**
 * 用途 初始化新的资产录入或填入既有登记数据。
 * 参数 row 为待编辑条目，缺省时开启新登记。
 * 返回值 无。
 */
function edit(row?: SystemAsset) {
  mutationFailure.value = ''
  editing.value = row ?? null
  Object.assign(form, row ? {
    kind: row.kind, identifier: row.identifier, relatedEndpoint: row.relatedEndpoint,
    label: row.label, source: row.source, description: row.description, occurredOn: row.occurredOn ?? '',
    supplierId: row.supplierId ?? '', active: row.active,
  } : { kind: 'ip', identifier: '', relatedEndpoint: '', label: '', source: '', description: '',
    occurredOn: '', supplierId: '', active: true })
}

/**
 * 用途 保存当前资产登记的证据字段及启停状态。
 * 参数 无，使用当前表单与选定记录版本。
 * 返回值 修改或新建请求任务。
 */
async function submit() {
  const row = editing.value
  const metadata = {
    label: form.label, source: form.source, description: form.description,
    occurredOn: form.kind === 'security_event' ? form.occurredOn : '',
    supplierId: form.kind === 'supplier' ? form.supplierId || undefined : undefined,
  }
  const result = row
    ? await save({ ...metadata, active: form.active }, row)
    : await save({ ...metadata, kind: form.kind, identifier: metadata.supplierId ? undefined : form.identifier,
      relatedEndpoint: form.kind === 'topology' ? form.relatedEndpoint : '' })
  if (result) {
    editing.value = undefined
    if (result.kind === 'topology') topologyRevision.value += 1
  }
}

watch(path, () => { editing.value = undefined })
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="archive-system-assets">
    <header class="panel-heading">
      <h2 id="archive-system-assets">资产与安全档案</h2>
      <button v-if="editable && editing === undefined" class="button primary small" type="button" @click="edit()">登记资产</button>
    </header>
    <form v-if="editing !== undefined && editable" class="archive-inventory-form" @submit.prevent="submit">
      <h3>{{ editing ? '编辑登记' : '新增资产登记' }}</h3>
      <fieldset :disabled="saving" class="archive-inventory-fields">
        <label class="field">
          <span>登记类型</span>
          <select v-model="form.kind" :disabled="!!editing" @change="form.supplierId = ''">
            <option v-for="(label, kind) in systemAssetKinds" :key="kind" :value="kind">{{ label }}</option>
          </select>
        </label>
        <ArchiveSupplierPicker v-if="form.kind === 'supplier' && !editing?.supplierId" v-model="form.supplierId" />
        <label v-else-if="editing?.supplierId" class="field">
          <span>已关联供应商</span><input :value="editing.supplierName || editing.supplierId" readonly>
        </label>
        <label v-if="editing || form.kind !== 'supplier' || !form.supplierId" class="field">
          <span>{{ identifierLabel }}</span><input v-model="form.identifier" required maxlength="253" :readonly="!!editing">
        </label>
        <label v-if="form.kind === 'topology'" class="field">
          <span>终点</span><input v-model="form.relatedEndpoint" required maxlength="253" :readonly="!!editing">
        </label>
        <label v-if="form.kind === 'security_event'" class="field">
          <span>发生日期</span><input v-model="form.occurredOn" required type="date">
        </label>
        <label class="field"><span>显示名称</span><input v-model="form.label" maxlength="200"></label>
        <label class="field">
          <span>来源或凭据编号</span><input v-model="form.source" required maxlength="300" placeholder="例如资产盘点清册及记录号">
        </label>
        <label v-if="editing" class="field">
          <span>记录状态</span>
          <select v-model="form.active"><option :value="true">在用</option><option :value="false">停用</option></select>
        </label>
        <label class="field archive-inventory-wide"><span>说明</span><textarea v-model="form.description" maxlength="2000" rows="2"></textarea></label>
      </fieldset>
      <p v-if="mutationFailure" class="form-message error-message" role="alert">{{ mutationFailure }}</p>
      <div class="archive-inventory-actions">
        <button class="button primary small" type="submit" :disabled="saving">保存登记</button>
        <button class="button neutral small" type="button" :disabled="saving" @click="editing = undefined">取消</button>
      </div>
    </form>
    <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
      empty-text="暂无资产、供应商或安全事件登记。" @retry="load()">
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>类别</th><th>登记标识</th><th>来源</th><th>状态</th><th>更新时间</th><th v-if="editable">操作</th></tr></thead>
          <tbody>
            <tr v-for="row in page.items" :key="row.id">
              <td>{{ systemAssetKinds[row.kind] }}</td>
              <td class="archive-inventory-identity">
                <RouterLink v-if="row.supplierId" :to="archiveSupplierLocation(row.supplierId)" class="text-action">
                  {{ row.supplierName || row.identifier }}
                </RouterLink>
                <strong v-else>{{ row.identifier }}<template v-if="row.relatedEndpoint"> → {{ row.relatedEndpoint }}</template></strong>
                <div v-if="row.supplierId && row.identifier !== row.supplierId"><small>原登记标识 {{ row.identifier }}</small></div>
                <br v-if="row.label || row.occurredOn"><small v-if="row.label">{{ row.label }}</small>
                <small v-if="row.occurredOn"> {{ row.occurredOn }}</small>
                <div v-if="row.description"><small>{{ row.description }}</small></div>
              </td>
              <td class="archive-inventory-identity">{{ row.source }}</td>
              <td>{{ row.active ? '在用' : '停用' }}</td>
              <td>{{ formatTime(row.updatedAt) }}</td>
              <td v-if="editable"><button class="text-action" type="button" :disabled="saving" @click="edit(row)">编辑</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <ArchiveInventoryPager :previous="cursors.length > 1" :next="!!page.nextCursor" :loading="loading || saving"
      @previous="load(cursors.slice(0, -1))" @next="page.nextCursor && load([...cursors, page.nextCursor])" />
  </section>
  <ArchiveSystemTopology :path="path" :revision="topologyRevision" />
</template>

<style src="./archiveInventory.css"></style>

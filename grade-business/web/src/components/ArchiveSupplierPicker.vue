<script setup lang="ts">
import { computed, ref, shallowRef } from 'vue'
import { supplierArchivePath, useArchiveInventory, type SupplierMaster } from '../lib/archiveInventory'

const selection = defineModel<string>({ required: true })
const path = computed(() => supplierArchivePath)
const keyword = ref('')
const applied = ref('')
const selected = shallowRef<SupplierMaster>()
const filters = computed(() => ({ active: 'true', q: applied.value, limit: 30 }))
const { page, loading, failure, load } = useArchiveInventory<SupplierMaster>(path, filters)
const choices = computed(() => selected.value && !page.value.items.some(item => item.id === selected.value?.id)
  ? [selected.value, ...page.value.items] : page.value.items)

/**
 * 用途 记录已选供应商主档，使后续关键字搜索保留当前选项。
 * 参数 event 为原生下拉选择事件。
 * 返回值 无。
 */
function choose(event: Event) {
  selection.value = (event.currentTarget as HTMLSelectElement).value
  selected.value = choices.value.find(item => item.id === selection.value)
}

/**
 * 用途 按名称或服务类型检索可建立关联的合作中供应商。
 * 参数 无，使用当前检索文字。
 * 返回值 无。
 */
function search() {
  const query = keyword.value.trim()
  if (applied.value === query) {
    void load([''])
    return
  }
  applied.value = query
}
</script>

<template>
  <div class="field archive-supplier-picker">
    <label><span>供应商主档</span>
      <select :value="selection" :disabled="loading" @change="choose">
        <option value="">使用自由文本登记</option>
        <option v-for="row in choices" :key="row.id" :value="row.id">{{ row.name }} · {{ row.serviceType }}</option>
      </select>
    </label>
    <div class="archive-supplier-search">
      <input v-model="keyword" type="search" maxlength="120" aria-label="检索供应商主档"
        placeholder="供应商名称 / 服务类型" @keydown.enter.prevent="search">
      <button type="button" class="button neutral small" :disabled="loading" @click="search">检索</button>
    </div>
    <small v-if="failure" class="form-message error-message" role="alert">{{ failure }}</small>
    <small v-else-if="!loading && !page.items.length" class="muted">暂无匹配的合作中供应商。</small>
    <small v-else-if="!loading && page.nextCursor" class="muted">可输入名称缩小候选范围。</small>
  </div>
</template>

<style scoped>
.archive-supplier-picker > label { display: grid; gap: 5px; }
.archive-supplier-search { display: flex; gap: 6px; }
.archive-supplier-search input { min-width: 0; flex: 1; }
</style>

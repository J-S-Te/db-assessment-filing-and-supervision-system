<script setup lang="ts">
import { computed } from 'vue'
import { ArrowLeft, ChevronRight } from '@lucide/vue'
import { useRoute } from 'vue-router'
import ArchiveList from '../components/ArchiveList.vue'
import ArchivePageLinks from '../components/ArchivePageLinks.vue'
import ArchiveRelatedRecords from '../components/ArchiveRelatedRecords.vue'
import ArchiveSuppliers from '../components/ArchiveSuppliers.vue'
import AsyncState from '../components/AsyncState.vue'
import {
  archiveListQuery, archivePaths, archiveRecordPath, archiveSystemStatuses, useArchiveProfile,
  type ArchiveUnitProfile,
} from '../lib/archives'
import { archiveSupplierLocation, systemAssetKinds } from '../lib/archiveInventory'
import { formatTime, levels } from '../lib/domain'

const route = useRoute()
const key = computed(() => typeof route.params.organization === 'string' ? route.params.organization : '')
const showSuppliers = computed(() => route.query.tab === 'suppliers')
const supplier = computed(() => typeof route.query.supplier === 'string' ? route.query.supplier : '')
const { profile, failure, loading, load } = useArchiveProfile<ArchiveUnitProfile>('units', key)
</script>

<template>
  <template v-if="!key">
    <nav class="archive-unit-nav" aria-label="单位档案分区">
      <RouterLink class="button" :class="showSuppliers ? 'neutral' : 'primary'" :to="archivePaths.units"
        :aria-current="showSuppliers ? undefined : 'page'">备案单位</RouterLink>
      <RouterLink class="button" :class="showSuppliers ? 'primary' : 'neutral'" :to="archiveSupplierLocation()"
        :aria-current="showSuppliers ? 'page' : undefined">供应商主档</RouterLink>
      <RouterLink class="button neutral" to="/archives/directory">返回单位名录</RouterLink>
    </nav>
    <ArchiveSuppliers v-if="showSuppliers" :selected="supplier" />
    <ArchiveList v-else kind="units" />
  </template>
  <section v-else aria-label="单位全息画像">
    <RouterLink class="back-link" :to="{ path: archivePaths.units, query: archiveListQuery(route.query, 'units') }">
      <ArrowLeft :size="16" aria-hidden="true" />返回单位档案
    </RouterLink>
    <AsyncState :loading="loading" :error="failure" @retry="load">
      <template v-if="profile">
        <header class="page-header detail-header">
          <div><div class="detail-title"><h1>{{ profile.unit.directoryName || profile.unit.organizationName }}</h1></div></div>
          <button class="button neutral small" type="button" @click="load">⟳ 刷新</button>
        </header>
        <div class="detail-main">
          <section class="panel detail-panel" aria-labelledby="unit-archive-summary">
            <header class="panel-heading"><h2 id="unit-archive-summary">单位概况</h2></header>
            <dl class="facts">
              <div v-if="profile.unit.creditCode"><dt>统一社会信用代码</dt><dd>{{ profile.unit.creditCode }}</dd></div>
              <div v-if="profile.unit.classification"><dt>单位类型</dt><dd>{{ profile.unit.classification }}</dd></div>
              <div v-if="profile.unit.district"><dt>所属区县</dt><dd>{{ profile.unit.district }}</dd></div>
              <div><dt>备案系统</dt><dd>{{ profile.unit.systemCount }}</dd></div>
              <div><dt>有效系统</dt><dd>{{ profile.unit.activeSystemCount }}</dd></div>
              <div><dt>已注销系统</dt><dd>{{ profile.unit.systemCount - profile.unit.activeSystemCount }}</dd></div>
              <div><dt>最近测评提交</dt><dd>{{ formatTime(profile.unit.latestReportAt) }}</dd></div>
              <div><dt>未销项隐患</dt><dd>{{ profile.unit.openHazardCount }}</dd></div>
              <div><dt>已销项隐患</dt><dd>{{ profile.unit.closedHazardCount }}</dd></div>
            </dl>
          </section>
          <section class="panel detail-panel" aria-labelledby="unit-archive-systems">
            <header class="panel-heading"><h2 id="unit-archive-systems">所属系统</h2></header>
            <div v-if="profile.systems.items.length" class="table-wrap">
              <table class="data-table">
                <thead><tr><th>系统名称</th><th>备案编号</th><th>等保级别</th><th>备案状态</th><th>未销项隐患</th></tr></thead>
                <tbody>
                  <tr v-for="row in profile.systems.items" :key="row.id">
                    <td><RouterLink class="text-action" :to="archiveRecordPath('systems', row.id)">
                      {{ row.systemName }}<ChevronRight :size="14" aria-hidden="true" />
                    </RouterLink></td>
                    <td class="code">{{ row.filingNumber }}</td>
                    <td>{{ levels[row.level] }}</td>
                    <td>{{ archiveSystemStatuses[row.status] }}</td>
                    <td>{{ row.openHazardCount }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p v-else class="empty-state muted">本页没有备案系统。</p>
            <ArchivePageLinks field="systemsCursor" :next-cursor="profile.systems.nextCursor" />
          </section>
          <section class="panel detail-panel" aria-labelledby="unit-safety-records">
            <header class="panel-heading"><h2 id="unit-safety-records">供应商与安全事件</h2></header>
            <div v-if="profile.assetRecords.items.length" class="table-wrap">
              <table class="data-table">
                <thead><tr><th>类别</th><th>登记编号 / 名称</th><th>关联备案系统</th><th>发生日期</th><th>来源</th><th>状态</th></tr></thead>
                <tbody>
                  <tr v-for="row in profile.assetRecords.items" :key="row.id">
                    <td>{{ systemAssetKinds[row.kind] }}</td>
                    <td>
                      <RouterLink v-if="row.supplierId" class="text-action" :to="archiveSupplierLocation(row.supplierId)">
                        {{ row.supplierName || row.identifier }}
                      </RouterLink>
                      <strong v-else>{{ row.identifier }}</strong>
                      <br v-if="row.label"><small v-if="row.label">{{ row.label }}</small>
                    </td>
                    <td><RouterLink class="text-action" :to="archiveRecordPath('systems', row.filingId)">{{ row.systemName }}</RouterLink></td>
                    <td>{{ row.occurredOn || '—' }}</td>
                    <td>{{ row.source }}</td><td>{{ row.active ? '在用' : '停用' }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <p v-else class="empty-state muted">本页没有供应商或安全事件登记。</p>
            <ArchivePageLinks field="assetRecordsCursor" :next-cursor="profile.assetRecords.nextCursor" />
          </section>
          <ArchiveRelatedRecords :profile="profile" show-system />
        </div>
      </template>
    </AsyncState>
  </section>
</template>

<style scoped>
.archive-unit-nav { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
</style>

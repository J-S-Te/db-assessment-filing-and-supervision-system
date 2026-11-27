<script setup lang="ts">
import { computed } from 'vue'
import { ArrowLeft, ChevronRight } from '@lucide/vue'
import { useRoute } from 'vue-router'
import ArchiveCoverageTargets from '../components/ArchiveCoverageTargets.vue'
import ArchiveSystemAssets from '../components/ArchiveSystemAssets.vue'
import ArchiveList from '../components/ArchiveList.vue'
import ArchiveRelatedRecords from '../components/ArchiveRelatedRecords.vue'
import AsyncState from '../components/AsyncState.vue'
import { archiveListQuery, archivePaths, archiveSystemStatuses, useArchiveProfile, type ArchiveSystemProfile } from '../lib/archives'
import { formatTime, levels } from '../lib/domain'

const route = useRoute()
const showCoverage = computed(() => route.query.tab === 'coverage')
const id = computed(() => typeof route.params.id === 'string' ? route.params.id : '')
const { profile, failure, loading, load } = useArchiveProfile<ArchiveSystemProfile>('systems', id)
</script>

<template>
  <template v-if="!id">
    <nav class="archives-section-nav" aria-label="系统档案分区">
      <RouterLink class="button" :class="!showCoverage ? 'primary' : 'neutral'" :to="archivePaths.systems"
        :aria-current="!showCoverage ? 'page' : undefined">系统档案</RouterLink>
      <RouterLink class="button" :class="showCoverage ? 'primary' : 'neutral'"
        :to="{ path: archivePaths.systems, query: { tab: 'coverage' } }"
        :aria-current="showCoverage ? 'page' : undefined">备案覆盖清册</RouterLink>
    </nav>
    <ArchiveCoverageTargets v-if="showCoverage" />
    <ArchiveList v-else kind="systems" />
  </template>
  <section v-else aria-label="系统档案详情">
    <RouterLink class="back-link" :to="{ path: archivePaths.systems, query: archiveListQuery(route.query, 'systems') }">
      <ArrowLeft :size="16" aria-hidden="true" />返回系统档案
    </RouterLink>
    <AsyncState :loading="loading" :error="failure" @retry="load">
      <template v-if="profile">
        <header class="page-header detail-header">
          <div>
            <div class="detail-title">
              <h1>{{ profile.system.systemName }}</h1>
              <span class="status-badge" :class="profile.system.status === 'active' ? 'status-approved' : 'status-draft'">
                {{ archiveSystemStatuses[profile.system.status] }}
              </span>
            </div>
            <p>{{ profile.system.organizationName }}</p>
          </div>
          <button class="button neutral small" type="button" @click="load">⟳ 刷新</button>
        </header>
        <div class="detail-main">
          <section class="panel detail-panel" aria-labelledby="system-archive-summary">
            <header class="panel-heading"><h2 id="system-archive-summary">系统概况</h2></header>
            <dl class="facts">
              <div><dt>关联备案</dt><dd><RouterLink class="text-action" :to="'/filings/' + encodeURIComponent(profile.system.id)">
                {{ profile.system.filingNumber }}<ChevronRight :size="14" aria-hidden="true" />
              </RouterLink></dd></div>
              <div><dt>等保级别</dt><dd>{{ levels[profile.system.level] }}</dd></div>
              <div><dt>备案测评机构</dt><dd>{{ profile.system.assessmentOrg }}</dd></div>
              <div><dt>最近测评提交</dt><dd>{{ formatTime(profile.system.latestReportAt) }}</dd></div>
              <div><dt>未销项隐患</dt><dd>{{ profile.system.openHazardCount }}</dd></div>
              <div><dt>已销项隐患</dt><dd>{{ profile.system.closedHazardCount }}</dd></div>
              <div v-if="profile.system.cancelledAt"><dt>注销时间</dt><dd>{{ formatTime(profile.system.cancelledAt) }}</dd></div>
            </dl>
          </section>
          <ArchiveSystemAssets :filing-id="profile.system.id" :editable="profile.system.status === 'active'" />
          <ArchiveRelatedRecords :profile="profile" />
        </div>
      </template>
    </AsyncState>
  </section>
</template>

<style scoped>
.archives-section-nav { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 12px; }
</style>

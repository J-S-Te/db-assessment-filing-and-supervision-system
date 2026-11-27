<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, shallowRef } from 'vue'
import ArchiveInventoryPager from './ArchiveInventoryPager.vue'
import AsyncState from './AsyncState.vue'
import DirectoryPicker from './DirectoryPicker.vue'
import { api, errorText, isAbort, params } from '../lib/api'
import { archiveRecordPath, type ArchiveSystem } from '../lib/archives'
import { coverageTargetPath, systemArchivePath, useArchiveInventory, type CoverageTarget } from '../lib/archiveInventory'
import type { Page } from '../lib/domain'

const path = computed(() => coverageTargetPath)
const { page, cursors, loading, saving, failure, mutationFailure, load, save } = useArchiveInventory<CoverageTarget>(path)
const editing = shallowRef<CoverageTarget | null>()
const form = reactive({ unitId: '', systemName: '', reference: '', source: '', filingId: '', active: true })
const filings = shallowRef<ArchiveSystem[]>([])
const finding = ref(false)
const filingMessage = ref('')
let filingRequest: AbortController | undefined

/**
 * 用途 在录入或变更系统名称时清理上次检索的备案候选。
 * 参数 无，使用当前组件的请求与候选状态。
 * 返回值 无。
 */
function clearFilingCandidates() {
  filingRequest?.abort()
  filingRequest = undefined
  finding.value = false
  filings.value = []
  filingMessage.value = ''
}

/**
 * 用途 开启登记或按当前版本编辑范围对象。
 * 参数 row 为已有对象，缺省时创建新对象。
 * 返回值 无。
 */
function edit(row?: CoverageTarget) {
  clearFilingCandidates()
  mutationFailure.value = ''
  editing.value = row ?? null
  Object.assign(form, row ? {
    unitId: row.unitId, systemName: row.systemName, reference: row.reference, source: row.source,
    filingId: row.filingId ?? '', active: row.active,
  } : { unitId: '', systemName: '', reference: '', source: '', filingId: '', active: true })
}

/**
 * 用途 检索当前登记系统可关联的有效备案候选。
 * 参数 无，使用表单中的系统名称。
 * 返回值 可取消的备案查询任务。
 */
async function findFilings() {
  filingRequest?.abort()
  const name = form.systemName.trim()
  if (!name) return
  const request = new AbortController()
  filingRequest = request
  finding.value = true
  filingMessage.value = ''
  try {
    const result = await api<Page<ArchiveSystem>>(systemArchivePath + params({ q: Array.from(name).slice(0, 120).join(''), status: 'active', limit: 100 }),
      { signal: request.signal })
    if (filingRequest !== request) return
    filings.value = result.items.filter(item => item.systemName === name)
    if (!filings.value.length) filingMessage.value = '暂无同名有效备案，可先登记应备案对象。'
  } catch (error) {
    if (filingRequest === request && !isAbort(error)) filingMessage.value = errorText(error)
  } finally {
    if (filingRequest === request) finding.value = false
  }
}

/**
 * 用途 保存范围对象或当前关联状态，再读取数据库响应。
 * 参数 无，使用表单状态及已有版本。
 * 返回值 实际写入任务。
 */
async function submit() {
  const row = editing.value
  const result = row
    ? await save({ filingId: form.filingId, active: form.active }, row)
    : await save({
      unitId: form.unitId, filingId: form.filingId, systemName: form.systemName,
      reference: form.reference, source: form.source,
    })
  if (result) editing.value = undefined
}

onScopeDispose(() => filingRequest?.abort())
</script>

<template>
  <section class="panel detail-panel" aria-labelledby="coverage-targets">
    <header class="panel-heading">
      <h2 id="coverage-targets">备案覆盖清册</h2>
      <button v-if="editing === undefined" class="button primary small" type="button" @click="edit()">登记应备案对象</button>
    </header>
    <p class="muted archive-inventory-caption">在册对象计入覆盖分母。与同一单位的有效备案关联后计入已覆盖。</p>
    <form v-if="editing !== undefined" class="archive-inventory-form" @submit.prevent="submit">
      <h3>{{ editing ? '调整备案关联与状态' : '新增应备案对象' }}</h3>
      <fieldset :disabled="saving" class="archive-inventory-fields">
        <DirectoryPicker v-if="!editing" v-model="form.unitId" kind="unit" label="所属单位" use-id required />
        <label v-else class="field"><span>所属单位</span><input :value="editing.unitName" readonly></label>
        <label class="field">
          <span>系统名称</span><input v-model="form.systemName" maxlength="200" required :readonly="!!editing"
            @input="form.filingId = ''; clearFilingCandidates()">
        </label>
        <label class="field">
          <span>范围对象编号</span><input v-model="form.reference" maxlength="120" required :readonly="!!editing">
        </label>
        <label class="field">
          <span>来源或凭据编号</span><input v-model="form.source" maxlength="300" required :readonly="!!editing">
        </label>
        <div class="field">
          <label>关联备案
            <select v-model="form.filingId" aria-label="关联备案">
              <option value="">暂未关联</option>
              <option v-if="editing?.filingId && !filings.some(item => item.id === editing?.filingId)" :value="editing.filingId">
                {{ editing.filingNumber || '当前关联' }}
              </option>
              <option v-for="item in filings" :key="item.id" :value="item.id">
                {{ item.filingNumber }} · {{ item.organizationName }}
              </option>
            </select>
          </label>
          <button class="button neutral small" type="button" :disabled="finding || !form.systemName.trim()"
            @click="findFilings">检索同名有效备案</button>
          <small v-if="filingMessage" role="status">{{ filingMessage }}</small>
        </div>
        <label v-if="editing" class="field">
          <span>范围状态</span>
          <select v-model="form.active"><option :value="true">在册</option><option :value="false">停用</option></select>
        </label>
      </fieldset>
      <p v-if="mutationFailure" class="form-message error-message" role="alert">{{ mutationFailure }}</p>
      <div class="archive-inventory-actions">
        <button class="button primary small" type="submit" :disabled="saving">保存范围</button>
        <button class="button neutral small" type="button" :disabled="saving" @click="editing = undefined">取消</button>
      </div>
    </form>
    <AsyncState :loading="loading" :error="failure" :empty="!page.items.length"
      empty-text="暂无具备来源凭据的覆盖范围登记。" @retry="load()">
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>系统</th><th>所属单位 / 区县</th><th>来源</th><th>关联备案</th><th>覆盖状态</th><th>操作</th></tr></thead>
          <tbody>
            <tr v-for="row in page.items" :key="row.id">
              <td class="archive-inventory-identity"><strong>{{ row.systemName }}</strong><br><small>{{ row.reference }}</small></td>
              <td>{{ row.unitName }}<br v-if="row.district"><small v-if="row.district">{{ row.district }}</small></td>
              <td class="archive-inventory-identity">{{ row.source }}</td>
              <td><RouterLink v-if="row.filingId && row.filingNumber" class="text-action"
                :to="archiveRecordPath('systems', row.filingId)">{{ row.filingNumber }}</RouterLink><span v-else>—</span></td>
              <td>{{ !row.active ? '已停用' : row.covered ? '已覆盖' : '未覆盖' }}</td>
              <td><button class="text-action" type="button" :disabled="saving" @click="edit(row)">编辑</button></td>
            </tr>
          </tbody>
        </table>
      </div>
    </AsyncState>
    <ArchiveInventoryPager :previous="cursors.length > 1" :next="!!page.nextCursor" :loading="loading || saving"
      @previous="load(cursors.slice(0, -1))" @next="page.nextCursor && load([...cursors, page.nextCursor])" />
  </section>
</template>

<style src="./archiveInventory.css"></style>

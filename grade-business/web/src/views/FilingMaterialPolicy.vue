<script setup lang="ts">
import { onMounted, shallowRef, ref } from 'vue'
import { api, errorText } from '../lib/api'
import { filingPolicyKinds, filingPolicyStages, type FilingMaterialPolicy, type FilingMaterialRule } from '../lib/filingPolicy'

const path = '/filing-material-policy'
const policy = shallowRef<FilingMaterialPolicy>()
const rules = ref<FilingMaterialRule[]>([])
const busy = ref(false)
const failure = ref('')
const notice = ref('')
const checkedLabels = ref(false)

/**
 * 用途 加载已保存的备案材料规则、启用状态和版本。
 * 参数 无。
 * 返回值 请求完成后填充编辑草稿，失败时显示原因。
 */
async function load() {
  if (busy.value) return
  busy.value = true
  notice.value = ''
  failure.value = ''
  try {
    const result = await api<FilingMaterialPolicy>(path)
    policy.value = result
    rules.value = result.rules.map(rule => ({ ...rule }))
    checkedLabels.value = false
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    busy.value = false
  }
}

/**
 * 用途 按已读版本提交规则，成功后同步服务端清理后的字段。
 * 参数 无，使用当前编辑草稿。
 * 返回值 当前保存任务。
 */
async function save() {
  if (busy.value || !policy.value) return
  notice.value = ''
  failure.value = ''
  checkedLabels.value = true
  if (rules.value.some(rule => !rule.label.trim())) return
  busy.value = true
  try {
    const result = await api<FilingMaterialPolicy>(path, {
      method: 'PUT', body: { expectedVersion: policy.value.version, rules: rules.value },
    })
    policy.value = result
    rules.value = result.rules.map(rule => ({ ...rule }))
    checkedLabels.value = false
    notice.value = '已保存材料规则。对应事项的提交和新发起的预审将按当前启用状态检查。'
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    busy.value = false
  }
}

onMounted(() => { void load() })
</script>

<template>
  <form class="panel policy-panel" aria-labelledby="material-policy-heading" novalidate @submit.prevent="save">
    <div class="policy-heading">
      <div>
        <h2 id="material-policy-heading">备案附加必传材料</h2>
        <p class="muted">已启用的规则参与材料预审和缺件校验。关闭后保留配置，保存后生效。
          填写文件名关键词时，附件须同时匹配材料类别和关键词。</p>
      </div>
      <button class="button neutral" type="button" :disabled="busy" @click="load">重新读取</button>
    </div>
    <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
    <p v-if="notice" class="form-message" role="status">{{ notice }}</p>
    <p v-if="!policy && busy" class="muted" role="status">正在读取材料规则…</p>
    <template v-if="policy">
      <p v-if="rules.length === 0" class="muted">当前只校验业务原有的必传材料。</p>
      <fieldset v-for="(rule, index) in rules" :key="index" :disabled="busy" class="policy-row">
        <legend>材料规则 {{ index + 1 }}</legend>
        <label class="field"><span>办理事项</span>
          <select v-model="rule.stage" @change="rule.kind = 'supporting'">
            <option v-for="(label, stage) in filingPolicyStages" :key="stage" :value="stage">{{ label }}</option>
          </select>
        </label>
        <label class="field"><span>材料类别</span>
          <select v-model="rule.kind">
            <option v-for="(label, kind) in filingPolicyKinds[rule.stage]" :key="kind" :value="kind">{{ label }}</option>
          </select>
        </label>
        <label class="field"><span>显示名称</span>
          <input v-model="rule.label" required maxlength="120"
            :aria-invalid="checkedLabels && !rule.label.trim() ? true : undefined"
            :aria-describedby="checkedLabels && !rule.label.trim() ? 'material-name-error-' + index : undefined">
          <small v-if="checkedLabels && !rule.label.trim()" :id="'material-name-error-' + index"
            class="error-message" role="alert">请填写显示名称。</small>
        </label>
        <label class="field"><span>文件名关键词（选填）</span>
          <input v-model="rule.filenameContains" maxlength="120">
        </label>
        <div class="policy-status">
          <span class="policy-status-label">规则状态</span>
          <button class="button neutral policy-switch" type="button" role="switch"
            :aria-checked="!rule.disabled" :aria-label="'启用规则：' + (rule.label || (index + 1))"
            @click="rule.disabled = !rule.disabled">
            <span class="policy-switch-track" aria-hidden="true"></span>
            {{ rule.disabled ? '已关闭' : '已启用' }}
          </button>
        </div>
        <button v-if="index >= policy.rules.length" class="button neutral" type="button"
          @click="rules.splice(index, 1)">取消新建</button>
      </fieldset>
      <div class="form-actions">
        <button class="button neutral" type="button" :disabled="busy || rules.length >= 40"
          @click="rules.push({ stage: 'initial', kind: 'supporting', label: '', filenameContains: '' })">新建规则</button>
        <button class="button primary" type="submit" :disabled="busy">保存材料规则</button>
      </div>
    </template>
  </form>
</template>

<style scoped>
.policy-panel { padding: 1.25rem; }
.policy-heading { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: .75rem; }
.policy-heading h2 { margin: 0 0 .4rem; }
.policy-row { display: flex; flex-wrap: wrap; align-items: end; gap: .7rem; margin: 1rem 0; padding: .75rem; }
.policy-row .field { flex: 1 1 11rem; }
.policy-status { display: grid; gap: .45rem; }
.policy-status-label { color: var(--muted); font-size: .875rem; }
.policy-switch { display: inline-flex; align-items: center; gap: .55rem; white-space: nowrap; }
.policy-switch-track { width: 2rem; height: 1.125rem; padding: .1875rem; border-radius: 1rem; background: #62768c; }
.policy-switch-track::after {
  content: '';
  display: block;
  width: .75rem;
  height: .75rem;
  border-radius: 50%;
  background: white;
}
.policy-switch[aria-checked='true'] .policy-switch-track { background: #185c9e; }
.policy-switch[aria-checked='true'] .policy-switch-track::after { transform: translateX(.875rem); }
</style>

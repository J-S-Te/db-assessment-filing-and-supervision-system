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

/**
 * 用途 加载当前正式启用的备案材料规则和版本。
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
  busy.value = true
  notice.value = ''
  failure.value = ''
  try {
    const result = await api<FilingMaterialPolicy>(path, {
      method: 'PUT', body: { expectedVersion: policy.value.version, rules: rules.value },
    })
    policy.value = result
    rules.value = result.rules.map(rule => ({ ...rule }))
    notice.value = '已保存规则，适用于之后提交的对应事项。'
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    busy.value = false
  }
}

onMounted(() => { void load() })
</script>

<template>
  <form class="panel policy-panel" aria-labelledby="material-policy-heading" @submit.prevent="save">
    <div class="policy-heading">
      <div>
        <h2 id="material-policy-heading">备案附加必传材料</h2>
        <p class="muted">按办理事项配置附加材料。填写文件名关键词后，仅使用类别及文件名同时匹配的材料。</p>
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
        <label class="field"><span>显示名称</span><input v-model="rule.label" required maxlength="120"></label>
        <label class="field"><span>文件名关键词（选填）</span>
          <input v-model="rule.filenameContains" maxlength="120">
        </label>
        <button class="button neutral" type="button" @click="rules.splice(index, 1)">移除</button>
      </fieldset>
      <div class="form-actions">
        <button class="button neutral" type="button" :disabled="busy || rules.length >= 40"
          @click="rules.push({ stage: 'initial', kind: 'supporting', label: '', filenameContains: '' })">添加规则</button>
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
</style>

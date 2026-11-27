<script setup lang="ts">
import { reactive, ref, shallowRef } from 'vue'
import DirectoryPicker from '../components/DirectoryPicker.vue'
import { api, errorText } from '../lib/api'

interface Receipt {
  id: string
  username: string
  temporaryPassword: string
}

const form = reactive({ institutionId: '', username: '', name: '' })
const pending = ref(false)
const failure = ref('')
const receipt = shallowRef<Receipt>()
const loginURL = new URL('/login', window.location.origin).href

/**
 * 用途 提交机构开户并在当前页面展示一次临时密码。
 * 参数 无，读取选定的有效机构及账号资料。
 * 返回值 开户和回执展示任务。
 */
async function openAccount() {
  if (pending.value || receipt.value) return
  pending.value = true
  failure.value = ''
  try {
    receipt.value = await api<Receipt>('/institution-accounts', { method: 'POST', body: form })
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    pending.value = false
  }
}

/**
 * 用途 结束凭据交接并清空当前页面的回执与开户表单。
 * 参数 无。
 * 返回值 无。
 */
function finishHandoff() {
  receipt.value = undefined
  form.institutionId = form.username = form.name = ''
}
</script>

<template>
  <section class="v2-list form-page" aria-labelledby="institution-account-heading">
    <header class="page-header">
      <div><h2 id="institution-account-heading">机构用户</h2><p>为资质有效的在册机构开通账号，每家机构限一个。</p></div>
    </header>
    <section v-if="receipt" class="panel form-panel" aria-labelledby="institution-account-receipt">
      <h3 id="institution-account-receipt" role="status">机构账号已开通</h3>
      <p class="muted handoff-notice">
        请保存以下交接信息，自行邮件联系机构负责人。此页仅展示一次临时密码，机构首次登录须修改密码。
      </p>
      <div class="form-grid">
        <label class="field wide"><span>登录地址</span><input :value="loginURL" readonly></label>
        <label class="field"><span>机构账号</span><input :value="receipt.username" readonly></label>
        <label class="field"><span>临时密码</span><input :value="receipt.temporaryPassword" readonly spellcheck="false"></label>
      </div>
      <div class="form-actions">
        <button class="button neutral" type="button" @click="finishHandoff">已保存，关闭回执</button>
      </div>
    </section>
    <form v-else class="panel form-panel" @submit.prevent="openAccount">
      <div class="form-grid">
        <DirectoryPicker v-model="form.institutionId" kind="institution" label="测评机构" use-id required
          class="wide" :disabled="pending" />
        <label class="field">
          <span>登录账号</span>
          <input v-model.trim="form.username" autocomplete="off" required minlength="3" maxlength="64"
            pattern="[a-zA-Z0-9][a-zA-Z0-9._\-]{2,63}" :disabled="pending">
          <small class="muted">3–64 个字符，支持字母、数字、点、下划线和短横线。</small>
        </label>
        <label class="field">
          <span>机构负责人姓名</span>
          <input v-model.trim="form.name" required maxlength="100" :disabled="pending">
        </label>
      </div>
      <p v-if="failure" class="form-message error-message" role="alert">{{ failure }}</p>
      <div class="form-actions">
        <button type="submit" class="button primary" :disabled="pending">{{ pending ? '开通中…' : '开通机构账号' }}</button>
      </div>
    </form>
  </section>
</template>

<style scoped>
.handoff-notice { margin: 10px 0 18px; }
</style>

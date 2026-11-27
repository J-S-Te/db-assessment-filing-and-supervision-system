<script setup lang="ts">
import { reactive, ref, shallowRef } from 'vue'
import { ShieldCheck } from '@lucide/vue'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import { api, errorText } from '../lib/api'
import { formatTime } from '../lib/domain'
import { applicantRegistrationPath, registrationStatuses } from '../lib/operatorRegistration'
import type { RegistrationReceipt, RegistrationResult } from '../lib/operatorRegistration'

const form = reactive({ unitName: '', creditCode: '', contactName: '', phone: '', username: '', password: '' })
const confirmation = ref('')
const mode = ref<'apply' | 'status'>('apply')
const lookupCode = ref('')
const result = shallowRef<RegistrationResult>()
const received = ref(false)
const pending = ref(false)
const failure = ref('')

/**
 * 用途 切换到注册或进度查询表单。
 * 参数 next 为要展示的办理入口。
 * 返回值 无。
 */
function selectMode(next: 'apply' | 'status') {
  mode.value = next
  failure.value = ''
}

/**
 * 用途 提交单位和账号资料，并展示只有申请人可获得的查询码。
 * 参数 无，读取当前注册表单。
 * 返回值 提交申请及更新页面的任务。
 */
async function submit() {
  if (pending.value) return
  failure.value = ''
  if (form.password !== confirmation.value) {
    failure.value = '两次输入的密码不一致。'
    return
  }
  pending.value = true
  try {
    const receipt = await api<RegistrationReceipt>(applicantRegistrationPath, { method: 'POST', body: form })
    lookupCode.value = receipt.lookupCode
    result.value = receipt
    received.value = true
    mode.value = 'status'
    form.password = confirmation.value = ''
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    pending.value = false
  }
}

/**
 * 用途 使用查询码读取最新注册进度。
 * 参数 无，查询凭据只在请求体中发送。
 * 返回值 状态读取与显示任务。
 */
async function lookup() {
  if (pending.value) return
  failure.value = ''
  pending.value = true
  result.value = undefined
  received.value = false
  try {
    result.value = await api<RegistrationResult>(applicantRegistrationPath + '/status',
      { method: 'POST', body: { lookupCode: lookupCode.value } })
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <main class="registration-page">
    <section class="registration-card" aria-labelledby="registration-title">
      <header>
        <RouterLink to="/login" class="back-link">返回登录</RouterLink>
        <h1 id="registration-title"><ShieldCheck :size="26" aria-hidden="true" />运营单位注册</h1>
      </header>
      <div class="registration-switch" role="group" aria-label="注册办理">
        <button type="button" class="button" :class="mode === 'apply' ? 'primary' : 'neutral'"
          :aria-pressed="mode === 'apply'" :disabled="pending" @click="selectMode('apply')">提交申请</button>
        <button type="button" class="button" :class="mode === 'status' ? 'primary' : 'neutral'"
          :aria-pressed="mode === 'status'" :disabled="pending" @click="selectMode('status')">查询进度</button>
      </div>

      <form v-if="mode === 'apply'" class="registration-form" @submit.prevent="submit">
        <p class="muted">每家运营单位可开通一个账号。请填写证照上的单位名称和统一社会信用代码。</p>
        <div class="form-grid">
          <label class="field"><span>单位名称</span>
            <input v-model.trim="form.unitName" autocomplete="organization" required maxlength="200">
          </label>
          <label class="field"><span>统一社会信用代码</span>
            <input v-model.trim="form.creditCode" autocapitalize="characters" spellcheck="false"
              required minlength="18" maxlength="18">
          </label>
          <label class="field"><span>管理员姓名</span>
            <input v-model.trim="form.contactName" autocomplete="name" required maxlength="100">
          </label>
          <label class="field"><span>联系人手机号</span>
            <input v-model.trim="form.phone" type="tel" autocomplete="tel-national"
              pattern="1[3-9][0-9]{9}" required maxlength="11">
          </label>
          <label class="field"><span>登录账号</span>
            <input v-model.trim="form.username" name="username" autocomplete="username"
              required minlength="3" maxlength="64" aria-describedby="registration-username-hint">
            <small id="registration-username-hint" class="muted">3–64 字符，支持字母、数字、点、下划线和短横线。</small>
          </label>
          <span class="registration-password-hint muted">审核通过后，使用这里设置的账号和密码登录。</span>
          <label class="field"><span>密码（至少 12 位）</span>
            <input v-model="form.password" name="password" type="password" autocomplete="new-password"
              required minlength="12" maxlength="256">
          </label>
          <label class="field"><span>确认密码</span>
            <input v-model="confirmation" type="password" autocomplete="new-password"
              required minlength="12" maxlength="256">
          </label>
        </div>
        <p v-if="failure" role="alert" class="form-message error-message">{{ failure }}</p>
        <button type="submit" class="button primary" :disabled="pending">{{ pending ? '提交中…' : '提交注册申请' }}</button>
      </form>

      <form v-else class="registration-form" @submit.prevent="lookup">
        <p v-if="received" class="form-message success-message" role="status">申请已受理。请复制并妥善保存下方查询码。</p>
        <label class="field"><span>申请查询码</span>
          <input v-model.trim="lookupCode" autocomplete="off" spellcheck="false"
            required minlength="43" maxlength="43" @input="result = undefined; received = false">
        </label>
        <p v-if="failure" role="alert" class="form-message error-message">{{ failure }}</p>
        <button type="submit" class="button primary" :disabled="pending">{{ pending ? '查询中…' : '查询申请状态' }}</button>
      </form>
      <section v-if="mode === 'status' && result" class="registration-result" aria-live="polite">
        <h2>{{ registrationStatuses[result.status] }}</h2>
        <p>提交时间 {{ formatTime(result.createdAt) }}</p>
        <p v-if="result.reviewedAt">审核时间 {{ formatTime(result.reviewedAt) }}</p>
        <RouterLink v-if="result.status === 'approved'" to="/login" class="back-link">使用注册账号登录</RouterLink>
        <template v-else-if="result.status === 'rejected'">
          <p>驳回原因 {{ result.reason }}</p>
          <button type="button" class="button neutral" @click="selectMode('apply')">修改后重新申请</button>
        </template>
      </section>
    </section>
    <CopyrightFooter />
  </main>
</template>

<style scoped>
.registration-page {
  display: grid;
  grid-template-rows: 1fr auto;
  min-height: 100dvh;
  padding: clamp(16px, 4vw, 52px) clamp(12px, 4vw, 40px) 10px;
  background: linear-gradient(#07102080, #081126a6), url('../assets/signin-space.webp') center / cover;
}
.registration-card {
  align-self: start;
  position: relative;
  width: min(100%, 740px);
  margin: auto;
  padding: clamp(20px, 4vw, 38px);
  background: var(--c-surface, #fff);
  border: 1px solid var(--line);
  border-radius: 20px;
  box-shadow: 0 24px 64px #05091852;
}
.registration-card h1 { display: flex; align-items: center; gap: 10px; margin: 14px 0 18px; }
.registration-card h1 svg, .back-link { color: var(--c-primary-700); }
.registration-switch { display: flex; gap: 8px; margin-bottom: 20px; }
.registration-form { display: grid; gap: 16px; }
.registration-form > button { justify-self: start; }
.registration-password-hint { align-self: center; }
.registration-result { margin-top: 18px; padding: 16px; border-radius: 10px; background: var(--c-surface-2); overflow-wrap: anywhere; }
.registration-result > * + * { margin-top: 8px; }
</style>

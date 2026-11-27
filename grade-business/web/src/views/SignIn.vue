<script setup lang="ts">
import { computed, ref } from 'vue'
import { ShieldCheck } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import { api, errorText } from '../lib/api'
import { canVisit, session, type Session } from '../lib/session'

const route = useRoute()
const router = useRouter()
const changing = computed(() => route.path === '/account/password')
const username = ref('')
const password = ref('')
const nextPassword = ref('')
const confirmPassword = ref('')
const pending = ref(false)
const failure = ref('')

/**
 * 用途 登录或完成首登改密，根据实际账号权限进入所属办理端。
 * 参数 无，读取当前表单和登录前的站内页面。
 * 返回值 登录、改密及随后导航的异步任务。
 */
async function submit() {
  if (pending.value) return
  failure.value = ''
  if (changing.value && nextPassword.value !== confirmPassword.value) {
    failure.value = '两次输入的新密码不一致。'
    return
  }
  pending.value = true
  try {
    if (changing.value) {
      await api('/auth/password', { method: 'POST', body: {
        currentPassword: password.value, newPassword: nextPassword.value,
      } })
      window.location.replace('/login?changed=1')
      return
    }
    const actor = await api<Session>('/auth/login', { method: 'POST', body: {
      username: username.value, password: password.value,
    } })
    session.value = actor
    const destination = route.query.redirect
    const resume = typeof destination === 'string' && /^\/(?![\/\\])/.test(destination) &&
      canVisit(actor, router.resolve(destination).path)
    await router.replace(actor.mustChangePassword ? '/account/password' : resume ? destination : actor.home)
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    password.value = nextPassword.value = confirmPassword.value = ''
    pending.value = false
  }
}

/**
 * 用途 退出改密界面并撤销当前登录会话。
 * 参数 无。
 * 返回值 注销任务及失败时的页面反馈。
 */
async function logout() {
  try {
    await api('/auth/logout', { method: 'POST' })
    window.location.replace('/login')
  } catch (error) {
    failure.value = errorText(error)
  }
}
</script>

<template>
  <main class="signin">
    <section class="signin-card panel" aria-labelledby="signin-title">
      <ShieldCheck :size="32" aria-hidden="true" />
      <h1 id="signin-title">{{ changing ? '修改账号密码' : '网络安全等级保护平台' }}</h1>
      <p v-if="changing && session?.mustChangePassword" class="muted">首次登录请修改初始密码。</p>
      <p v-else-if="route.query.changed === '1'" class="muted" role="status">密码已修改，请重新登录。</p>
      <form class="signin-form" @submit.prevent="submit">
        <label v-if="!changing" class="field">
          <span>账号</span>
          <input v-model.trim="username" autocomplete="username" maxlength="64" required>
        </label>
        <label class="field">
          <span>{{ changing ? '当前密码' : '密码' }}</span>
          <input v-model="password" type="password" autocomplete="current-password" maxlength="256" required>
        </label>
        <template v-if="changing">
          <label class="field">
            <span>新密码（至少 12 位）</span>
            <input v-model="nextPassword" type="password" autocomplete="new-password" minlength="12" maxlength="256" required>
          </label>
          <label class="field">
            <span>确认新密码</span>
            <input v-model="confirmPassword" type="password" autocomplete="new-password" minlength="12" maxlength="256" required>
          </label>
        </template>
        <p v-if="failure" role="alert" class="form-message error-message">{{ failure }}</p>
        <button class="button primary" :disabled="pending" type="submit">
          {{ pending ? '处理中' : changing ? '保存密码' : '登录' }}
        </button>
        <button v-if="changing" class="button neutral" type="button" :disabled="pending" @click="logout">退出登录</button>
      </form>
    </section>
    <CopyrightFooter />
  </main>
</template>

<style scoped>
.signin { min-height: 100dvh; display: grid; place-content: center; gap: 2rem; padding: 1.5rem; }
.signin-card { width: min(28rem, 90vw); padding: 2rem; }
.signin-card h1 { font-size: 1.4rem; margin: .75rem 0; }
.signin-form { display: grid; gap: .9rem; margin-top: 1.25rem; }
</style>

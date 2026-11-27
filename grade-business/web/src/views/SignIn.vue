<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import SignInClock from '../components/SignInClock.vue'
import SignInScene from '../components/SignInScene.vue'
import { api, errorText } from '../lib/api'
import { canVisit, session, type Session } from '../lib/session'
import '../features/signin3d/styles.css'

const route = useRoute()
const router = useRouter()
const changing = computed(() => route.path === '/account/password')
const username = ref('')
const password = ref('')
const nextPassword = ref('')
const confirmPassword = ref('')
const pending = ref(false)
const failure = ref('')
const loginAccepted = ref(false)
const rememberKey = 'grade-business.signin.username'
const remember = ref(false)
const showPassword = ref(false)
const usernameField = ref<HTMLInputElement>()
const passwordField = ref<HTMLInputElement>()
const notice = ref('')
const encrypted = window.location.protocol === 'https:'
let noticeTimer = 0

const statusText = computed(() => {
  if (loginAccepted.value) return '认证通过'
  if (failure.value) return '认证失败'
  if (pending.value) return '身份校验中'
  return '系统在线'
})

/**
 * 用途 更新当前浏览器的用户名预填偏好。
 * 参数 value 为用户允许保存的用户名，空值表示清除。
 * 返回值 无，存储不可用时仍允许登录。
 */
function saveUsername(value: string) {
  try {
    if (value) window.localStorage.setItem(rememberKey, value)
    else window.localStorage.removeItem(rememberKey)
  } catch {
    // 浏览器禁止本地存储时，登录依然使用当前填写的用户名。
  }
}

/**
 * 用途 显示短时操作提示，保留表单内容。
 * 参数 message 为用户刚触发的操作说明。
 * 返回值 无。
 */
function showNotice(message: string) {
  window.clearTimeout(noticeTimer)
  notice.value = message
  noticeTimer = window.setTimeout(() => { notice.value = '' }, 3200)
}

/**
 * 用途 切换登录口令的可见性并把焦点还给密码框。
 * 参数 无。
 * 返回值 无。
 */
function togglePassword() {
  showPassword.value = !showPassword.value
  passwordField.value?.focus()
}

onMounted(() => {
  if (changing.value) return
  try {
    const remembered = window.localStorage.getItem(rememberKey)
    if (remembered) {
      username.value = remembered
      remember.value = true
    }
  } catch {
    // 存储不可用时保持空表单。
  }
})
onUnmounted(() => window.clearTimeout(noticeTimer))

/**
 * 用途 展示服务端通过账号口令验证的反馈，再进入账号有权访问的页面。
 * 参数 actor 为登录接口确认的账号会话。
 * 返回值 完成反馈和页面导航的异步任务。
 */
async function enterAfterLogin(actor: Session) {
  const signInRoute = router.currentRoute.value
  session.value = actor
  password.value = ''
  loginAccepted.value = true
  // 短暂保留认证成功提示，再进入会话允许的页面。
  await new Promise<void>(resolve => window.setTimeout(resolve, 500))
  if (router.currentRoute.value !== signInRoute) return

  const destination = signInRoute.query.redirect
  const resume = typeof destination === 'string' && /^\/(?![\/\\])/.test(destination) &&
    canVisit(actor, router.resolve(destination).path)
  // 审核入口由同源 Go 路由提供，不能交给 SPA 的兜底页面处理。
  if (!actor.mustChangePassword && resume && ['/review', '/review/'].includes(router.resolve(destination).path)) {
    window.location.assign('/review/' + router.resolve(destination).hash)
    return
  }
  await router.replace(actor.mustChangePassword ? '/account/password' : resume ? destination : actor.home)
}

/**
 * 用途 登录或完成首登改密，根据实际账号权限进入所属办理端。
 * 参数 无，读取当前表单和登录前的站内页面。
 * 返回值 登录、改密及随后导航的异步任务。
 */
async function submit() {
  if (pending.value) return
  failure.value = ''
  if (!changing.value && (!username.value || !password.value)) {
    showNotice('请先填写用户名与密码。')
    const missing = username.value ? passwordField.value : usernameField.value
    missing?.focus()
    return
  }
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
    saveUsername(remember.value ? username.value : '')
    await enterAfterLogin(actor)
  } catch (error) {
    failure.value = errorText(error)
  } finally {
    password.value = nextPassword.value = confirmPassword.value = ''
    loginAccepted.value = false
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
  <main class="signin-3d" :class="{ 'signin-3d--changing': changing }">
    <SignInScene v-if="!changing" />

    <header class="signin-topbar-3d">
      <div class="topbar__brand">
        <span class="topbar__mark" aria-hidden="true">
          <svg viewBox="0 0 32 32" width="28" height="28">
            <path d="M16 3l11 4v9c0 7-4.7 11.6-11 13-6.3-1.4-11-6-11-13V7z"
              fill="none" stroke="currentColor" stroke-width="1.6" />
            <path d="M16 9v14M11 13h10M12.5 18h7" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
          </svg>
        </span>
        <span class="topbar__titles">
          <strong>网络安全等级保护监管平台</strong>
          <small>Cybersecurity Classified Protection Supervision Platform</small>
        </span>
      </div>
      <div class="topbar__meta">
        <span class="status" :class="{ 'status--busy': pending && !loginAccepted, 'status--error': failure }"
          role="status">
          <i class="status__dot" aria-hidden="true"></i><span>{{ statusText }}</span>
        </span>
        <SignInClock compact class="topbar__clock" />
      </div>
    </header>

    <div class="login-column">
      <section class="login-card" aria-labelledby="signin-title">
        <div class="login-card__head">
          <p class="login-card__eyebrow" lang="en">IDENTITY GATEWAY</p>
          <h1 id="signin-title" class="login-card__title">{{ changing ? '修改账号密码' : '身份认证' }}</h1>
          <p v-if="changing && session?.mustChangePassword" class="login-card__desc">
            首次登录请修改初始密码。
          </p>
          <p v-else-if="loginAccepted" class="login-card__desc" role="status">登录成功，正在进入…</p>
          <p v-else-if="route.query.changed === '1'" class="login-card__desc" role="status">
            密码已修改，请重新登录。
          </p>
          <p v-else-if="!changing" class="login-card__desc">
            请使用监管平台分配的账号登录，认证通过后进入等级保护监管业务系统。
          </p>
        </div>

        <form class="login-form" :novalidate="!changing" @submit.prevent="submit">
          <label v-if="!changing" class="signin-field">
            <span class="field__label">用户名</span>
            <span class="field__control">
              <svg class="field__icon" viewBox="0 0 24 24" aria-hidden="true">
                <circle cx="12" cy="8" r="3.4" /><path d="M4.8 20c1.2-3.6 4-5.4 7.2-5.4s6 1.8 7.2 5.4" />
              </svg>
              <input ref="usernameField" v-model.trim="username" name="username" type="text"
                maxlength="64" placeholder="请输入用户名" autocomplete="username" required>
            </span>
          </label>

          <div class="signin-field">
            <label for="signin-password" class="field__label">{{ changing ? '当前密码' : '密码' }}</label>
            <div class="field__control">
              <svg class="field__icon" viewBox="0 0 24 24" aria-hidden="true">
                <rect x="4.5" y="10.5" width="15" height="9.5" rx="2" /><path d="M8 10.5V7.8a4 4 0 018 0v2.7" />
              </svg>
              <input id="signin-password" ref="passwordField" v-model="password" name="password"
                :type="showPassword ? 'text' : 'password'" :placeholder="changing ? '请输入当前密码' : '请输入密码'"
                autocomplete="current-password" maxlength="256" required>
              <button type="button" class="field__toggle" aria-label="显示密码" :aria-pressed="showPassword"
                @click="togglePassword">{{ showPassword ? '隐藏' : '显示' }}</button>
            </div>
          </div>

          <template v-if="changing">
            <label class="signin-field">
              <span class="field__label">新密码（至少 12 位）</span>
              <span class="field__control">
                <input v-model="nextPassword" name="nextPassword" type="password" autocomplete="new-password"
                  minlength="12" maxlength="256" required>
              </span>
            </label>
            <label class="signin-field">
              <span class="field__label">确认新密码</span>
              <span class="field__control">
                <input v-model="confirmPassword" name="confirmPassword" type="password" autocomplete="new-password"
                  minlength="12" maxlength="256" required>
              </span>
            </label>
          </template>

          <div v-else class="login-form__row">
            <label class="checkbox">
              <input v-model="remember" type="checkbox" @change="!remember && saveUsername('')">
              <span>记住用户名</span>
            </label>
            <button type="button" class="link-btn"
              @click="showNotice('请联系本单位等保管理员重置密码。')">忘记密码？</button>
          </div>

          <p v-if="failure" class="login-error" role="alert">{{ failure }}</p>
          <button type="submit" class="btn-primary" :class="{ 'is-loading': pending && !loginAccepted }"
            :disabled="pending" :aria-busy="pending"
            :aria-label="loginAccepted ? '正在进入' : pending ? '认证中' : changing ? '保存密码' : '登录'">
            <span class="btn-primary__label">
              {{ loginAccepted ? '正在进入' : pending ? '认证中' : changing ? '保存密码' : '登 录' }}
            </span>
            <span class="btn-primary__spinner" aria-hidden="true"></span>
          </button>

          <button v-if="changing" type="button" class="link-btn" :disabled="pending" @click="logout">退出登录</button>
          <div v-else class="login-form__alt">
            <span class="login-form__divider"><i></i>其他认证方式<i></i></span>
            <div class="login-form__methods">
              <button type="button" class="method" aria-label="指纹登录"
                @click="showNotice('当前暂未开通生物识别，请使用账号密码登录。')">
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M12 3a6 6 0 016 6v3M12 3a6 6 0 00-6 6v3M9 12v3.5a3 3 0 006 0V12M8 21h8" />
                </svg>
                <span>指纹</span>
              </button>
              <button type="button" class="method" aria-label="短信验证码"
                @click="showNotice('当前暂未开通短信验证，请使用账号密码登录。')">
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <rect x="6" y="2.8" width="12" height="18.4" rx="2.4" /><path d="M10.5 18.6h3" />
                </svg>
                <span>短信</span>
              </button>
              <button type="button" class="method" aria-label="证书认证"
                @click="showNotice('当前暂未开通证书认证，请使用账号密码登录。')">
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <rect x="3.5" y="4.5" width="17" height="13" rx="2" />
                  <path d="M8 20.5h8M12 17.5v3M7.5 11l2.2 2.2 4.3-4.3" />
                </svg>
                <span>证书</span>
              </button>
            </div>
          </div>
        </form>

        <div class="login-card__foot">
          <span class="lock" aria-hidden="true">
            <svg viewBox="0 0 24 24" width="13" height="13">
              <rect x="5" y="10.5" width="14" height="9" rx="2" /><path d="M8 10.5V8a4 4 0 018 0v2.5" />
            </svg>
          </span>
          <span>会话采用 HttpOnly Cookie<span v-if="encrypted"> · HTTPS</span></span>
          <span v-if="!changing" aria-hidden="true">·</span>
          <RouterLink v-if="!changing" class="link-btn login-card__register" to="/operator-register">
            运营单位注册与进度查询
          </RouterLink>
        </div>
      </section>
    </div>

    <CopyrightFooter class="login-copyright" />
    <div v-if="notice" class="toast" role="status">{{ notice }}</div>
  </main>
</template>

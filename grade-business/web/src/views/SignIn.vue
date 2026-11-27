<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowRight, Fingerprint, LockKeyhole, ShieldCheck, UserRound } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import SignInSecurityCore from '../components/SignInSecurityCore.vue'
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
const loginAccepted = ref(false)
const biometricHelp = ref(false)
const loginState = computed(() => {
  if (loginAccepted.value) return 'success'
  if (failure.value) return 'error'
  return pending.value ? 'checking' : 'idle'
})

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
  // 跳转会卸载基座，短暂保留登录页，让用户看见绿灯与成功提示。
  await new Promise<void>(resolve => window.setTimeout(resolve, 500))
  if (router.currentRoute.value !== signInRoute) return

  const destination = signInRoute.query.redirect
  const resume = typeof destination === 'string' && /^\/(?![\/\\])/.test(destination) &&
    canVisit(actor, router.resolve(destination).path)
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
  <main class="signin">
    <div class="signin-layout" :class="{ 'signin-layout-single': changing }">
      <SignInSecurityCore v-if="!changing" :login-state="loginState" />
      <section class="signin-card" aria-labelledby="signin-title">
        <header class="signin-heading">
          <span class="signin-mark"><ShieldCheck :size="26" aria-hidden="true" /></span>
          <h1 id="signin-title">{{ changing ? '修改账号密码' : '网络安全等级保护平台' }}</h1>
          <p v-if="changing && session?.mustChangePassword">首次登录请修改初始密码。</p>
          <p v-else-if="loginAccepted" class="signin-message signin-success" role="status">
            登录成功，正在进入…
          </p>
          <p v-else-if="route.query.changed === '1'" class="signin-message" role="status">
            密码已修改，请重新登录。
          </p>
          <p v-else-if="!changing">请使用授权账号登录</p>
        </header>
        <form class="signin-form" @submit.prevent="submit">
          <label v-if="!changing" class="signin-field">
            <span>用户名</span>
            <span class="signin-control">
              <UserRound :size="17" aria-hidden="true" />
              <input v-model.trim="username" name="username" autocomplete="username"
                maxlength="64" placeholder="请输入用户名" required>
            </span>
          </label>
          <label class="signin-field">
            <span>{{ changing ? '当前密码' : '密码' }}</span>
            <span class="signin-control">
              <LockKeyhole :size="17" aria-hidden="true" />
              <input v-model="password" name="password" type="password" autocomplete="current-password"
                maxlength="256" :placeholder="changing ? '请输入当前密码' : '请输入密码'" required>
            </span>
          </label>
          <template v-if="changing">
            <label class="signin-field">
              <span>新密码（至少 12 位）</span>
              <input v-model="nextPassword" name="nextPassword" type="password" autocomplete="new-password"
                minlength="12" maxlength="256" required>
            </label>
            <label class="signin-field">
              <span>确认新密码</span>
              <input v-model="confirmPassword" name="confirmPassword" type="password"
                autocomplete="new-password" minlength="12" maxlength="256" required>
            </label>
          </template>
          <p v-if="failure" role="alert" class="signin-error">{{ failure }}</p>
          <button class="signin-button signin-primary" :disabled="pending" type="submit">
            {{ loginAccepted ? '正在进入' : pending ? '处理中' : changing ? '保存密码' : '登录' }}
            <ArrowRight v-if="!changing && !pending" :size="18" aria-hidden="true" />
          </button>
          <button v-if="changing" class="signin-button signin-secondary" type="button"
            :disabled="pending" @click="logout">
            退出登录
          </button>
        </form>
        <div v-if="!changing" class="signin-biometric">
          <button class="signin-button signin-secondary" type="button" aria-controls="biometric-help"
            :aria-expanded="biometricHelp" @click="biometricHelp = !biometricHelp">
            <Fingerprint :size="19" aria-hidden="true" /> 生物识别登录
          </button>
          <p id="biometric-help" :hidden="!biometricHelp" role="status">
            当前暂未开通生物识别，请使用账号密码登录。
          </p>
        </div>
      </section>
    </div>
    <CopyrightFooter />
  </main>
</template>

<style scoped>
.signin {
  --muted: #98acc5;
  --focus: #8edcff;
  display: grid;
  grid-template-rows: 1fr auto;
  min-height: 100dvh;
  padding: clamp(22px, 4vw, 68px) clamp(20px, 5vw, 84px) 10px;
  overflow-x: clip;
  color: #e9f4ff;
  color-scheme: dark;
  background-color: #070e1b;
  background-image:
    linear-gradient(180deg, #050a1238, #0714281a 54%, #03081266),
    url('../assets/signin-space.webp');
  background-position: center, center 58%;
  background-size: cover;
}

.signin-layout {
  width: min(100%, 1280px);
  margin: auto;
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(340px, 430px);
  align-items: center;
  gap: clamp(22px, 5vw, 82px);
}

.signin-layout-single {
  grid-template-columns: minmax(0, 430px);
  justify-content: center;
}

.signin-card {
  position: relative;
  z-index: 0;
  min-width: 0;
  padding: clamp(24px, 3.3vw, 43px);
  border: 1px solid #9fcfff60;
  border-radius: 20px;
  box-shadow: inset 0 1px 0 #c2e7ff1a, 0 28px 76px #0008, 0 0 30px #268be21a;
}

/* 分开卡片与输入框的模糊层，让玻璃表面保留不同深浅的光线。 */
.signin-card::before {
  position: absolute;
  z-index: -1;
  inset: 0;
  border-radius: inherit;
  background: linear-gradient(145deg, #1a3655bc, #09182bc7 58%, #0c2038bd);
  -webkit-backdrop-filter: blur(16px) saturate(125%);
  backdrop-filter: blur(16px) saturate(125%);
  pointer-events: none;
  content: "";
}

.signin-card::after {
  position: absolute;
  top: -1px;
  left: 8%;
  width: 62%;
  height: 1px;
  background: linear-gradient(90deg, transparent, #a7e4ffcc, transparent);
  pointer-events: none;
  content: "";
}

.signin-mark {
  display: grid;
  width: 51px;
  height: 51px;
  margin-bottom: 20px;
  place-items: center;
  border: 1px solid #67c7fa70;
  border-radius: 12px;
  background: linear-gradient(145deg, #22588b79, #172a4673);
  color: #8ddcff;
  box-shadow: inset 0 0 15px #4ea9e326, 0 0 26px #217de71c;
}

.signin-heading h1 {
  color: #f2f8ff;
  font-size: clamp(1.48rem, 2.2vw, 2.18rem);
  font-weight: 690;
  letter-spacing: .035em;
}

.signin-heading p { margin-top: 9px; color: var(--muted); }
.signin-heading .signin-message { color: #9de2ff; }
.signin-heading .signin-success { color: #8aefb1; }
.signin-form { display: grid; gap: 17px; margin-top: 30px; }
.signin-field { display: grid; gap: 7px; color: #d9e7fa; font-weight: 520; }
.signin-control { position: relative; display: block; }

.signin-control svg {
  position: absolute;
  z-index: 1;
  inset: 50% auto auto 14px;
  color: #acd4f3;
  pointer-events: none;
  transform: translateY(-50%);
}

.signin-field input {
  min-height: 47px;
  padding: 10px 13px;
  border-color: #a7d7ff59;
  border-radius: 9px;
  background: linear-gradient(125deg, #3b6b9396, #18324da8 55%, #3a648880);
  box-shadow: inset 0 1px 0 #c7e8ff2b, inset 0 -1px 0 #0a152b68, 0 5px 14px #01061138;
  -webkit-backdrop-filter: blur(9px) saturate(135%);
  backdrop-filter: blur(9px) saturate(135%);
  color: #f1f7ff;
}

.signin-control input { padding-inline-start: 43px; }
.signin-field input::placeholder { color: #c0d4e9; }

.signin-field input:focus {
  border-color: var(--focus);
  box-shadow: inset 0 1px 0 #eff9ff4d, 0 0 0 3px #45b9fa29, 0 6px 20px #04162660;
}

.signin-button {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 9px;
  width: 100%;
  min-height: 47px;
  padding: 9px 14px;
  border: 1px solid transparent;
  border-radius: 8px;
  font-weight: 630;
}

.signin-button:disabled { opacity: .65; }

.signin-primary {
  margin-top: 5px;
  border-color: #6fc6ffbb;
  background: linear-gradient(107deg, #1956c6, #267bd8 68%, #1564b4);
  color: #fff;
  box-shadow: 0 8px 26px #156ee235, inset 0 1px 0 #afe8ff35;
}

.signin-primary:hover:not(:disabled) { background: linear-gradient(107deg, #1e64db, #1771cc); }

.signin-secondary {
  border-color: #577aa374;
  background: #18305069;
  color: #c6ddf6;
}

.signin-secondary:hover:not(:disabled) { border-color: #88ceff; background: #203e63; }

.signin-biometric { margin-top: 19px; padding-top: 17px; border-top: 1px solid #7aa7d12b; }
.signin-biometric p { margin-top: 8px; color: #aec4de; font-size: .88rem; }

.signin-error {
  padding: 8px 11px;
  border: 1px solid #ff708650;
  border-radius: 8px;
  background: #7426373d;
  color: #ffc0ca;
}

@media (max-width: 760px) {
  .signin {
    padding-top: 14px;
    background-position: center, 59% center;
  }
  .signin-layout {
    grid-template-columns: minmax(0, 430px);
    justify-content: center;
    justify-items: center;
    gap: 14px;
    margin-block: auto 17px;
  }
  .signin-layout-single { margin-block: auto; }
  .signin-card { width: 100%; padding: 22px; }
  .signin-mark { width: 44px; height: 44px; margin-bottom: 14px; }
  .signin-form { gap: 13px; margin-top: 22px; }
  .signin-field input, .signin-button { min-height: 44px; }
}

@media (max-width: 360px) {
  :global(body:has(.signin)) { min-width: 0; }
}
</style>

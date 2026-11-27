<script setup lang="ts">
import { computed, ref } from 'vue'
import { ArrowRight, Fingerprint, LockKeyhole, ShieldCheck, UserRound } from '@lucide/vue'
import { useRoute, useRouter } from 'vue-router'
import CopyrightFooter from '../components/CopyrightFooter.vue'
import SignInClock from '../components/SignInClock.vue'
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
const passwordHelp = ref(false)
const loginState = computed(() => {
  if (loginAccepted.value) return 'success'
  if (failure.value) return 'error'
  return pending.value ? 'checking' : 'idle'
})

/**
 * 用途 汇总当前登录状态对应的顶部提示语。
 * 参数 无。
 * 返回值 顶部状态条展示的文案。
 */
const statusText = computed(() => {
  if (loginAccepted.value) return '认证通过'
  if (failure.value) return '认证失败'
  if (pending.value) return '身份校验中'
  return '系统在线'
})

/**
 * 用途 依据登录状态挑选顶部状态点的颜色。
 * 参数 无。
 * 返回值 状态点的配色类名。
 */
const statusTone = computed(() => {
  if (loginAccepted.value) return 'is-ok'
  if (failure.value) return 'is-error'
  if (pending.value) return 'is-busy'
  return 'is-ok'
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
    <header class="signin-topbar">
      <div class="signin-brand">
        <span class="signin-brand-logo">
          <ShieldCheck :size="22" aria-hidden="true" />
        </span>
        <span class="signin-brand-text">
          <strong>网络安全等级保护平台</strong>
          <span>CYBERSECURITY LEVEL PROTECTION PLATFORM</span>
        </span>
      </div>
      <div class="signin-status">
        <span class="signin-status-live">
          <i class="signin-status-dot" :class="statusTone"></i>{{ statusText }}
        </span>
        <span class="signin-status-sep" aria-hidden="true"></span>
        <SignInClock class="signin-status-clock" />
      </div>
    </header>

    <div class="signin-layout" :class="{ 'signin-layout-single': changing }">
      <SignInSecurityCore v-if="!changing" :login-state="loginState" />
      <section class="signin-card" aria-labelledby="signin-title">
        <i class="signin-corner is-tl" aria-hidden="true"></i>
        <i class="signin-corner is-tr" aria-hidden="true"></i>
        <i class="signin-corner is-bl" aria-hidden="true"></i>
        <i class="signin-corner is-br" aria-hidden="true"></i>
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
          <div v-if="!changing" class="signin-rowline">
            <span>同一账号 5 分钟内最多校验 12 次</span>
            <button type="button" class="signin-link" @click="passwordHelp = !passwordHelp">忘记密码？</button>
          </div>
          <p v-if="passwordHelp" class="signin-hint" role="status">
            平台不提供自助找回，请联系本单位等保管理员重置密码。
          </p>
          <button class="signin-button signin-primary" :disabled="pending" type="submit">
            <span v-if="pending" class="signin-spinner" aria-hidden="true"></span>
            {{ loginAccepted ? '正在进入' : pending ? '认证中…' : changing ? '保存密码' : '登录' }}
            <ArrowRight v-if="!changing && !pending" :size="18" aria-hidden="true" />
          </button>
        </form>
        <div v-if="!changing" class="signin-biometric">
          <p class="signin-divider">或使用其他方式登录</p>
          <button class="signin-button signin-secondary" type="button" aria-controls="biometric-help"
            :aria-expanded="biometricHelp" @click="biometricHelp = !biometricHelp">
            <Fingerprint :size="19" aria-hidden="true" /> 生物识别登录
          </button>
          <p id="biometric-help" :hidden="!biometricHelp" role="status">
            当前暂未开通生物识别，请使用账号密码登录。
          </p>
        </div>
        <RouterLink v-if="!changing" class="signin-registration" to="/operator-register">
          运营单位注册与进度查询 →
        </RouterLink>
        <p v-if="!changing" class="signin-note">
          登录凭据仅用于身份校验，通过后签发会话令牌；登录与越权访问均记入等保审计日志。
        </p>
      </section>
    </div>

    <footer class="signin-footer">
      <div class="signin-slogan">
        安全 · 合规 · 共治 · 守护数字未来
        <small>SECURITY · COMPLIANCE · CO-GOVERNANCE · SAFEGUARDING THE DIGITAL FUTURE</small>
      </div>
      <CopyrightFooter />
    </footer>
  </main>
</template>

<style scoped>
/* 隐藏登录页的可视滚动条，保留矮屏访问表单所需的滚动。 */
:global(html:has(.signin)) { scrollbar-width: none; }
:global(html:has(.signin)::-webkit-scrollbar) { display: none; }

.signin {
  --muted: #98acc5;
  --focus: #8edcff;
  position: relative;
  display: grid;
  grid-template-rows: auto 1fr auto;
  min-height: 100dvh;
  padding: 0 clamp(20px, 2.4vw, 46px) 10px;
  overflow: clip;
  color: #e9f4ff;
  color-scheme: dark;
  background-color: #070e1b;
  background-image:
    linear-gradient(180deg, #050a1238, #0714281a 54%, #03081266),
    url('../assets/signin-space.webp');
  background-position: center, center 58%;
  background-size: cover;
}

/* ---------- 顶部指挥条 ---------- */
.signin-topbar {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 82px;
}

.signin-topbar::after {
  position: absolute;
  inset: auto 0 0;
  height: 1px;
  background: linear-gradient(90deg, transparent, rgba(111, 198, 255, .45) 22%, rgba(111, 198, 255, .45) 78%, transparent);
  content: "";
}

.signin-brand {
  display: flex;
  align-items: center;
  gap: 14px;
}

.signin-brand-logo {
  display: grid;
  width: 40px;
  height: 40px;
  place-items: center;
  border: 1px solid #67c7fa70;
  border-radius: 11px;
  background: linear-gradient(145deg, #22588b79, #172a4673);
  color: #bfeaff;
  box-shadow: inset 0 0 15px #4ea9e326, 0 0 26px #217de71c;
}

.signin-brand-text strong {
  display: block;
  color: #f2f8ff;
  font-size: 21px;
  font-weight: 700;
  letter-spacing: .06em;
}

.signin-brand-text span {
  display: block;
  margin-top: 2px;
  color: #7ea3c6;
  font-size: 10px;
  letter-spacing: .24em;
}

.signin-status {
  display: flex;
  align-items: center;
  gap: 26px;
  color: #a9c4dd;
  font-size: 13px;
}

.signin-status-live {
  display: flex;
  align-items: center;
  gap: 9px;
}

.signin-status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #3ce08a;
  box-shadow: 0 0 10px #3ce08a;
}

.signin-status-dot.is-busy { background: #5fd8ff; box-shadow: 0 0 10px #5fd8ff; }
.signin-status-dot.is-error { background: #ff6b7f; box-shadow: 0 0 10px #ff6b7f; }

.signin-status-sep {
  width: 1px;
  height: 18px;
  background: rgba(120, 190, 240, .18);
}

.signin-status-clock {
  color: #8fb0cc;
  font-family: ui-monospace, Menlo, Consolas, monospace;
  font-size: 12.5px;
  letter-spacing: .06em;
}

/* ---------- 主体布局 ---------- */
.signin-layout {
  width: min(100%, 1828px);
  min-height: 0;
  /* 只做水平居中，垂直方向保持拉伸，场景才能占满可用高度。 */
  margin-inline: auto;
  padding-block: 12px;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 424px;
  grid-template-rows: auto;
  align-items: stretch;
  gap: 40px;
}

.signin-layout-single {
  grid-template-columns: minmax(0, 430px);
  justify-content: center;
}

.signin-card {
  position: relative;
  z-index: 10;
  align-self: center;
  min-width: 0;
  padding: clamp(24px, 3.3vw, 40px) clamp(24px, 3vw, 34px) clamp(20px, 2.6vw, 32px);
  border: 1px solid #9fcfff60;
  border-radius: 20px;
  box-shadow: inset 0 1px 0 #c2e7ff1a, 0 28px 76px #0008, 0 0 30px #268be21a;
  transform: perspective(1300px) rotateY(-5deg);
}

/* 固定渐变和高光提供玻璃层次，免去对背景的滤镜采样。 */
.signin-card::before {
  position: absolute;
  z-index: -1;
  inset: 0;
  border-radius: inherit;
  background: linear-gradient(145deg, #1a3655df, #09182be5 58%, #0c2038db);
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

/* 四角切角，呼应安防舱的定位框。 */
.signin-corner {
  position: absolute;
  z-index: 2;
  width: 24px;
  height: 24px;
  border: 1.5px solid rgba(126, 214, 255, .9);
}

.signin-corner.is-tl { top: -2px; left: -2px; border-right: 0; border-bottom: 0; }
.signin-corner.is-tr { top: -2px; right: -2px; border-left: 0; border-bottom: 0; }
.signin-corner.is-bl { bottom: -2px; left: -2px; border-right: 0; border-top: 0; }
.signin-corner.is-br { right: -2px; bottom: -2px; border-left: 0; border-top: 0; }

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
.signin-registration { display: block; margin-top: 16px; text-align: center; color: #9de2ff; }
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
  color: #f1f7ff;
}

.signin-control input { padding-inline-start: 43px; }
.signin-field input::placeholder { color: #c0d4e9; }

.signin-field input:focus {
  border-color: var(--focus);
  box-shadow: inset 0 1px 0 #eff9ff4d, 0 0 0 3px #45b9fa29, 0 6px 20px #04162660;
}

.signin-rowline {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: -4px;
  color: #7f9cba;
  font-size: 12px;
}

.signin-link {
  padding: 0;
  border: 0;
  background: none;
  color: #9de2ff;
  font: inherit;
  cursor: pointer;
}

.signin-link:hover { text-decoration: underline; }

.signin-hint {
  margin-top: -6px;
  color: #aec4de;
  font-size: .88rem;
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

.signin-spinner {
  width: 16px;
  height: 16px;
  border: 2px solid rgba(255, 255, 255, .32);
  border-top-color: #fff;
  border-radius: 50%;
  animation: signin-spin .8s linear infinite;
}

.signin-secondary {
  border-color: #577aa374;
  background: #18305069;
  color: #c6ddf6;
}

.signin-secondary:hover:not(:disabled) { border-color: #88ceff; background: #203e63; }

.signin-biometric { margin-top: 19px; padding-top: 17px; border-top: 1px solid #7aa7d12b; }
.signin-biometric p { margin-top: 8px; color: #aec4de; font-size: .88rem; }

.signin-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 0 0 14px !important;
  color: #6f89a6;
  font-size: 12px;
}

.signin-divider::before,
.signin-divider::after {
  flex: 1;
  height: 1px;
  background: linear-gradient(90deg, transparent, #8fc0e640, transparent);
  content: "";
}

.signin-note {
  margin-top: 18px;
  padding-top: 14px;
  border-top: 1px solid #7aa7d12b;
  color: #8fa9c4;
  font-size: 11.6px;
  line-height: 1.6;
}

.signin-error {
  padding: 8px 11px;
  border: 1px solid #ff708650;
  border-radius: 8px;
  background: #7426373d;
  color: #ffc0ca;
}

/* ---------- 底部 ---------- */
.signin-footer {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  padding: 16px 0 6px;
}

.signin-slogan {
  color: #cfe3f7;
  font-size: 15px;
  letter-spacing: .14em;
}

.signin-slogan small {
  display: block;
  margin-top: 5px;
  color: #63809e;
  font-size: 10px;
  letter-spacing: .22em;
}

.signin-footer :deep(.system-copyright) {
  padding: 0;
  text-align: right;
}

@keyframes signin-spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 1180px) {
  .signin-layout { grid-template-columns: minmax(0, 1fr) 400px; gap: 24px; }
}

@media (max-width: 980px) {
  .signin-layout {
    grid-template-columns: minmax(0, 430px);
    grid-template-rows: auto auto;
    justify-content: center;
    justify-items: center;
  }
  .signin-layout-single { grid-template-columns: minmax(0, 430px); }
  .signin-card { width: 100%; }
}

@media (max-width: 760px) {
  .signin {
    padding-top: 0;
    background-position: center, 59% center;
  }
  .signin-topbar { height: 66px; }
  .signin-brand-text strong { font-size: 16px; }
  .signin-brand-text span { display: none; }
  .signin-status { gap: 14px; font-size: 12px; }
  .signin-status-clock { display: none; }
  .signin-card { padding: 22px; }
  .signin-mark { width: 44px; height: 44px; margin-bottom: 14px; }
  .signin-form { gap: 13px; margin-top: 22px; }
  .signin-field input, .signin-button { min-height: 44px; }
  .signin-footer { flex-direction: column; align-items: flex-start; gap: 6px; }
  .signin-slogan { font-size: 13px; }
  .signin-footer :deep(.system-copyright) { text-align: left; }
}

@media (max-width: 360px) {
  :global(body:has(.signin)) { min-width: 0; }
}
</style>

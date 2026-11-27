<script setup lang="ts">
import { computed, defineAsyncComponent, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import {
  Bell, BellRing, BookOpenText, Building2, ClipboardCheck, ClipboardList, ClipboardPenLine,
  FileChartColumn, FileUp, Files, FolderArchive, History, LayoutDashboard, ListChecks,
  Megaphone, MessageSquareText, Radar, ScanSearch, ScanText, Settings, ShieldCheck, Users, Workflow, Wrench,
} from '@lucide/vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import CopyrightFooter from './components/CopyrightFooter.vue'
import V2RouteDialog from './components/V2RouteDialog.vue'
import { api, errorText, params } from './lib/api'
import { dashboardPath, dashboardTabLocation } from './lib/dashboard'
import { queryValue } from './lib/paging'
import { publicationUnread, refreshPublicationUnread } from './lib/publications'
import { canVisit, session } from './lib/session'
import { businessScopes, refreshBusinessOnFocus } from './lib/scopedBusiness'
import { cancelWorkbenchRefresh, refreshWorkbench, workbench } from './lib/workbench'

const route = useRoute()
const router = useRouter()
const sidebarOpen = ref(false)
const reminderPanel = ref<HTMLDetailsElement>()
const { overview, loading: remindersLoading, failure: remindersFailure } = workbench
let reminderTimer: ReturnType<typeof setInterval> | undefined
const reminders = computed(() => {
  const snapshot = overview.value
  if (!snapshot) return []
  const { remediation } = snapshot
  return [
    { label: '工作流已逾期', count: snapshot.workflow?.overdue ?? snapshot.reviewDeadlines?.overdue ?? 0, path: '/', query: {} },
    { label: '工作流即将到期', count: snapshot.workflow?.near ?? 0, path: '/', query: {} },
    { label: '待审核预警处置', count: snapshot.pendingAlertReviews ?? 0, path: '/alerts', query: { review: 'pending' } },
    { label: '待补正材料异常', count: snapshot.openMaterialIssues ?? 0, path: '/alerts', query: { view: 'materials' } },
    { label: '活跃预警', count: snapshot.activeAlerts, path: '/alerts', query: {} },
    { label: '已逾期隐患', count: remediation.overdue, path: '/hazards', query: { due: 'overdue' } },
    { label: '待复核隐患', count: remediation.pendingReview, path: '/hazards', query: { status: 'pending_review' } },
    { label: '待审核整改规划', count: remediation.pendingPlanReview, path: '/remediations',
      query: { status: 'pending_review' } },
    { label: '待初审测评报告', count: snapshot.reportInitial, path: '/reports', query: { status: 'pending_initial' } },
    { label: '待终审测评报告', count: snapshot.reportFinal, path: '/reports', query: { status: 'pending_final' } },
    { label: '10 日内到期隐患', count: remediation.dueWithin10Days, path: '/hazards',
      query: { due: 'within_10_days' } },
  ].filter(item => item.count > 0)
})
const regulatory = [
  { key: 'home', name: 'M1 工作台', icon: LayoutDashboard, path: '/' },
  { key: 'grade', name: 'M2 等级保护', icon: ClipboardCheck, path: '/filings' },
  { key: 'archive', name: 'M3 全息档案', icon: FolderArchive, path: '/archives/directory' },
  { key: 'monitor', name: 'M4 监测预警', icon: BellRing, path: '/alerts' },
]
const governance = [
  { key: 'agent', name: 'M5 智能体', icon: BookOpenText, path: '/knowledge' },
  { key: 'screen', name: 'M6 指挥驾驶舱', icon: Radar, path: dashboardPath },
  { key: 'system', name: 'M7 系统管理', icon: Settings, path: '/audit' },
  { key: 'ai-review', name: 'M8 AI 智能审核', icon: ScanText, path: '' },
]
const navigation = [
  { label: '业务监管', items: regulatory },
  { label: '智能与治理', items: governance },
]
const modules = [
  {
    key: 'grade',
    title: '等级保护',
    subtitle: '备案 · 实施 · 测评 · 整改规划',
    selected: ['filings', 'implementations', 'reports', 'remediations', 'filing-material-policy'],
    tabs: [
      { key: 'filings', name: '备案管理', path: '/filings' },
      { key: 'implementations', name: '实施管理', path: '/implementations' },
      { key: 'reports', name: '测评管理', path: '/reports' },
      { key: 'remediations', name: '整改规划审核', path: '/remediations' },
      { key: 'filing-material-policy', name: '备案材料规则', path: '/filing-material-policy' },
    ],
  },
  {
    key: 'archive',
    title: '全息档案',
    subtitle: '单位 · 系统资产 · 隐患 · 人员全息画像',
    selected: ['institutions', 'assets', 'hazards', 'personnel'],
    tabs: [
      { key: 'institutions', name: '单位档案', path: '/archives/directory' },
      { key: 'assets', name: '系统资产', path: '/archives/systems' },
      { key: 'hazards', name: '隐患管理', path: '/hazards' },
      { key: 'personnel', name: '人员管理', path: '/archives/directory?tab=people' },
    ],
  },
  {
    key: 'monitor',
    title: '监测预警',
    subtitle: '预警中心 · 监督检查 · 信息发布',
    selected: ['alerts', 'supervisions', 'publications'],
    tabs: [
      { key: 'alerts', name: '预警中心', path: '/alerts' },
      { key: 'supervisions', name: '监督检查', path: '/supervisions' },
      { key: 'publications', name: '信息发布', path: '/monitor/publications' },
    ],
  },
  {
    key: 'agent',
    title: '智能体',
    subtitle: 'AI 对话 · 知识库 · 数据中心',
    selected: ['knowledge'],
    tabs: [
      { key: 'conversation', name: 'AI 对话', path: '' },
      { key: 'knowledge', name: '知识库', path: '/knowledge' },
      { key: 'data', name: '数据中心', path: '' },
    ],
  },
  {
    key: 'system',
    title: '系统管理',
    subtitle: '基础配置 · 运维审计',
    selected: ['tag-catalog', 'workday-calendar', 'audit'],
    tabs: [
      { key: 'tag-catalog', name: '标签管理', path: '/tag-catalog' },
      { key: 'workday-calendar', name: '工作日历', path: '/workday-calendar' },
      { key: 'audit', name: '操作日志', path: '/audit' },
    ],
  },
]
const section = computed(() => {
  if (route.path === '/archives/directory' && route.query.tab === 'people') return 'personnel'
  return typeof route.meta.section === 'string' ? route.meta.section : ''
})
const scopedAudience = computed(() => section.value === 'institution' || section.value === 'operator'
  ? section.value : undefined)
const noticeScopeKey = computed(() => scopedAudience.value ? businessScopes[scopedAudience.value].key : '')
const noticeScope = computed(() => scopedAudience.value ? queryValue(route.query[noticeScopeKey.value]).trim() : '')
const authFailure = ref('')
const authLayout = computed(() => route.meta.authLayout === true)
const scopedUnread = ref(0)
let scopedUnreadRequest: AbortController | undefined
const fullscreenLayout = computed(() => route.meta.fullscreenLayout === true)
const regulatoryShell = computed(() => session.value?.roleCode === 'ROLE_SYS_ADMIN' && !fullscreenLayout.value && !authLayout.value)
const scopedNavigation = {
  institution: {
    name: '测评机构端', role: '测评机构', avatar: '测', groups: [
      { label: '业务办理', items: [
        { key: 'workbench', label: 'I1 工作台', icon: LayoutDashboard, path: 'workbench' },
        { key: 'filings', label: 'I2 备案材料', icon: FileUp, path: 'filings' },
        { key: 'implementations', label: 'I3 实施管理', icon: ClipboardPenLine, path: 'implementations' },
        { key: 'reports', label: 'I4 测评报告', icon: FileChartColumn, path: 'reports' },
        { key: 'hazards', label: '隐患复测', icon: ScanSearch, path: 'hazards' },
        { key: 'directory', label: 'I6 基础数据', icon: Users, path: 'directory' },
      ] },
      { label: '站内消息', items: [
        { key: 'publications', label: 'I7 消息公告', icon: Megaphone, path: 'publications' },
        { key: 'alerts', label: '预警消息', icon: BellRing, path: 'alerts' },
        { key: 'feedback', label: '消息通知', icon: MessageSquareText, path: 'feedback' },
      ] },
    ],
  },
  operator: {
    name: '网络运营者端', role: '网络运营者', avatar: '运', groups: [
      { label: '常用办理', items: [
        { key: 'workbench', label: '我的工作台', icon: LayoutDashboard, path: 'workbench' },
        { key: 'filings', label: '备案办理', icon: Files, path: 'filings' },
        { key: 'implementations', label: '实施报备', icon: ClipboardPenLine, path: 'implementations' },
        { key: 'hazards', label: '隐患整改', icon: Wrench, path: 'hazards' },
        { key: 'remediations', label: '整改规划', icon: ClipboardList, path: 'remediations' },
        { key: 'assessment-history', label: '测评历史对比', icon: History, path: 'assessment-history' },
        { key: 'supervisions', label: '监督检查自查', icon: ListChecks, path: 'supervisions' },
      ] },
      { label: '消息与提醒', items: [
        { key: 'feedback', label: '审核意见', icon: MessageSquareText, path: 'feedback' },
        { key: 'alerts', label: '预警处置', icon: BellRing, path: 'alerts' },
        { key: 'publications', label: '通知公告', icon: Megaphone, path: 'publications' },
      ] },
    ],
  },
}
const visibleNavigation = computed(() => navigation.map(group => ({
  ...group, items: group.items.filter(item => session.value?.menu.includes(item.key)),
})).filter(group => group.items.length))
const activeScopedNavigation = computed(() => scopedAudience.value && scopedNavigation[scopedAudience.value])
const activeScopedGroup = computed(() => {
  const resource = route.path.split('/')[2]
  if (resource === 'material-expiries' || resource === 'assessment-dues') return 'alerts'
  if (resource === 'filings' || resource === 'filing-applications') {
    return 'filings'
  }
  return resource
})
const currentScopeQuery = computed(() => ({ [noticeScopeKey.value]: noticeScope.value || undefined }))
const module = computed(() => modules.find(item => item.selected.includes(section.value)))
const visibleTabs = computed(() => {
  const actor = session.value
  return actor && module.value
    ? module.value.tabs.filter(tab => tab.path && canVisit(actor, tab.path.split('?')[0] ?? '')) : []
})
const activeGroup = computed(() => module.value?.key ?? section.value)
const moduleIcon = computed(() => navigation.flatMap(group => group.items)
  .find(item => item.key === module.value?.key)?.icon)
const CaseList = defineAsyncComponent(() => import('./views/CaseList.vue'))
const lists = {
  filings: { component: CaseList, props: { kind: 'filings' } },
  reports: { component: CaseList, props: { kind: 'reports' } },
  'filing-applications': { component: defineAsyncComponent(() => import('./views/FilingApplicationList.vue')) },
  implementations: { component: defineAsyncComponent(() => import('./views/ImplementationList.vue')) },
  remediations: { component: defineAsyncComponent(() => import('./views/RemediationList.vue')) },
  hazards: { component: defineAsyncComponent(() => import('./views/HazardList.vue')) },
  knowledge: { component: defineAsyncComponent(() => import('./views/KnowledgeList.vue')) },
}
const overlay = computed(() => {
  if (route.path === '/filings/history-import') return undefined
  const [, item, name, action] = route.path.split('/')
  if (!section.value || !item || !name || !Object.hasOwn(lists, item)) return undefined
  const content = lists[item as keyof typeof lists]
  const form = name === 'new' || action === 'edit'
  const width = !form && (item === 'filings' || item === 'reports') ? 760 : form && item === 'implementations' ? 720 : 680
  return { ...content, path: '/' + item, width }
})
const backdropRef = ref<HTMLElement>()

/**
 * 用途 返回当前账号所属的工作台地址。
 * 参数 audience 为会话允许使用的办理端。
 * 返回值 带可信主体名称的工作台路径及查询条件。
 */
function scopedHome(audience: keyof typeof businessScopes) {
  return { path: '/' + audience + '/workbench',
    query: { [businessScopes[audience].key]: session.value?.subjectName } }
}

/**
 * 用途 撤销会话并清除页面载入过的数据。
 * 参数 无，使用浏览器当前登录会话。
 * 返回值 退出任务，失败时在顶栏显示原因。
 */
async function logout() {
  authFailure.value = ''
  try {
    await api('/auth/logout', { method: 'POST' })
    window.location.replace('/login')
  } catch (error) {
    authFailure.value = errorText(error)
  }
}

/**
 * 用途 返回当前业务列表并保留可复用的明确筛选条件。
 * 参数 无，列表路径来自打开的业务页。
 * 返回值 路由导航任务。
 */
function closeOverlay() {
  const current = overlay.value
  if (!current) return
  const query: LocationQueryRaw = {}
  const filters = ['q', 'status', 'severity', 'due', 'planId', 'category', 'lifecycle', 'cursor']
  if (current.path === '/implementations') filters.push('type', 'archive')
  if (current.path === '/filing-applications') filters.push('type', 'filingId')
  if (current.path === '/knowledge') filters.push('source')
  if (current.path === '/reports') filters.push('risk')
  for (const key of filters) {
    if (typeof route.query[key] === 'string') query[key] = route.query[key]
  }
  return router.push({ path: current.path, query }).then(() => backdropRef.value?.focus())
}

/**
 * 用途 在页面可见时核对站内待办与临期数据。
 * 参数 无。
 * 返回值 无。
 */
function refreshVisibleReminders() {
  if (regulatoryShell.value && document.visibilityState === 'visible' && !remindersLoading.value) {
    void refreshWorkbench()
  }
}

/**
 * 用途 刷新已选机构或运营单位顶栏中的待签收消息数量。
 * 参数 无，当前单位来自运营端路由。
 * 返回值 站内未读数量的查询任务。
 */
async function refreshScopedUnread() {
  if (!noticeScope.value || scopedUnreadRequest) return
  const request = new AbortController()
  scopedUnreadRequest = request
  try {
    const result = await api<{ count: number }>('/' + section.value + '/feedback/unread-count'
      + params({ [noticeScopeKey.value]: noticeScope.value }), { signal: request.signal })
    if (scopedUnreadRequest === request) scopedUnread.value = result.count
  } catch {
    if (scopedUnreadRequest === request) scopedUnread.value = 0
  } finally {
    if (scopedUnreadRequest === request) scopedUnreadRequest = undefined
  }
}

/**
 * 用途 在打开业务清单或切换路由时关闭提醒浮层。
 * 参数 无。
 * 返回值 无。
 */
function closeReminders() {
  if (reminderPanel.value) reminderPanel.value.open = false
}

watch(() => route.fullPath, () => {
  sidebarOpen.value = false
  closeReminders()
})
watch([scopedAudience, noticeScope], ([audience, scope]) => {
  scopedUnreadRequest?.abort()
  scopedUnreadRequest = undefined
  scopedUnread.value = 0
  void refreshScopedUnread()
  void refreshPublicationUnread(audience, scope)
}, { immediate: true })
refreshBusinessOnFocus(() => {
  void refreshScopedUnread()
  void refreshPublicationUnread(scopedAudience.value, noticeScope.value)
}, 10_000)
watch(regulatoryShell, (active) => {
  if (active) refreshVisibleReminders()
  else cancelWorkbenchRefresh()
})
onMounted(() => {
  refreshVisibleReminders()
  reminderTimer = setInterval(refreshVisibleReminders, 10_000)
  document.addEventListener('visibilitychange', refreshVisibleReminders)
  window.addEventListener('focus', refreshVisibleReminders)
})
onBeforeUnmount(() => {
  scopedUnreadRequest?.abort()
  void refreshPublicationUnread()
  cancelWorkbenchRefresh()
  clearInterval(reminderTimer)
  document.removeEventListener('visibilitychange', refreshVisibleReminders)
  window.removeEventListener('focus', refreshVisibleReminders)
})
</script>

<template>
  <RouterView v-if="authLayout || fullscreenLayout" />
  <div v-else-if="scopedAudience && activeScopedNavigation" class="workspace v2-shell role-shell"
    :class="scopedAudience === 'institution' ? 'assessment-shell' : 'operator-shell'">
    <a class="skip-link" href="#scoped-content">跳至主要内容</a>
    <aside id="scoped-sidebar" class="v2-sidebar" :class="{ 'v2-sidebar-open': sidebarOpen }">
      <RouterLink class="v2-brand" :to="scopedHome(scopedAudience)" :aria-label="activeScopedNavigation.name + '工作台'">
        <span class="v2-logo" aria-hidden="true">
          <component :is="scopedAudience === 'institution' ? FileChartColumn : Building2" :size="17" />
        </span>
        <span>{{ scopedAudience === 'institution' ? '等级保护测评' : '等级保护办理' }}<small>{{ activeScopedNavigation.name }}</small></span>
      </RouterLink>
      <nav class="v2-navigation" :aria-label="activeScopedNavigation.name + '导航'">
        <template v-for="group in activeScopedNavigation.groups" :key="group.label">
          <div class="v2-nav-group">{{ group.label }}</div>
          <RouterLink v-for="item in group.items.filter(item => session?.menu.includes(item.key))" :key="item.key" class="v2-nav-item"
            :class="{ active: activeScopedGroup === item.key }"
            :aria-current="activeScopedGroup === item.key ? 'page' : undefined"
            :to="{ path: '/' + scopedAudience + '/' + item.path, query: {
              ...currentScopeQuery, ...(item.key === 'hazards' && scopedAudience === 'institution'
                ? { status: 'pending_retest' } : {}),
            } }">
            <span class="v2-nav-icon" aria-hidden="true"><component :is="item.icon" :size="15" /></span>{{ item.label }}
            <span v-if="item.key === 'feedback' && scopedUnread" class="scoped-unread"
              :aria-label="scopedUnread + ' 条待签收消息'">{{ scopedUnread > 99 ? '99+' : scopedUnread }}</span>
            <span v-if="item.key === 'publications' && publicationUnread?.count" class="scoped-unread"
              :aria-label="publicationUnread.count + ' 条未读公告'">
              {{ publicationUnread.count > 99 ? '99+' : publicationUnread.count }}
            </span>
          </RouterLink>
        </template>
      </nav>
      <div class="v2-sidebar-foot">{{ activeScopedNavigation.name }} · 本地业务测试</div>
    </aside>
    <button v-if="sidebarOpen" class="v2-scrim" type="button" aria-label="关闭导航" @click="sidebarOpen = false"></button>
    <div class="workspace-main">
      <header class="topbar v2-topbar scoped-topbar">
        <button class="v2-menu-toggle" type="button" :aria-expanded="sidebarOpen" aria-controls="scoped-sidebar"
          aria-label="打开业务导航" @click="sidebarOpen = !sidebarOpen">☰</button>
        <RouterLink class="v2-crumb scoped-home" :to="scopedHome(scopedAudience)">{{ activeScopedNavigation.name }}</RouterLink>
        <span class="v2-crumb scoped-crumb">{{ route.meta.title }}</span>
        <span class="v2-topbar-spacer"></span>
        <RouterLink class="button neutral small" to="/account/password">修改密码</RouterLink>
        <button class="button neutral small" type="button" @click="logout">退出登录</button>
        <span v-if="authFailure" role="alert">{{ authFailure }}</span>
        <span class="v2-avatar scoped-avatar" aria-hidden="true">{{ activeScopedNavigation.avatar }}</span>
        <span class="v2-operator scoped-role" :title="noticeScope">
          <strong>{{ noticeScope || activeScopedNavigation.name }}</strong>
          <small>{{ activeScopedNavigation.role }}</small>
        </span>
      </header>
      <main id="scoped-content" class="content v2-content" tabindex="-1">
        <RouterView />
      </main>
      <CopyrightFooter />
    </div>
  </div>
  <div v-else-if="session" class="workspace v2-shell regulator-shell">
    <a class="skip-link" href="#workspace-content">跳至主要内容</a>
    <aside id="v2-sidebar" class="v2-sidebar" :class="{ 'v2-sidebar-open': sidebarOpen }">
      <RouterLink class="v2-brand" to="/" aria-label="网络安全等级保护平台工作台">
        <span class="v2-logo" aria-hidden="true"><ShieldCheck :size="21" /></span>
        <span>网络安全等级保护平台<small>网络安全监管</small></span>
      </RouterLink>
      <nav aria-label="业务导航" class="v2-navigation">
        <template v-for="group in visibleNavigation" :key="group.label">
          <div class="v2-nav-group">{{ group.label }}</div>
          <template v-for="item in group.items" :key="item.key">
            <RouterLink v-if="item.path" class="v2-nav-item" :class="{ active: activeGroup === item.key }"
              :to="item.key === 'screen' ? dashboardTabLocation : item.path"
              :target="item.key === 'screen' ? '_blank' : undefined"
              :rel="item.key === 'screen' ? 'noopener' : undefined"
              :aria-current="activeGroup === item.key ? 'page' : undefined">
              <span class="v2-nav-icon" aria-hidden="true"><component :is="item.icon" :size="15" /></span>{{ item.name }}
            </RouterLink>
            <button v-else class="v2-nav-item v2-unavailable v2-reserved-entry" type="button" disabled
              title="待对接审核系统" aria-describedby="ai-review-status">
              <span class="v2-nav-icon" aria-hidden="true"><component :is="item.icon" :size="15" /></span>{{ item.name }}
              <small id="ai-review-status" class="v2-reserved-status">待接入</small>
            </button>
          </template>
        </template>
      </nav>
      <div class="v2-sidebar-foot">监管工作台 · 本地业务测试</div>
    </aside>
    <button v-if="sidebarOpen" class="v2-scrim" type="button" aria-label="关闭导航" @click="sidebarOpen = false"></button>

    <div class="workspace-main">
      <header class="topbar v2-topbar">
        <button class="v2-menu-toggle" type="button" :aria-expanded="sidebarOpen" aria-controls="v2-sidebar"
          aria-label="打开业务导航" @click="sidebarOpen = !sidebarOpen">☰</button>
        <nav aria-label="当前位置" class="v2-crumb">{{ module?.title ?? route.meta.title }}</nav>
        <span class="v2-topbar-spacer"></span>
        <RouterLink class="v2-flow-link" to="/flow"><Workflow :size="15" aria-hidden="true" />流程示意</RouterLink>
        <RouterLink class="button neutral small" to="/account/password">修改密码</RouterLink>
        <button class="button neutral small" type="button" @click="logout">退出登录</button>
        <span v-if="authFailure" role="alert">{{ authFailure }}</span>
        <details v-if="regulatoryShell" ref="reminderPanel" class="v2-reminder" @keydown.escape="closeReminders">
          <summary class="v2-bell" :aria-label="'待办提醒，' + reminders.length + ' 类事项'">
            <Bell :size="18" aria-hidden="true" />
            <span v-if="reminders.length" class="v2-reminder-badge" aria-hidden="true">{{ reminders.length }}</span>
          </summary>
          <div class="v2-reminder-panel">
            <header>
              <strong>待办提醒</strong>
              <button class="text-action" type="button" :disabled="remindersLoading" @click="refreshWorkbench">刷新</button>
            </header>
            <p v-if="remindersFailure" class="muted" role="status">
              {{ overview ? '刷新失败，显示上次数据。' : remindersFailure }}
            </p>
            <p v-else-if="remindersLoading && !overview" class="muted" role="status">正在读取待办…</p>
            <p v-else-if="!reminders.length" class="muted">暂无待办提醒。</p>
            <RouterLink v-for="item in reminders" :key="item.label" class="v2-reminder-link"
              :to="{ path: item.path, query: item.query }" @click="closeReminders">
              {{ item.label }} <strong>{{ item.count }}</strong>
            </RouterLink>
            <RouterLink class="v2-reminder-link" to="/monitor/publications" @click="closeReminders">
              信息发布回执
            </RouterLink>
          </div>
        </details>
        <span class="v2-avatar" aria-hidden="true">监</span>
        <span class="v2-operator"><strong>{{ session.name }}</strong>
          <small>{{ session.roleCode === 'ROLE_SYS_ADMIN' ? '系统管理员' : '监管员' }}</small></span>
      </header>
      <main id="workspace-content" ref="backdropRef" class="content v2-content" tabindex="-1">
        <div :key="'scan-' + activeGroup" class="regulator-navigation-scan" aria-hidden="true"></div>
        <template v-if="module">
          <header :key="module.key" class="v2-page-head regulator-module-head">
            <span class="regulator-module-icon" aria-hidden="true"><component :is="moduleIcon" :size="24" /></span>
            <div><h1>{{ module.title }}</h1><p>{{ module.subtitle }}</p></div>
          </header>
          <nav class="v2-tabs" :aria-label="module.title + '页签'">
            <template v-for="tab in visibleTabs" :key="tab.key">
              <RouterLink v-if="tab.path" class="v2-tab" :class="{ active: section === tab.key }" :to="tab.path"
                :aria-current="section === tab.key ? 'page' : undefined">{{ tab.name }}</RouterLink>
              <span v-else class="v2-tab v2-unavailable" aria-disabled="true" title="本地版本尚未接入">{{ tab.name }}</span>
            </template>
          </nav>
        </template>
        <template v-if="overlay">
          <div inert aria-hidden="true">
            <component :is="overlay.component" v-bind="'props' in overlay ? overlay.props : {}" />
          </div>
          <V2RouteDialog :label="String(route.meta.title ?? '业务办理')" :width="overlay.width" @close="closeOverlay">
            <RouterView />
          </V2RouteDialog>
        </template>
        <RouterView v-else />
      </main>
      <CopyrightFooter />
    </div>
  </div>
</template>

<style scoped>
.v2-reserved-entry {
  width: 100%; background: transparent; font: inherit; text-align: left; cursor: default;
}
.v2-reserved-status { margin-inline-start: auto; font-size: .75rem; white-space: nowrap; }
.scoped-unread { padding: 0 5px; border-radius: 10px; background: var(--c-primary); color: white; }
.scoped-topbar { gap: 10px; }
.scoped-home { color: var(--c-primary-700); font-weight: 600; white-space: nowrap; }
.scoped-crumb { border-left: 1px solid var(--line); padding-left: 10px; }
.scoped-role { min-width: 0; max-width: 180px; }
.scoped-role strong { text-overflow: ellipsis; white-space: nowrap; overflow: hidden; }
@media (max-width: 540px) {
  .scoped-crumb, .scoped-role, .scoped-avatar { display: none; }
}
</style>

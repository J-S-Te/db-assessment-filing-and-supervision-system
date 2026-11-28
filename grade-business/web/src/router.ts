import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { canVisit, deniedRoute, refreshSession, session } from './lib/session'
import { caseLabels, type CaseKind } from './lib/domain'

/**
 * 用途 统一登记备案或测评的各个直接访问页面。
 * 参数 kind 为业务资源名称。
 * 返回值 对应的列表、新建、查看和编辑路由。
 */
function caseRoutes(kind: CaseKind): RouteRecordRaw[] {
  const meta = { section: kind, title: caseLabels[kind].module }
  const view = () => import('./views/CaseForm.vue')
  return [
    { path: '/' + kind, component: () => import('./views/CaseList.vue'), props: { kind }, meta },
    { path: '/' + kind + '/new', component: view, props: { kind }, meta },
    { path: '/' + kind + '/:id', component: () => import('./views/CaseDetail.vue'), props: { kind }, meta },
    { path: '/' + kind + '/:id/edit', component: view, props: { kind }, meta },
  ]
}

const implementationMeta = { section: 'implementations', title: '实施管理' }
const filingApplicationMeta = { section: 'filings', title: '备案变更与注销' }
const remediationMeta = { section: 'remediations', title: '整改规划' }
const hazardMeta = { section: 'hazards', title: '隐患台账' }
const unitArchiveMeta = { section: 'institutions', title: '单位档案' }
const systemArchiveMeta = { section: 'assets', title: '系统档案' }
const unitArchive = () => import('./views/ArchiveUnits.vue')
const businessDirectory = () => import('./views/BusinessDirectory.vue')
const systemArchive = () => import('./views/ArchiveSystems.vue')
const implementationForm = () => import('./views/ImplementationForm.vue')
const filingApplicationForm = () => import('./views/FilingApplicationForm.vue')
const remediationForm = () => import('./views/RemediationForm.vue')
const institutionMeta = { section: 'institution', title: '机构报送' }
const operatorFilingMeta = { section: 'operator', title: '我的备案办理' }
const operatorRemediationMeta = { section: 'operator', title: '我的整改规划' }
const institutionList = () => import('./views/InstitutionSubmissions.vue')
const institutionCaseForm = () => import('./views/CaseForm.vue')

/**
 * 用途 两个办理端进入各自首页时按需加载共用工作台。
 * 参数 无，办理端由对应的路由传入页面。
 * 返回值 工作台页面模块的加载任务。
 */
const scopedWorkbench = () => import('./views/ScopedWorkbench.vue')

/**
 * 用途 按需载入登录与首登改密共用表单。
 * 参数 无。
 * 返回值 登录页模块的加载任务。
 */
const loadSignIn = () => import('./views/SignIn.vue')

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: loadSignIn, meta: { authLayout: true } },
    { path: '/operator-register', component: () => import('./views/OperatorRegister.vue'),
      meta: { authLayout: true, title: '运营单位注册' } },
    { path: '/operator-registrations', component: () => import('./views/OperatorRegistrationReview.vue'),
      meta: { section: 'operator-registrations', title: '注册审核' } },
    { path: '/account/password', component: loadSignIn, meta: { authLayout: true } },
    { path: '/institution-accounts', component: () => import('./views/InstitutionAccounts.vue'),
      meta: { section: 'institution-accounts', title: '机构用户' } },
    { path: '/', component: () => import('./views/Workbench.vue'), meta: { section: 'home', title: '工作台' } },
    ...caseRoutes('filings'),
    { path: '/filings/:id/material-review', component: () => import('./views/FilingReview.vue'),
      props: true, meta: { section: 'filings', title: '材料审阅', standalone: true } },
    { path: '/filing-material-policy', component: () => import('./views/FilingMaterialPolicy.vue'),
      meta: { section: 'filing-material-policy', title: '备案材料规则' } },
    { path: '/filings/history-import', component: () => import('./views/FilingHistoryImport.vue'),
      meta: { section: 'filings', title: '导入历史材料' } },
    { path: '/filing-applications', component: () => import('./views/FilingApplicationList.vue'), meta: filingApplicationMeta },
    { path: '/filing-applications/new', component: filingApplicationForm, meta: filingApplicationMeta },
    { path: '/filing-applications/:id', component: () => import('./views/FilingApplicationDetail.vue'), meta: filingApplicationMeta },
    { path: '/filing-applications/:id/edit', component: filingApplicationForm, meta: filingApplicationMeta },
    { path: '/implementations', component: () => import('./views/ImplementationList.vue'), meta: implementationMeta },
    { path: '/implementations/new', component: implementationForm, meta: implementationMeta },
    { path: '/implementations/:id', component: () => import('./views/ImplementationDetail.vue'), meta: implementationMeta },
    { path: '/implementations/:id/edit', component: implementationForm, meta: implementationMeta },
    ...caseRoutes('reports'),
    { path: '/remediations', component: () => import('./views/RemediationList.vue'), meta: remediationMeta },
    { path: '/remediations/new', component: remediationForm, meta: remediationMeta },
    { path: '/remediations/:id', component: () => import('./views/RemediationDetail.vue'), meta: remediationMeta },
    { path: '/remediations/:id/edit', component: remediationForm, meta: remediationMeta },
    { path: '/hazards', component: () => import('./views/HazardList.vue'), meta: hazardMeta },
    { path: '/hazards/:id', component: () => import('./views/HazardDetail.vue'), meta: hazardMeta },
    { path: '/archives/units', component: unitArchive, meta: unitArchiveMeta },
    { path: '/archives/directory', component: businessDirectory, meta: unitArchiveMeta },
    { path: '/archives/units/:organization', component: unitArchive, meta: unitArchiveMeta },
    { path: '/archives/systems', component: systemArchive, meta: systemArchiveMeta },
    { path: '/archives/systems/:id', component: systemArchive, meta: systemArchiveMeta },
    { path: '/alerts', component: () => import('./views/MonitorAlerts.vue'),
      meta: { section: 'alerts', title: '预警中心' } },
    { path: '/supervisions/:id?', component: () => import('./views/SupervisionTasks.vue'),
      meta: { section: 'supervisions', title: '监督检查' } },
    { path: '/material-expiries/:id?', component: () => import('./views/MaterialExpiryList.vue'),
      meta: { section: 'alerts', title: '材料有效期' } },
    { path: '/assessment-dues/:id?', component: () => import('./views/AssessmentDueList.vue'),
      meta: { section: 'alerts', title: '测评到期台账' } },
    { path: '/monitor/publications', component: () => import('./views/MonitorPublications.vue'),
      meta: { section: 'publications', title: '信息发布' } },
    { path: '/dashboard', component: () => import('./views/CommandDashboard.vue'),
      meta: { section: 'screen', title: '指挥驾驶舱', fullscreenLayout: true } },
    { path: '/audit', component: () => import('./views/BusinessAudit.vue'),
      meta: { section: 'audit', title: '业务操作审计' } },
    { path: '/workday-calendar', component: () => import('./views/WorkdayCalendar.vue'),
      meta: { section: 'workday-calendar', title: '工作日历' } },
    { path: '/tag-catalog', component: () => import('./views/TagCatalog.vue'),
      meta: { section: 'tag-catalog', title: '标签管理' } },
    { path: '/flow', component: () => import('./views/BusinessFlow.vue'),
      meta: { section: 'flow', title: '流程示意' } },
    { path: '/knowledge', component: () => import('./views/KnowledgeList.vue'), meta: { section: 'knowledge', title: '知识库' } },
    { path: '/knowledge/new', component: () => import('./views/KnowledgeForm.vue'), meta: { section: 'knowledge', title: '知识库' } },
    { path: '/knowledge/:id/edit', component: () => import('./views/KnowledgeForm.vue'), props: { mode: 'edit' }, meta: { section: 'knowledge', title: '编辑知识' } },
    { path: '/knowledge/:id/tags', component: () => import('./views/KnowledgeForm.vue'), props: { mode: 'tags' }, meta: { section: 'knowledge', title: '知识标签' } },
    { path: '/knowledge/:id', component: () => import('./views/KnowledgeDetail.vue'), meta: { section: 'knowledge', title: '知识库' } },
    { path: '/institution/workbench', component: scopedWorkbench, props: { audience: 'institution' },
      meta: { section: 'institution', title: '工作台' } },
    { path: '/institution/hazards', component: () => import('./views/InstitutionRetest.vue'),
      meta: { section: 'institution', title: '隐患复测' } },
    { path: '/institution/hazards/:id', component: () => import('./views/InstitutionRetest.vue'),
      meta: { section: 'institution', title: '隐患复测详情' } },
    { path: '/institution/reports/:id', component: () => import('./views/InstitutionReport.vue'),
      meta: institutionMeta },
    { path: '/institution/filings', component: institutionList, props: { resource: 'filings' }, meta: institutionMeta },
    { path: '/institution/reports', component: institutionList, props: { resource: 'reports' }, meta: institutionMeta },
    { path: '/institution/filing-applications', component: institutionList,
      props: { resource: 'filing-applications' }, meta: institutionMeta },
    { path: '/institution/implementations', component: institutionList,
      props: { resource: 'implementations' }, meta: institutionMeta },
    { path: '/institution/filings/new', component: institutionCaseForm,
      props: { kind: 'filings', institutional: true }, meta: institutionMeta },
    { path: '/institution/reports/new', component: institutionCaseForm,
      props: { kind: 'reports', institutional: true }, meta: institutionMeta },
    { path: '/institution/reports/:id/edit', component: institutionCaseForm,
      props: { kind: 'reports', institutional: true }, meta: institutionMeta },
    { path: '/institution/filing-applications/new', component: filingApplicationForm,
      props: { institutional: true }, meta: institutionMeta },
    { path: '/institution/implementations/new', component: implementationForm,
      props: { institutional: true }, meta: institutionMeta },
    { path: '/institution/implementations/:id/edit', component: implementationForm,
      props: { institutional: true }, meta: institutionMeta },
    { path: '/institution/feedback', component: () => import('./views/BusinessFeedback.vue'),
      props: { audience: 'institution' }, meta: { section: 'institution', title: '机构审核意见' } },
    { path: '/institution/directory', component: businessDirectory, props: { institutional: true },
      meta: { section: 'institution', title: '基础数据' } },
    { path: '/institution/publications', component: () => import('./views/OperatorPublications.vue'),
      props: { audience: 'institution' }, meta: { section: 'institution', title: '消息公告' } },
    { path: '/institution/alerts', component: () => import('./views/MonitorAlerts.vue'),
      props: { audience: 'institution' }, meta: { section: 'institution', title: '预警消息' } },
    { path: '/institution/material-expiries/:id?', component: () => import('./views/MaterialExpiryList.vue'),
      props: { audience: 'institution' }, meta: { section: 'institution', title: '材料有效期' } },
    { path: '/institution/assessment-dues/:id?', component: () => import('./views/AssessmentDueList.vue'),
      props: { audience: 'institution' }, meta: { section: 'institution', title: '测评到期台账' } },
    { path: '/operator/workbench', component: scopedWorkbench, props: { audience: 'operator' },
      meta: { section: 'operator', title: '我的工作台' } },
    { path: '/operator/filings', component: institutionList,
      props: { resource: 'filings', audience: 'operator' }, meta: operatorFilingMeta },
    { path: '/operator/filing-applications', component: institutionList,
      props: { resource: 'filing-applications', audience: 'operator' }, meta: operatorFilingMeta },
    { path: '/operator/filings/new', component: institutionCaseForm,
      props: { kind: 'filings', operator: true }, meta: operatorFilingMeta },
    { path: '/operator/filing-applications/new', component: filingApplicationForm,
      props: { operator: true }, meta: operatorFilingMeta },
    { path: '/operator/feedback', component: () => import('./views/BusinessFeedback.vue'),
      props: { audience: 'operator' }, meta: { section: 'operator', title: '运营者审核意见' } },
    { path: '/operator/publications', component: () => import('./views/OperatorPublications.vue'),
      props: { audience: 'operator' }, meta: { section: 'operator', title: '站内通知公告' } },
    { path: '/operator/hazards', component: () => import('./views/HazardList.vue'),
      props: { audience: 'operator' }, meta: { section: 'operator', title: '我的整改单' } },
    { path: '/operator/alerts', component: () => import('./views/MonitorAlerts.vue'),
      props: { audience: 'operator' }, meta: { section: 'operator', title: '预警处置' } },
    { path: '/operator/material-expiries/:id?', component: () => import('./views/MaterialExpiryList.vue'),
      props: { audience: 'operator' }, meta: { section: 'operator', title: '材料有效期' } },
    { path: '/operator/assessment-dues/:id?', component: () => import('./views/AssessmentDueList.vue'),
      props: { audience: 'operator' }, meta: { section: 'operator', title: '测评到期台账' } },
    { path: '/operator/supervisions/:id?', component: () => import('./views/SupervisionTasks.vue'),
      props: { operator: true }, meta: { section: 'operator', title: '监督检查自查' } },
    { path: '/operator/remediations', component: () => import('./views/OperatorRemediations.vue'),
      meta: operatorRemediationMeta },
    { path: '/operator/assessment-history', component: () => import('./views/OperatorAssessmentHistory.vue'),
      meta: { section: 'operator', title: '历次测评对比' } },
    { path: '/operator/remediations/new', component: remediationForm, props: { audience: 'operator' },
      meta: operatorRemediationMeta },
    { path: '/operator/remediations/:id/edit', component: remediationForm, props: { audience: 'operator' },
      meta: operatorRemediationMeta },
    { path: '/institution/:resource(filings|filing-applications|implementations)/:id',
      component: () => import('./views/ScopedBusinessDetail.vue'), props: { audience: 'institution' },
      meta: { section: 'institution', title: '机构业务办理' } },
    { path: '/operator/:resource(filings|filing-applications|remediations|hazards)/:id',
      component: () => import('./views/ScopedBusinessDetail.vue'), props: { audience: 'operator' },
      meta: { section: 'operator', title: '运营者业务办理' } },
    { path: '/:pathMatch(.*)*', component: () => import('./views/NotFound.vue'), meta: { title: '页面不存在' } },
  ],
  scrollBehavior: (to, from, saved) => saved ?? (to.path === from.path ? false : { top: 0 }),
})

router.beforeEach(async to => {
  const publicPage = to.path === '/login' || to.path === '/operator-register'
  if (to.path === '/login') {
    void loadSignIn().catch(() => {
      // 这里只进行预加载，实际路由加载失败由 Vue Router 报告。
    })
  }
  let actor
  try {
    actor = await refreshSession()
  } catch {
    session.value = null
    return publicPage ? true : { path: '/login', query: { redirect: to.fullPath } }
  }
  if (!actor) {
    return publicPage ? true : { path: '/login', query: { redirect: to.fullPath } }
  }
  if (actor.mustChangePassword) return to.path === '/account/password' ? true : '/account/password'
  if (to.path === '/account/password') return true
  if (!canVisit(actor, to.path)) {
    if (!publicPage) deniedRoute.value = to.path
    return actor.home
  }
  if (actor.subjectName) {
    const key = actor.portal === 'institution' ? 'institution' : 'organization'
    const other = actor.portal === 'institution' ? 'organization' : 'institution'
    if (to.query[key] !== actor.subjectName || to.query[other] !== undefined ||
      (actor.portal === 'institution' && to.query.review === 'assigned')) {
      const query = { ...to.query, [key]: actor.subjectName }
      delete query[other]
      if (actor.portal === 'institution') delete query.review
      return { path: to.path, query, hash: to.hash }
    }
  }
  return true
})

router.afterEach((to, _from, failure) => {
  if (!failure && to.redirectedFrom?.path !== deniedRoute.value) deniedRoute.value = undefined
})

window.addEventListener('grade-business-session-expired', () => {
  const current = router.currentRoute.value
  if (current.path !== '/login') {
    void router.replace({ path: '/login', query: { redirect: current.fullPath } })
  }
})

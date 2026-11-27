// 驾驶舱本地预览用的模拟接口服务，仅用于设计核对，不参与生产构建。
import { createServer } from 'node:http'

const districts = [
  { district: '柯城区', systems: 62, high: 3, medium: 2, low: 1, overdue: 2, pendingRetest: 1, riskIndex: 100, riskLevel: 'high' },
  { district: '江山市', systems: 41, high: 1, medium: 1, low: 1, overdue: 1, pendingRetest: 0, riskIndex: 60, riskLevel: 'mid' },
  { district: '龙游县', systems: 38, high: 1, medium: 1, low: 0, overdue: 0, pendingRetest: 0, riskIndex: 50, riskLevel: 'mid' },
  { district: '开化县', systems: 29, high: 0, medium: 1, low: 0, overdue: 0, pendingRetest: 0, riskIndex: 33, riskLevel: 'low' },
  { district: '常山县', systems: 24, high: 0, medium: 0, low: 1, overdue: 0, pendingRetest: 0, riskIndex: 20, riskLevel: 'low' },
  { district: '衢江区', systems: 20, high: 0, medium: 0, low: 0, overdue: 0, pendingRetest: 0, riskIndex: 10, riskLevel: 'low' },
]

const alerts = [
  { id: 'a1', hazardId: 'h1', type: 'high_risk', severity: 'high', systemName: '衢州政务云核心业务系统', organizationName: '衢州市大数据局', deadline: '2026-11-24', status: 'pending', triggeredAt: '2026-11-27T10:12:00+08:00' },
  { id: 'a2', hazardId: 'h2', type: 'high_risk', severity: 'high', systemName: '市级医疗健康信息平台', organizationName: '市卫健委', deadline: '2026-11-22', status: 'pending', triggeredAt: '2026-11-27T09:05:00+08:00' },
  { id: 'a3', hazardId: 'h3', type: 'high_risk', severity: 'medium', systemName: '工业互联网标识解析节点', organizationName: '柯城区工信局', deadline: '2026-12-02', status: 'processing', triggeredAt: '2026-11-26T16:40:00+08:00' },
  { id: 'a4', hazardId: 'h4', type: 'high_risk', severity: 'medium', systemName: '智慧交通综合管理平台', organizationName: '市交通局', deadline: '2026-12-06', status: 'pending', triggeredAt: '2026-11-26T14:20:00+08:00' },
  { id: 'a5', hazardId: 'h5', type: 'high_risk', severity: 'low', systemName: '农村信用联社核心系统', organizationName: '衢州农商行', deadline: '2026-12-12', status: 'processing', triggeredAt: '2026-11-25T11:08:00+08:00' },
]

const trend = Array.from({ length: 30 }, (_, index) => ({
  date: `2026-11-${String(index + 1).padStart(2, '0')}`,
  filingsCreated: index > 24 ? 18 + (index % 5) * 6 : Math.round(12 + (index % 4) * 5),
  reportsCreated: index > 26 ? 9 + (index % 3) * 3 : Math.round(5 + (index % 3) * 2),
  alertsTriggered: 2 + (index % 4),
  hazardsClosed: 4 + (index % 5),
}))

const statistics = {
  updatedAt: '2026-11-27T19:29:58+08:00',
  filings: { active: 986, cancelled: 32, underReview: 148, returned: 62, draft: 58, activeOrganizations: 214 },
  hazards: { high: 24, medium: 31, low: 21 },
  openHazards: { high: 5, medium: 4, low: 3 },
  reportFirstReview: { approved: 46, decided: 50 },
  trend,
  filingCoverage: { covered: 156, eligible: 200 },
  filingCoverageByDistrict: [],
  registeredAssets: { ip: 412, domain: 86, topology: 120, supplier: 34, securityEvent: 58, cloud: 96, bigData: 18 },
  highRiskSystems: [
    { filingId: 'f1', systemName: '衢州政务云核心业务系统', organizationName: '衢州市大数据局', count: 4 },
    { filingId: 'f2', systemName: '市级医疗健康信息平台', organizationName: '市卫健委', count: 3 },
    { filingId: 'f3', systemName: '工业互联网标识解析节点', organizationName: '柯城区工信局', count: 2 },
    { filingId: 'f4', systemName: '智慧交通综合管理平台', organizationName: '市交通局', count: 2 },
  ],
}

const overview = {
  filingTotal: 1286, filingsThisYear: 142, implementationOpen: 8,
  implementationInReview: 2, reportInitial: 21, reportFinal: 9,
  hazardTotal: 53, hazardClosed: 41, hazardDueWithin30Days: 6,
  tasks: [], activeAlerts: 5,
  remediation: { overdue: 3, pendingReview: 4, pendingPlanReview: 2 },
  reviewDeadlines: { overdue: 3, near: 5, upcoming: 11 },
  alertCounts: { total: 34, pending: 3, processing: 2, closed: 29 },
}

const duty = {
  total: 10, overdueHazards: 3, pendingReview: 4, pendingAlerts: 3, pendingPlanReview: 2,
  topDistrict: '柯城区', topDistrictIndex: 100,
  conclusion: '当前范围待办 12 项，其中 3 项已逾期、3 项活跃预警待处置；柯城区风险指数最高（100），建议优先督办。',
}

const delta = (current, previous, yearAgo, worse) => ({
  current, previous, yearAgo, change: current - previous,
  rate: previous > 0 ? Math.round((current - previous) * 100 / previous) : null,
  worse,
})

const comparison = {
  windowDays: 30,
  hasYearAgo: true,
  filingsCreated: delta(142, 128, 96, false),
  reportsCreated: delta(86, 74, 52, false),
  alertsTriggered: delta(35, 41, 58, true),
  hazardsClosed: delta(41, 33, 27, false),
  openHazards: delta(12, 10, 15, true),
  activeAlerts: delta(5, 6, 11, false),
  riskIndex: delta(100, 80, 120, true),
  completionRate: { current: 77, previous: 72, yearAgo: 64, change: 5, hasCurrent: true },
  scopeNote: '按当前有效备案范围统计；上期为紧邻的等长窗口，去年同期为同一区间的去年同期。',
}

const districtResponse = {
  district: null,
  counts: {
    systems: 214, high: 5, medium: 4, low: 3, overdue: 3,
    pendingRetest: 1, pendingReview: 4, pendingPlanReview: 2, pendingAlerts: 3,
  },
  risk: { index: 100, score: 220, level: 'high', weights: '逾期 40% / 高风险 40% / 待复测 20%' },
  duty,
  comparison,
  tasks: [
    { id: 't1', title: '政务云核心业务系统 备份缺失', systemName: '衢州政务云核心业务系统', organizationName: '衢州市大数据局', status: 'open', severity: 'high', deadline: '2026-11-20', overdue: true },
    { id: 't2', title: '医疗平台 审计日志留存不足', systemName: '市级医疗健康信息平台', organizationName: '市卫健委', status: 'open', severity: 'high', deadline: '2026-11-24', overdue: true },
    { id: 't3', title: '标识解析节点 管理端口暴露', systemName: '工业互联网标识解析节点', organizationName: '柯城区工信局', status: 'pending_review', severity: 'high', deadline: '2026-11-26', overdue: true },
    { id: 't4', title: '智慧交通平台 证书 30 天内过期', systemName: '智慧交通综合管理平台', organizationName: '市交通局', status: 'pending_retest', severity: 'medium', deadline: '2026-12-02', overdue: false },
    { id: 't5', title: '农商行系统 弱口令策略不合规', systemName: '农村信用联社核心系统', organizationName: '衢州农商行', status: 'open', severity: 'medium', deadline: '2026-12-09', overdue: false },
  ],
  highRiskSystems: statistics.highRiskSystems,
  alerts,
}

const session = {
  id: 'u1', name: '系统管理员', roleCode: 'ROLE_SYS_ADMIN', mustChangePassword: false,
  portal: 'admin', dataScope: 'ALL', home: '/', pages: ['/dashboard', '/filings', '/hazards', '/alerts', '/reports', '/implementations', '/remediations'], menu: ['/dashboard'],
  permissions: [], subjectId: 'u1', subjectName: '系统管理员',
}

const routes = {
  '/api/v1/auth/session': session,
  '/api/v1/dashboard/statistics': statistics,
  '/api/v1/dashboard/analytics': { industries: [], commonIssues: [] },
  '/api/v1/workbench/overview': overview,
  '/api/v1/alerts/summary': { total: 34, pending: 3, processing: 2, closed: 29 },
  '/api/v1/dashboard/risk-by-district': { items: districts, withoutDistrict: { systems: 0, high: 0, medium: 0, low: 0, overdue: 0, pendingRetest: 0 }, omittedDistricts: 0 },
  '/api/v1/dashboard/district-response': districtResponse,
}

createServer((req, res) => {
  const url = new URL(req.url ?? '/', 'http://localhost')
  const path = url.pathname
  res.setHeader('Content-Type', 'application/json; charset=utf-8')
  if (path === '/api/v1/alerts') {
    res.end(JSON.stringify({ items: alerts }))
    return
  }
  const body = routes[path]
  if (body !== undefined) {
    res.end(JSON.stringify(body))
    return
  }
  res.statusCode = 404
  res.end(JSON.stringify({ code: 'not_found', message: path }))
}).listen(18446, '127.0.0.1', () => console.log('mock api on 18446'))

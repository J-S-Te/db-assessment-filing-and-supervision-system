/* ============================================================
   U2 备案办理 · ② 抽取结果与表单回填 —— 全量工作台（P0 骨架 + P1 交互闭环）
   依据：《网络安全等级保护备案表（2025 版）》6 张表 + 《定级报告模版（2025 版）》1 份
   数据全部为演示数据，不依赖后端。
   ============================================================ */
(function () {

  var S = [
    /* ========== 表1 单位基本情况（30 项） ========== */
    { id: 't1', code: '表1', name: '单位基本情况', groups: [
      { n: '一、单位基本信息', f: [
        { l: '单位名称', t: 'text', v: '华兴证券股份有限公司', s: 'c', req: 1, src: '建设方案 P2', cfl: ['华兴证券股份有限公司', '华兴证券'] },
        { l: '备案表编号', t: 'text', v: '', s: 'm', req: 1, note: '由系统在提交备案时自动生成' },
        { l: '统一社会信用代码', t: 'text', v: '91440300100008888K', s: 'f', req: 1, src: '营业执照 P1' },
        { l: '行政区划代码', t: 'text', v: '440304', s: 'p', src: '按行政区划推得，建议核对' },
        { l: '行政区划（省 / 市 / 县）', t: 'cascade', v: ['广东省', '深圳市', '福田区'], s: 'f', req: 1, src: '建设方案 P1' },
        { l: '详细地址', t: 'text', v: '福华三路 88 号', s: 'f', req: 1 },
        { l: '邮政编码', t: 'text', v: '518048', s: 'f' }
      ]},
      { n: '二、单位负责人', f: [
        { l: '姓名', t: 'text', v: '周明', s: 'f', req: 1 },
        { l: '职务 / 职称', t: 'text', v: '副总裁', s: 'f' },
        { l: '办公电话', t: 'text', v: '0755-8888xxxx', s: 'f' },
        { l: '电子邮件', t: 'text', v: 'zhouming@huaxing.com', s: 'f' }
      ]},
      { n: '三、网络安全责任部门', f: [
        { l: '部门名称', t: 'text', v: '信息技术部', s: 'f' },
        { l: '联系人姓名', t: 'text', v: '陈志远', s: 'f', req: 1 },
        { l: '职务 / 职称', t: 'text', v: '信息技术部总监', s: 'f' },
        { l: '办公电话', t: 'text', v: '0755-8888xxxx', s: 'f' },
        { l: '移动电话', t: 'text', v: '138****6789', s: 'f' },
        { l: '电子邮件', t: 'text', v: 'chenzy@huaxing.com', s: 'f' }
      ]},
      { n: '四、数据安全管理部门', f: [
        { l: '部门名称', t: 'text', v: '数据安全管理部', s: 'f' },
        { l: '联系人姓名', t: 'text', v: '孙晓雯', s: 'f', req: 1 },
        { l: '职务 / 职称', t: 'text', v: '数据安全主管', s: 'f' },
        { l: '办公电话', t: 'text', v: '0755-8888xxxx', s: 'p' },
        { l: '移动电话', t: 'text', v: '', s: 'm' },
        { l: '电子邮件', t: 'text', v: 'sunxw@huaxing.com', s: 'f' }
      ]},
      { n: '五、单位属性', f: [
        { l: '隶属关系', t: 'select', v: '4 县（区、市、旗）', s: 'f', o: ['1 中央', '2 省（自治区、直辖市）', '3 地（区、市、州、盟）', '4 县（区、市、旗）', '9 其他'] },
        { l: '单位类型', t: 'select', v: '4 企业', s: 'f', o: ['1 党委机关', '2 政府机关', '3 事业单位', '4 企业', '9 其他'] },
        { l: '行业类别', t: 'select', v: '27 证券', s: 'f', o: ['11 电信', '21 铁路', '22 银行', '27 证券', '28 保险', '23 海关', '73 电子政务', '99 其他'] }
      ]},
      { n: '六、定级对象数量', f: [
        { l: '第二级定级对象数', t: 'text', v: '0', s: 'f' },
        { l: '第三级定级对象数', t: 'text', v: '1', s: 'f' },
        { l: '第四级定级对象数', t: 'text', v: '0', s: 'f' },
        { l: '第五级定级对象数', t: 'text', v: '', s: 'm' }
      ]}
    ]},

    /* ========== 表2 定级对象情况（17 项） ========== */
    { id: 't2', code: '表2', name: '定级对象情况', groups: [
      { n: '定级对象标识', f: [
        { l: '定级对象（系统名称）', t: 'text', v: '核心交易系统', s: 'f', req: 1, src: '建设方案 P3' },
        { l: '定级对象编号', t: 'text', v: 'DJ-2026-0001', s: 'f' },
        { l: '定级对象类型（可多选）', t: 'check', v: ['信息系统（云计算技术）'], s: 'f',
          o: ['通信网络设施', '信息系统（云计算技术）', '信息系统（移动互联技术）', '信息系统（物联网技术）', '信息系统（工业控制技术）', '信息系统（大数据技术）', '数据资源'] }
      ]},
      { n: '承载业务情况', f: [
        { l: '业务类型', t: 'select', v: '4 公众服务', s: 'f', o: ['1 生产作业', '2 指挥调度', '3 内部办公', '4 公众服务', '9 其他'] },
        { l: '业务描述', t: 'textarea', v: '支撑证券经纪、交易撮合与清算等核心业务。', s: 'f' }
      ]},
      { n: '网络服务情况', f: [
        { l: '服务范围', t: 'select', v: '10 全国', s: 'f', o: ['10 全国', '11 跨省（区、市）', '20 全省（区、市）', '21 跨地（市、区）', '30 地（市、区）内', '99 其它'] },
        { l: '服务对象', t: 'select', v: '3 两者均包括', s: 'f', o: ['1 单位内部人员', '2 社会公众人员', '3 两者均包括', '9 其他'] }
      ]},
      { n: '网络平台', f: [
        { l: '部署范围', t: 'select', v: '3 广域网', s: 'f', o: ['1 局域网', '2 城域网', '3 广域网', '9 其他'] },
        { l: '网络性质', t: 'select', v: '2 互联网', s: 'f', o: ['1 业务专网', '2 互联网'] },
        { l: '源站 IP 地址范围', t: 'text', v: '10.20.0.0/16', s: 'f' },
        { l: '域名', t: 'text', v: 'trade.huaxing.com', s: 'p', src: '建设方案 P9' },
        { l: '主要协议 / 端口', t: 'text', v: 'TCP/443、8443', s: 'p', src: '拓扑图注释' }
      ]},
      { n: '互联与运行', f: [
        { l: '网络互联情况', t: 'select', v: '3 与本单位其他系统连接', s: 'f', o: ['1 与其他行业系统连接', '2 与本行业其他单位系统连接', '3 与本单位其他系统连接', '9 其它'] },
        { l: '何时投入运行使用', t: 'date', v: '2019-08-01', s: 'f' },
        { l: '是否是分系统', t: 'select', v: '否', s: 'f', o: ['否', '是'] },
        { l: '上级系统名称（否填“无”）', t: 'text', v: '行情推送系统', s: 'c', src: '建设方案 P12', cfl: ['行情推送系统', '无'] },
        { l: '上级系统所属单位名称', t: 'text', v: '', s: 'm' }
      ]}
    ]},

    /* ========== 表3 定级情况（11 项） ========== */
    { id: 't3', code: '表3', name: '定级情况', groups: [
      { n: '一、安全保护等级确定', f: [
        { l: '业务信息安全保护等级', t: 'select', v: '第三级', s: 'f', o: ['第一级', '第二级', '第三级', '第四级', '第五级'] },
        { l: '系统服务安全保护等级', t: 'select', v: '第三级', s: 'f', o: ['第一级', '第二级', '第三级', '第四级', '第五级'] },
        { l: '定级对象安全保护等级', t: 'select', v: '第三级', s: 'f', req: 1, o: ['第二级', '第三级', '第四级', '第五级'] },
        { l: '定级时间', t: 'date', v: '2026-03-12', s: 'f' }
      ]},
      { n: '二、定级报告与专家评审', f: [
        { l: '定级报告', t: 'yn', v: { y: '有', file: '定级报告 V2.docx' }, s: 'f' },
        { l: '专家评审情况', t: 'radio', v: '已评审', s: 'f', o: ['已评审', '未评审'] }
      ]},
      { n: '三、上级行业主管部门', f: [
        { l: '是否有上级行业主管部门', t: 'select', v: '无', s: 'f', o: ['无', '有'] },
        { l: '上级行业主管部门名称', t: 'text', v: '无', s: 'f' },
        { l: '上级行业主管部门审核定级情况', t: 'radio', v: '未审核', s: 'p', o: ['已审核', '未审核'] }
      ]},
      { n: '四、填表信息', f: [
        { l: '填表人', t: 'text', v: '陈志远', s: 'f' },
        { l: '填表日期', t: 'date', v: '', s: 'm' }
      ]}
    ]},

    /* ========== 表4 新技术应用（19 项） ========== */
    { id: 't4', code: '表4', name: '新技术应用', groups: [
      { n: '云计算应用场景', f: [
        { l: '是否采用云计算', t: 'switch', v: '是', s: 'f' },
        { l: '责任主体类型', t: 'select', v: '云服务客户', s: 'f', o: ['云服务商', '云服务客户'] },
        { l: '云计算服务模式', t: 'select', v: 'IaaS', s: 'f', o: ['IaaS', 'PaaS', 'SaaS', '其他'] },
        { l: '云计算部署模式', t: 'select', v: '私有云', s: 'f', o: ['私有云', '公有云', '混合云', '其他'] },
        { l: '云平台名称 / 安全等级', t: 'text', v: '华兴金融云 / 等保三级', s: 'f' },
        { l: '平台备案编号', t: 'text', v: 'YUN-2025-0031', s: 'p' },
        { l: '云服务客户运维地点', t: 'text', v: '深圳', s: 'f' }
      ]},
      { n: '移动互联应用场景', f: [
        { l: '是否采用移动互联', t: 'switch', v: '是', s: 'f' },
        { l: '移动应用软件 / 小程序名称', t: 'text', v: '华兴证券 APP、华兴掌厅小程序', s: 'f' },
        { l: '无线通道情况', t: 'select', v: '公共 WIFI', s: 'p', o: ['公共 WIFI', '专用 WIFI', '移动通信网'] },
        { l: '移动终端情况', t: 'select', v: '通用终端', s: 'p', o: ['通用终端', '专用终端'] }
      ]},
      { n: '物联网应用场景', f: [
        { l: '是否采用物联网', t: 'switch', v: '否', s: 'f' }
      ]},
      { n: '工业控制系统应用场景', f: [
        { l: '是否采用工业控制系统', t: 'switch', v: '否', s: 'f' }
      ]},
      { n: '大数据应用场景', f: [
        { l: '是否采用大数据', t: 'switch', v: '是', s: 'f' },
        { l: '大数据系统组成（可多选）', t: 'check', v: ['大数据平台', '大数据应用', '大数据资源'], s: 'p', o: ['大数据平台', '大数据应用', '大数据资源'] },
        { l: '大数据出境情况', t: 'select', v: '无出境需求', s: 'p', o: ['无出境需求', '有出境需求'] },
        { l: '大数据应用数量', t: 'text', v: '', s: 'm' },
        { l: '大数据平台名称 / 备案编号', t: 'text', v: '', s: 'm' },
        { l: '大数据平台安全等级', t: 'text', v: '等保三级', s: 'f' }
      ]}
    ]},

    /* ========== 表5 提交材料（6 项，与 ① 上传区动态联动） ========== */
    { id: 't5', code: '表5', name: '提交材料', groups: [
      { n: '定级对象提交材料情况（有 / 无 + 附件名称）', f: [
        { l: '网络拓扑结构及说明', t: 'yn', up: 'topo', v: { y: '有', file: 'HIS网络拓扑（2026-08）.png' }, s: 'f', src: '① 上传区 · 网络拓扑结构图' },
        { l: '网络安全组织架构及管理制度清单', t: 'yn', up: 'policy', v: { y: '无', file: '' }, s: 'm' },
        { l: '网络安全建设设计方案或整改设计方案', t: 'yn', up: 'plan', v: { y: '有', file: 'HIS系统建设方案V3.2.docx' }, s: 'p', src: '① 上传区 · 系统建设方案说明书', note: '待确认：版本号与最终归档版本是否一致' },
        { l: '网络安全专用产品清单及相应安全证书', t: 'yn', v: { y: '有', file: '' }, s: 'p', note: '未抽取到证书附件，需补传' },
        { l: '使用的安全服务清单', t: 'yn', v: { y: '无', file: '' }, s: 'p' },
        { l: '行业主管部门指导定级文件', t: 'yn', v: { y: '无', file: '' }, s: 'p' }
      ]}
    ]},

    /* ========== 表6 数据摸底（12 项） ========== */
    { id: 't6', code: '表6', name: '数据摸底', groups: [
      { n: '数据基本情况', f: [
        { l: '数据名称', t: 'text', v: '客户交易信息', s: 'f', req: 1 },
        { l: '拟定数据级别', t: 'select', v: '2 重要及以上数据', s: 'f', o: ['1 一般数据', '2 重要及以上数据'] },
        { l: '数据类别', t: 'text', v: '交易数据', s: 'f' },
        { l: '数据安全责任部门', t: 'text', v: '数据安全管理部', s: 'f' },
        { l: '数据安全负责人', t: 'text', v: '孙晓雯', s: 'f' },
        { l: '个人信息涉及情况（可多选）', t: 'check', v: ['1 涉及敏感个人信息'], s: 'p', o: ['1 涉及敏感个人信息', '2 涉及未成年人的个人信息', '3 涉及一般个人信息', '4 不涉及'] },
        { l: '数据总量', t: 'text', v: '500 TB / 1200 万条', s: 'p' },
        { l: '数据月增长量', t: 'text', v: '20 TB', s: 'p' },
        { l: '数据来源（可多选）', t: 'check', v: ['1 系统采集', '2 系统产生'], s: 'p', o: ['1 系统采集', '2 系统产生', '3 人工填报', '4 交易购买', '5 共享交换'] },
        { l: '单位间数据流转情况', t: 'rows', v: ['数据来源单位：中国结算', '数据来源单位：交易所', '数据流出单位：外部审计机构'], s: 'p' },
        { l: '与其他数据处理者的交互情况', t: 'select', v: '3 共同处理', s: 'p', o: ['1 对外提供', '2 委托处理', '3 共同处理', '4 无交互'] },
        { l: '数据存储位置（可多选）', t: 'check', v: [], s: 'm', o: ['1 私有云', '2 公有云', '3 混合云', '本单位机房', '境内'] }
      ]}
    ]},

    /* ========== 定级报告（8 段） ========== */
    { id: 't7', code: '', name: '定级报告（1 份）', groups: [
      { n: '一、定级对象描述', f: [
        { l: '（一）责任主体（建设 / 运维 / 安全主管 / 定级责任单位）', t: 'textarea', s: 'f',
          v: '定级对象于 2019 年由信息技术部负责建设，目前由信息技术部负责运行维护，风险管理部为安全主管部门，信息技术部为定级责任单位，承担网络安全和数据安全保护责任。' },
        { l: '（二）定级对象构成（物理环境 / 网络结构 / 边界 / 安全措施 / 是否采用新技术）', t: 'textarea', s: 'f',
          v: '网络边界部署边界防火墙与下一代防火墙，内部划分为交易区、办公区与隔离区；采用云计算技术（私有云，安全保护等级三级）与移动互联技术。详见附件网络拓扑图。' },
        { l: '（三）承载业务（业务情况 / 系统规模 / 互联情况）+ 子系统清单', t: 'subsys', s: 'f',
          v: { desc: '承载证券经纪、交易撮合与清算等核心业务，为全公司约 8,600 名用户提供交易服务，与其他系统通过内部服务总线互联。',
               rows: [['核心交易系统', '委托、撮合、清算'], ['网上委托', 'PC / 移动端下单'], ['行情推送', '实时行情下发']] } },
        { l: '（四）承载数据（数据类别 / 级别 / 处理环节 / 流转）', t: 'textarea', s: 'f',
          v: '承载交易数据、客户基本信息及交易记录（涉及个人信息，属一般数据）。数据仅内部使用，处理活动含传输、存储、使用，无对外流转。' },
        { l: '（五）安全责任（责任单位 / 责任人）', t: 'textarea', s: 'c', src: '定级报告 P3 与安全制度清单不一致',
          cfl: ['陈志远 · 信息技术部总监', '孙晓雯 · 数据安全主管'],
          v: '网络安全保护义务由华兴证券履行，日常安全由信息技术部落实；直接负责的主管人员：陈志远（信息技术部总监）。' }
      ]},
      { n: '二、安全保护等级确定', f: [
        { l: '（一）业务信息安全保护等级', t: 'grade', s: 'f',
          v: { desc: '处理客户交易委托、成交及资金清算数据，含大量个人信息，数据级别为一般数据（含个人信息）。',
               obj: '社会秩序、公共利益', harm: '严重损害', lvl: '第三级', shade: [1, 1] } },
        { l: '（二）系统服务安全保护等级', t: 'grade', s: 'p',
          note: '侵害程度由“严重损害”推得，建议结合 7×24 服务承诺再确认',
          v: { desc: '面向投资者提供 7×24 交易与行情服务，服务中断将影响市场交易秩序与公众利益。',
               obj: '社会秩序、公共利益', harm: '严重损害', lvl: '第三级', shade: [1, 1] } },
        { l: '（三）安全保护等级确定', t: 'concl', s: 'p', note: '结论段落待定级报告定稿后回填盖章版本',
          v: { text: '定级对象安全保护等级由业务信息安全保护等级与系统服务安全保护等级较高者决定，最终确定核心交易系统网络安全保护等级为第三级。',
               rows: [['核心交易系统', '第三级', '第三级', '第三级']] } }
      ]}
    ]}
  ];

  var onlyAbnormal = false;
  var activeId = 't1';
  var EX_MAP = {};
  var abnPtr = 0;

  /* ---------- ① 上传区数据源（与表5 双向联动） ---------- */
  var UPLOADS = [
    { key: 'plan', label: '系统建设方案说明书', req: true, file: 'HIS系统建设方案V3.2.docx' },
    { key: 'topo', label: '网络拓扑结构图', req: true, file: 'HIS网络拓扑（2026-08）.png' },
    { key: 'policy', label: '安全需求与管理制度清单', req: false, file: '' },
    { key: 'other', label: '其他补充材料', req: false, file: '' }
  ];
  var UP_FAKE = {
    plan: 'HIS系统建设方案V3.2.docx',
    topo: 'HIS网络拓扑（2026-08）.png',
    policy: '网络安全管理制度清单.pdf',
    other: '其他补充材料.pdf'
  };

  /* ---------- 定级矩阵（客体 × 侵害程度 → 等级） ---------- */
  var OBJS = ['公民、法人和其他组织的合法权益', '社会秩序、公共利益', '国家安全或地区安全、国计民生'];
  var HARMS = ['一般损害', '严重损害', '特别严重损害'];
  var LVL = ['第一级', '第二级', '第三级', '第四级', '第五级'];
  var GRADE_ROWS = [[1, 2, 2], [2, 3, 4], [4, 5, 5]];


  /* ---------- 初始化 uid ---------- */
  var seq = 0;
  S.forEach(function (sec) {
    sec.groups.forEach(function (g) {
      g.f.forEach(function (f) {
        f.uid = 'exf' + (seq++);
        EX_MAP[f.uid] = f;
      });
    });
  });

  /* ---------- 工具 ---------- */
  function esc(v) {
    return String(v == null ? '' : v).replace(/&/g, '&amp;').replace(/</g, '&lt;')
      .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }
  function isDone(s) { return s === 'f' || s === 'h'; }
  function tag(s) {
    if (s === 'f') return '<span class="src-tag hi">高置信</span>';
    if (s === 'p') return '<span class="src-tag mid">待确认</span>';
    if (s === 'm') return '<span class="src-tag low">缺失</span>';
    if (s === 'h') return '<span class="src-tag man">人工</span>';
    return '<span class="src-tag cfl">冲突</span>';
  }
  /* 计入进度的字段：技术场景为「否」时，其明细不计入（否则永远到不了 100%） */
  function flat(sec) {
    var out = [];
    sec.groups.forEach(function (g) {
      var isTech = g.f[0] && g.f[0].t === 'switch';
      var off = isTech && g.f[0].v === '否';
      out.push(g.f[0]);
      if (!off) { for (var i = 1; i < g.f.length; i++) out.push(g.f[i]); }
    });
    return out;
  }
  function stats() {
    var o = { total: 0, active: 0, na: 0, filled: 0, p: 0, m: 0, c: 0 };
    S.forEach(function (sec) {
      sec.groups.forEach(function (g) { o.total += g.f.length; });
      flat(sec).forEach(function (f) {
        o.active++;
        if (isDone(f.s)) o.filled++;
        else if (f.s === 'p') o.p++;
        else if (f.s === 'm') o.m++;
        else if (f.s === 'c') o.c++;
      });
    });
    o.na = o.total - o.active;
    o.pending = o.p + o.m + o.c;
    return o;
  }
  function secStats(sec) {
    var o = { total: 0, filled: 0, active: 0, pending: 0, block: 0 };
    sec.groups.forEach(function (g) { o.total += g.f.length; });
    flat(sec).forEach(function (f) {
      o.active++;
      if (isDone(f.s)) o.filled++;
      else { o.pending++; if (f.s === 'm' || f.s === 'c') o.block++; }
    });
    return o;
  }
  function dotCls(st) {
    if (st.block > 0) return 'bad';
    if (st.pending > 0) return 'warn';
    return 'ok';
  }
  function label(sec) { return (sec.code ? sec.code + ' ' : '') + sec.name; }

  /* ---------- 控件渲染 ---------- */
  function ctrl(f) {
    var v = f.v;
    switch (f.t) {
      case 'textarea':
        return '<textarea class="input" onchange="exMarkManual(\'' + f.uid + '\',this)">' + esc(v) + '</textarea>';
      case 'select':
        return '<select class="input" onchange="exMarkManual(\'' + f.uid + '\',this)">' +
          f.o.map(function (o) { return '<option' + (o === v ? ' selected' : '') + '>' + esc(o) + '</option>'; }).join('') +
          '</select>';
      case 'date':
        return '<input class="input" type="date" value="' + esc(v) + '" onchange="exMarkManual(\'' + f.uid + '\',this)" />';
      case 'radio':
        return '<div class="ex-chk">' + f.o.map(function (o) {
          return '<label><input type="radio" name="' + f.uid + '"' + (o === v ? ' checked' : '') +
            ' onchange="exMarkManual(\'' + f.uid + '\',this)" />' + esc(o) + '</label>';
        }).join('') + '</div>';
      case 'check':
        return '<div class="ex-chk">' + f.o.map(function (o) {
          return '<label><input type="checkbox"' + (v.indexOf(o) > -1 ? ' checked' : '') +
            ' onchange="exMarkManual(\'' + f.uid + '\',this)" />' + esc(o) + '</label>';
        }).join('') + '</div>';
      case 'cascade':
        return '<div class="ex-cas">' + v.map(function (x, i) {
          return '<input class="input" value="' + esc(x) + '" placeholder="' + ['省 / 自治区 / 直辖市', '地市 / 州 / 盟', '区县 / 市 / 旗'][i] +
            '" onchange="exMarkManual(\'' + f.uid + '\',this)" />';
        }).join('') + '</div>';
      case 'yn':
        return '<div class="ex-yn">' +
          '<select class="input" style="width:104px;flex:none;" onchange="exMarkManual(\'' + f.uid + '\',this)">' +
          ['有', '无'].map(function (o) { return '<option' + (o === v.y ? ' selected' : '') + '>' + o + '</option>'; }).join('') +
          '</select>' +
          '<input class="input" value="' + esc(v.file) + '" placeholder="附件名称" onchange="exMarkManual(\'' + f.uid + '\',this)" />' +
          '</div>';
      case 'rows':
        return '<div class="ex-rows">' + v.map(function (x) {
          return '<input class="input" value="' + esc(x) + '" onchange="exMarkManual(\'' + f.uid + '\',this)" />';
        }).join('') + '<button type="button" class="ex-mini" onclick="exAddRow(\'' + f.uid + '\')">＋ 添加一行</button></div>';
      case 'subsys':
        return '<textarea class="input" onchange="exMarkManual(\'' + f.uid + '\',this)">' + esc(v.desc) + '</textarea>' +
          '<table class="ex-mat" style="margin-top:8px;width:100%;">' +
          '<thead><tr><th style="width:48px;">序号</th><th>子系统名称</th><th>业务功能描述</th></tr></thead><tbody>' +
          v.rows.map(function (r, i) {
            return '<tr><td>' + (i + 1) + '</td><td>' + esc(r[0]) + '</td><td>' + esc(r[1]) + '</td></tr>';
          }).join('') + '</tbody></table>' +
          '<button type="button" class="ex-mini" style="margin-top:8px;" onclick="exAddSubsys(\'' + f.uid + '\')">＋ 添加子系统</button>';
      case 'grade':
        return '<textarea class="input" onchange="exMarkManual(\'' + f.uid + '\',this)">' + esc(v.desc) + '</textarea>' +
          '<div class="ex-cas" style="margin-top:8px;">' +
          exSel(['公民、法人和其他组织的合法权益', '社会秩序、公共利益', '国家安全或地区安全、国计民生'], v.obj, f.uid) +
          exSel(['一般损害', '严重损害', '特别严重损害'], v.harm, f.uid) +
          exSel(['第一级', '第二级', '第三级', '第四级', '第五级'], v.lvl, f.uid) +
          '</div>' + matrixHtml(v.shade);
      case 'concl':
        return '<textarea class="input" onchange="exMarkManual(\'' + f.uid + '\',this)">' + esc(v.text) + '</textarea>' +
          '<table class="ex-mat" style="margin-top:8px;width:100%;"><thead><tr><th>定级对象名称</th><th>安全保护等级</th><th>业务信息安全</th><th>系统服务安全</th></tr></thead><tbody>' +
          v.rows.map(function (r) {
            return '<tr>' + r.map(function (c, i) { return '<td' + (i === 0 ? ' style="text-align:left;font-weight:600;"' : '') + '>' + esc(c) + '</td>'; }).join('') + '</tr>';
          }).join('') + '</tbody></table>';
      default:
        return '<input class="input" value="' + esc(v) + '"' + (v ? '' : ' placeholder="待补充"') +
          ' onchange="exMarkManual(\'' + f.uid + '\',this)" />';
    }
  }
  function exSel(opts, val, uid) {
    return '<select class="input" onchange="exMarkManual(\'' + uid + '\',this)">' +
      opts.map(function (o) { return '<option' + (o === val ? ' selected' : '') + '>' + o + '</option>'; }).join('') +
      '</select>';
  }
  function matrixHtml(shade) {
    var h = '<div class="ex-note" style="margin-top:8px;">表：受破坏时侵害的客体 × 侵害程度 → 等级（随上方三项选择自动同步）</div>';
    h += '<table class="ex-mat" style="margin-top:4px;width:100%;"><thead><tr><th style="text-align:left;width:42%;">受破坏时侵害的客体</th><th>一般损害</th><th>严重损害</th><th>特别严重损害</th></tr></thead><tbody>';
    OBJS.forEach(function (o, ri) {
      h += '<tr><td style="text-align:left;font-weight:600;">' + o + '</td>';
      GRADE_ROWS[ri].forEach(function (lv, ci) {
        var on = shade && shade[0] === ri && shade[1] === ci;
        h += '<td class="mc' + (on ? ' shade' : '') + '">' + LVL[lv - 1] + '</td>';
      });
      h += '</tr>';
    });
    return h + '</tbody></table>';
  }
  /* 客体 / 侵害程度 变化 → 反推等级与矩阵涂黑位置 */
  function gradeSync(f) {
    var oi = OBJS.indexOf(f.v.obj), hi = HARMS.indexOf(f.v.harm);
    if (oi < 0) oi = 0;
    if (hi < 0) hi = 0;
    f.v.lvl = LVL[GRADE_ROWS[oi][hi] - 1];
    f.v.shade = [oi, hi];
  }

  function fieldHtml(f) {
    var full = ['textarea', 'check', 'rows', 'cascade', 'grade', 'concl', 'subsys'].indexOf(f.t) > -1;
    var cls = 'ex-fld' + (full ? ' full' : '') + (f.s === 'm' ? ' is-m' : '') + (f.s === 'c' ? ' is-c' : '') + (f.up ? ' is-up' : '');
    var h = '<div class="' + cls + '" id="' + f.uid + '">';
    h += '<div class="ex-lb"><span>' + esc(f.l) + '</span>' + (f.req ? '<span class="req">*</span>' : '') + tag(f.s) +
      (f.up ? '<span class="ex-badge-up">① 联动</span>' : '') + '</div>';
    h += ctrl(f);
    if (f.src) h += '<div class="ex-note src-line"><span>来源：' + esc(f.src) + '</span></div>';
    if (f.note) h += '<div class="ex-note"><span>' + esc(f.note) + '</span></div>';
    if (f.s === 'c' && f.cfl) {
      h += '<div class="ex-cfl"><span style="font-size:11px;color:var(--c-text-3);">采信口径：</span>' +
        f.cfl.map(function (o, i) {
          return '<span class="opt" onclick="exResolve(\'' + f.uid + '\',' + i + ')">' + esc(o) + '</span>';
        }).join('') + '</div>';
    }
    if (f.s === 'm') {
      h += '<div class="ex-note ex-mnote">' + (f.up
        ? '<span class="lk" onclick="exGoUpload(\'' + f.up + '\')">去 ① 上传该材料（上传后自动判定「有」）</span>'
        : '<span class="lk" onclick="exFillMissing(\'' + f.uid + '\')">补录（演示：填入默认值并标记为人工）</span>') + '</div>';
    }
    if (f.s === 'p') {
      h += '<div class="ex-act"><span class="lk-ok" onclick="exConfirmField(\'' + f.uid + '\')">✓ 确认此项无误</span></div>';
    }
    return h + '</div>';
  }
  function sectTag(f, s) {
    return '<span>' + esc(f.l) + '</span>' + (f.req ? '<span class="req">*</span>' : '') + tag(s) +
      (f.up ? '<span class="ex-badge-up">① 联动</span>' : '');
  }

  /* ---------- 三大区域渲染 ---------- */
  function renderChips() {
    var box = document.getElementById('ex-chips');
    if (!box) return;
    var all = stats();
    var h = '';
    S.forEach(function (sec) {
      var st = secStats(sec);
      var cls = 'ex-chip' + (st.block > 0 ? ' is-bad' : (st.pending === 0 ? ' is-ok' : '')) + (activeId === sec.id ? ' on' : '');
      h += '<div class="' + cls + '" onclick="exGoSection(\'' + sec.id + '\')">' +
        '<span class="n">' + label(sec) + '</span>' +
        '<span class="v">' + st.filled + ' / ' + st.active + ' 项' + (st.pending ? ' · ' + st.pending + ' 待处理' : ' · 已完成') + '</span></div>';
    });
    h += '<div class="ex-chip ex-filter' + (onlyAbnormal ? ' on' : '') + '" onclick="exToggleOnlyAbnormal()">' +
      (onlyAbnormal ? '✓ 正在只看异常' : '仅看异常 ' + all.pending + ' 项') + '</div>';
    box.innerHTML = h;
  }

  function renderNav() {
    var box = document.getElementById('ex-nav');
    if (!box) return;
    var h = '<div class="ex-nav-t">章节目录 · 6 张表 + 1 份报告</div>';
    S.forEach(function (sec) {
      var st = secStats(sec);
      h += '<div class="ex-nav-i' + (activeId === sec.id ? ' on' : '') + '" onclick="exGoSection(\'' + sec.id + '\')">' +
        '<span class="ex-dot ' + dotCls(st) + '"></span>' +
        '<span class="nm">' + label(sec) + '</span>' +
        '<span class="ct">' + st.filled + '/' + st.active + '</span></div>';
    });
    h += '<div class="ex-nav-t" style="padding-top:12px;">状态</div>' +
      '<div class="ex-nav-i" style="cursor:default;"><span class="ex-dot ok"></span><span class="nm">已回填 ' + stats().filled + ' 项</span></div>' +
      '<div class="ex-nav-i" style="cursor:default;"><span class="ex-dot bad"></span><span class="nm">待处理 ' + stats().pending + ' 项</span></div>';
    box.innerHTML = h;
  }

  function renderBody() {
    var box = document.getElementById('ex-body');
    if (!box) return;
    var sec = S.filter(function (x) { return x.id === activeId; })[0];
    if (!sec) return;
    var st = secStats(sec);
    var h = '<div class="ex-body-hd">' +
      '<div><span class="ex-body-t">' + label(sec) + '</span>' +
      '<span class="ex-body-s">' + st.active + ' 项 · ' + st.filled + ' 项已回填 · ' + st.pending + ' 项待处理' +
      (st.total - st.active > 0 ? ' · ' + (st.total - st.active) + ' 项因场景为「否」无需填写' : '') + '</span></div>' +
      (st.pending ? '<button class="ex-mini" onclick="exConfirmSection(\'' + sec.id + '\')">本表待确认项一键确认</button>' : '') +
      '</div>';

    var shown = 0;
    sec.groups.forEach(function (g, gi) {
      var isTech = g.f[0] && g.f[0].t === 'switch';
      var sw = isTech ? g.f[0] : null;
      var swOff = isTech && sw && sw.v === '否';
      var body = (isTech && !swOff) ? g.f.slice(1) : (isTech ? [] : g.f);
      var list = body.filter(function (f) { return !onlyAbnormal || !isDone(f.s); });
      var swHidden = onlyAbnormal && sw && isDone(sw.s);
      if (list.length === 0 && swHidden) return;
      if (list.length === 0 && isTech && sw && !swHidden) {
        h += '<div class="ex-sec"><div class="ex-sec-hd">' +
          '<span class="ex-sec-t">' + esc(g.n) + '</span>' +
          '<span class="ex-sw">' + tag(sw.s) +
          '<select class="input" style="width:88px;" onchange="exMarkManual(\'' + sw.uid + '\',this)">' +
          ['是', '否'].map(function (o) { return '<option' + (o === sw.v ? ' selected' : '') + '>' + o + '</option>'; }).join('') +
          '</select><span class="ex-sec-off">选择「否」时本节无需填写，不计入进度</span></span></div></div>';
        shown++;
        return;
      }
      if (list.length === 0) return;
      var pend = swOff ? 0 : body.filter(function (f) { return !isDone(f.s); }).length;
      h += '<div class="ex-sec"><div class="ex-sec-hd">' +
        '<span class="ex-sec-t">' + esc(g.n) + '</span>' +
        (isTech && sw ? '<span class="ex-sw">' + tag(sw.s) +
          '<select class="input" style="width:88px;" onchange="exMarkManual(\'' + sw.uid + '\',this)">' +
          ['是', '否'].map(function (o) { return '<option' + (o === sw.v ? ' selected' : '') + '>' + o + '</option>'; }).join('') +
          '</select></span>' : '') +
        '<span class="ex-sec-m">' + list.length + ' 项' + (pend ? ' · ' + pend + ' 待处理' : '') + '</span>' +
        (pend ? '<button class="ex-mini" onclick="exConfirmGroup(\'' + sec.id + '\',' + gi + ')">本节确认</button>' : '') +
        '</div><div class="ex-grid">' + list.map(fieldHtml).join('') + '</div></div>';
      shown++;
    });

    if (shown === 0) {
      h += '<div class="ex-empty">本表已无异常项。<button class="ex-mini" onclick="exToggleOnlyAbnormal()">返回全部字段</button></div>';
    }
    h += '<div class="ex-body-foot">';
    if (onlyAbnormal) {
      var all = abnList();
      var cur = Math.min(abnPtr, Math.max(0, all.length - 1));
      h += '<span class="ex-abn">' +
        '<button class="ex-mini" onclick="exGoAbnormal(-1)">← 上一异常</button>' +
        '<span class="ex-abn-i">异常 ' + (all.length ? cur + 1 : 0) + ' / ' + all.length + '</span>' +
        '<button class="ex-mini" onclick="exGoAbnormal(1)">下一异常 →</button></span>';
    }
    h += '<span class="ex-foot-sp"></span>' +
      '<button class="btn btn-sm" onclick="exPrevSection()">← 上一章</button>' +
      '<button class="btn btn-sm" onclick="exNextSection()">下一章 →</button></div>';
    box.innerHTML = h;
  }

  function renderFoot() {
    var o = stats();
    var t = document.getElementById('ex-progress-txt');
    if (t) t.textContent = '已回填 ' + o.filled + ' / ' + o.active + ' 项 · 待处理 ' + o.pending + ' 项';
    var b = document.getElementById('ex-bar-fill');
    if (b) b.style.width = (o.active ? (o.filled / o.active * 100) : 0).toFixed(1) + '%';
    var s = document.getElementById('ex-submit');
    if (s) {
      var block = o.m + o.c;
      if (block > 0) {
        s.textContent = '提交备案 · ' + block + ' 项需先处理';
        s.classList.add('is-blocked');
      } else {
        s.textContent = '确认无误，提交备案';
        s.classList.remove('is-blocked');
      }
    }
    var sum = document.getElementById('ex-sum');
    if (sum) sum.innerHTML = '共 <strong>' + o.total + '</strong> 项 · 已回填 ' + o.filled + ' · 待确认 ' + o.p +
      ' · 缺失 ' + o.m + ' · 冲突 ' + o.c + (o.na ? ' · ' + o.na + ' 项无需填写' : '');
    var hint = document.getElementById('u2-parse-hint');
    if (hint) hint.innerHTML = '已解析 ' + UPLOADS.filter(function (u) { return u.file; }).length + ' 份材料，共提取 ' + o.total +
      ' 项：<b>' + o.filled + '</b> 项已回填 · <b>' + o.p + '</b> 项待确认 · <b>' + o.m + '</b> 项缺失 · <b>' + o.c +
      '</b> 项冲突' + (o.na ? '（另有 ' + o.na + ' 项因场景为「否」无需填写）' : '') + '。';
  }

  function renderAll() { renderChips(); renderNav(); renderBody(); renderFoot(); }

  /* ---------- 交互 ---------- */
  function abnList() {
    var out = [];
    S.forEach(function (sec) {
      flat(sec).forEach(function (f) { if (!isDone(f.s)) out.push({ sec: sec, f: f }); });
    });
    return out;
  }
  function abnIndexOf(secId) {
    var list = abnList();
    for (var i = 0; i < list.length; i++) if (list[i].sec.id === secId) return i;
    return -1;
  }
  function flashEl(el) {
    if (!el) return;
    el.classList.add('flash');
    setTimeout(function () { el.classList.remove('flash'); }, 1500);
  }
  function focusAbn(i, doScroll) {
    var list = abnList();
    if (!list[i]) return;
    var it = list[i];
    abnPtr = i;
    activeId = it.sec.id;
    renderAll();
    if (doScroll) {
      var el = document.getElementById(it.f.uid);
      if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
      flashEl(el);
    }
  }
  /* 单字段处理完毕后：进度联动 + 仅看异常模式下自动落到下一项 */
  function afterFieldDone() {
    renderAll();
    if (!onlyAbnormal) return;
    var list = abnList();
    if (!list.length) { if (typeof toast === 'function') toast('全部异常项已处理完毕'); return; }
    focusAbn(Math.min(abnPtr, list.length - 1), true);
  }

  window.exGoSection = function (id) {
    activeId = id;
    if (onlyAbnormal) {
      var k = abnIndexOf(id);
      if (k > -1) abnPtr = k;
    }
    renderAll();
    var body = document.getElementById('ex-body');
    if (body && body.scrollIntoView) body.scrollIntoView({ block: 'nearest' });
  };
  window.exPrevSection = function () {
    var i = S.findIndex(function (x) { return x.id === activeId; });
    exGoSection(S[Math.max(0, i - 1)].id);
  };
  window.exNextSection = function () {
    var i = S.findIndex(function (x) { return x.id === activeId; });
    exGoSection(S[Math.min(S.length - 1, i + 1)].id);
  };
  window.exToggleOnlyAbnormal = function () {
    onlyAbnormal = !onlyAbnormal;
    if (onlyAbnormal) {
      var cur = S.filter(function (x) { return x.id === activeId; })[0];
      if (secStats(cur).pending === 0) {
        var first = S.filter(function (x) { return secStats(x).pending > 0; })[0];
        if (first) activeId = first.id;
      }
      var k = abnIndexOf(activeId);
      abnPtr = k > -1 ? k : 0;
    }
    renderAll();
    if (onlyAbnormal) {
      var list = abnList();
      var el = list[abnPtr] && document.getElementById(list[abnPtr].f.uid);
      if (el && el.scrollIntoView) el.scrollIntoView({ block: 'center', behavior: 'smooth' });
      if (typeof toast === 'function') toast('已切换：仅显示异常项，用底部「上一异常 / 下一异常」逐项处理');
    } else if (typeof toast === 'function') toast('已切换：显示全部字段');
  };
  window.exGoAbnormal = function (dir) {
    var list = abnList();
    if (!list.length) { if (typeof toast === 'function') toast('已无待处理项'); return; }
    var i = abnPtr + dir;
    if (i < 0) { i = 0; if (typeof toast === 'function') toast('已经是第一项异常'); }
    if (i > list.length - 1) { i = list.length - 1; if (typeof toast === 'function') toast('已经是最后一项异常'); }
    focusAbn(i, true);
  };

  /* 控件 → 数据模型 回写（切换章节不丢编辑） */
  function capture(f, el, key) {
    var box = document.getElementById(f.uid);
    function vals(sel) {
      if (!box) return [];
      return Array.prototype.slice.call(box.querySelectorAll(sel)).map(function (i) { return i.value; });
    }
    switch (f.t) {
      case 'text': case 'textarea': case 'date': case 'select': case 'radio': case 'switch':
        f.v = el.value; break;
      case 'subsys': f.v.desc = el.value; break;
      case 'concl': f.v.text = el.value; break;
      case 'check':
        if (box) {
          f.v = Array.prototype.slice.call(box.querySelectorAll('input[type=checkbox]:checked'))
            .map(function (i) { return i.parentNode.textContent.trim(); });
        }
        break;
      case 'cascade': f.v = vals('.ex-cas input'); break;
      case 'rows': f.v = vals('.ex-rows input'); break;
      case 'yn':
        var sy = box && box.querySelector('.ex-yn select');
        var fi = box && box.querySelector('.ex-yn input');
        f.v = { y: sy ? sy.value : f.v.y, file: fi ? fi.value : f.v.file };
        break;
      case 'grade':
        if (key) f.v[key] = el.value;
        break;
    }
  }
  window.exMarkManual = function (uid, el, key) {
    var f = EX_MAP[uid];
    if (!f) return;
    capture(f, el, key);
    if (f.t === 'switch') { renderAll(); return; }
    if (f.t === 'grade') {
      if (key === 'obj' || key === 'harm') gradeSync(f);
      renderAll();
      return;
    }
    // 冲突项必须显式选择采信口径，不能靠“打字”静默消解
    if (f.s === 'c') { renderChips(); renderNav(); renderFoot(); return; }
    if (f.s !== 'h') f.s = 'h';
    var box = document.getElementById(uid);
    if (box) {
      var lb = box.querySelector('.ex-lb');
      if (lb) lb.innerHTML = sectTag(f, 'h');
      box.classList.remove('is-m', 'is-c');
      var act = box.querySelector('.ex-act');
      if (act) act.parentNode.removeChild(act);
      var mnote = box.querySelector('.ex-mnote');
      if (mnote) mnote.parentNode.removeChild(mnote);
      var cfl = box.querySelector('.ex-cfl');
      if (cfl) cfl.parentNode.removeChild(cfl);
    }
    renderChips(); renderNav(); renderFoot();
  };
  /* 逐字段确认：P1 的核心动作 */
  window.exConfirmField = function (uid) {
    var f = EX_MAP[uid];
    if (!f) return;
    if (f.s === 'c') { if (typeof toast === 'function') toast('冲突项请先在上方选择采信口径'); return; }
    if (f.s === 'm') { if (typeof toast === 'function') toast('缺失项请先补录或上传材料'); return; }
    if (isDone(f.s)) return;
    f.s = 'h';
    afterFieldDone();
  };
  window.exResolve = function (uid, idx) {
    var f = EX_MAP[uid];
    if (!f || !f.cfl) return;
    if (f.t === 'textarea') f.v = '网络安全保护义务由华兴证券履行，日常安全由信息技术部落实；直接负责的主管人员：' + f.cfl[idx] + '。';
    else f.v = f.cfl[idx];
    f.s = 'h';
    afterFieldDone();
    if (typeof toast === 'function') toast('已采信：' + f.cfl[idx]);
  };
  window.exFillMissing = function (uid) {
    var f = EX_MAP[uid];
    if (!f) return;
    if (f.t === 'yn') f.v = { y: '有', file: '待上传附件.pdf' };
    else if (f.t === 'date') f.v = '2026-03-15';
    else f.v = '待人工补充';
    f.s = 'h';
    afterFieldDone();
    if (typeof toast === 'function') toast('已补录并标记为「人工」，请核对后提交');
  };
  window.exConfirmGroup = function (secId, gi) {
    var sec = S.filter(function (x) { return x.id === secId; })[0];
    if (!sec) return;
    var g = sec.groups[gi];
    var isTech = g.f[0] && g.f[0].t === 'switch';
    var off = isTech && g.f[0].v === '否';
    var n = 0;
    (off ? [g.f[0]] : g.f).forEach(function (f) { if (f.s === 'p') { f.s = 'h'; n++; } });
    afterFieldDone();
    if (typeof toast === 'function') toast(n ? '已确认 ' + n + ' 项' : '本节无待确认项');
  };
  window.exConfirmSection = function (secId) {
    var sec = S.filter(function (x) { return x.id === secId; })[0];
    if (!sec) return;
    var n = 0;
    flat(sec).forEach(function (f) { if (f.s === 'p') { f.s = 'h'; n++; } });
    afterFieldDone();
    if (typeof toast === 'function') toast(n ? '已确认 ' + n + ' 项待确认字段' : '本表无待确认项');
  };
  window.exConfirmAllPending = function () {
    var n = 0;
    S.forEach(function (sec) { flat(sec).forEach(function (f) { if (f.s === 'p') { f.s = 'h'; n++; } }); });
    renderAll();
    if (typeof toast === 'function') toast(n ? '已批量确认 ' + n + ' 项待确认字段' : '没有可批量确认的项');
  };
  window.exAddRow = function (uid) {
    var f = EX_MAP[uid]; if (!f) return;
    f.v.push(''); f.s = 'h'; renderBody(); renderChips(); renderNav(); renderFoot();
  };
  window.exAddSubsys = function (uid) {
    var f = EX_MAP[uid]; if (!f) return;
    f.v.rows.push(['待补充', '待补充']); f.s = 'h'; renderBody(); renderChips(); renderNav(); renderFoot();
  };
  /* ---------- ① 上传区 ↔ 表5 双向联动 ---------- */
  function upOf(key) {
    var r = null;
    UPLOADS.forEach(function (u) { if (u.key === key) r = u; });
    return r;
  }
  function renderUploads() {
    var box = document.getElementById('up-grid');
    if (!box) return;
    box.innerHTML = UPLOADS.map(function (u) {
      var h = '<div class="up-card' + (u.file ? ' has-file' : '') + '" id="upc-' + u.key + '">' +
        '<div class="uh"><span>' + esc(u.label) + (u.req ? ' <span class="req-star">*</span>' : '') + '</span>' +
        (u.file ? '<span class="up-ok">已上传</span>' : '<span class="up-no">待上传</span>') + '</div>';
      if (u.file) {
        h += '<div class="ufile">' + esc(u.file) + '<span class="x" title="移除该材料" onclick="exRemoveUpload(\'' + u.key + '\')">×</span></div>';
      } else {
        h += '<div class="upload" style="padding:10px" onclick="exAddUpload(\'' + u.key + '\')">＋ 上传</div>';
      }
      return h + '</div>';
    }).join('');
  }
  /* 把上传状态写回表5；返回变更的字段数 */
  function syncUploads() {
    var sec = S.filter(function (s) { return s.id === 't5'; })[0];
    if (!sec) return 0;
    var n = 0;
    sec.groups.forEach(function (g) {
      g.f.forEach(function (f) {
        if (!f.up) return;
        var u = upOf(f.up);
        if (!u) return;
        if (u.file) {
          if (f.s !== 'h') f.s = (f.up === 'plan') ? 'p' : 'f';
          f.v = { y: '有', file: u.file };
          f.src = '① 上传区 · ' + u.label;
          f.note = (f.up === 'plan') ? '待确认：版本号与最终归档版本是否一致' : '';
          n++;
        } else {
          f.s = 'm';
          f.v = { y: '无', file: '' };
          f.src = '';
          f.note = '① 上传区「' + u.label + '」未上传，补传后自动判定为「有」';
          n++;
        }
      });
    });
    return n;
  }
  window.exAddUpload = function (key) {
    var u = upOf(key);
    if (!u) return;
    u.file = UP_FAKE[key] || (u.label + '.pdf');
    renderUploads();
    syncUploads();
    renderAll();
    if (typeof toast === 'function') toast('已上传「' + u.label + '」，表5 已自动判定为「有」并回填附件名');
  };
  window.exRemoveUpload = function (key) {
    var u = upOf(key);
    if (!u || !u.file) return;
    var old = u.file;
    u.file = '';
    renderUploads();
    syncUploads();
    renderAll();
    if (typeof toast === 'function') toast('已移除「' + old + '」，表5 对应项转为「缺失」');
  };
  window.exGoUpload = function (key) {
    var card = document.getElementById('upc-' + key);
    if (!card) return;
    if (card.scrollIntoView) card.scrollIntoView({ block: 'center', behavior: 'smooth' });
    flashEl(card);
    if (typeof toast === 'function') toast('请在①上传区补充该材料，上传后表5 自动同步');
  };

  window.exTrySubmit = function () {
    var o = stats();
    if (o.m + o.c > 0) {
      var list = abnList();
      var k = -1;
      for (var i = 0; i < list.length; i++) {
        if (list[i].f.s === 'm' || list[i].f.s === 'c') { k = i; break; }
      }
      if (k > -1) {
        onlyAbnormal = true;
        focusAbn(k, true);
      }
      if (typeof toast === 'function') toast('还有 ' + (o.m + o.c) + ' 项缺失 / 冲突需处理，已定位到第一项');
      return;
    }
    if (o.p > 0 && typeof toast === 'function') { toast('还有 ' + o.p + ' 项待确认，建议先确认再提交'); }
    if (typeof openModal === 'function') openModal('m-u2-filing-submit');
  };

  /* ---------- 启动 ---------- */
  function boot() {
    if (!document.getElementById('ex-chips')) return;
    renderUploads();
    syncUploads();   // 表5 初始状态由 ① 上传区推导
    renderAll();
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot);
  else boot();

})();

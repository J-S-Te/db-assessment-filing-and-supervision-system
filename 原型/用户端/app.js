// 用户端（网络运营者）原型交互 —— 与机构端/管理端共用同一套交互约定
(function () {
  var pages = ['p-u1', 'p-u2', 'p-u3', 'p-u4', 'p-flow'];
  var navMap = { 'p-u1': 'sid-u1', 'p-u2': 'sid-u2', 'p-u3': 'sid-u3', 'p-u4': 'sid-u4' };
  // 顶栏面包屑：显示当前页（与机构端/管理端一致，避免与侧栏品牌信息重复）
  var pageTitles = {
    'p-u1': '用户端 · U1 首页',
    'p-u2': '用户端 · U2 备案办理',
    'p-u3': '用户端 · U3 测评与整改',
    'p-u4': '用户端 · U4 消息中心',
    'p-flow': '全局 · 流程示意'
  };

  function $(id) { return document.getElementById(id); }

  window.goto = function (pageId) {
    pages.forEach(function (p) {
      var el = $(p); if (el) el.classList.toggle('active', p === pageId);
    });
    // 导航高亮：流程示意为全局，不高亮具体业务菜单
    Object.keys(navMap).forEach(function (k) {
      var ni = $(navMap[k]); if (ni) ni.classList.toggle('active', k === pageId);
    });
    var crumb = $('crumb'); if (crumb && pageTitles[pageId]) crumb.textContent = pageTitles[pageId];
    // URL 统一用「#/p-xxx」形式：若 hash 与元素 id 完全同名（#p-u1），浏览器会在 load 阶段
    // 对当前 URL 的 fragment 做一次锚点滚动，把页面顶到该元素位置、吃掉顶部内容。
    // 加 "/" 前缀后 fragment 不再是有效元素 id，浏览器不再滚动；replaceState 也不会触发滚动。
    var hash = '#/' + pageId;
    if (location.hash !== hash) history.replaceState(null, '', hash);
    closeDrawer();
    window.scrollTo(0, 0);
  };

  window.switchTab = function (tabsId, tabId) {
    var tabs = $(tabsId); if (!tabs) return;
    tabs.querySelectorAll('.tab').forEach(function (t) {
      t.classList.toggle('active', t.getAttribute('data-tab') === tabId);
    });
    // pane 与 tab 同文档，按 id 直接定位（tabId 即 pane id）
    document.querySelectorAll('.tab-pane').forEach(function (p) {
      p.classList.toggle('active', p.id === tabId);
    });
  };

  window.openModal = function (id) {
    var m = $(id); if (m) m.classList.add('open');
  };
  window.closeModal = function (id) {
    var m = $(id); if (m) m.classList.remove('open');
  };

  var toastTimer;
  window.toast = function (msg) {
    var t = $('toast'); if (!t) {
      t = document.createElement('div'); t.id = 'toast'; t.className = 'toast';
      document.body.appendChild(t);
    }
    t.textContent = msg; t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { t.classList.remove('show'); }, 2200);
  };

  // ===== U2 智能问答引导（演示版：关键词命中 → 预置回答）=====
  // 说明：一期为架构预留，此处不含真实模型调用；二期接入知识库后替换 U2_QA 即可。
  var U2_QA = [
    {
      k: ['定几级', '几级', '定级', '等级'],
      a: '依据《定级指南》GB/T 22240—2020，定级对象按<strong>业务系统</strong>划分：' +
         '<ul><li><strong>住院结算系统</strong>：涉及资金交易与公民费用数据，严重损害公民权益 → 建议 <strong>第三级</strong></li>' +
         '<li><strong>电子病历系统</strong>：涉及个人健康信息，泄露影响范围较大 → 建议 <strong>第三级</strong></li></ul>' +
         '两系统若共用同一基础网络与数据库，可合并为同一备案对象。' +
         '<div class="hint">依据出处：GB/T 22239—2019 8.1；GB/T 22240—2020 第 5 章</div>'
    },
    {
      k: ['材料', '准备', '资料', '清单'],
      a: '第三级备案需提交 <strong>5 项材料</strong>：' +
         '<ul><li>《信息系统安全等级保护备案表》（原件 2 份 + 电子版）</li>' +
         '<li>系统定级报告（含专家评审意见）</li>' +
         '<li>系统拓扑结构及说明（加盖公章）</li>' +
         '<li>安全组织架构与责任人名单</li>' +
         '<li>安全管理制度清单</li></ul>' +
         '其中<strong>拓扑图</strong>与<strong>专家评审意见</strong>是最常见的退回原因。' +
         '<div class="hint">可在本页「表单智能填充」上传材料，自动回填备案表。</div>'
    },
    {
      k: ['多久', '时限', '时间', '周期', '几天'],
      a: '备案审核时限（演示口径）：<ul>' +
         '<li>系统预审：<strong>1 个工作日</strong>（材料完整性与规范性校验）</li>' +
         '<li>机构初审：<strong>3 个工作日</strong></li>' +
         '<li>监管终审：<strong>5 个工作日</strong></li></ul>' +
         '合计约 <strong>7–9 个工作日</strong>；被退回补正后重新计时。' +
         '<div class="hint">依据出处：本地公安机关网安部门备案工作规范</div>'
    },
    {
      k: ['测评', '什么时候测', '年检'],
      a: '第三级系统通常<strong>每年至少测评一次</strong>：<ul>' +
         '<li>备案完成后 <strong>3 个月内</strong>完成首次测评</li>' +
         '<li>之后每年 1 次；发生影响等级判定的重大变更后须重新测评</li></ul>' +
         '测评由具备资质的测评机构执行，报告经机构端上传、监管终审后结论回传本端。' +
         '<div class="hint">依据出处：GB/T 28449；《网络安全等级保护条例》相关要求</div>'
    },
    {
      k: ['变更', '注销', '下线', '迁移'],
      a: '系统发生<strong>扩容、迁云、等级调整、单位信息变更</strong>时，应在事项发生后 10 个工作日内发起<strong>变更备案</strong>；' +
         '系统停止服务时发起<strong>注销备案</strong>并说明数据处置方式。' +
         '可在本页「变更 / 注销申请」Tab 直接发起。' +
         '<div class="hint">两类申请复用「机构初审 → 监管终审」同一套审核状态机。</div>'
    }
  ];

  function u2Append(html, isMe) {
    var box = document.getElementById('u2-chat');
    if (!box) return null;
    var d = document.createElement('div');
    d.className = 'bubble ' + (isMe ? 'me' : 'bot');
    d.innerHTML = html;
    box.appendChild(d);
    if (d.scrollIntoView) d.scrollIntoView({ block: 'nearest' });
    return d;
  }

  window.u2Ask = function (text) {
    text = (text || '').replace(/^\s+|\s+$/g, '');
    if (!text) { toast('请输入问题（演示）'); return; }
    u2Append(text, true);
    var input = document.getElementById('u2-chat-input');
    if (input) input.value = '';
    var hit = null;
    for (var i = 0; i < U2_QA.length; i++) {
      var ks = U2_QA[i].k;
      for (var j = 0; j < ks.length; j++) {
        if (text.indexOf(ks[j]) > -1) { hit = U2_QA[i]; break; }
      }
      if (hit) break;
    }
    setTimeout(function () {
      u2Append(
        hit ? hit.a
            : '（演示回答）我理解您想了解「' + text + '」。当前可引导的方向：' +
              '<ul><li>定级建议与依据条款</li><li>备案材料清单与样例</li><li>审核时限与流程节点</li>' +
              '<li>变更 / 注销办理口径</li></ul>' +
              '<div class="hint">完整法规知识库对话将于二期上线。</div>',
        false
      );
    }, 380);
  };

  // 移动端抽屉
  function openDrawer() { $('sidebar').classList.add('open'); $('scrim').classList.add('open'); }
  function closeDrawer() { $('sidebar').classList.remove('open'); $('scrim').classList.remove('open'); }
  window.closeDrawer = closeDrawer;
  $('hamburger').addEventListener('click', function () {
    $('sidebar').classList.contains('open') ? closeDrawer() : openDrawer();
  });
  $('scrim').addEventListener('click', closeDrawer);

  // 头像下拉
  $('avatarBtn').addEventListener('click', function (e) {
    e.stopPropagation();
    $('avatarMenu').classList.toggle('show');
  });
  document.addEventListener('click', function () {
    var m = $('avatarMenu'); if (m) m.classList.remove('show');
  });

  // 模态点击遮罩关闭
  document.querySelectorAll('.modal-mask').forEach(function (mask) {
    mask.addEventListener('click', function (e) { if (e.target === mask) mask.classList.remove('open'); });
  });
  document.addEventListener('keydown', function (e) { if (e.key === 'Escape') {
    document.querySelectorAll('.modal-mask.open').forEach(function (m) { m.classList.remove('open'); });
    closeDrawer();
  } });

  // 路由初始化（兼容 #/p-u1 与历史 #p-u1 两种写法）
  var init = (location.hash || '').replace(/^#\/?/, '');
  if (pages.indexOf(init) === -1) init = 'p-u1';
  goto(init);
})();

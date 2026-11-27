// ============ 管理端高保真原型 · 交互 ============
(function () {
  "use strict";

  var CRUMB = {
    workbench: "工作台",
    grade: "等级保护",
    archive: "全息档案",
    monitor: "监测预警",
    agent: "智能体",
    screen: "指挥驾驶舱",
    sys: "系统管理",
    msg: "消息中心",
    flow: "流程示意"
  };

  var sidebar = document.getElementById("sidebar");
  var scrim = document.getElementById("scrim");
  var crumb = document.getElementById("crumb");

  function showPage(id) {
    var pages = document.querySelectorAll(".page");
    pages.forEach(function (p) { p.classList.remove("active"); });
    var target = document.getElementById("p-" + id);
    if (target) target.classList.add("active");
    var navs = document.querySelectorAll(".nav-item");
    navs.forEach(function (n) {
      n.classList.toggle("active", n.getAttribute("data-page") === id);
    });
    crumb.textContent = CRUMB[id] || id;
    if (window.innerWidth <= 900) closeDrawer();
    window.scrollTo(0, 0);
  }

  function goto(id) {
    if (location.hash !== "#/" + id) history.replaceState(null, "", "#/" + id);
    showPage(id);
  }
  window.goto = goto;

  // 页面 + 一级 Tab 联合跳转：gotoTab('grade','g-tab-4')，供工作台待办/跨页入口直达指定 Tab
  window.gotoTab = function (pageId, tabId) {
    goto(pageId);
    var pane = document.getElementById(tabId);
    if (!pane) return;
    var sec = pane.closest(".page");
    if (!sec) return;
    sec.querySelectorAll(".tab-pane").forEach(function (p) { p.classList.toggle("active", p.id === tabId); });
    var tab = sec.querySelector('.tab[data-tab="' + tabId + '"]');
    if (tab) {
      tab.parentElement.querySelectorAll(".tab").forEach(function (t) { t.classList.remove("active"); });
      tab.classList.add("active");
    }
  };

  function route() {
    var id = location.hash.replace(/^#\/?/, "") || "workbench";
    showPage(id);
  }
  window.addEventListener("hashchange", route);

  document.querySelectorAll(".nav-item").forEach(function (n) {
    n.addEventListener("click", function (e) {
      e.preventDefault();
      goto(n.getAttribute("data-page"));
    });
  });

  // 移动端抽屉
  function openDrawer() { sidebar.classList.add("open"); scrim.classList.add("open"); }
  function closeDrawer() { sidebar.classList.remove("open"); scrim.classList.remove("open"); }
  document.getElementById("menuBtn").addEventListener("click", function () {
    sidebar.classList.contains("open") ? closeDrawer() : openDrawer();
  });
  scrim.addEventListener("click", closeDrawer);
  window.closeAdv = function () {
    var d = document.getElementById("advDrop");
    if (d) d.classList.remove("open");
  };
  document.getElementById("avatarBtn").addEventListener("click", function (e) {
    e.stopPropagation();
    var d = document.getElementById("advDrop");
    if (d) d.classList.toggle("open");
  });
  document.addEventListener("click", function (e) {
    if (!e.target.closest(".who-wrap")) window.closeAdv();
  });

  // 一级 Tab（.tabs 容器内）→ 切换 .tab-pane（page 内全局唯一）
  document.querySelectorAll(".tab").forEach(function (t) {
    t.addEventListener("click", function () {
      var group = t.parentElement;
      if (group.classList.contains("seg-group")) {
        // 二级子切换：仅切换同 group 内 .sub-pane
        var pid = t.getAttribute("data-tab");
        group.querySelectorAll(".tab").forEach(function (x) { x.classList.remove("active"); });
        t.classList.add("active");
        var pane = group.closest(".tab-pane");
        if (pane) pane.querySelectorAll(".sub-pane").forEach(function (p) {
          p.classList.toggle("active", p.id === pid);
        });
        return;
      }
      // 一级切换
      group.querySelectorAll(".tab").forEach(function (x) { x.classList.remove("active"); });
      t.classList.add("active");
      var sec = t.closest(".page");
      var pid = t.getAttribute("data-tab");
      sec.querySelectorAll(".tab-pane").forEach(function (p) {
        p.classList.toggle("active", p.id === pid);
      });
    });
  });

  // 大屏分段切换（.screen-tabs → .screen-pane）
  document.querySelectorAll(".stab").forEach(function (st) {
    st.addEventListener("click", function () {
      var group = st.parentElement;
      group.querySelectorAll(".stab").forEach(function (x) { x.classList.remove("active"); });
      st.classList.add("active");
      var sec = st.closest(".page");
      var pid = st.getAttribute("data-tab");
      sec.querySelectorAll(".screen-pane").forEach(function (p) {
        p.classList.toggle("active", p.id === pid);
      });
    });
  });

  // 模态框
  window.openModal = function (id) {
    var m = document.getElementById(id);
    if (m) m.classList.add("open");
  };
  window.closeModal = function (id) {
    var m = document.getElementById(id);
    if (m) m.classList.remove("open");
  };
  document.querySelectorAll(".modal-mask").forEach(function (m) {
    m.addEventListener("click", function (e) {
      if (e.target === m) m.classList.remove("open");
    });
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") document.querySelectorAll(".modal-mask.open").forEach(function (m) { m.classList.remove("open"); });
  });

  // Toast
  window.toast = function (msg) {
    var wrap = document.getElementById("toastWrap");
    var el = document.createElement("div");
    el.className = "toast";
    el.textContent = msg;
    wrap.appendChild(el);
    setTimeout(function () { el.remove(); }, 2600);
  };

  // ============ M5 智能体 · 常用模板「月报摘要」 ============
  function scrollToEl(el) {
    if (!el) return;
    if (el.scrollIntoView) el.scrollIntoView({ behavior: "smooth", block: "center" });
  }

  window.genMonthlyReport = function () {
    var card = document.getElementById("tplMonthly");
    var chat = document.getElementById("agentChat");
    if (!chat) return;
    if (card && card.classList.contains("tpl-busy")) return;
    if (card) {
      card.classList.add("tpl-busy");
      setTimeout(function () { card.classList.remove("tpl-busy"); }, 1600);
    }
    window.gotoTab("agent", "ag-tab-1");

    var me = document.createElement("div");
    me.className = "bubble me";
    me.textContent = "帮我生成 2026 年 9 月的监管月报摘要";
    chat.appendChild(me);

    var typing = document.createElement("div");
    typing.className = "bubble bot";
    typing.innerHTML = '<span class="typing"><i></i><i></i><i></i></span><span class="muted">正在汇总备案、测评、预警数据…</span>';
    chat.appendChild(typing);
    scrollToEl(typing);

    setTimeout(function () {
      var tpl = document.getElementById("tplMonthlyMsg");
      var node = tpl ? tpl.content.cloneNode(true) : null;
      var msg = node ? node.firstElementChild : null;
      if (!msg) { typing.className = "bubble bot"; typing.textContent = "月报生成失败，请重试"; return; }
      chat.replaceChild(msg, typing);
      scrollToEl(msg);
      toast("2026 年 9 月监管月报摘要已生成");
    }, 1400);
  };

  window.openMonthlyReport = function () { window.openModal("m-agent-monthly"); };

  window.copyMonthlyPoints = function () {
    var text = "【2026年9月等级保护监管月报要点】新增备案 38 个、完成测评 26 个、处置预警 17 起；高危漏洞 5 项（环比 -28.6%），整改闭环率 93.3%；重点关注：市第一医院 HIS 系统 SQL 注入高危漏洞，整改期限 2026-10-15。";
    function done() { toast("月报要点已复制"); }
    function fallback() {
      var ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand("copy"); done(); } catch (e) { toast("复制失败，请手动选择文本"); }
      document.body.removeChild(ta);
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, fallback);
    } else { fallback(); }
  };

  window.pushMonthlyToPublish = function () {
    window.closeModal("m-agent-monthly");
    window.gotoTab("monitor", "m-tab-2");
    setTimeout(function () { toast("已创建通报草稿《2026年9月等级保护监管月报》，待审核发布"); }, 320);
  };

  // ============ 模态内分区页签（.mtab → .mpane） ============
  document.querySelectorAll(".mtab").forEach(function (t) {
    t.addEventListener("click", function () {
      var group = t.parentElement;
      group.querySelectorAll(".mtab").forEach(function (x) { x.classList.remove("active"); });
      t.classList.add("active");
      var modal = t.closest(".modal");
      if (!modal) return;
      modal.querySelectorAll(".mpane").forEach(function (p) {
        p.classList.toggle("active", p.id === t.getAttribute("data-mtab"));
      });
    });
  });

  // ============ M2.1 备案管理 · 视图切换（全部 / 变更 / 注销） ============
  window.setFilingView = function (btn) {
    if (!btn) return;
    var box = btn.parentElement;
    box.querySelectorAll("button").forEach(function (b) { b.classList.remove("active"); });
    btn.classList.add("active");
    var view = btn.getAttribute("data-view");
    var rows = document.querySelectorAll("#filingTable tbody tr");
    var shown = 0;
    rows.forEach(function (tr) {
      var v = tr.getAttribute("data-view") || "常规";
      var ok = (view === "全部") || (v === view);
      tr.style.display = ok ? "" : "none";
      if (ok) shown++;
    });
    var empty = document.getElementById("filingEmpty");
    if (empty) empty.style.display = shown ? "none" : "block";
    var counts = { "全部": "1,286", "变更": "128", "注销": "36" };
    var el = document.getElementById("filingCount");
    if (el && counts[view]) el.textContent = "共 " + counts[view] + " 条 · 每页 20 条";
  };

  // 重置筛选（状态下拉 / 关键词 / 视图）
  window.resetFilingFilters = function () {
    var sel = document.getElementById("filingStatus");
    var kw = document.getElementById("filingKw");
    if (sel) sel.selectedIndex = 0;
    if (kw) kw.value = "";
    var all = document.querySelector('#filingViews button[data-view="全部"]');
    if (all) window.setFilingView(all);
    window.toast("筛选条件已重置");
  };

  // 列设置浮层
  window.toggleColset = function (e) {
    if (e) e.stopPropagation();
    var p = document.getElementById("colsetPanel");
    if (p) p.classList.toggle("open");
  };
  document.addEventListener("click", function (e) {
    var p = document.getElementById("colsetPanel");
    if (p && p.classList.contains("open") && !e.target.closest(".colset")) p.classList.remove("open");
  });
  window.toggleCol = function (i, on) {
    var t = document.getElementById("filingTable");
    if (!t) return;
    t.classList.toggle("hide-c" + i, !on);
  };

  // 刷新
  window.refreshFiling = function (btn) {
    var ic = btn ? btn.querySelector(".spin") : null;
    if (ic) {
      ic.classList.add("on");
      setTimeout(function () { ic.classList.remove("on"); }, 700);
    }
    setTimeout(function () { window.toast("数据已刷新"); }, 720);
  };

  // ============ M2.2 实施管理 · 视图切换（实施报备 / 记录 / 变更 / 归档） ============
  window.setImplView = function (btn) {
    if (!btn) return;
    var box = btn.parentElement;
    box.querySelectorAll("button").forEach(function (b) { b.classList.remove("active"); });
    btn.classList.add("active");
    var view = btn.getAttribute("data-impl");
    var shown = 0;
    document.querySelectorAll("#implTable tbody tr").forEach(function (tr) {
      var ok = tr.getAttribute("data-impl") === view;
      tr.style.display = ok ? "" : "none";
      if (ok) shown++;
    });
    var empty = document.getElementById("implEmpty");
    if (empty) empty.style.display = shown ? "none" : "block";
    var counts = { "报备中": "3", "记录": "286", "变更": "42", "归档": "11" };
    var el = document.getElementById("implCount");
    if (el && counts[view]) el.textContent = "共 " + counts[view] + " 条 · 每页 20 条";
  };

  // 录入态：常规报备不提示变更次数，变更报备才展示约束
  window.syncImplNotice = function () {
    var t = document.getElementById("implAddType");
    var n = document.getElementById("implNotice");
    if (!t || !n) return;
    var isChange = t.value === "变更报备";
    n.style.display = isChange ? "flex" : "none";
  };

  // 资产登记：切换类型（IP / 域名 / 云 / 大数据）
  window.pickAssetKind = function (btn) {
    if (!btn) return;
    var box = btn.parentElement;
    box.querySelectorAll("button").forEach(function (b) { b.classList.remove("active"); });
    btn.classList.add("active");
    var map = {
      "IP 资产": ["IP 地址 / 网段", "如 10.20.8.24 或 10.20.8.0/24"],
      "域名": ["域名", "如 his.jzdyrmyy.cn"],
      "云平台": ["平台名称", "如 医保专有云"],
      "大数据平台": ["平台名称", "如 医保结算数据仓"]
    };
    var v = map[btn.textContent.trim()] || map["IP 资产"];
    var label = document.getElementById("assetKeyLabel");
    var input = document.getElementById("assetKeyInput");
    if (label) label.textContent = v[0];
    if (input) input.placeholder = v[1];
  };

  // ============ M4.2 信息发布 · 发布表单（按体裁预置类型） ============
  window.openPublish = function (type) {
    var map = { "通知通报": "发布通报", "安全预警公告": "发布安全预警公告", "通知公告": "发布通知公告" };
    var sel = document.getElementById("pubType");
    var ttl = document.getElementById("pubTitle");
    if (sel) sel.value = map[type] ? type : "通知通报";
    if (ttl) ttl.textContent = map[type] || map["通知通报"];
    window.openModal("m-monitor-publish");
  };

  // ============ M2.1 原始材料查看器 ============
  var RAW_DOCS = {
    report: {
      name: "定级报告.pdf", type: "pdf", tag: "PDF", size: "2.4 MB", by: "江州信安测评 2026-08-30 09:12",
      pages: [
        '<div class="doc-no">编号：JZ-DJ-2026-0158</div>' +
        '<div class="doc-t">信息系统安全等级保护定级报告</div>' +
        '<div class="doc-sub">（市三甲医院 HIS 系统）</div>' +
        '<div class="doc-h">一、系统基本情况</div>' +
        '<table class="doc-tbl">' +
        '<tr><td class="k">系统名称</td><td data-f="sysname">市三甲医院 HIS 系统</td></tr>' +
        '<tr><td class="k">责任单位</td><td data-f="unit">江州市第一人民医院</td></tr>' +
        '<tr><td class="k">系统类型</td><td>医疗卫生 · 生产类</td></tr>' +
        '<tr><td class="k">上线时间</td><td>2021 年 6 月</td></tr>' +
        '<tr><td class="k">承载业务</td><td>门诊挂号、电子病历、检验检查、住院医嘱等核心诊疗业务</td></tr>' +
        '</table>' +
        '<div class="doc-h">二、定级要素判定</div>' +
        '<table class="doc-tbl">' +
        '<tr><td class="k">业务信息安全保护等级</td><td data-f="s3">S3</td></tr>' +
        '<tr><td class="k">系统服务安全保护等级</td><td data-f="a3">A3</td></tr>' +
        '<tr><td class="k">安全保护等级</td><td data-f="g3">G3（第三级）</td></tr>' +
        '</table>' +
        '<p>依据《信息安全技术 网络安全等级保护定级指南》（GB/T 22240—2020），本系统承载公民个人诊疗信息与医院核心业务，业务信息遭到破坏后侵害客体为公民、法人和其他组织的合法权益，侵害程度为严重损害，故业务信息安全保护等级定为 S3。</p>' +
        '<div class="doc-foot">— 1 —</div>',

        '<div class="doc-no">编号：JZ-DJ-2026-0158</div>' +
        '<div class="doc-h">三、定级理由与影响分析</div>' +
        '<p>系统中断将导致门诊与住院业务大面积停摆，影响范围涉及社会秩序与公共利益，侵害程度为严重损害，故系统服务安全保护等级定为 A3。</p>' +
        '<table class="doc-tbl">' +
        '<tr><td class="k">受侵害客体</td><td>公民、法人和其他组织的合法权益；社会秩序与公共利益</td></tr>' +
        '<tr><td class="k">侵害程度</td><td>严重损害</td></tr>' +
        '<tr><td class="k">定级结论</td><td data-f="expert">同意定为第三级（S3A3G3）</td></tr>' +
        '</table>' +
        '<div class="doc-h">四、专家评审</div>' +
        '<p>2026 年 8 月 26 日，我院组织 5 名专家组成评审组，对本系统定级情况进行会议评审，评审组一致同意本系统定为第三级。</p>' +
        '<div class="doc-sign">评审组组长：<span></span><br>专家签字：<span></span><span></span><span></span><br>评审日期：2026-08-26</div>' +
        '<div class="doc-foot">— 2 —</div>',

        '<div class="doc-no">编号：JZ-DJ-2026-0158</div>' +
        '<div class="doc-h">五、定级结论</div>' +
        '<p>经专家评审与主管部门审核，市三甲医院 HIS 系统安全保护等级确定为第三级（S3A3G3），应按第三级要求开展备案、建设整改、等级测评与监督检查。</p>' +
        '<div class="doc-seal"><div><b>★</b>江州市第一<br>人民医院<br>定级专用章</div></div>' +
        '<div class="doc-sign" style="margin-top:150px">定级单位（盖章）：<span></span><br>日期：2026 年 8 月 30 日</div>' +
        '<div class="doc-foot">— 3 —</div>'
      ]
    },
    form: {
      name: "备案表.docx", type: "doc", tag: "DOC", size: "186 KB", by: "江州信安测评 2026-08-30 09:14",
      pages: [
        '<div class="doc-t" style="font-size:18px">网络安全等级保护备案表</div>' +
        '<div class="doc-sub">（第三级及以上系统适用）</div>' +
        '<table class="doc-tbl">' +
        '<tr><td class="k">备案编号</td><td>JZ-2026-0158</td></tr>' +
        '<tr><td class="k">系统名称</td><td>市三甲医院 HIS 系统</td></tr>' +
        '<tr><td class="k">责任单位</td><td>江州市第一人民医院</td></tr>' +
        '<tr><td class="k">单位地址</td><td>江州市解放路 128 号</td></tr>' +
        '<tr><td class="k">安全保护等级</td><td>第三级（S3A3G3）</td></tr>' +
        '<tr><td class="k">测评机构</td><td>江州信安测评技术有限公司</td></tr>' +
        '<tr><td class="k">联系人 / 电话</td><td>周×× / 0579-8xxxxxxx</td></tr>' +
        '<tr><td class="k">提交日期</td><td>2026-08-30</td></tr>' +
        '</table>' +
        '<p>本单位承诺以上信息真实、准确，如有变更将在 30 日内办理变更备案。</p>' +
        '<div class="doc-sign">单位负责人签字：<span></span><br>日期：2026 年 8 月 30 日</div>' +
        '<div class="doc-seal"><div><b>★</b>江州市第一<br>人民医院</div></div>' +
        '<div class="doc-foot">— 1 —</div>'
      ]
    },
    review: {
      name: "专家评审意见.pdf", type: "pdf", tag: "PDF", size: "612 KB", by: "江州信安测评 2026-08-30 09:15",
      pages: [
        '<div class="doc-t" style="font-size:18px">定级专家评审意见</div>' +
        '<div class="doc-sub">市三甲医院 HIS 系统</div>' +
        '<p>受江州市第一人民医院委托，评审组于 2026 年 8 月 26 日对本系统定级材料进行会议评审。经审阅定级报告与业务影响分析材料，形成意见如下：</p>' +
        '<p>一、系统承载核心诊疗业务，涉及大量公民个人健康信息，定级要素判定依据充分。</p>' +
        '<p>二、业务信息安全保护等级 S3、系统服务安全保护等级 A3 的判定符合 GB/T 22240—2020 要求。</p>' +
        '<table class="doc-tbl">' +
        '<tr><td class="k">评审时间</td><td>2026-08-26 14:00—16:30</td></tr>' +
        '<tr><td class="k">评审方式</td><td>会议评审</td></tr>' +
        '<tr><td class="k">评审结论</td><td>同意定级为第三级（S3A3G3）</td></tr>' +
        '<tr><td class="k">签字情况</td><td style="color:#C0392B">专家签字页缺失（应 5 人，实收 0 页）</td></tr>' +
        '</table>' +
        '<div class="doc-foot">— 1 —</div>'
      ]
    },
    topo: {
      name: "网络拓扑图.png", type: "img", tag: "PNG", size: "1.1 MB", by: "江州信安测评 2026-08-30 09:16",
      pages: [
        '<div class="doc-t" style="font-size:16px">市三甲医院 HIS 系统网络拓扑图</div>' +
        '<div class="topo">' +
        '<div class="topo-row"><div class="topo-node">互联网<small>出口线路</small></div><div class="topo-arrow">→</div>' +
        '<div class="topo-node">边界防火墙<small>10.20.8.1</small></div><div class="topo-arrow">→</div>' +
        '<div class="topo-node">核心交换机<small>10.20.8.2</small></div></div>' +
        '<div class="topo-band"><div class="topo-node">应用服务器<small>10.20.8.11</small></div>' +
        '<div class="topo-node">数据库服务器<small>10.20.8.21</small></div>' +
        '<div class="topo-node">运维终端<small>10.20.8.101</small></div></div>' +
        '<div class="topo-lbl">HIS 生产区 · VLAN 80 · 网段 <span data-f="ip">10.20.8.0/24</span></div>' +
        '</div>' +
        '<p class="noind">注：本图由院方信息科提供，拓扑变更时须同步更新并重新报备。</p>' +
        '<div class="doc-foot">— 1 —</div>'
      ]
    }
  };

  var RAW_FIELDS = [
    { k: "系统名称", v: "市三甲医院 HIS 系统", f: "sysname", file: "report", pg: 1, src: "定级报告 第 1 页 · 基本情况表", conf: 98 },
    { k: "责任单位", v: "江州市第一人民医院", f: "unit", file: "report", pg: 1, src: "定级报告 第 1 页 · 基本情况表", conf: 99 },
    { k: "业务信息安全保护等级", v: "S3", f: "s3", file: "report", pg: 1, src: "定级报告 第 1 页 · 定级要素", conf: 96 },
    { k: "系统服务安全保护等级", v: "A3", f: "a3", file: "report", pg: 1, src: "定级报告 第 1 页 · 定级要素", conf: 96 },
    { k: "安全保护等级", v: "G3（第三级）", f: "g3", file: "report", pg: 1, src: "定级报告 第 1 页 · 定级要素", conf: 94 },
    { k: "专家评审结论", v: "同意定为第三级", f: "expert", file: "report", pg: 2, src: "定级报告 第 2 页 · 定级理由", conf: 76, flag: "专家签字页未上传，结论由会议纪要推定，建议人工复核" },
    { k: "拓扑 IP 段", v: "10.20.8.0/24", f: "ip", file: "topo", pg: 1, src: "网络拓扑图 · 生产区标注", conf: 72, flag: "与资产清单 10.21.6.0/24 不一致，需机构说明" }
  ];

  var rawState = { file: "report", pg: 1, zoom: 1, field: null };
  var rawTraced = {};

  function rawConfClass(n) { return n >= 90 ? "" : (n >= 80 ? "mid" : "low"); }

  function renderRawFiles() {
    var box = document.getElementById("rawFiles");
    if (!box) return;
    box.innerHTML = '<div class="rf-h">材料清单（4 份 / 7 页）</div>' +
      Object.keys(RAW_DOCS).map(function (key) {
        var d = RAW_DOCS[key];
        var badge = key === "topo" ? '<span class="rf-badge">IP 段待核</span>' : "";
        return '<div class="raw-file' + (key === rawState.file ? " active" : "") + '" onclick="rawSwitch(\'' + key + '\')">' +
          '<div class="rf-t ' + d.type + '">' + d.tag + '</div>' +
          '<div class="rf-b"><div class="rf-n">' + d.name + '</div>' +
          '<div class="rf-m">' + d.pages.length + ' 页 · ' + d.size + '</div>' + badge + '</div></div>';
      }).join("");
  }

  function renderRawPage() {
    var d = RAW_DOCS[rawState.file];
    var box = document.getElementById("rawPageBox");
    if (!box || !d) return;
    box.innerHTML = d.pages[rawState.pg - 1] || "";
    box.style.setProperty("--rz", rawState.zoom);
    document.getElementById("rawPg").textContent = rawState.pg;
    document.getElementById("rawPgTotal").textContent = d.pages.length;
    document.getElementById("rawToolName").textContent = d.name;
    document.getElementById("rawHeadName").textContent = d.name;
    document.getElementById("rawZoomTxt").textContent = Math.round(rawState.zoom * 100) + "%";
    document.getElementById("rawPrev").disabled = rawState.pg <= 1;
    document.getElementById("rawNext").disabled = rawState.pg >= d.pages.length;
    var sc = document.getElementById("rawScroll");
    if (sc) sc.scrollTop = 0;
    applyRawHighlight();
  }

  function applyRawHighlight() {
    var box = document.getElementById("rawPageBox");
    if (!box) return;
    box.querySelectorAll("[data-f]").forEach(function (el) {
      el.classList.toggle("hl", !!rawState.field && el.getAttribute("data-f") === rawState.field);
    });
    var hit = rawState.field ? box.querySelector('[data-f="' + rawState.field + '"]') : null;
    if (hit && hit.scrollIntoView) hit.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }

  function renderRawFields() {
    var box = document.getElementById("rawFields");
    if (!box) return;
    box.innerHTML = RAW_FIELDS.map(function (it, i) {
      var on = rawState.field === it.f;
      return '<div class="rf-item' + (on ? " on" : "") + '" data-idx="' + i + '" onclick="rawFocus(' + i + ')">' +
        '<div class="k">' + it.k + '</div>' +
        '<div class="v">' + it.v + '</div>' +
        '<div class="src"><span>' + it.src + '</span><span class="conf"><i class="' + rawConfClass(it.conf) + '" style="width:' + it.conf + '%"></i></span><span>' + it.conf + '%</span></div>' +
        (it.flag ? '<div class="flag">' + it.flag + '</div>' : "") +
        '</div>';
    }).join("");
  }

  function rawTraceLine(fileKey) {
    var d = RAW_DOCS[fileKey];
    var now = new Date();
    var pad = function (n) { return n < 10 ? "0" + n : "" + n; };
    var ts = now.getFullYear() + "-" + pad(now.getMonth() + 1) + "-" + pad(now.getDate()) + " " + pad(now.getHours()) + ":" + pad(now.getMinutes());
    var line = ts + " 审核员 张×× 查看 " + d.name + "（第 " + rawState.pg + " 页）";
    var el = document.getElementById("rawTrace");
    if (el) el.textContent = "本次查看已记录：" + line;
    if (rawTraced[fileKey]) return;
    rawTraced[fileKey] = true;
    var tl = document.getElementById("filingTl");
    if (!tl) return;
    var item = document.createElement("div");
    item.className = "tl-item";
    item.innerHTML = '<div class="tl-t">查看原件 · 审计留痕</div><div class="tl-m">' + line + '</div>';
    tl.appendChild(item);
  }

  window.openRawDoc = function (file, field) {
    if (!RAW_DOCS[file]) file = "report";
    rawState.file = file;
    rawState.pg = 1;
    rawState.zoom = 1;
    rawState.field = field || null;
    if (field) {
      var it = RAW_FIELDS.filter(function (x) { return x.f === field; })[0];
      if (it) { rawState.file = it.file; rawState.pg = it.pg; }
    }
    window.closeModal("m-grade-filing-detail");
    renderRawFiles();
    renderRawPage();
    renderRawFields();
    rawTraceLine(rawState.file);
    window.openModal("m-grade-raw-viewer");
    // 打开后按可视宽度自适应，避免窄屏下原件被裁切
    setTimeout(function () { window.rawFit(); }, 60);
  };

  window.closeRawDoc = function () {
    window.closeModal("m-grade-raw-viewer");
    window.openModal("m-grade-filing-detail");
  };

  window.rawSwitch = function (key) {
    rawState.file = key;
    rawState.pg = 1;
    rawState.field = null;
    renderRawFiles();
    renderRawPage();
    renderRawFields();
    var side = document.querySelector(".raw-side");
    if (side) side.scrollTop = 0;
    rawTraceLine(key);
  };

  window.rawPage = function (delta) {
    var d = RAW_DOCS[rawState.file];
    var next = rawState.pg + delta;
    if (next < 1 || next > d.pages.length) return;
    rawState.pg = next;
    renderRawPage();
    rawTraceLine(rawState.file);
  };

  window.rawZoom = function (delta) {
    var steps = [0.75, 1, 1.25, 1.5];
    var cur = steps.indexOf(rawState.zoom);
    if (cur < 0) cur = 1;
    var next = Math.min(steps.length - 1, Math.max(0, cur + delta));
    rawState.zoom = steps[next];
    renderRawPage();
  };

  window.rawFit = function () {
    var sc = document.getElementById("rawScroll");
    if (!sc) return;
    var avail = sc.clientWidth - 36;
    // 窄屏时页面宽度为 100%，基准取容器宽度，避免二次缩小
    var base = window.innerWidth <= 760 ? Math.max(240, sc.clientWidth - 24) : 620;
    rawState.zoom = Math.min(1, Math.max(0.6, Math.round((avail / base) * 100) / 100));
    renderRawPage();
  };

  window.rawFocus = function (idx) {
    var it = RAW_FIELDS[idx];
    if (!it) return;
    rawState.file = it.file;
    rawState.pg = it.pg;
    rawState.field = it.f;
    renderRawFiles();
    renderRawPage();
    renderRawFields();
    rawTraceLine(rawState.file);
    var on = document.querySelector("#rawFields .rf-item.on");
    if (on && on.scrollIntoView) on.scrollIntoView({ block: "nearest" });
  };

  window.rawDownload = function (name) {
    var n = name || (RAW_DOCS[rawState.file] && RAW_DOCS[rawState.file].name) || "原件";
    window.toast("已下载 " + n + "，下载行为已留痕");
  };


  function updateMsgBadge() {
    var n = document.querySelectorAll("#msgList .msg-item.unread").length;
    var badge = document.getElementById("msgDot");
    if (!badge) return;
    if (n > 0) { badge.textContent = n; badge.style.display = ""; }
    else { badge.style.display = "none"; }
  }

  window.readMsg = function (el) {
    if (!el) return;
    el.classList.remove("unread");
    var d = el.querySelector(".msg-dot");
    if (d) d.remove();
    updateMsgBadge();
  };

  window.markAllRead = function () {
    document.querySelectorAll("#msgList .msg-item.unread").forEach(function (el) {
      el.classList.remove("unread");
      var d = el.querySelector(".msg-dot");
      if (d) d.remove();
    });
    updateMsgBadge();
    toast("全部消息已标为已读");
  };

  window.setMsgView = function (btn) {
    if (!btn) return;
    var box = btn.parentElement;
    box.querySelectorAll("button").forEach(function (b) { b.classList.remove("active"); });
    btn.classList.add("active");
    var view = btn.getAttribute("data-msg");
    var items = document.querySelectorAll("#msgList .msg-item");
    var shown = 0;
    items.forEach(function (it) {
      var ok;
      if (view === "全部") ok = true;
      else if (view === "未读") ok = it.classList.contains("unread");
      else ok = it.getAttribute("data-msg") === view;
      it.style.display = ok ? "" : "none";
      if (ok) shown++;
    });
    var empty = document.getElementById("msgEmpty");
    if (empty) empty.style.display = shown ? "none" : "block";
  };

  route();
})();

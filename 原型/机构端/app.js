// ============ 机构端高保真原型 · 交互 ============
(function () {
  "use strict";

  var CRUMB = {
    workbench: "工作台",
    filing: "备案材料",
    implement: "实施管理",
    report: "测评报告",
    "report-import": "测评报告 / 导入",
    ai: "智能辅助",
    data: "基础数据",
    msg: "消息中心",
    account: "账号设置",
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

  // 哈希路由
  function route() {
    var id = location.hash.replace(/^#\/?/, "") || "workbench";
    showPage(id);
  }
  window.addEventListener("hashchange", route);

  // 侧边导航点击
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
  document.getElementById("avatarBtn").addEventListener("click", function () { goto("account"); });

  // Tab 切换（同 section 内）
  document.querySelectorAll(".tab").forEach(function (t) {
    t.addEventListener("click", function () {
      var group = t.parentElement;
      group.querySelectorAll(".tab").forEach(function (x) { x.classList.remove("active"); });
      t.classList.add("active");
      var sec = t.closest(".page");
      var pid = t.getAttribute("data-tab");
      sec.querySelectorAll(".tab-pane").forEach(function (p) {
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

  route();
})();

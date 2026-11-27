"""在隔离 PostgreSQL 本地预览验证直接漏洞登记与重大风险督办。"""

import csv
import io
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode

from acceptance import API, RUN, business_date, hazard_action, request, transition, upload


def registration_filing():
    """建立不含测评报告或规划的有效备案。
    参数 无，系统与归属使用本批次命名。
    返回值 备案 ID、运营者和机构范围名称。
    """
    owner = "监管登记验收单位 " + RUN
    institution = "监管登记验收机构 " + RUN
    info = request("POST", API + "/filings", {
        "number": "REGISTRY-F-" + RUN, "systemName": "监管登记验收系统 " + RUN,
        "organizationName": owner, "assessmentOrg": institution, "level": "3",
    }, status=201)
    path = API + "/filings/" + info["id"]
    for kind in ("filing_form", "classification_report"):
        upload(path, kind, kind + ".pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return info["id"], owner, institution


def registration_payload(filing, ledger):
    """生成当前台账分类中可登记的最少业务输入。
    参数 filing 为有效备案标识，ledger 为台账分类。
    返回值 按接口定义提交的登记数据。
    """
    return {
        "ledger": ledger, "filingId": filing, "title": "业务控制核对 " + ledger + " " + RUN,
        "severity": "high", "source": "manual", "discoveredOn": business_date(-2),
        "deadline": business_date(3), "description": "合成发现及影响说明",
        "measure": "完成配置整改并保留可核对证据", "responsibility": "运行保障组",
    }


def verify_registration(filing, ledger):
    """核对监管登记粒度、并发重复、分类和 CSV 筛选。
    参数 filing 关联有效备案，ledger 指定系统漏洞或重大风险。
    返回值 新建的待派单事项。
    """
    base = API + "/hazards"
    payload = registration_payload(filing, ledger)
    if ledger == "vulnerability":
        options = request("GET", base + "/filing-options?" + urlencode({"q": RUN}))["items"]
        assert len(options) == 1 and options[0]["id"] == filing
        invalid = request("POST", base, {**payload, "deadline": business_date(-4)}, status=400)
        assert invalid["code"] == "invalid_input"
    created = request("POST", base, payload, status=201)
    assert created["status"] == "pending_dispatch"
    duplicate = {**payload, "title": "  " + payload["title"].replace(" ", "  ").upper() + "  ",
                 "discoveredOn": business_date(-1)}
    assert request("POST", base, duplicate, status=409)["code"] == "duplicate_registration"
    query = "?" + urlencode({"ledger": ledger, "q": payload["title"], "status": "pending_dispatch"})
    items = request("GET", base + query)["items"]
    assert [row["id"] for row in items] == [created["id"]]
    result = request("GET", base + "/export" + query).decode("utf-8-sig")
    rows = list(csv.reader(io.StringIO(result)))
    assert len(rows) == 2 and payload["title"] in rows[1], rows
    other = "major_risk" if ledger == "vulnerability" else "vulnerability"
    assert request("GET", base + "?" + urlencode({"ledger": other, "q": payload["title"]}))["items"] == []
    return created


def assert_notice(audience, scope, identifier, state):
    """核对当前办理方准确收到且仅收到一次指定状态的消息。
    参数 audience、scope 为办理范围，identifier、state 为当前事项与消息类型。
    返回值 匹配到的真实送达记录。
    """
    page = request("GET", API + "/" + audience + "/feedback" + scope)
    notices = [item for item in page["items"] if item["resourceId"] == identifier and item["state"] == state]
    assert len(notices) == 1, notices
    return notices[0]


def verify_dispatch_and_supervision(created, owner_scope, institution_scope, major_risk):
    """执行派单和督办，检查工作台状态、角色和并发重复。
    参数 created 为待派单记录，两范围分别属于运营者和机构，major_risk 表示重大风险。
    返回值 最新状态和版本。
    """
    identifier = created["id"]
    admin = API + "/hazards/" + identifier
    operator = API + "/operator/hazards/" + identifier
    partner = API + "/institution/hazards/" + identifier
    request("GET", operator + owner_scope, status=404)
    request("GET", partner + institution_scope, status=404)
    assert any(row["id"] == identifier for row in request("GET", API + "/workbench/overview")["tasks"])
    payload = {"action": "dispatch", "expectedVersion": created["version"]}
    current = request("POST", admin + "/actions", payload)
    assert current["status"] == "open"
    request("POST", admin + "/actions", payload, status=409)
    assert_notice("operator", owner_scope, identifier, "open")
    assert any(row["id"] == identifier for row in request("GET", API + "/operator/workbench" + owner_scope)["tasks"])
    assert request("GET", operator + owner_scope)["status"] == "open"
    if major_risk:
        body = {"action": "supervise", "comment": "请落实整改并反馈", "expectedVersion": current["version"]}
        current = request("POST", admin + "/actions", body)
        body["expectedVersion"] = current["version"]
        with ThreadPoolExecutor(max_workers=3) as pool:
            repeats = [pool.submit(request, "POST", admin + "/actions", body, 409) for _ in range(3)]
            assert all(job.result()["code"] == "already_supervised" for job in repeats)
        assert_notice("operator", owner_scope, identifier, "hazard_supervision")
        assert request("GET", admin)["version"] == current["version"]
    return current


def verify_retest_and_close(current, owner_scope, institution_scope):
    """验证材料上传、机构复测、监管销项与三端办理历史。
    参数 current 为派单后的记录及版本，两范围分别属于运营者和机构。
    返回值 无。
    """
    identifier = current["id"]
    admin = API + "/hazards/" + identifier
    operator = API + "/operator/hazards/" + identifier
    partner = API + "/institution/hazards/" + identifier
    action = {"action": "submit", "comment": "已完成本轮整改"}
    assert request("POST", operator + "/actions" + owner_scope,
                   {**action, "expectedVersion": current["version"]}, status=400)["code"] == "evidence_required"
    upload(operator, "evidence", "registration-evidence.pdf", scope=owner_scope)
    action["expectedVersion"] = request("GET", operator + owner_scope)["version"]
    submitted = request("POST", operator + "/actions" + owner_scope, action)
    assert submitted["status"] == "pending_retest"
    assert_notice("institution", institution_scope, identifier, "pending_retest")
    assert any(row["id"] == identifier for row in
               request("GET", API + "/institution/workbench" + institution_scope)["tasks"])
    outside = "?" + urlencode({"institution": "无关机构 " + RUN})
    request("GET", partner + outside, status=404)
    hazard_action(admin, "close", "", status=409)
    approve = {"action": "retest_pass", "expectedVersion": submitted["version"]}
    assert request("POST", partner + "/actions" + institution_scope,
                   approve, status=400)["code"] == "retest_report_required"
    upload(partner, "retest_report", "registration-retest.pdf", scope=institution_scope)
    approve["expectedVersion"] = request("GET", partner + institution_scope)["version"]
    assert request("POST", partner + "/actions" + institution_scope, approve)["status"] == "pending_review"
    hazard_action(admin, "close", "监管复核确认闭环", "closed")
    for route in (admin, operator + owner_scope, partner + institution_scope):
        detail = request("GET", route)
        assert detail["status"] == "closed" and "planId" not in detail and "reportId" not in detail
    history = request("GET", admin + "/history")["items"]
    assert [entry["action"] for entry in history[-2:]] == ["dispatch", "register"]
    assert "隐患销项回执" in request("GET", operator + "/receipt" + owner_scope).decode()
    assert_notice("operator", owner_scope, identifier, "closed")
    assert_notice("institution", institution_scope, identifier, "closed")


if __name__ == "__main__":
    filing, owner, institution = registration_filing()
    owner_scope = "?" + urlencode({"organization": owner})
    institution_scope = "?" + urlencode({"institution": institution})
    vulnerability = verify_registration(filing, "vulnerability")
    major_risk = verify_registration(filing, "major_risk")
    print("PASS: 两类监管直接登记、有效备案选项、同名去重和分类筛选导出")
    for item, significant in ((vulnerability, False), (major_risk, True)):
        current = verify_dispatch_and_supervision(item, owner_scope, institution_scope, significant)
        verify_retest_and_close(current, owner_scope, institution_scope)
        print("PASS: " + ("重大风险督办" if significant else "系统漏洞派单") +
              "与三端材料校验、复测、销项、留痕和通知")

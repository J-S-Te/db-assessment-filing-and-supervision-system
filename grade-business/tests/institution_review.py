"""在隔离本地预览核对机构复测、机构初审和监管终审。"""

from concurrent.futures import ThreadPoolExecutor
from http.client import HTTPConnection
from json import dumps
from urllib.parse import urlencode

from acceptance import (
    API, ORIGIN, PORT, RUN, check_business_delivery, hazard_action, request, transition, upload,
)
from operator_rectification import OWNER as HAZARD_OWNER, SCOPE as HAZARD_SCOPE, plan_hazards, seed_plan


ORG = "三端初审验收单位 " + RUN
INST = "三端初审验收机构 " + RUN
OTHER = "其他复测机构 " + RUN
OPER = "?" + urlencode({"organization": ORG})
PARTNER = "?" + urlencode({"institution": INST})
OUTSIDE = "?" + urlencode({"institution": OTHER})


def one_winner(path, payload):
    """以两个独立 HTTP 连接争用同一已读取的业务版本。
    参数 path 为办理接口，payload 是两个请求共用的动作和版本。
    返回值 无。
    """
    def submit():
        """提交一个真实并发操作。
        参数 无，动作和连接设置来自当前用例。
        返回值 请求的 HTTP 状态。
        """
        connection = HTTPConnection(ORIGIN, PORT, timeout=10)
        try:
            connection.request("POST", path, body=dumps(payload).encode(),
                               headers={"Content-Type": "application/json"})
            response = connection.getresponse()
            response.read()
            return response.status
        finally:
            connection.close()

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(lambda _: submit(), range(2)))
    assert sorted(results) == [200, 409], results


def verify_initial_review():
    """验证运营者首报只能由对应机构初审，再由监管终审。
    参数 无，测试单位和编号来自本批唯一标识。
    返回值 无。
    """
    root = API + "/filings"
    scoped = API + "/operator/filings"
    partners = API + "/institution/filings"
    op = request("POST", scoped + OPER, {
        "number": "INSTITUTION-REVIEW-" + RUN, "systemName": "三端初审验收系统",
        "assessmentOrg": INST, "level": "3",
    }, status=201)
    identifier = op["id"]
    root += "/" + identifier
    scoped += "/" + identifier
    partner = partners + "/" + identifier
    request("POST", scoped + "/submit" + OPER, {"expectedVersion": op["version"]}, status=400)
    for kind in ("filing_form", "classification_report"):
        upload(scoped, kind, kind + ".pdf", scope=OPER)
    version = request("GET", scoped + OPER)["version"]
    assert request("POST", scoped + "/submit" + OPER, {"expectedVersion": version})["status"] == "pending_initial"
    current = request("GET", root)
    action = {"decision": "approve", "expectedVersion": current["version"], "comment": "对应机构核对材料无误"}
    assert current["initialReviewAudience"] == "institution" and current["allowedActions"] == []
    assert request("GET", partner + PARTNER)["allowedActions"] == ["approve", "return_for_correction"]
    assert request("GET", partners + PARTNER + "&review=assigned")["items"][0]["id"] == identifier
    assert request("GET", partners + OUTSIDE + "&review=assigned")["items"] == []
    request("POST", root + "/review-actions", action, status=409)
    request("POST", partner + "/review-actions" + OUTSIDE, action, status=404)
    assert not any(row["id"] == identifier for row in request("GET", API + "/workbench/overview")["tasks"])
    check_business_delivery(root, "institution", INST, "pending_initial")
    one_winner(partner + "/review-actions" + PARTNER, action)
    for endpoint in (root, scoped + OPER, partner + PARTNER):
        assert request("GET", endpoint)["status"] == "pending_final"
    check_business_delivery(root, "operator", ORG, "pending_final")
    final = request("GET", root)
    request("POST", partner + "/review-actions" + PARTNER,
            {**action, "expectedVersion": final["version"]}, status=409)
    transition(root, "approve", "approved")
    assert request("GET", scoped + OPER)["status"] == "approved"
    assert request("GET", partner + PARTNER)["status"] == "approved"
    history = request("GET", partner + "/review-history" + PARTNER)["items"]
    assert [event["toStatus"] for event in history] == ["approved", "pending_final", "pending_initial"]
    assert INST in history[1]["operator"]
    print("PASS: 三端备案角色、材料、工作台、通知、重复初审和监管终审")


def verify_institution_retest():
    """验证运营者申请机构复测、退回补正、复测报告和监管销项。
    参数 无，测试规划和材料由本批本地生成。
    返回值 无。
    """
    plan, filing = seed_plan()
    hazard = plan_hazards(plan, filing)["转办事项"]
    root = API + "/hazards/" + hazard
    operator = API + "/operator/hazards/" + hazard
    institution = API + "/institution/hazards/" + hazard
    report = request("GET", root)["reportId"]
    owner = request("GET", API + "/reports/" + report)["assessmentOrg"]
    scope = "?" + urlencode({"institution": owner})
    submit = {"action": "submit", "comment": "已完成初次整改"}
    upload(operator, "evidence", "before-first-retest.pdf", scope=HAZARD_SCOPE)
    version = request("GET", operator + HAZARD_SCOPE)["version"]
    result = request("POST", operator + "/actions" + HAZARD_SCOPE, {**submit, "expectedVersion": version})
    assert result["status"] == "pending_retest"
    assert request("GET", root)["status"] == "pending_retest"
    assert request("GET", institution + scope)["status"] == "pending_retest"
    request("GET", institution + OUTSIDE, status=404)
    collection = API + "/institution/hazards"
    assert request("GET", collection + scope + "&status=pending_retest")["items"][0]["id"] == hazard
    check_business_delivery(root, "institution", owner, "pending_retest")
    hazard_action(root, "close", "尝试跳过机构复测", status=409)
    action = {"action": "retest_pass", "comment": "待核对", "expectedVersion": result["version"]}
    request("POST", institution + "/actions" + OUTSIDE, action, status=404)
    assert request("POST", institution + "/actions" + scope, action, status=400)["code"] == "retest_report_required"
    upload(institution, "retest_report", "first-retest.pdf", scope=scope)
    stale_retest_version = result["version"]
    version = request("GET", institution + scope)["version"]
    returned = request("POST", institution + "/actions" + scope,
                       {"action": "retest_return", "expectedVersion": version, "comment": "仍有需要补正项"})
    assert returned["status"] == "returned"
    check_business_delivery(root, "operator", HAZARD_OWNER, "returned")
    request("POST", operator + "/actions" + HAZARD_SCOPE, {**submit, "expectedVersion": returned["version"]}, status=400)
    upload(operator, "evidence", "new-round-evidence.pdf", scope=HAZARD_SCOPE)
    version = request("GET", operator + HAZARD_SCOPE)["version"]
    result = request("POST", operator + "/actions" + HAZARD_SCOPE, {**submit, "expectedVersion": version})
    assert result["status"] == "pending_retest"
    assert request("GET", institution + scope)["retestReportReady"] is False
    request("POST", institution + "/actions" + scope, {**action, "expectedVersion": result["version"]}, status=400)
    upload(institution, "retest_report", "stale-in-flight.pdf", scope=scope, status=409,
           expected_version=stale_retest_version)
    upload(institution, "retest_report", "new-round-retest.pdf", scope=scope)
    version = request("GET", institution + scope)["version"]
    assert request("GET", institution + scope)["retestReportReady"] is True
    one_winner(institution + "/actions" + scope, {**action, "expectedVersion": version})
    check_business_delivery(root, "operator", HAZARD_OWNER, "pending_review")
    hazard_action(root, "close", "监管复核确认销项", "closed")
    check_business_delivery(root, "institution", owner, "closed")
    assert request("GET", operator + HAZARD_SCOPE)["status"] == "closed"
    assert request("GET", institution + scope)["status"] == "closed"
    history = request("GET", institution + "/history" + scope)["items"]
    assert [event["action"] for event in history[:5]] == ["close", "retest_pass", "submit", "retest_return", "submit"]
    print("PASS: 三端隐患复测、报告分轮、退回补正、重复复测与监管销项")


if __name__ == "__main__":
    verify_initial_review()
    verify_institution_retest()

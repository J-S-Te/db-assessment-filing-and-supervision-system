"""在隔离本地预览验收运营者整改单和预警处置。"""
from urllib.parse import urlencode

from acceptance import API, RUN, business_date, check_business_delivery, hazard_action, institution_retest, request, transition, upload

OWNER = "整改预警验收单位 " + RUN
OTHER = "其他整改预警单位 " + RUN
SCOPE = "?" + urlencode({"organization": OWNER})
OUTSIDE = "?" + urlencode({"organization": OTHER})
ROOT_HAZARDS = API + "/hazards"
ROOT_ALERTS = API + "/alerts"
HAZARDS = API + "/operator/hazards"
ALERTS = API + "/operator/alerts"
PLAN = API + "/operator/remediations"
CASES = (
    ("转办事项", "high", business_date(4)),
    ("延期事项", "medium", business_date(-2)),
    ("误报事项", "high", business_date(6)),
)


def seed_plan():
    """生成已通过的备案、测评和有三条不同隐患的整改规划。
    参数 无，本轮单位和编号来自测试常量。
    返回值 规划和备案的监管标识。
    """
    filing = request("POST", API + "/filings", {
        "number": "RECT-F-" + RUN, "systemName": "整改与预警验收系统", "organizationName": OWNER,
        "assessmentOrg": "统一验收机构 " + RUN, "level": "3",
    }, status=201)["id"]
    filing_path = API + "/filings/" + filing
    for kind in ("filing_form", "classification_report"):
        upload(filing_path, kind, kind + ".pdf")
    complete_review(filing_path)
    report = request("POST", API + "/reports", {
        "number": "RECT-R-" + RUN, "filingId": filing, "assessmentOrg": "统一验收机构 " + RUN, "risk": "high",
    }, status=201)["id"]
    report_path = API + "/reports/" + report
    upload(report_path, "report_file", "rectification-report.pdf")
    complete_review(report_path)
    deadline = business_date(8)
    input_data = {"reportId": report, "deadline": deadline,
                  "risks": [{"risk": risk, "description": name, "measure": "本地验收整改措施",
                             "responsibility": "本地责任部门", "deadline": due}
                            for name, risk, due in CASES]}
    draft = request("POST", PLAN + SCOPE, input_data, status=201)["id"]
    plan_path = PLAN + "/" + draft
    for kind in ("plan", "responsibility", "schedule"):
        upload(plan_path, kind, kind + ".pdf", report_id=report, scope=SCOPE)
    current = request("GET", plan_path + SCOPE)
    mutation = request("POST", plan_path + "/submit" + SCOPE, {"expectedVersion": current["version"]})
    assert mutation["status"] == "pending_review"
    decision = {"decision": "approve", "expectedVersion": mutation["version"],
                "deadline": deadline, "comment": "按规划登记真实整改单"}
    assert request("POST", API + "/remediations/" + draft + "/review-actions", decision)["status"] == "approved"
    return draft, filing


def complete_review(path):
    """经提交、初审和终审通过测试记录。
    参数 path 为备案或测评的监管详情地址。
    返回值 无。
    """
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")


def plan_hazards(plan_id, filing_id):
    """验证规划关联、按归属与条件筛选及规划回执。
    参数 plan_id 和 filing_id 是已生效的规划与备案。
    返回值 按说明索引的真实整改单。
    """
    suffix = "&planId=" + plan_id
    rows = request("GET", HAZARDS + SCOPE + suffix)["items"]
    assert len(rows) == len(CASES) and all(row["planId"] == plan_id for row in rows)
    assert request("GET", HAZARDS + OUTSIDE + suffix)["items"] == []
    filtered = request("GET", HAZARDS + SCOPE + "&filingId=" + filing_id + "&due=overdue&severity=medium")["items"]
    assert [item["description"] for item in filtered] == ["延期事项"]
    match_id = rows[0]["id"]
    lookup = request("GET", HAZARDS + SCOPE + "&q=" + match_id[:8])["items"]
    assert [item["id"] for item in lookup] == [match_id]
    first_page = request("GET", HAZARDS + SCOPE + suffix + "&status=open&limit=1")
    assert first_page["nextCursor"] and len(first_page["items"]) == 1
    next_page = request("GET", HAZARDS + SCOPE + suffix + "&limit=1&status=open&cursor=" + first_page["nextCursor"])
    assert next_page["items"][0]["id"] != first_page["items"][0]["id"]
    plan = PLAN + "/" + plan_id
    assert request("GET", plan + SCOPE)["receiptAvailable"]
    receipt = request("GET", plan + "/receipt" + SCOPE).decode()
    assert plan_id in receipt and "审核通过" in receipt
    request("GET", plan + "/receipt" + OUTSIDE, status=404)
    return {row["description"]: row["id"] for row in rows}


def verify_hazard_review(identifier):
    """验证整改单证据校验、复核、退回、重提、销项进度和回执。
    参数 identifier 为运营单位第一条整改单的标识。
    返回值 无。
    """
    regulated = ROOT_HAZARDS + "/" + identifier
    path = HAZARDS + "/" + identifier
    request("GET", path + "/history" + OUTSIDE, status=404)
    request("GET", path + "/receipt" + OUTSIDE, status=404)
    request("GET", path + "/receipt" + SCOPE, status=409)
    action = {"action": "submit", "expectedVersion": request("GET", path + SCOPE)["version"], "comment": "已完成整改"}
    request("POST", path + "/actions" + SCOPE, action, status=400)
    upload(path, "evidence", "repair-evidence.pdf", scope=SCOPE)
    action["expectedVersion"] = request("GET", path + SCOPE)["version"]
    assert request("POST", path + "/actions" + SCOPE, action)["status"] == "pending_retest"
    request("POST", path + "/actions" + SCOPE, action, status=409)
    assert request("GET", path + SCOPE)["receiptAvailable"]
    assert "提交" in request("GET", path + "/receipt" + SCOPE).decode()
    institution_retest(regulated, "retest_return", "请提交本轮核验结果")
    action["expectedVersion"] = request("GET", path + SCOPE)["version"]
    request("POST", path + "/actions" + SCOPE, action, status=400)
    upload(path, "evidence", "repair-evidence-next.pdf", scope=SCOPE)
    action["expectedVersion"] = request("GET", path + SCOPE)["version"]
    assert request("POST", path + "/actions" + SCOPE, action)["status"] == "pending_retest"
    institution_retest(regulated)
    hazard_action(regulated, "close", "完成复核", "closed")
    handle, scope = check_business_delivery(regulated, "operator", OWNER, "closed")
    assert handle == path and scope == SCOPE and request("GET", path + SCOPE)["status"] == "closed"
    history = request("GET", path + "/history" + SCOPE)["items"]
    assert [event["action"] for event in history] == ["close", "retest_pass", "submit", "retest_return", "submit"]
    assert "销项" in request("GET", path + "/receipt" + SCOPE).decode()
    print("PASS: 规划直达、单位隔离、筛选、证据与重复提交、整改时间线及回执")


def alert_action(path, action, status=200, operator=True, **fields):
    """在指定办理端使用最新版本提交一个预警动作。
    参数 path、action、status 指定业务地址、动作和期望 HTTP 状态，operator 指操作端，fields 提供必要说明和日期。
    返回值 实际预警变更结果或预期拒绝。
    """
    scope = SCOPE if operator else ""
    body = {"action": action, "expectedVersion": request("GET", path + scope)["version"], **fields}
    return request("POST", path + "/actions" + scope, body, status=status)


def verify_alerts(items, plan_id):
    """验证三种预警处置和跨端状态、通知、真实期限同步。
    参数 items 为来源工单名称与标识，plan_id 为来源规划。
    返回值 无。
    """
    request("POST", ROOT_ALERTS + "/sync")
    rows = request("GET", ALERTS + SCOPE)["items"]
    alerts = {row["hazardId"]: row for row in rows}
    assert set(items.values()) <= set(alerts)
    assert request("GET", ALERTS + OUTSIDE)["items"] == []
    observed = {}
    counter = API + "/operator/feedback/unread-count"
    initial_unread = request("GET", counter + SCOPE)["count"]
    assert initial_unread >= len(items) and request("GET", counter + OUTSIDE)["count"] == 0
    for name, hazard in items.items():
        alert_id = alerts[hazard]["id"]
        regulated = ROOT_ALERTS + "/" + alert_id
        path = ALERTS + "/" + alert_id
        request("GET", path + OUTSIDE, status=404)
        check_business_delivery(regulated, "operator", OWNER, "pending", current_status="pending")
        observed[name] = (regulated, path)
    assert request("GET", counter + SCOPE)["count"] == initial_unread - len(items)
    converted, scoped = observed["转办事项"]
    assert alert_action(scoped, "convert")["hazardId"] == items["转办事项"]
    alert_action(scoped, "convert", status=409)
    assert len(request("GET", HAZARDS + SCOPE + "&planId=" + plan_id)["items"]) == len(CASES)
    assert request("GET", converted)["disposition"]["status"] == "completed"
    print("PASS: 预警站内提醒、归属和转办幂等")

    extended, scoped = observed["延期事项"]
    new_date = business_date(10)
    alert_action(scoped, "request_extension", status=400, comment="日期无效", requestedDeadline=business_date(-3))
    pending = alert_action(scoped, "request_extension", comment="需要延长核验窗口", requestedDeadline=new_date)
    assert pending["disposition"]["status"] == "pending"
    assert request("GET", extended)["disposition"] == pending["disposition"]
    alert_action(scoped, "request_extension", status=409, comment="重复申请", requestedDeadline=new_date)
    alert_action(extended, "close", status=409, operator=False, comment="在审期间关闭")
    assert request("GET", API + "/workbench/overview")["pendingAlertReviews"] >= 1
    assert any(row["id"] == pending["id"] for row in request("GET", ROOT_ALERTS + "?review=pending")["items"])
    hazard_path = HAZARDS + "/" + items["延期事项"]
    assert request("GET", hazard_path + SCOPE)["deadline"] != new_date
    approved = alert_action(extended, "approve", operator=False, comment="核对后同意延期")
    assert approved["disposition"]["status"] == "approved"
    expiry = request("GET", scoped + SCOPE)
    assert expiry["deadline"] == new_date and not expiry.get("currentlyOverdue")
    assert request("GET", hazard_path + SCOPE)["deadline"] == new_date
    assert request("GET", hazard_path + "/history" + SCOPE)["items"][0]["action"] == "extend_deadline"
    check_business_delivery(extended, "operator", OWNER, "approved", current_status=expiry["status"])
    alert_action(scoped, "request_extension", comment="提交后工单已完成", requestedDeadline=business_date(12))
    upload(hazard_path, "evidence", "deadline-evidence.pdf", scope=SCOPE)
    next_version = request("GET", hazard_path + SCOPE)["version"]
    request("POST", hazard_path + "/actions" + SCOPE,
            {"action": "submit", "expectedVersion": next_version, "comment": "提前完成整改"})
    institution_retest(ROOT_HAZARDS + "/" + items["延期事项"])
    hazard_action(ROOT_HAZARDS + "/" + items["延期事项"], "close", "确认整改通过", "closed")
    reviewable = request("GET", extended)
    assert reviewable["allowedActions"] == ["reject"] and reviewable["approvalBlockedReason"]
    alert_action(extended, "approve", status=409, operator=False, comment="失效申请不得通过")
    rejected = alert_action(extended, "reject", operator=False, comment="整改单已销项")
    assert rejected["disposition"]["status"] == "rejected"
    print("PASS: 延期待审与期限回传、销项后拒绝过期审批")

    false_alarm, scoped = observed["误报事项"]
    alert_action(scoped, "report_false_alarm", comment="请核对数据来源")
    rejected = alert_action(false_alarm, "reject", operator=False, comment="请提供更完整的说明")
    assert rejected["disposition"]["status"] == "rejected"
    check_business_delivery(false_alarm, "operator", OWNER, "rejected", current_status=rejected["status"])
    alert_action(scoped, "report_false_alarm", comment="已经补充核验")
    closed = alert_action(false_alarm, "approve", operator=False, comment="已核验误报")
    assert closed["status"] == "closed" and closed["disposition"]["status"] == "approved"
    assert request("GET", HAZARDS + "/" + items["误报事项"] + SCOPE)["status"] == "open"
    check_business_delivery(false_alarm, "operator", OWNER, "approved", current_status="closed")
    assert not any(row["id"] == closed["id"] for row in request("GET", ROOT_ALERTS + "?review=pending")["items"])
    print("PASS: 误报驳回重提、监管核准、关预警保留整改单及回传")


if __name__ == "__main__":
    plan_id, filing_id = seed_plan()
    hazards = plan_hazards(plan_id, filing_id)
    verify_alerts(hazards, plan_id)
    verify_hazard_review(hazards["转办事项"])

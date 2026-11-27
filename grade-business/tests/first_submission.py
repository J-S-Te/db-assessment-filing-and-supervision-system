"""在隔离本地服务检验首次报送、监管办理和运营者整改。"""

from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode

from acceptance import (
    API,
    DOCUMENT,
    RUN,
    business_date,
    check_business_delivery,
    hazard_action,
    implementation_action,
    institution_retest,
    request,
    transition,
    upload,
)


INSTITUTION = "首报验收机构 " + RUN
ORGANIZATION = "首报验收运营单位 " + RUN
OTHER = "不同归属 " + RUN
INST_QUERY = "?" + urlencode({"institution": INSTITUTION})
OPER_QUERY = "?" + urlencode({"organization": ORGANIZATION})
REPORT_KINDS = (
    "report_file", "remediation_plan", "assessment_conclusion", "issues_recommendations",
    "cover_basic_statement", "cloud_extension", "big_data_extension", "major_risk_recommendations", "supporting",
)


def visible_state(regulator, scoped, query, expected):
    """比对监管端与办理端对同一记录的状态和版本。
    参数 regulator、scoped 为两端详情接口，query 为所属范围，expected 为预期状态。
    返回值 当前办理端详情。
    """
    source = request("GET", regulator)
    record = request("GET", scoped + query)
    assert source["id"] == record["id"] and source["version"] == record["version"]
    assert source["status"] == record["status"] == expected, (source, record)
    return record


def scoped_submit(path, query, expected, action=False):
    """以当前版本经指定端提交实际业务材料。
    参数 path、query 为范围详情，expected 是预期状态，action 表示实施动作接口。
    返回值 已提交的持久化状态及版本。
    """
    version = request("GET", path + query)["version"]
    body = {"expectedVersion": version}
    if action:
        body["action"] = "submit"
    result = request("POST", path + ("/actions" if action else "/submit") + query, body)
    assert result["status"] == expected, result
    return result


def verify_filing_submission():
    """提交机构首次备案，并通过监管初审终审批复。
    参数 无，编号、机构和运营单位来自当前测试。
    返回值 已审核通过的备案标识。
    """
    collection = API + "/institution/filings"
    data = {"number": "FIRST-F-" + RUN, "systemName": "三端首报验收系统",
            "organizationName": ORGANIZATION, "level": "3"}
    request("POST", collection + INST_QUERY, {**data, "assessmentOrg": OTHER}, status=400)
    draft = request("POST", collection + INST_QUERY, data, 201)
    identifier = draft["id"]
    scoped = collection + "/" + identifier
    admin = API + "/filings/" + identifier
    assert visible_state(admin, scoped, INST_QUERY, "draft")["assessmentOrg"] == INSTITUTION
    elsewhere = "?" + urlencode({"institution": OTHER})
    request("GET", scoped + elsewhere, status=404)
    assert request("GET", collection + elsewhere)["items"] == []
    request("POST", scoped + "/submit" + INST_QUERY, {"expectedVersion": draft["version"]}, status=400)
    upload(scoped, "filing_form", "first-filing.pdf", scope=INST_QUERY)
    upload(scoped, "classification_report", "classification.pdf", scope=INST_QUERY)
    scoped_submit(scoped, INST_QUERY, "pending_initial")
    visible_state(admin, scoped, INST_QUERY, "pending_initial")
    transition(admin, "approve", "pending_final")
    visible_state(admin, scoped, INST_QUERY, "pending_final")
    transition(admin, "approve", "approved")
    check_business_delivery(admin, "institution", INSTITUTION, "approved")
    visible_state(admin, scoped, INST_QUERY, "approved")
    print("PASS: 机构首次备案、材料校验、监管初终审及站内回执")
    return identifier


def verify_application_submission(filing_id, kind):
    """从机构端发起变更或注销并核对监管审批和备案结果。
    参数 filing_id 为当前有效备案，kind 为申请类型。
    返回值 无。
    """
    collection = API + "/institution/filing-applications"
    form = {"filingId": filing_id, "type": kind, "reason": "首报审批留痕"}
    if kind == "change":
        form["changes"] = {"systemName": "首报已变更系统 " + RUN}
    request("POST", collection + "?" + urlencode({"institution": OTHER}), form, status=404)
    item = request("POST", collection + INST_QUERY, form, 201)
    request("POST", collection + INST_QUERY, form, status=409)
    path = "/" + item["id"]
    admin = API + "/filing-applications" + path
    scoped = collection + path
    scoped_submit(scoped, INST_QUERY, "pending_initial")
    visible_state(admin, scoped, INST_QUERY, "pending_initial")
    transition(admin, "approve", "pending_final")
    transition(admin, "approve", "approved")
    check_business_delivery(admin, "institution", INSTITUTION, "approved")
    assert any(row["id"] == item["id"] for row in request("GET", collection + INST_QUERY)["items"])
    filing = request("GET", API + "/filings/" + filing_id)
    assert filing.get("cancelledAt") if kind == "cancel" else filing["systemName"] == form["changes"]["systemName"]
    if kind == "cancel":
        check_business_delivery(API + "/filings/" + filing_id, "institution", INSTITUTION,
                                "approved", current_status="cancelled")
    print("PASS: 机构首次申请" + ("变更" if kind == "change" else "注销") + "、生效与回执")


def verify_implementation_submission(filing_id):
    """为本机构备案创建并提交首份实施报备。
    参数 filing_id 为已通过的关联备案。
    返回值 无。
    """
    collection = API + "/institution/implementations"
    opts = request("GET", collection + "/filing-options" + INST_QUERY)["items"]
    assert filing_id in {item["id"] for item in opts}
    form = {"filingId": filing_id, "type": "routine", "reporter": "首报测试经办人",
            "plannedStart": business_date(2), "plannedEnd": business_date(5), "plan": "本地首报实施计划"}
    request("POST", collection + INST_QUERY, {**form, "assessmentOrg": OTHER}, status=400)
    draft = request("POST", collection + INST_QUERY, form, 201)
    scoped = collection + "/" + draft["id"]
    admin = API + "/implementations/" + draft["id"]
    scoped_submit(scoped, INST_QUERY, "submitted", action=True)
    visible_state(admin, scoped, INST_QUERY, "submitted")
    implementation_action(admin, "accept", "pending_initial")
    implementation_action(admin, "approve", "pending_final")
    implementation_action(admin, "approve", "pending_supervision")
    implementation_action(admin, "approve", "approved")
    visible_state(admin, scoped, INST_QUERY, "approved")
    check_business_delivery(admin, "institution", INSTITUTION, "approved")
    print("PASS: 机构首次实施报备、监管受理和复核、各端同源状态")


def verify_reports(filing_id):
    """检验 V2 报告九类材料、五项必传和历史规则兼容。
    参数 filing_id 为实库实际审核通过的备案。
    返回值 历史报告和机构新报告的标识。
    """
    base = API + "/reports"
    legacy = request("POST", base, {"number": "FIRST-OLD-R-" + RUN, "filingId": filing_id,
                      "risk": "medium", "assessmentOrg": INSTITUTION}, 201)["id"]
    old = base + "/" + legacy
    upload(old, "report_file", "legacy.pdf")
    transition(old, "submit", "pending_initial")
    transition(old, "approve", "pending_final")
    transition(old, "approve", "approved")
    institution = API + "/institution/reports"
    data = {"number": "FIRST-R-" + RUN, "filingId": filing_id, "risk": "medium"}
    request("POST", institution + "?" + urlencode({"institution": OTHER}), data, status=404)
    item = request("POST", institution + INST_QUERY, data, 201)
    path = "/" + item["id"]
    admin, scoped = base + path, institution + path
    draft = visible_state(admin, scoped, INST_QUERY, "draft")
    assert draft["requiredMaterials"] == list(REPORT_KINDS[:5]) and "edit" in draft["allowedActions"]
    for key in REPORT_KINDS[:4] + REPORT_KINDS[5:]:
        upload(scoped, key, key + ".pdf", scope=INST_QUERY)
    version = request("GET", scoped + INST_QUERY)["version"]
    request("POST", scoped + "/submit" + INST_QUERY, {"expectedVersion": version}, status=400)
    request("POST", admin + "/submit", {"expectedVersion": version}, status=400)
    upload(scoped, REPORT_KINDS[4], "cover-statement.pdf", scope=INST_QUERY)
    scoped_submit(scoped, INST_QUERY, "pending_initial")
    visible_state(admin, scoped, INST_QUERY, "pending_initial")
    transition(admin, "return_for_correction", "returned", "补充主要问题说明")
    visible_state(admin, scoped, INST_QUERY, "returned")
    upload(scoped, "issues_recommendations", "corrected.pdf", scope=INST_QUERY)
    scoped_submit(scoped, INST_QUERY, "pending_initial")
    transition(admin, "approve", "pending_final")
    transition(admin, "approve", "approved")
    visible_state(admin, scoped, INST_QUERY, "approved")
    feedback = API + "/institution/report-feedback"
    messages = [row for row in request("GET", feedback + INST_QUERY)["items"] if row["reportId"] == item["id"]]
    assert [row["decision"] for row in messages] == ["approved", "returned"]
    request("POST", feedback + "/" + messages[0]["id"] + "/acknowledge" + INST_QUERY)
    print("PASS: 九类测评材料、前五项必传、监管防绕过、补正、终审和签收")
    return legacy, item["id"]


def draft_plan(collection, query, report_id, evidence=False):
    """保存测试规划和完整三项必传材料。
    参数 collection、query 指定办理端，report_id 为关联测评，evidence 控制可选初次证据。
    返回值 规划标识。
    """
    deadline = business_date(9)
    form = {"reportId": report_id, "deadline": deadline, "risks": [
        {"risk": "high", "description": "制度留痕未登记", "responsibility": "本地整改组",
         "measure": "登记并复核整改记录", "deadline": deadline},
    ]}
    item = request("POST", collection + query, form, 201)
    path = collection + "/" + item["id"]
    request("POST", path + "/submit" + query, {"expectedVersion": item["version"]}, status=400)
    kinds = ("plan", "responsibility", "schedule")
    if evidence:
        kinds += ("high_risk_evidence",)
    for key in kinds:
        upload(path, key, key + ".pdf", report_id=report_id, scope=query)
    return item["id"]


def verify_operator_plan(filing_id, old_report, new_report):
    """验证运营者首办、跨端抢占、撤回释放、补正和转入隐患销项。
    参数 filing_id、old_report、new_report 为同一备案及其前后报告标识。
    返回值 无。
    """
    collection = API + "/operator/remediations"
    regulator = API + "/remediations"
    choices = request("GET", collection + "/eligible-reports" + OPER_QUERY)["items"]
    assert new_report in {row["id"] for row in choices} and old_report not in {row["id"] for row in choices}
    other_query = "?" + urlencode({"organization": OTHER})
    assert request("GET", collection + "/eligible-reports" + other_query)["items"] == []
    first = draft_plan(collection, OPER_QUERY, new_report, evidence=True)
    stale_report = draft_plan(regulator, "", old_report)
    admin = regulator + "/" + first
    scoped = collection + "/" + first
    request("GET", scoped + other_query, status=404)
    pending = scoped_submit(scoped, OPER_QUERY, "pending_review")
    visible_state(admin, scoped, OPER_QUERY, "pending_review")
    conflict = regulator + "/" + stale_report
    version = request("GET", conflict)["version"]
    outcome = request("POST", conflict + "/submit", {"expectedVersion": version}, status=409)
    assert outcome["code"] == "filing_in_review", outcome
    withdrawn = request("POST", scoped + "/withdraw" + OPER_QUERY, {"expectedVersion": pending["version"]})
    assert withdrawn["status"] == "withdrawn"
    visible_state(admin, scoped, OPER_QUERY, "withdrawn")
    candidates = [draft_plan(collection, OPER_QUERY, new_report) for _ in range(2)]
    second = submit_competition(collection, candidates)
    admin, scoped = regulator + "/" + second, collection + "/" + second
    visible_state(admin, scoped, OPER_QUERY, "pending_review")
    current = request("GET", admin)
    review = {"decision": "return_for_correction", "expectedVersion": current["version"],
              "deadline": current["deadline"], "reason": "请增加本轮证明", "comment": "首报监管补正"}
    assert request("POST", admin + "/review-actions", review)["status"] == "returned"
    check_business_delivery(admin, "operator", ORGANIZATION, "returned")
    upload(scoped, "high_risk_evidence", "new-proof.pdf", report_id=new_report, scope=OPER_QUERY)
    scoped_submit(scoped, OPER_QUERY, "pending_review")
    current = request("GET", admin)
    review = {"decision": "approve", "expectedVersion": current["version"],
              "deadline": current["deadline"], "comment": "补正核验通过"}
    assert request("POST", admin + "/review-actions", review)["status"] == "approved"
    check_business_delivery(admin, "operator", ORGANIZATION, "approved")
    visible_state(admin, scoped, OPER_QUERY, "approved")
    hazard = request("GET", API + "/hazards?planId=" + second)["items"][0]
    assert hazard["filingId"] == filing_id and hazard["reportId"] == new_report
    admin_hazard = API + "/hazards/" + hazard["id"]
    assigned, scope = check_business_delivery(admin_hazard, "operator", ORGANIZATION, "open")
    upload(assigned, "evidence", "proof.pdf", document=DOCUMENT, scope=scope)
    current = request("GET", assigned + scope)
    request("POST", assigned + "/actions" + scope,
            {"action": "submit", "expectedVersion": current["version"], "comment": "本地整改已完成"})
    institution_retest(admin_hazard)
    hazard_action(admin_hazard, "close", "复核销项", "closed")
    visible_state(admin_hazard, assigned, scope, "closed")
    assert request("GET", API + "/institution/reports/" + new_report + INST_QUERY)["status"] == "approved"
    assert request("GET", API + "/institution/filings/" + filing_id + INST_QUERY)["status"] == "approved"
    print("PASS: 运营者首次整改、同备案跨报告在审冲突、撤回释放、补正和隐患销项")


def submit_competition(collection, candidates):
    """同时提交同一备案的两个独立草稿并核对占位结果。
    参数 collection 为运营者集合接口，candidates 为两个材料齐备的规划标识。
    返回值 成功进入在审的规划标识。
    """
    # request 对 HTTP 状态做强断言，交由独立连接保存两个实际返回结果。
    from http.client import HTTPConnection
    from json import dumps, loads
    from acceptance import ORIGIN, PORT

    versions = {item: request("GET", collection + "/" + item + OPER_QUERY)["version"] for item in candidates}

    def submit(identifier):
        """发出独立连接上的真实 JSON 提交。
        参数 identifier 为当前测试规划。
        返回值 规划标识、HTTP 状态和已解码响应。
        """
        connection = HTTPConnection(ORIGIN, PORT, timeout=10)
        try:
            connection.request("POST", collection + "/" + identifier + "/submit" + OPER_QUERY,
                               dumps({"expectedVersion": versions[identifier]}), {"Content-Type": "application/json"})
            response = connection.getresponse()
            return identifier, response.status, loads(response.read())
        finally:
            connection.close()

    with ThreadPoolExecutor(max_workers=2) as pool:
        outcomes = list(pool.map(submit, candidates))
    assert sorted(row[1] for row in outcomes) == [200, 409], outcomes
    rejected = next(row for row in outcomes if row[1] == 409)
    assert rejected[2]["code"] == "filing_in_review"
    assert request("GET", collection + "/" + rejected[0] + OPER_QUERY)["status"] == "draft"
    return next(row[0] for row in outcomes if row[1] == 200)


if __name__ == "__main__":
    assert request("GET", "/readyz") == {"status": "ready"}
    filing_id = verify_filing_submission()
    verify_application_submission(filing_id, "change")
    verify_implementation_submission(filing_id)
    older, current = verify_reports(filing_id)
    verify_operator_plan(filing_id, older, current)
    verify_application_submission(filing_id, "cancel")

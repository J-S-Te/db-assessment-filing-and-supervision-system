"""在明确隔离的本机预览中验收运营者自主备案和换机构复测。"""

from urllib.parse import urlencode

from acceptance import (
    API, RUN, business_date, check_business_delivery, implementation_action, request, transition, upload,
)
from first_submission import REPORT_KINDS


OWNER = "自主备案测试单位 " + RUN
OLD = "原测评测试机构 " + RUN
TARGET = "目标测评测试机构 " + RUN
OTHER = "其他测试机构 " + RUN
OPER = "?" + urlencode({"organization": OWNER})
OUTSIDE = "?" + urlencode({"organization": OTHER})
OLD_SCOPE = "?" + urlencode({"institution": OLD})
TARGET_SCOPE = "?" + urlencode({"institution": TARGET})
FIRST_NUMBER = "OP-F-" + RUN


def scoped_path(admin, audience):
    """从监管接口取得同一笔事项的办理端路径。
    参数 admin 为监管地址，audience 为运营者或机构。
    返回值 办理端业务路径，不含归属查询。
    """
    return API + "/" + audience + admin.removeprefix(API)


def check_states(admin, expected, institution=OLD_SCOPE):
    """核对三端状态和版本是否来自同一笔业务。
    参数 admin 为监管地址，expected 为预期状态，institution 为当前机构范围。
    返回值 运营者接口上的完整详情。
    """
    root = request("GET", admin)
    operator = request("GET", scoped_path(admin, "operator") + OPER)
    partner = request("GET", scoped_path(admin, "institution") + institution)
    assert all(row["id"] == root["id"] and row["version"] == root["version"]
               and row["status"] == expected for row in (root, operator, partner)), (root, operator, partner)
    return operator


def institution_transition(admin, decision, expected, scope=OLD_SCOPE, comment="机构初审验收"):
    """从已有备案或申请的机构端办理初审。
    参数 admin 为监管详情，decision 为审核决定，expected 为目标状态，scope、comment 为归属与意见。
    返回值 审核写入后的状态和版本。
    """
    scoped = scoped_path(admin, "institution")
    current = request("GET", scoped + scope)
    body = {"expectedVersion": current["version"], "decision": decision, "comment": comment}
    result = request("POST", scoped + "/review-actions" + scope, body)
    assert result["status"] == expected and request("GET", admin)["status"] == expected, result
    return result


def scoped_action(path, action, http_status=200):
    """以当前运营者版本办理提交或撤回。
    参数 path 为运营者详情，action 为动作名，http_status 为预期 HTTP 状态。
    返回值 已提交的动作结果或预期业务错误。
    """
    version = request("GET", path + OPER)["version"]
    return request("POST", path + "/" + action + OPER, {"expectedVersion": version}, http_status)


def check_receipt(path, expected_text):
    """校验回执下载内容和办理范围。
    参数 path 为运营者详情，expected_text 为应写入回执的备案编号。
    返回值 无。
    """
    result = request("GET", path + "/receipt" + OPER).decode()
    assert expected_text in result, result
    request("GET", path + "/receipt" + OUTSIDE, status=404)


def verify_first_filing():
    """运营者首报和补正后取得审批回执。
    参数 无，测试资料使用本批独立编号。
    返回值 通过审核的监管备案路径。
    """
    collection = API + "/operator/filings"
    form = {"number": FIRST_NUMBER, "systemName": "自主备案首报测试系统", "assessmentOrg": OLD, "level": "3"}
    request("POST", collection + OPER, {**form, "organizationName": OTHER}, status=400)
    draft = request("POST", collection + OPER, form, 201)
    path = collection + "/" + draft["id"]
    admin = API + "/filings/" + draft["id"]
    request("GET", path + OUTSIDE, status=404)
    request("GET", path + "/receipt" + OPER, status=409)
    assert check_states(admin, "draft")["organizationName"] == OWNER
    assert request("GET", collection + OPER)["items"][0]["id"] == draft["id"]
    upload(path, "filing_form", "operator-filing.pdf", scope=OPER)
    scoped_action(path, "submit", 400)
    upload(path, "classification_report", "operator-classification.pdf", scope=OPER)
    assert scoped_action(path, "submit")["status"] == "pending_initial"
    check_states(admin, "pending_initial")
    check_receipt(path, FIRST_NUMBER)
    institution_transition(admin, "return_for_correction", "returned", comment="补充备案说明")
    check_states(admin, "returned")
    check_business_delivery(admin, "operator", OWNER, "returned")
    check_business_delivery(admin, "institution", OLD, "returned")
    assert scoped_action(path, "submit")["status"] == "pending_initial"
    institution_transition(admin, "approve", "pending_final")
    check_states(admin, "pending_final")
    transition(admin, "approve", "approved")
    result = check_states(admin, "approved")
    assert result["receiptAvailable"]
    check_receipt(path, FIRST_NUMBER)
    expected = request("GET", admin + "/review-history")["items"]
    assert len(expected) == 5 and request("GET", path + "/review-history" + OPER)["items"] == expected
    check_business_delivery(admin, "operator", OWNER, "approved")
    print("PASS: 自主首报、材料校验、退回重提、三端状态、签收及回执")
    return admin


def open_application(filing, kind, changes=None):
    """从运营者端发起备案变更或注销草稿。
    参数 filing 为监管备案路径，kind 为类型，changes 为变更字段。
    返回值 新申请的监管端路径和运营者端路径。
    """
    base = API + "/operator/filing-applications"
    body = {"filingId": filing.split("/")[-1], "type": kind, "reason": "运营者申请办理验收"}
    if changes:
        body["changes"] = changes
    row = request("POST", base + OPER, body, 201)
    assert row["status"] == "draft"
    return API + "/filing-applications/" + row["id"], base + "/" + row["id"]


def verify_change(filing):
    """运营者提交普通变更，验证材料及同一备案的在审约束。
    参数 filing 为待变更备案监管路径。
    返回值 无。
    """
    admin, path = open_application(filing, "change", {"systemName": "普通变更后的业务系统"})
    request("GET", path + OUTSIDE, status=404)
    scoped_action(path, "submit", 400)
    file = upload(path, "change_statement", "change-statement.pdf", scope=OPER)
    assert request("GET", admin + "/attachments/" + file["id"]).startswith(b"%PDF")
    assert scoped_action(path, "submit")["status"] == "pending_initial"
    duplicate = request("POST", API + "/operator/filing-applications" + OPER, {
        "filingId": filing.split("/")[-1], "type": "cancel", "reason": "尝试重复办理",
    }, 409)
    assert duplicate["code"] == "active_application", duplicate
    check_states(admin, "pending_initial")
    institution_transition(admin, "approve", "pending_final")
    transition(admin, "approve", "approved")
    check_states(admin, "approved")
    check_receipt(path, FIRST_NUMBER)
    assert check_states(filing, "approved")["systemName"] == "普通变更后的业务系统"
    print("PASS: 自主变更、监管材料下载、同一备案在审互斥及变更回执")


def start_implementation(filing, target):
    """创建并提交换机构复测的实施报备。
    参数 filing 为已有备案地址，target 为本条报备的目标测评机构。
    返回值 实施监管路径及运营者路径。
    """
    body = {"filingId": filing.split("/")[-1], "type": "change", "assessmentOrg": target,
            "reporter": "运营者验收人员", "plannedStart": business_date(2), "plannedEnd": business_date(7),
            "plan": "隔离本地验收，验证复测报备前置条件。"}
    base = API + "/operator/implementations"
    assert request("POST", base + OUTSIDE, body, status=400)["code"] == "invalid_filing"
    row = request("POST", base + OPER, body, 201)
    scoped = base + "/" + row["id"]
    assert scoped_action(scoped, "submit")["status"] == "submitted"
    return API + "/implementations/" + row["id"], scoped


def review_implementation(admin):
    """把变更实施报备推进到监管复核。
    参数 admin 为预期处于待受理的报备。
    返回值 无。
    """
    for action, expected in (("accept", "pending_initial"), ("approve", "pending_final"),
                             ("approve", "pending_supervision")):
        implementation_action(admin, action, expected)


def verify_retest(filing):
    """验证错机构和未复核的报备均无法支撑换机构，匹配完成后才能终审改派。
    参数 filing 为要换机构的备案。
    返回值 无。
    """
    case_id = filing.split("/")[-1]
    admin, path = open_application(filing, "change", {"assessmentOrg": TARGET})
    upload(path, "change_statement", "retest-statement.pdf", scope=OPER)
    assert scoped_action(path, "submit", 409)["code"] == "retest_required"
    assert "retestImplementation" not in request("GET", path + OPER)
    shadow, _ = start_implementation(filing, OTHER)
    review_implementation(shadow)
    implementation_action(shadow, "approve", "approved")
    assert scoped_action(path, "submit", 409)["code"] == "retest_required"
    assert "retestImplementation" not in request("GET", path + OPER)
    report, scoped = start_implementation(filing, TARGET)
    check_states(report, "submitted", TARGET_SCOPE)
    request("GET", scoped_path(report, "institution") + OLD_SCOPE, status=404)
    review_implementation(report)
    check_states(report, "pending_supervision", TARGET_SCOPE)
    report_id = report.split("/")[-1]
    assert request("GET", path + OPER)["retestImplementation"] == {"id": report_id, "status": "pending_supervision"}
    assert scoped_action(path, "submit", 409)["code"] == "retest_required"
    assert request("GET", path + OPER)["status"] == "draft"
    implementation_action(report, "approve", "approved")
    check_states(report, "approved", TARGET_SCOPE)
    check_business_delivery(report, "operator", OWNER, "approved")
    assert request("GET", scoped + "/review-history" + OPER)["items"]
    assert request("GET", path + OPER)["retestImplementation"] == {"id": report_id, "status": "approved"}
    assert scoped_action(path, "submit")["status"] == "pending_initial"
    assert check_states(admin, "pending_initial")["implementationId"] == report_id
    institution_transition(admin, "approve", "pending_final")
    assert check_states(filing, "approved")["assessmentOrg"] == OLD
    transition(admin, "approve", "approved")
    assert check_states(filing, "approved", TARGET_SCOPE)["assessmentOrg"] == TARGET
    assert any(row["id"] == case_id for row in request("GET", API + "/institution/filings" + TARGET_SCOPE)["items"])
    request("GET", scoped_path(filing, "institution") + OLD_SCOPE, status=404)
    check_business_delivery(admin, "operator", OWNER, "approved")
    verify_new_institution_report(case_id)
    print("PASS: 换机构复测的报备约束、终审改派、三端状态和新机构复测报告")


def verify_new_institution_report(filing_id):
    """由换入后的机构沿用测评报告流程提交和审批复测报告。
    参数 filing_id 为已确认改派生效的备案标识。
    返回值 无。
    """
    base = API + "/institution/reports"
    payload = {"number": "OP-RETEST-R-" + RUN, "filingId": filing_id, "risk": "medium"}
    request("POST", base + OLD_SCOPE, payload, status=404)
    draft = request("POST", base + TARGET_SCOPE, payload, status=201)
    path = base + "/" + draft["id"]
    admin = API + "/reports/" + draft["id"]
    for key in REPORT_KINDS[:5]:
        upload(path, key, key + ".pdf", scope=TARGET_SCOPE)
    details = request("GET", path + TARGET_SCOPE)
    request("POST", path + "/submit" + TARGET_SCOPE, {"expectedVersion": details["version"]})
    transition(admin, "approve", "pending_final")
    transition(admin, "approve", "approved")
    result = request("GET", path + TARGET_SCOPE)
    assert result["assessmentOrg"] == TARGET and result["status"] == "approved"
    request("GET", path + OLD_SCOPE, status=404)


def verify_cancellation(filing):
    """运营者注销换机构后的备案并下载证明。
    参数 filing 为已完成机构变更的有效备案。
    返回值 无。
    """
    admin, path = open_application(filing, "cancel")
    scoped_action(path, "submit", 400)
    upload(path, "cancellation_statement", "retention-statement.pdf", scope=OPER)
    scoped_action(path, "submit")
    check_states(admin, "pending_initial", TARGET_SCOPE)
    institution_transition(admin, "approve", "pending_final", TARGET_SCOPE)
    transition(admin, "approve", "approved")
    assert check_states(filing, "approved", TARGET_SCOPE)["cancelledAt"]
    check_receipt(path, FIRST_NUMBER)
    check_business_delivery(admin, "operator", OWNER, "approved")
    check_business_delivery(filing, "operator", OWNER, "approved", current_status="cancelled")
    print("PASS: 自主注销、数据处置说明、注销结果同步及回执")


if __name__ == "__main__":
    filing_path = verify_first_filing()
    verify_change(filing_path)
    verify_retest(filing_path)
    verify_cancellation(filing_path)

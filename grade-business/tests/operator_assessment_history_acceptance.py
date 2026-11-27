"""在隔离本地服务联验机构报告、监管核验和运营者历史对比。"""

import json
import os
from urllib.parse import urlencode

from acceptance import API, RUN, request, transition, upload


ORGANIZATION = "历史对比联验运营单位 " + RUN
INSTITUTION = "历史对比联验测评机构 " + RUN
SYSTEM_NAME = "历史对比联验系统 " + RUN
OPERATOR = API + "/operator/assessment-history"
REPORTS = API + "/reports"
SCOPED_REPORTS = API + "/institution/reports"
INST_QUERY = "?" + urlencode({"institution": INSTITUTION})
OPER_QUERY = "?" + urlencode({"organization": ORGANIZATION})


def register_system():
    """办理一次真实备案，作为两期报告的共同数据来源。
    参数 无，使用本次专用单位及测评机构。
    返回值 已经终审通过的测试备案标识。
    """
    filing = request("POST", API + "/filings", {
        "number": "AH-F-" + RUN, "systemName": SYSTEM_NAME, "organizationName": ORGANIZATION,
        "assessmentOrg": INSTITUTION, "level": "3",
    }, 201)
    path = API + "/filings/" + filing["id"]
    for kind in ("filing_form", "classification_report"):
        upload(path, kind, kind + ".pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return filing["id"]


def prior_report(filing):
    """登记只有报告正文的历史测评，用于核对旧数据兼容。
    参数 filing 为前后两期共用的已通过备案标识。
    返回值 监管报告地址和正文证据标识。
    """
    row = request("POST", REPORTS, {
        "number": "AH-R-" + RUN + "-PREV", "filingId": filing, "risk": "high", "assessmentOrg": INSTITUTION,
    }, 201)
    path = REPORTS + "/" + row["id"]
    evidence = upload(path, "report_file", "历史测评正文.pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return path, evidence["id"]


def current_report(filing):
    """经机构端实际报送本期五项材料并完成监管两级审核。
    参数 filing 为两期测评对应的同一备案标识。
    返回值 监管报告地址和规范结论证据标识。
    """
    row = request("POST", SCOPED_REPORTS + INST_QUERY, {
        "number": "AH-R-" + RUN + "-CURRENT", "filingId": filing, "risk": "low",
    }, 201)
    scoped = SCOPED_REPORTS + "/" + row["id"]
    path = REPORTS + "/" + row["id"]
    files = {}
    for kind in ("report_file", "remediation_plan", "assessment_conclusion",
                 "issues_recommendations", "cover_basic_statement"):
        files[kind] = upload(scoped, kind, kind + ".pdf", scope=INST_QUERY)["id"]
    assert not request("GET", path + "/assessment-result")["editable"]
    version = request("GET", scoped + INST_QUERY)["version"]
    posted = request("POST", scoped + "/submit" + INST_QUERY, {"expectedVersion": version})
    assert posted["status"] == "pending_initial"
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return path, files["assessment_conclusion"]


def record_result(path, evidence, score, high, conclusion, extra_checks=False):
    """以实际报告材料核验结果并确认重复请求只产生一个版本。
    参数 path、evidence 指报告和证据，score、high、conclusion 为合成业务结果。
    参数 extra_checks 表示是否验证旧版本与额外字段的拒绝行为。
    返回值 已在监管侧登记的结构化结果。
    """
    endpoint = path + "/assessment-result"
    editor = request("GET", endpoint)
    assert editor["editable"] and "result" not in editor and len(editor["evidence"]) == 1, editor
    assert editor["evidence"][0]["id"] == evidence
    body = {"expectedReportVersion": request("GET", path)["version"], "expectedResultVersion": 0,
            "evidenceId": evidence, "score": score, "conclusion": conclusion, "high": high, "medium": 1, "low": 0}
    saved = request("POST", endpoint, body)
    assert saved == request("POST", endpoint, body) and saved["version"] == 1 and saved["actor"]
    if extra_checks:
        request("POST", endpoint, {**body, "score": 100.001}, status=400)
        request("POST", endpoint, {**body, "score": 99}, status=409)
        request("POST", endpoint, {**body, "clientIp": "192.0.2.10"}, status=400)
    assert request("GET", endpoint + "/history")["items"] == [saved]
    return saved


def verify_operator_history():
    """验证运营者在同备案中对比真实得分、结论及风险变化。
    参数 无，所有数据由当前隔离 API 录入。
    返回值 便于浏览器复用的已创建业务标识。
    """
    filing = register_system()
    old_path, old_evidence = prior_report(filing)
    new_path, new_evidence = current_report(filing)
    systems = request("GET", OPERATOR + "/systems" + OPER_QUERY)["items"]
    assert systems == [{"id": filing, "number": "AH-F-" + RUN, "systemName": SYSTEM_NAME}], systems
    path = OPERATOR + "/" + filing
    outsider = "?" + urlencode({"organization": ORGANIZATION + " 外部"})
    assert request("GET", OPERATOR + "/systems" + outsider)["items"] == []
    request("GET", path + outsider, status=404)
    before = request("GET", path + OPER_QUERY)
    assert len(before["items"]) == 2 and all("result" not in item for item in before["items"]), before
    record_result(old_path, old_evidence, 75.5, 4, "basically_conforming")
    record_result(new_path, new_evidence, 91.25, 0, "conforming", extra_checks=True)
    old_id, new_id = old_path.rsplit("/", 1)[-1], new_path.rsplit("/", 1)[-1]
    query = "&" + urlencode({"from": old_id, "to": new_id})
    comparison = request("GET", path + "/comparison" + OPER_QUERY + query)
    assert comparison["riskChange"] == "decreased" and comparison["scoreDelta"] == 15.75, comparison
    assert comparison["highDelta"] == -4 and comparison["mediumDelta"] == comparison["lowDelta"] == 0
    page = request("GET", path + OPER_QUERY)
    assert [item["id"] for item in page["items"]] == [new_id, old_id], page
    for item in page["items"]:
        assert set(item) == {"id", "number", "assessmentOrg", "risk", "assessedAt", "result"}, item
        assert set(item["result"]) == {"score", "conclusion", "high", "medium", "low"}, item
    print("PASS: 机构报送、监管证据核验及幂等留痕、运营者同备案对比")
    return {"organization": ORGANIZATION, "filingId": filing, "priorReportId": old_id, "currentReportId": new_id}


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请显式指定本轮隔离业务预览的 GRADE_ACCEPTANCE_PORT")
    print(json.dumps(verify_operator_history(), ensure_ascii=False))

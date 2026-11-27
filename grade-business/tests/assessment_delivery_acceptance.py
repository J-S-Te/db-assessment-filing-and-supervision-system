"""在隔离预览联验运营者归档材料、整改要求和归属变更后的下载范围。"""

import json
import os
from urllib.parse import urlencode

from acceptance import API, DOCUMENT, RUN, business_date, request, transition, upload
from directory_acceptance import DIRECTORY, credit_code
from operator_assessment_history_acceptance import INSTITUTION, ORGANIZATION, OPER_QUERY, OPERATOR
from operator_assessment_history_acceptance import current_report, prior_report, register_system
from unit_merge_acceptance import MERGES, create_unit, operator_scope


MEASURE = "补齐并核对年度运行制度"


def material_path(filing, report):
    """定位指定备案与终审报告的材料与整改要求。
    参数 filing、report 分别为实际备案和报告标识。
    返回值 可追加范围查询或附件标识的 API 路径。
    """
    return f"{OPERATOR}/{filing}/reports/{report}/materials"


def approve_requirements(report):
    """保存并批准本期报告对应的整改规划。
    参数 report 为已经终审通过的报告标识。
    返回值 审核确认的整改要求期限。
    """
    deadline = business_date(14)
    row = request("POST", API + "/remediations", {
        "reportId": report, "deadline": deadline, "risks": [{
            "risk": "low", "description": "内部制度记录待更新", "measure": MEASURE,
            "responsibility": "隔离内部责任组", "deadline": deadline,
        }],
    }, 201)
    path = API + "/remediations/" + row["id"]
    for kind in ("plan", "responsibility", "schedule"):
        upload(path, kind, kind + ".pdf", report_id=report)
    submitted = transition(path, "submit", "pending_review")
    approved = request("POST", path + "/review-actions", {
        "decision": "approve", "comment": "归档联验确认要求", "expectedVersion": submitted["version"],
        "deadline": deadline,
    })
    assert approved["status"] == "approved"
    return deadline


def verify_material_delivery():
    """核对合法下载、Range、同备案终审范围和已审批整改要求。
    参数 无，运行标识隔离每次业务数据。
    返回值 供最终浏览器联验复用的测试单位、备案和报告标识。
    """
    filing = register_system()
    report_path, internal = current_report(filing)
    report = report_path.rsplit("/", 1)[-1]
    route = material_path(filing, report)
    before = request("GET", route + OPER_QUERY)
    assert set(before) == {"items"}, before
    assert [file["kind"] for file in before["items"]] == ["report_file", "remediation_plan"], before
    for file in before["items"]:
        assert set(file) == {"id", "kind", "fileName", "size", "uploadedAt"}
        download = route + "/" + file["id"]
        assert request("GET", download + OPER_QUERY) == DOCUMENT
        assert request("GET", download + OPER_QUERY, status=206,
                       headers={"Range": "bytes=0-3"}) == b"%PDF"
        request("GET", download + "?" + urlencode({"organization": "其他单位"}), status=404)
    request("GET", route + "/" + internal + OPER_QUERY, status=404)
    request("GET", route + OPER_QUERY + "&" + urlencode({"organization": "重复单位"}), status=400)
    draft = request("POST", API + "/reports", {
        "number": "DELIVERY-DRAFT-" + RUN, "filingId": filing,
        "risk": "low", "assessmentOrg": INSTITUTION,
    }, 201)
    request("GET", material_path(filing, draft["id"]) + OPER_QUERY, status=404)
    deadline = approve_requirements(report)
    after = request("GET", route + OPER_QUERY)
    assert after["items"] == before["items"]
    assert after["requirements"] == {"deadline": deadline, "items": [{
        "risk": "low", "description": "内部制度记录待更新", "measure": MEASURE, "deadline": deadline,
    }]}, after
    print("PASS: 终审报告、原件字节与 Range、内部材料隔离、已审批整改要求")
    return {"organization": ORGANIZATION, "filingId": filing, "reportId": report,
            "reportNumber": "AH-R-" + RUN + "-CURRENT", "requirement": MEASURE}


def verify_no_historical_inheritance():
    """通过真实单位合并核对历史报告原件不会随备案改派自动开放。
    参数 无，当前运行生成新的主档及来源档案。
    返回值 无。
    """
    source_name = "归档历史来源 " + RUN
    target = create_unit("归档主档")
    filing = request("POST", API + "/filings", {
        "number": "DELIVERY-MERGE-" + RUN, "systemName": "归档归属联验 " + RUN,
        "organizationName": source_name, "assessmentOrg": INSTITUTION, "level": "2",
    }, 201)["id"]
    filing_path = API + "/filings/" + filing
    for kind in ("filing_form", "classification_report"):
        upload(filing_path, kind, kind + ".pdf")
    transition(filing_path, "submit", "pending_initial")
    transition(filing_path, "approve", "pending_final")
    transition(filing_path, "approve", "approved")
    report_path, evidence = prior_report(filing)
    assert not request("GET", report_path).get("directorySnapshot")
    path = material_path(filing, report_path.rsplit("/", 1)[-1])
    download = path + "/" + evidence
    assert request("GET", download + "?" + urlencode({"organization": source_name})) == DOCUMENT
    source = request("POST", DIRECTORY + "/units", {"name": source_name, "code": credit_code()}, 201)
    merge = {"targetId": target["id"], "sourceIds": [source["id"]]}
    preview = request("POST", MERGES + "/preview", merge)
    receipt = request("POST", MERGES, {**merge, "token": preview["token"]})
    assert receipt["movedCount"] == 1
    reference = request("GET", filing_path)
    assert reference["organizationName"] == target["scopeName"]
    for unit in (source, target):
        request("GET", path + operator_scope(unit), status=404)
        request("GET", download + operator_scope(unit), status=404)
    assert request("GET", report_path + "/attachments/" + evidence) == DOCUMENT
    print("PASS: 合并后单位访问边界、历史测评材料仍可由监管追溯")


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请显式指定本轮隔离预览 GRADE_ACCEPTANCE_PORT")
    fixture = verify_material_delivery()
    verify_no_historical_inheritance()
    print(json.dumps(fixture, ensure_ascii=False))

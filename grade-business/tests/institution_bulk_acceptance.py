"""在本机隔离预览验证机构批量办理、复制和测评台账。"""

import csv
import io
import json
import os
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode
from uuid import uuid4

from acceptance import API, RUN, request, transition, upload


FILINGS = API + "/institution/filings"
REPORTS = API + "/institution/reports"
INSTITUTION = "批量联验机构 " + RUN
UNIT = "批量联验单位 " + RUN
SCOPE = "?" + urlencode({"institution": INSTITUTION})
OTHER = "?" + urlencode({"institution": INSTITUTION + "其他"})


def filing(suffix, scope=SCOPE, complete=False):
    """创建本批的合成备案并按需上传两份真实材料。
    参数 suffix、scope 区分记录与机构，complete 控制是否上传完整材料。
    返回值 新备案的详情及当前版本。
    """
    row = request("POST", FILINGS + scope, {
        "number": "BULK-" + RUN + suffix, "systemName": "批量联验系统 " + suffix,
        "organizationName": UNIT, "level": "3",
    }, 201)
    path = FILINGS + "/" + row["id"]
    if complete:
        for category in ("filing_form", "classification_report"):
            upload(path, category, category + ".pdf", scope=scope)
    return request("GET", path + scope)


def batch_input(action, rows):
    """构造预期版本明确、可原样重放的批次。
    参数 action 为业务动作，rows 为当前已查询的记录摘要。
    返回值 可以传到真实接口的最小业务字段。
    """
    return {"action": action, "requestId": str(uuid4()),
            "items": [{"id": row["id"], "expectedVersion": row["version"]} for row in rows]}


def verify_bulk(full, missing, foreign):
    """核对批送逐条结果、并发写入和按状态删除。
    参数 full、missing、foreign 为材料齐备、缺材料和其他机构的独立样本。
    返回值 无。
    """
    batch = FILINGS + "/batch" + SCOPE
    body = batch_input("submit", [full, missing, foreign])
    with ThreadPoolExecutor(max_workers=2) as workers:
        results = [workers.submit(request, "POST", batch, body) for _ in range(2)]
        first, second = (future.result() for future in results)
    assert first == second and [row["outcome"] for row in first["items"]] == ["submitted", "rejected", "rejected"]
    assert [row.get("code") for row in first["items"]] == [None, "materials_required", "not_found"]
    assert all("version" not in row for row in first["items"][1:])
    assert request("POST", batch, {**body, "items": body["items"][:1]}, 409)["code"] == "conflict"
    assert request("POST", batch, batch_input("submit", [full, full]), 400)["code"] == "invalid_input"
    current = request("GET", FILINGS + "/" + full["id"] + SCOPE)
    history = request("GET", FILINGS + "/" + full["id"] + "/review-history" + SCOPE)["items"]
    assert current["status"] == "pending_initial" and len(history) == 1
    removal = batch_input("discard", [current, missing, foreign])
    result = request("POST", batch, removal)
    assert result == request("POST", batch, removal)
    assert [row["outcome"] for row in result["items"]] == ["rejected", "discarded", "rejected"]
    request("GET", FILINGS + "/" + missing["id"] + SCOPE, status=404)
    assert request("GET", FILINGS + "/" + foreign["id"] + OTHER)["status"] == "draft"


def verify_copy(source):
    """核对源记录范围、复制幂等和新草稿材料独立。
    参数 source 为本机构带材料的源备案。
    返回值 无。
    """
    path = FILINGS + "/" + source["id"] + "/copy"
    body = {"number": "COPY-" + RUN, "expectedVersion": source["version"], "requestId": str(uuid4())}
    request("POST", path + OTHER, body, 404)
    pair = request("POST", path + SCOPE, body, 201)
    assert pair == request("POST", path + SCOPE, body, 201)
    assert pair["id"] != source["id"] and pair["status"] == "draft" and pair["version"] == 1
    request("POST", path + SCOPE, {**body, "number": body["number"] + "-DIFFERENT"}, 409)
    path = FILINGS + "/" + pair["id"]
    target = request("GET", path + SCOPE)
    for field in ("systemName", "organizationName", "assessmentOrg", "level"):
        assert target[field] == source[field]
    assert request("GET", path + "/attachments" + SCOPE)["items"] == []
    request("POST", path + "/submit" + SCOPE, {"expectedVersion": target["version"]}, 400)


def verify_report_export(source):
    """核对机构专属台账、当前筛选和安全 CSV 字段。
    参数 source 为可以通过的真实机构备案。
    返回值 可复用作页面联验的报告编号。
    """
    admin = API + "/filings/" + source["id"]
    transition(admin, "approve", "pending_final")
    transition(admin, "approve", "approved")
    report_number = "+BULK-REPORT-" + RUN
    for number, assessment_org, risk in [
        (report_number, INSTITUTION, "high"),
        ("BULK-REPORT-LOW-" + RUN, INSTITUTION, "low"),
        ("BULK-REPORT-OTHER-" + RUN, INSTITUTION + "其他", "high"),
    ]:
        request("POST", API + "/reports", {
            "number": number, "assessmentOrg": assessment_org, "filingId": source["id"], "risk": risk,
        }, 201)
    export = REPORTS + "/export"
    data = request("GET", export + SCOPE + "&risk=high&status=draft").decode("utf-8-sig")
    rows = list(csv.reader(io.StringIO(data)))
    assert len(rows) == 2 and len(rows[0]) == 6 and rows[1][0] == "'" + report_number
    assert rows[1][2] == INSTITUTION and rows[1][4] == "高风险"
    request("GET", export + "?risk=high", status=400)
    request("GET", export + SCOPE + "&cursor=ignored", status=400)
    return report_number


if __name__ == "__main__":
    assert os.environ.get("GRADE_ACCEPTANCE_PORT"), "请明确选择本轮独立本地端口"
    original = filing("-SOURCE", complete=True)
    verify_copy(original)
    print("PASS: 机构备案复制、范围隔离、重复请求以及新草稿独立材料")
    verify_bulk(original, filing("-MISSING"), filing("-OTHER", OTHER))
    print("PASS: 批送材料校验、状态限制、逐条范围、并发重放及删除")
    report_no = verify_report_export(original)
    print("PASS: 机构测评按登记范围和风险筛选导出，CSV 列和内容校验")
    ready = filing("-UI-READY", complete=True)
    unfinished = filing("-UI-MISSING")
    print(json.dumps({"institution": INSTITUTION, "sourceNumber": original["number"],
                      "sourceId": original["id"], "reportNumber": report_no,
                      "readyNumber": ready["number"], "incompleteNumber": unfinished["number"]},
                     ensure_ascii=False))

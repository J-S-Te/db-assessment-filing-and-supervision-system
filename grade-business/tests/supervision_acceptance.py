"""使用本机隔离预览验证监督检查自查报送以及真实超期线索关联。"""

import os
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode
from uuid import uuid4

from acceptance import API, DOCUMENT, RUN, business_date, request
from hazard_ledger_acceptance import registration_filing, registration_payload


REGULATOR = API + "/supervisions"
OPERATOR = API + "/operator/supervisions"


def upload_material(path, scope, kind, version, document=DOCUMENT, status=201):
    """上传当前任务版本下的单份监督检查材料。
    参数 path、scope 为任务和单位，kind、version、document 为附件数据，status 为期望状态。
    返回值 已保存的最小材料字段和任务版本。
    """
    boundary = "supervision-" + RUN
    parts = [
        f'--{boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\n{kind}\r\n',
        f'--{boundary}\r\nContent-Disposition: form-data; name="expectedVersion"\r\n\r\n{version}\r\n',
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{kind}.pdf"\r\n'
        'Content-Type: application/pdf\r\n\r\n',
    ]
    body = "".join(parts).encode() + document + f"\r\n--{boundary}--\r\n".encode()
    return request("POST", path + "/attachments" + scope, body, status, {
        "Content-Type": "multipart/form-data; boundary=" + boundary,
    })


def action(path, scope, command, version, expected=200, **fields):
    """依据已经看到的任务版本办理状态操作。
    参数 path、scope、command、version 标记范围与动作，expected 是状态码，fields 为审核说明。
    返回值 业务动作结果或拒绝说明。
    """
    return request("POST", path + "/actions" + scope, {
        "action": command, "expectedVersion": version, **fields,
    }, expected)


def create_annual(filing, owner):
    """创建去重的真实年度任务，区分监管草稿与运营者可见任务。
    参数 filing、owner 为已批准的备案及运营单位。
    返回值 监管任务、运营者任务、运营者范围和原草稿。
    """
    options = request("GET", REGULATOR + "/filing-options?" + urlencode({"q": RUN}))["items"]
    assert any(item["id"] == filing and item["organizationName"] == owner for item in options)
    annual = {"filingId": filing, "title": "年度监督自查联验 " + RUN,
              "dueOn": business_date(10), "requestId": str(uuid4())}
    with ThreadPoolExecutor(max_workers=2) as executor:
        attempts = [executor.submit(request, "POST", REGULATOR, annual, 201) for _ in range(2)]
        first, duplicate = (item.result() for item in attempts)
    assert first == duplicate and first["status"] == "draft"
    request("POST", REGULATOR, {**annual, "requestId": str(uuid4())}, 409)
    regulator = REGULATOR + "/" + first["id"]
    operator = OPERATOR + "/" + first["id"]
    scope = "?" + urlencode({"organization": owner})
    request("GET", operator + scope, status=404)
    action(regulator, "", "publish", first["version"], 422)
    invalid = upload_material(regulator, "", "inspection_template", first["version"], b"invalid", 415)
    assert invalid["code"] == "unsupported_attachment"
    return regulator, operator, scope, first


def verify_publication(regulator, operator, scope, first, other):
    """核对两份模板及只对正确备案单位下发，状态与待办同步。
    参数 regulator、operator、scope、first 为两端任务和初始记录，other 为错误单位范围。
    返回值 下发后的版本。
    """
    replaced = upload_material(regulator, "", "inspection_template", first["version"], DOCUMENT + b"\n")
    one = upload_material(regulator, "", "inspection_template", replaced["version"])
    two = upload_material(regulator, "", "summary_template", one["version"])
    published = action(regulator, "", "publish", two["version"])
    assert published == action(regulator, "", "publish", two["version"])
    template = operator + "/attachments/" + one["attachment"]["id"]
    assert request("GET", template + scope) == DOCUMENT
    request("GET", operator + "/attachments/" + replaced["attachment"]["id"] + scope, status=404)
    request("GET", template + other, status=404)
    request("GET", operator + other, status=404)
    details = request("GET", operator + scope)
    assert details["status"] == "open" and len(details["attachments"]) == 2
    deadlines = request("GET", API + "/operator/workbench/deadlines" + scope)["items"]
    assert any(t["id"] == first["id"] and t["deadline"] == first["dueOn"] for t in deadlines)
    action(operator, scope, "submit", published["version"], 422)
    return published["version"]


def verify_submission(regulator, operator, scope, current):
    """核对判定材料、退回指定类别、原证据留存和再次提交。
    参数 regulator、operator、scope 为两端任务和归属，current 为开始材料办理的版本。
    返回值 已办结的任务结果。
    """
    unsent = upload_material(operator, scope, "inspection_form", current, DOCUMENT + b"\n")
    form = upload_material(operator, scope, "inspection_form", unsent["version"])
    early_file = regulator + "/attachments/" + form["attachment"]["id"]
    request("GET", early_file, status=404)
    summary = upload_material(operator, scope, "rectification_summary", form["version"])
    submitted = action(operator, scope, "submit", summary["version"])
    assert request("GET", early_file) == DOCUMENT
    request("GET", regulator + "/attachments/" + unsent["attachment"]["id"], status=404)
    returned = action(regulator, "", "return", submitted["version"],
                      comment="更新汇总表", correctionKind="rectification_summary")
    action(operator, scope, "submit", returned["version"], 422)
    replay = upload_material(operator, scope, "rectification_summary", returned["version"])
    assert replay["version"] == returned["version"] and replay["attachment"] == summary["attachment"]
    fixed = upload_material(operator, scope, "rectification_summary", returned["version"], DOCUMENT + b"\n")
    updated = action(operator, scope, "submit", fixed["version"])
    approved = action(regulator, "", "approve", updated["version"], comment="核准办理")
    events = request("GET", operator + "/history" + scope)["items"]
    submissions = [event for event in events if event["action"] == "submit"]
    assert [item["rectification"]["open"] for item in submissions] == [1, 1]
    assert [item["materials"][1]["id"] for item in submissions] == [fixed["attachment"]["id"], summary["attachment"]["id"]]
    assert approved["status"] == "approved"
    assert request("GET", operator + scope)["status"] == "approved"
    assert request("GET", early_file) == DOCUMENT
    return approved


def assert_notices(owner_scope, other_scope, identifier):
    """验证通知只到原收件单位，已读回执和当前业务状态独立。
    参数 owner_scope、other_scope 为单位范围，identifier 为自查任务。
    返回值 无。
    """
    inbox = API + "/operator/feedback"
    items = request("GET", inbox + owner_scope)["items"]
    rows = [item for item in items if item["resource"] == "supervisions" and item["resourceId"] == identifier]
    assert sorted(row["state"] for row in rows) == ["approved", "open", "returned"]
    assert {row["status"] for row in rows} == {"approved"}
    for row in rows:
        request("POST", inbox + "/" + row["id"] + "/acknowledge" + other_scope, status=404)
    path = inbox + "/" + rows[0]["id"] + "/acknowledge" + owner_scope
    assert request("POST", path) == request("POST", path)
    assert not any(item["resourceId"] == identifier for item in request("GET", inbox + other_scope)["items"])


def prepare_lead(filing):
    """从实际超期的已派发隐患生成真实线索。
    参数 filing 是测试备案标识。
    返回值 监管超期线索的预警标识。
    """
    payload = {**registration_payload(filing, "major_risk"),
               "discoveredOn": business_date(-3), "deadline": business_date(-1)}
    hazard = request("POST", API + "/hazards", payload, 201)
    action(API + "/hazards/" + hazard["id"], "", "dispatch", hazard["version"])
    request("POST", API + "/alerts/sync")
    alerts = request("GET", API + "/alerts?type=overdue")["items"]
    identifier = next(item["id"] for item in alerts if item["hazardId"] == hazard["id"])
    assert any(item["alertId"] == identifier for item in
               request("GET", API + "/alerts/supervision-leads")["items"])
    return identifier


def verify_lead(filing, owner, lead):
    """验证原线索只能分派本备案，独立于相同年度自查记录。
    参数 filing、owner、lead 为真实备案、运营单位和超期线索。
    返回值 无。
    """
    options = request("GET", REGULATOR + "/filing-options?" + urlencode({"leadAlertId": lead}))["items"]
    assert [(item["id"], item["organizationName"]) for item in options] == [(filing, owner)]
    payload = {"filingId": str(uuid4()), "title": "线索自查 " + RUN, "dueOn": business_date(5),
               "requestId": str(uuid4()), "leadAlertId": lead}
    request("POST", REGULATOR, payload, 409)
    result = request("POST", REGULATOR, {**payload, "filingId": filing}, 201)
    assert result["status"] == "draft" and result["leadAlertId"] == lead
    request("POST", REGULATOR, {**payload, "filingId": filing, "requestId": str(uuid4())}, 409)


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请显式指定隔离本地预览端口。")
    filing, owner, _ = registration_filing()
    lead = prepare_lead(filing)
    wrong_scope = "?" + urlencode({"organization": owner + "无关"})
    reg, op, scope, first = create_annual(filing, owner)
    version = verify_publication(reg, op, scope, first, wrong_scope)
    approved = verify_submission(reg, op, scope, version)
    assert_notices(scope, wrong_scope, approved["id"])
    print(f"PASS: 监督自查模板、收件待办、按轮材料、重复提交去重与原范围回执（{RUN}）")
    verify_lead(filing, owner, lead)
    print("PASS: 实际超期线索可取对应备案并单独创建任务，其他备案和重复办理被拒绝")

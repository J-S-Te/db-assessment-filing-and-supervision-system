"""在本机隔离库验证等保管理端主业务的真实接口。"""

from datetime import datetime, timedelta
from http.client import HTTPConnection
from io import BytesIO
import json
import os
import time
from urllib.parse import urlencode
from uuid import uuid4
from zipfile import ZipFile
from zoneinfo import ZoneInfo


ORIGIN = "127.0.0.1"
PORT = int(os.environ.get("GRADE_ACCEPTANCE_PORT", "18444"))
API = "/api/v1"
RUN = uuid4().hex[:10]
DOCUMENT = b"%PDF-1.4\nLocal only: traceable business acceptance fixture\n%%EOF\n"


def request(method, path, payload=None, status=200, headers=None):
    """向本机真实服务发起请求并核对状态。
    参数 method、path 指请求方法及地址，payload 为输入，status 为预期 HTTP 状态，headers 为附加请求头。
    返回值 经过 JSON 解码或原始字节形式的响应体。
    """
    connection = HTTPConnection(ORIGIN, PORT, timeout=10)
    request_headers = dict(headers or {})
    body = payload
    if isinstance(body, dict):
        body = json.dumps(payload, ensure_ascii=False).encode()
        request_headers.setdefault("Content-Type", "application/json")
    try:
        connection.request(method, path, body, request_headers)
        response = connection.getresponse()
        data = response.read()
        assert response.status == status, (method, path, response.status, data[:300])
        return json.loads(data) if "application/json" in (response.getheader("Content-Type") or "") else data
    finally:
        connection.close()


def upload(path, kind, name, document=DOCUMENT, report_id=None, status=201, scope="", expected_version=None):
    """上传一份虚构业务材料，并核对下载内容。
    参数 path、kind、name、document 指定地址和材料，report_id、status 和 scope 为归属、预期状态与机构范围，expected_version 指定可选隐患版本。
    返回值 新材料字段，或预期的业务拒绝响应。
    """
    boundary = "grade-" + RUN
    headers = {"Content-Type": "multipart/form-data; boundary=" + boundary}
    if kind in {"evidence", "retest_report"} and "/hazards/" in path:
        version = expected_version if expected_version is not None else request("GET", path + scope)["version"]
        headers["If-Match"] = f'"{version}"'
    prefix = f'--{boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\n{kind}\r\n'
    if report_id:
        prefix += f'--{boundary}\r\nContent-Disposition: form-data; name="reportId"\r\n\r\n{report_id}\r\n'
    part = f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n'
    suffix = f"\r\n--{boundary}--\r\n"
    record = request("POST", path + "/attachments" + scope,
                     (prefix + part).encode() + document + suffix.encode(), status, headers)
    if status != 201:
        return record
    assert set(record) == {"id", "kind", "fileName", "size", "uploadedAt"}, record
    assert request("GET", path + "/attachments/" + record["id"] + scope) == document
    return record


def precheck(path):
    """发起规则预审并轮询真实后台任务。
    参数 path 是需要验证的备案地址。
    返回值 材料版本对应的已完成规则结果。
    """
    job = request("POST", path + "/prechecks", status=202)
    duplicate = request("POST", path + "/prechecks", status=202)
    assert job["id"] == duplicate["id"]
    for _ in range(50):
        snapshot = request("GET", path + "/prechecks/" + job["id"])
        if snapshot["status"] == "succeeded":
            return snapshot
        assert snapshot["status"] in {"queued", "running"}, snapshot
        time.sleep(0.1)
    raise AssertionError("预审任务未完成")


def transition(path, action, expected, comment=None):
    """按当前版本执行提交或审核，检查响应状态与字段。
    参数 path 为记录地址，action 为操作，expected 为预期阶段，comment 为人工意见。
    返回值 已保存的业务状态和版本。
    """
    current = request("GET", path)
    body = {"expectedVersion": current["version"]}
    if action == "submit":
        result = request("POST", path + "/submit", body)
    else:
        body["decision"] = action
        if comment:
            body["comment"] = comment
        result = request("POST", path + "/review-actions", body)
    assert result["status"] == expected and set(result) == {"id", "status", "version"}, result
    assert request("GET", path)["status"] == expected
    return result


def check_business_delivery(path, audience, recipient, state, current_status=None):
    """查收到指定业务的站内意见并验证幂等签收和监管可见的回执。
    参数 path、audience、recipient、state 指业务、办理端、收件方和意见状态；current_status 可指定当前业务状态。
    返回值 业务端路径和范围查询字符串。
    """
    resource, identifier = path.removeprefix(API + "/").split("/")
    key = "institution" if audience == "institution" else "organization"
    scope = "?" + urlencode({key: recipient})
    inbox = API + "/" + audience + "/feedback"
    entries = request("GET", inbox + scope)["items"]
    msg = next(item for item in entries if
               item["resource"] == resource and item["resourceId"] == identifier and item["state"] == state)
    assert set(msg) <= {"id", "resource", "resourceId", "reference", "subject",
                        "state", "comment", "status", "sentAt", "acknowledgedAt"}
    assert msg["status"] == (current_status or state)
    ack = inbox + "/" + msg["id"] + "/acknowledge"
    request("POST", ack + "?" + urlencode({key: "不同归属校验"}), status=404)
    receipt = request("POST", ack + scope)
    assert receipt == request("POST", ack + scope) and set(receipt) == {"acknowledgedAt"}
    confirmations = request("GET", API + "/review-feedback/" + resource + "/" + identifier)["items"]
    assert any(item["id"] == msg["id"] and item["acknowledgedAt"] == receipt["acknowledgedAt"] for item in confirmations)
    return API + "/" + audience + "/" + resource + "/" + identifier, scope


def verify_filing():
    """验证备案录入、材料规则、补正重提及初终审。
    参数 无，测试号使用当前唯一验收前缀。
    返回值 已终审通过的测试备案标识。
    """
    data = {"number": "QA-F-" + RUN, "systemName": "测试系统 " + RUN, "organizationName": "隔离验收单位",
            "assessmentOrg": "本地测评机构", "level": "3"}
    request("POST", API + "/filings", {**data, "clientIp": "192.0.2.15"}, 400)
    request("POST", API + "/filings", data, 403, {"Origin": "http://local-test.invalid"})
    created = request("POST", API + "/filings", data, 201)
    path = API + "/filings/" + created["id"]
    request("POST", path + "/submit", {"expectedVersion": created["version"]}, 400)
    evidence = upload(path, "filing_form", "filing.pdf")
    first = precheck(path)
    assert any(item.get("attachmentId") == evidence["id"] for item in first["findings"])
    assert any(item["ruleId"] == "classification_report.missing" for item in first["findings"])
    upload(path, "classification_report", "classification.pdf")
    assert request("GET", path + "/prechecks/" + first["id"])["stale"] is True
    transition(path, "submit", "pending_initial")
    transition(path, "return_for_correction", "returned", "请补交确认材料")
    assigned, scope = check_business_delivery(path, "institution", data["assessmentOrg"], "returned")
    upload(assigned, "supporting", "correction.pdf", scope=scope)
    current = request("GET", assigned + scope)
    assert request("POST", assigned + "/submit" + scope,
                   {"expectedVersion": current["version"]})["status"] == "pending_initial"
    transition(path, "approve", "pending_final", "初审已核对")
    transition(path, "approve", "approved", "终审已核对")
    check_business_delivery(path, "institution", data["assessmentOrg"], "approved")
    timeline = request("GET", path + "/review-history")["items"]
    assert len(timeline) == 5 and any(item.get("comment") == "请补交确认材料" for item in timeline)
    found = request("GET", API + "/filings?" + urlencode({"q": data["number"], "status": "approved"}))["items"]
    assert len(found) == 1 and set(found[0]) == {"id", "number", "systemName", "organizationName", "assessmentOrg", "level", "status"}
    print("PASS: 备案录入、精简接口、规则预审、材料下载、补正重提、初审终审及留痕")
    return created["id"]


def verify_report(filing_id):
    """验证测评报告、补正重提、组合检索与审核留痕。
    参数 filing_id 为真实保存的备案标识。
    返回值 已通过测评报告的标识。
    """
    data = {"number": "QA-R-" + RUN, "filingId": filing_id, "assessmentOrg": "本地测评机构", "risk": "medium"}
    report = request("POST", API + "/reports", data, 201)
    path = API + "/reports/" + report["id"]
    detail = request("GET", path)
    assert detail["filingId"] == filing_id and detail["filingNumber"] == "QA-F-" + RUN
    original = upload(path, "report_file", "assessment.pdf")
    submitted = transition(path, "submit", "pending_initial")
    reason = "请补充测评证据并重新核对风险等级"
    returned = transition(path, "return_for_correction", "returned", reason)
    scope = "?" + urlencode({"institution": data["assessmentOrg"]})
    inbox = API + "/institution/report-feedback"
    institution_report = API + "/institution/reports/" + report["id"]
    message = next(item for item in request("GET", inbox + scope)["items"] if item["reportId"] == report["id"])
    assert message["decision"] == "returned" and message["comment"] == reason
    assert message["status"] == "returned" and "acknowledgedAt" not in message
    receipt_path = inbox + "/" + message["id"] + "/acknowledge"
    receipt = request("POST", receipt_path + scope)
    assert receipt == request("POST", receipt_path + scope) and set(receipt) == {"acknowledgedAt"}
    wrong_scope = "?" + urlencode({"institution": "另一个隔离测试机构"})
    request("GET", institution_report + wrong_scope, status=404)
    request("POST", receipt_path + wrong_scope, status=404)
    delivered = request("GET", path + "/feedback")["items"]
    assert len(delivered) == 1 and delivered[0]["acknowledgedAt"] == receipt["acknowledgedAt"]
    corrected = {**data, "risk": "high"}
    request("PATCH", path, {**corrected, "expectedVersion": submitted["version"]}, 409)
    request("PATCH", path, {**corrected, "expectedVersion": returned["version"]})
    supplement = upload(institution_report, "supporting", "corrected-evidence.pdf", scope=scope)
    correction = request("GET", institution_report + scope)
    assert correction["canCorrect"] and correction["returnComment"] == reason
    updated = request("POST", institution_report + "/submit" + scope, {"expectedVersion": correction["version"]})
    assert updated["status"] == "pending_initial" and request("GET", path)["status"] == "pending_initial"
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    events = request("GET", path + "/review-history")["items"]
    assert len(events) == 5 and any(e.get("comment") == reason for e in events)
    files = request("GET", path + "/attachments")["items"]
    assert {item["id"] for item in files} == {original["id"], supplement["id"]}
    filters = {"q": data["number"], "status": "approved", "risk": "high"}
    found = request("GET", API + "/reports?" + urlencode(filters))["items"]
    assert len(found) == 1 and found[0]["id"] == report["id"] and found[0]["submittedAt"]
    assert request("GET", API + "/reports?" + urlencode({**filters, "risk": "medium"}))["items"] == []
    csv = request("GET", API + "/reports/export?" + urlencode(filters)).decode("utf-8-sig")
    assert csv.splitlines()[0] == "报告编号,系统名称,测评机构,提交时间,风险级别,审核状态"
    assert len(csv.splitlines()) == 2 and data["number"] in csv and "高风险" in csv
    request("GET", API + "/reports/export?limit=1", status=400)
    result_messages = [item for item in request("GET", inbox + scope)["items"] if item["reportId"] == report["id"]]
    assert [item["decision"] for item in result_messages] == ["approved", "returned"]
    assert all(item["status"] == "approved" for item in result_messages)
    print("PASS: 测评意见回传、签收、机构补正重提、终审同步、风险筛选与留痕")
    return report["id"]


def test_report_docx():
    """生成一份实际可读取的合成测评报告。
    参数 无。
    返回值 写入中文业务栏目及内容的 DOCX 字节。
    """
    namespace = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
    paragraphs = ["测评结果汇总", "资产清单：内部业务应用", "漏洞详情：日志留存不足",
                  "风险等级：中风险", "整改建议：延长日志留存期限"]
    document = f'<w:document xmlns:w="{namespace}"><w:body>'
    document += "".join(f"<w:p><w:r><w:t>{line}</w:t></w:r></w:p>" for line in paragraphs)
    document += "</w:body></w:document>"
    archive = BytesIO()
    with ZipFile(archive, "w") as package:
        package.writestr("[Content_Types].xml", '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>')
        package.writestr("word/document.xml", document)
    return archive.getvalue()


def verify_extraction(filing_id, other_report_id):
    """检验报告附件的提取、校对、并发版本和归属。
    参数 filing_id 为现有备案标识，other_report_id 是用于交叉归属测试的另一个报告。
    返回值 已通过的 DOCX 测评报告标识。
    """
    report = request("POST", API + "/reports", {"number": "QA-X-" + RUN, "filingId": filing_id,
                     "assessmentOrg": "隔离测评机构", "risk": "medium"}, 201)
    path = API + "/reports/" + report["id"]
    file = upload(path, "report_file", "test-assessment.docx", test_report_docx())
    suffix = "/attachments/" + file["id"] + "/extraction"
    request("POST", API + "/reports/" + other_report_id + suffix, status=404)
    assert request("GET", path + suffix, status=404)["code"] == "extraction_not_found"
    extracted = request("POST", path + suffix, status=201)
    assert set(extracted) == {"fields", "version"} and extracted["version"] == 1, extracted
    assert request("POST", path + suffix) == extracted
    fields = {item["key"]: item for item in extracted["fields"]}
    assert fields["risk"] == {"key": "risk", "original": "中风险", "startParagraph": 4, "endParagraph": 4}
    assert set(fields) == {"assets", "findings", "risk", "recommendations"}
    bad = {"expectedVersion": 1, "changes": [{"key": "clientIp", "value": "192.0.2.14"}]}
    request("PATCH", path + suffix, bad, status=400)
    correction = {"expectedVersion": 1, "changes": [{"key": "risk", "value": "高风险，已人工复核"}]}
    confirmed = request("PATCH", path + suffix, correction)
    assert confirmed["confirmedAt"] and confirmed["version"] == 2
    assert confirmed["fields"][2]["value"] == "高风险，已人工复核", confirmed
    assert confirmed["fields"][2]["original"] == "中风险" and request("GET", path + suffix) == confirmed
    request("PATCH", path + suffix, correction, status=409)
    assert request("GET", path)["risk"] == "medium"
    transition(path, "submit", "pending_initial")
    verify_compliance(path, file, confirmed)
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    print("PASS: DOCX 提取、原文定位、不同报告附件归属和人工校对版本")
    return report["id"]


def verify_compliance(path, file, confirmed):
    """保存并追溯人工合规取证，应保留来源版本与当前独立审核状态。
    参数 path、file 和 confirmed 为真实报告、附件和已经保存的校对证据。
    返回值 无。
    """
    catalog = API + "/knowledge"
    knowledge_id = request("POST", catalog, {"title": "验收依据 " + RUN, "category": "standard",
                       "source": "隔离验收资料", "body": "日志留存要求样例"}, 201)["id"]
    knowledge = request("GET", catalog + "/" + knowledge_id)
    basis = {"expectedReportVersion": request("GET", path)["version"], "attachmentId": file["id"],
             "extractionVersion": confirmed["version"], "fieldKey": "findings", "knowledgeId": knowledge_id,
             "knowledgeVersion": knowledge["version"], "decision": "needs_remediation",
             "comment": "人工核对，报告中记录了日志留存不足"}
    evidence = request("POST", path + "/compliance", basis, 201)
    assert evidence["field"]["startParagraph"] == 3 and evidence["field"]["original"] == "日志留存不足"
    assert request("GET", path)["status"] == "pending_initial"
    request("PATCH", catalog + "/" + knowledge_id + "/status", {"active": False, "expectedVersion": knowledge["version"]})
    request("POST", path + "/compliance", basis, 409)
    assert request("GET", path + "/compliance")["items"] == [evidence]
    assert request("GET", catalog + "/" + knowledge_id + "/versions/1")["title"] == evidence["knowledgeTitle"]
    print("PASS: 合规证据固定校对和知识版本，单独留痕并保留审核状态")


def verify_report_history(old_id, new_id):
    """核对同一系统真实测评档案、风险变化和校对证据。
    参数 old_id、new_id 为同一备案前后通过的两份报告。
    返回值 无。
    """
    path = API + "/reports/" + new_id
    history = request("GET", path + "/history-reports?limit=1")
    assert history["items"][0]["id"] == new_id and history["nextCursor"]
    next_page = request("GET", path + "/history-reports?" + urlencode({"cursor": history["nextCursor"]}))
    assert old_id in [row["id"] for row in next_page["items"]]
    compare = path + "/history-comparison?" + urlencode({"from": old_id, "to": new_id})
    result = request("GET", compare)
    assert result["riskChange"] == "decreased" and result["to"]["evidence"]["version"] == 2
    assert "evidence" not in result["from"] and "findings" not in result
    assert all(row["state"] == "insufficient" for row in result["fields"])
    print("PASS: 历史报告归档分页、跨期风险和真实证据对比")


def verify_filing_application(filing_id, kind):
    """办理变更或注销申请并确认主备案结果和历史。
    参数 filing_id 为已通过备案，kind 为申请类别。
    返回值 无。
    """
    filing = API + "/filings/" + filing_id
    old = request("GET", filing)
    base = API + "/filing-applications"
    payload = {"filingId": filing_id, "type": kind, "reason": "隔离联测申请 " + RUN}
    if kind == "change":
        abandoned = request("POST", base, {**payload, "type": "cancel"}, 201)
        discard = base + "/" + abandoned["id"]
        transition(discard, "withdraw", "withdrawn")
        assert request("GET", discard + "/review-history")["items"][0]["action"] == "withdraw"
        payload["changes"] = {"systemName": "核准变更后的测试系统 " + RUN}
    row = request("POST", base, payload, 201)
    path = base + "/" + row["id"]
    request("POST", base, payload, 409)
    transition(path, "submit", "pending_initial")
    transition(path, "return_for_correction", "returned", "请补全申请说明")
    assigned, scope = check_business_delivery(path, "institution", old["assessmentOrg"], "returned")
    current = request("GET", assigned + scope)
    form = {"reason": "机构补正后的申请说明", "expectedVersion": current["version"]}
    if kind == "change":
        form["changes"] = payload["changes"]
    edited = request("PATCH", assigned + scope, form)
    assert request("POST", assigned + "/submit" + scope,
                   {"expectedVersion": edited["version"]})["status"] == "pending_initial"
    transition(path, "approve", "pending_final", "申请已核对")
    assert request("GET", filing)["systemName"] == old["systemName"]
    transition(path, "approve", "approved", "同意申请")
    check_business_delivery(path, "institution", old["assessmentOrg"], "approved")
    current = request("GET", filing)
    detail = request("GET", path)
    assert detail["appliedAt"] and len(request("GET", path + "/review-history")["items"]) == 5
    if kind == "change":
        assert detail["changes"] == [{"field": "systemName", "before": old["systemName"],
                                      "after": payload["changes"]["systemName"]}]
        assert current["systemName"] == payload["changes"]["systemName"]
    else:
        assert current["cancelledAt"]
        lookup = request("GET", API + "/filings?" + urlencode({"q": old["number"], "lifecycle": "cancelled"}))
        assert lookup["items"][0]["id"] == filing_id
        blocked = {"number": "QA-BLOCK-" + RUN, "filingId": filing_id, "assessmentOrg": "隔离机构", "risk": "low"}
        assert request("POST", API + "/reports", blocked, 409)["code"] == "filing_inactive"
    print("PASS: 备案" + ("变更" if kind == "change" else "注销") + "的提交、补正、两级审核及主记录")


def business_date(offset):
    """计算相对于当前中国时区日期的测试业务日期。
    参数 offset 为日期的偏移天数。
    返回值 公历日期字符串。
    """
    return (datetime.now(ZoneInfo("Asia/Shanghai")).date() + timedelta(days=offset)).isoformat()


def review_workday(days):
    """计算无日历覆盖的隔离预览审核截止日。
    参数 days 为进入当前审核环节后应计的工作日数。
    返回值 上海日期 ISO 字符串，未配置期限时返回无。
    """
    if not days:
        return None
    day = datetime.now(ZoneInfo("Asia/Shanghai")).date()
    for _ in range(days):
        day += timedelta(days=1)
        while day.weekday() >= 5:
            day += timedelta(days=1)
    return day.isoformat()


def implementation_action(path, action, expected, status=200, comment=None):
    """按当前记录版本操作实施报备。
    参数 path 为报备地址，action 为操作，expected 为预期状态，status 是 HTTP 状态，comment 为意见。
    返回值 实际接口响应。
    """
    row = request("GET", path)
    body = {"action": action, "expectedVersion": row["version"]}
    if comment:
        body["comment"] = comment
    result = request("POST", path + "/actions", body, status)
    if status == 200:
        assert result["status"] == expected and request("GET", path)["status"] == expected, result
    return result


def verify_implementation(filing_id):
    """验证实施报备额度、撤销冷却、两级审核、监管复核和归档。
    参数 filing_id 为已通过且有效的真实备案。
    返回值 无。
    """
    base = API + "/implementations"
    payload = {"filingId": filing_id, "type": "change", "reporter": "隔离测试联络员",
               "plannedStart": business_date(2), "plannedEnd": business_date(8), "plan": "本地受控测试计划"}
    first = base + "/" + request("POST", base, payload, 201)["id"]
    second = base + "/" + request("POST", base, payload, 201)["id"]
    third = base + "/" + request("POST", base, payload, 201)["id"]
    fourth = base + "/" + request("POST", base, payload, 201)["id"]
    implementation_action(first, "submit", "submitted")
    implementation_action(second, "submit", "submitted")
    implementation_action(third, "submit", "submitted")
    quota_path = base + "/quota?filingId=" + filing_id
    quota = request("GET", quota_path)
    assert quota["used"] == quota["limit"] == 3
    refused = implementation_action(fourth, "submit", "draft", 409)
    assert refused["code"] == "change_quota", refused

    implementation_action(third, "accept", "pending_initial")
    assert "withdraw" not in request("GET", third)["allowedActions"]
    implementation_action(third, "return_for_correction", "returned", comment="请补正实施计划")
    bureau = request("GET", third)["assessmentOrg"]
    assigned, scope = check_business_delivery(third, "institution", bureau, "returned")
    current = request("GET", assigned + scope)
    assert current["allowedActions"] == ["edit", "submit"]
    edited = request("PATCH", assigned + scope,
                     {**payload, "plan": "已补正实施时间和范围", "expectedVersion": current["version"]})
    result = request("POST", assigned + "/actions" + scope, {"action": "submit", "expectedVersion": edited["version"]})
    assert result["status"] == "submitted"
    implementation_action(third, "accept", "pending_initial")
    implementation_action(third, "approve", "pending_final")
    implementation_action(third, "approve", "pending_supervision")
    implementation_action(third, "approve", "approved", comment="监管复核已完成")
    check_business_delivery(third, "institution", bureau, "approved")
    history = request("GET", third + "/history")["items"]
    assert history[0]["toStatus"] == "approved" and any(step["action"] == "accept" for step in history), history
    archived = implementation_action(third, "archive", "approved", comment="隔离验收归档")
    assert archived["archivedAt"] and request("GET", third)["archivedAt"] == archived["archivedAt"]
    assert "archive" not in request("GET", third)["allowedActions"]
    assert request("GET", third + "/history")["items"][0]["action"] == "archive"
    assert archived["id"] in {item["id"] for item in request("GET", base + "?archive=archived")["items"]}
    assert archived["id"] not in {item["id"] for item in request("GET", base + "?archive=unarchived")["items"]}

    implementation_action(first, "withdraw", "withdrawn")
    withdrawn = request("GET", first + "/history")["items"][0]
    assert withdrawn["action"] == "withdraw", withdrawn
    quota = request("GET", quota_path)
    assert quota["used"] == 2 and quota["limit"] == 3, quota
    available_at = datetime.fromisoformat(quota["resubmitAvailableAt"])
    assert available_at - datetime.fromisoformat(withdrawn["occurredAt"]) == timedelta(days=15), quota
    current = request("GET", fourth)
    assert current["resubmitAvailableAt"] == quota["resubmitAvailableAt"], current
    assert "submit" not in current["allowedActions"], current
    refused = implementation_action(fourth, "submit", "draft", 409)
    assert refused["code"] == "resubmission_cooldown", refused
    assert request("GET", fourth)["status"] == "draft"
    print("PASS: 实施变更额度、撤销冷却、初审终审、监管复核、归档及留痕")


def verify_remediation(report_id, alternate_report):
    """验证整改风险措施、三类材料、期限及审核统计。
    参数 report_id 为已通过的真实测评报告，alternate_report 为另一份可用报告标识。
    返回值 已通过且期望自动同步隐患台账的整改规划标识。
    """
    base = API + "/remediations"
    payload = {"reportId": report_id, "deadline": business_date(8), "risks": [
        {"risk": "medium", "description": "日志留存不足", "measure": "延长留存期限",
         "responsibility": "测试运维组", "deadline": business_date(7)},
        {"risk": "high", "description": "关键操作记录不完整", "measure": "补齐操作记录",
         "responsibility": "隔离审计组", "deadline": business_date(-1)},
    ]}
    row = request("POST", base, payload, 201)
    path = base + "/" + row["id"]
    request("POST", path + "/submit", {"expectedVersion": row["version"]}, 400)
    replacement = {**payload, "reportId": alternate_report, "expectedVersion": row["version"]}
    switched = request("PATCH", path, replacement)
    upload(path, "plan", "stale-report.pdf", report_id=report_id, status=409)
    assert request("GET", path + "/attachments") == {"items": []}
    request("PATCH", path, {**payload, "expectedVersion": switched["version"]})
    for kind in ("plan", "responsibility", "schedule"):
        upload(path, kind, kind + ".pdf", report_id=report_id)
    after_upload = request("GET", path)["version"]
    replacement = {**payload, "reportId": alternate_report, "expectedVersion": after_upload}
    request("PATCH", path, replacement, 409)
    assert request("GET", path)["reportId"] == report_id
    transition(path, "submit", "pending_review")
    dashboard = request("GET", API + "/hazards/workbench")
    assert dashboard["pendingPlanReview"] >= 1 and any(plan["id"] == row["id"] for plan in dashboard["pendingPlans"])
    criteria = {"due": "within_10_days", "status": "pending_review", "q": "QA-R-" + RUN}
    assert any(plan["id"] == row["id"] for plan in request("GET", base + "?" + urlencode(criteria))["items"])
    assert request("GET", base + "/statistics")["pending"] >= 1
    for decision, status in (("return_for_correction", "returned"), ("approve", "approved")):
        current = request("GET", path)
        review = {"expectedVersion": current["version"], "decision": decision, "comment": "隔离验收意见",
                  "deadline": current["deadline"]}
        if decision == "return_for_correction":
            review["reason"] = "请补充期限说明"
        assert request("POST", path + "/review-actions", review)["status"] == status
        if status == "returned":
            assigned, scope = check_business_delivery(path, "operator", current["organizationName"], "returned")
            reminded = request("POST", base + "/reminders", {"planIds": [row["id"]]})["items"][0]
            assert reminded["status"] == "sent" and reminded["noticeId"]
            repeated = request("POST", base + "/reminders", {"planIds": [row["id"]]})["items"][0]
            assert repeated["status"] == "already_sent" and repeated["noticeId"] == reminded["noticeId"]
            check_business_delivery(path, "operator", current["organizationName"], "reminder", "returned")
            query = urlencode({**criteria, "status": "returned"})
            exported = request("GET", base + "/export?" + query).decode("utf-8-sig")
            assert "备案编号" in exported and current["organizationName"] in exported and current["deadline"] in exported
            upload(assigned, "plan", "corrected-plan.pdf", report_id=report_id, scope=scope)
            updated = request("GET", assigned + scope)
            assert request("POST", assigned + "/submit" + scope,
                           {"expectedVersion": updated["version"]})["status"] == "pending_review"
        else:
            check_business_delivery(path, "operator", current["organizationName"], "approved")
    assert request("GET", path)["risks"] == payload["risks"]
    assert len(request("GET", path + "/review-history")["items"]) == 4
    assert request("GET", base + "/statistics")["firstPassRate"] is not None
    print("PASS: 整改附件、期限筛选、催办签收、导出、退回重提、审核通过及留痕")
    return row["id"]


def hazard_action(path, action, comment=None, expected=None, status=200, version=None):
    """使用指定或当前版本操作一条实际隐患。
    参数 path 为详情地址，action 和 comment 为办理内容，expected、status 和 version 描述预期和版本。
    返回值 业务结果或预期的错误。
    """
    if version is None:
        version = request("GET", path)["version"]
    body = {"action": action, "expectedVersion": version}
    if comment:
        body["comment"] = comment
    result = request("POST", path + "/actions", body, status)
    if expected is not None:
        assert result["status"] == expected and request("GET", path)["status"] == expected, result
    return result


def institution_retest(path, action="retest_pass", comment="机构核对本轮证据后出具结论"):
    """用真实机构范围完成一轮隐患复测并返回持久化结果。
    参数 path 为监管隐患地址，action 和 comment 为复测结论及意见。
    返回值 存储后的隐患状态和版本。
    """
    hazard = request("GET", path)
    report = request("GET", API + "/reports/" + hazard["reportId"])
    scope = "?" + urlencode({"institution": report["assessmentOrg"]})
    scoped = API + "/institution" + path.removeprefix(API)
    if action == "retest_pass":
        upload(scoped, "retest_report", "institution-retest.pdf", scope=scope)
    record = request("GET", scoped + scope)
    result = request("POST", scoped + "/actions" + scope,
                     {"action": action, "comment": comment, "expectedVersion": record["version"]})
    assert result["status"] == ("pending_review" if action == "retest_pass" else "returned"), result
    return result


def verify_hazard_ledger(plan_id, filing_id, report_id):
    """验证审批自动同步、隐患逐项整改、销项、证据归属和工作台。
    参数 plan_id、filing_id、report_id 为持久化的业务来源。
    返回值 无。
    """
    base = API + "/hazards"
    dashboard = API + "/workbench/overview"
    items = request("GET", base + "?planId=" + plan_id)["items"]
    assert len(items) == 2 and all(item["status"] == "open" for item in items)
    assert all(item["reportId"] == report_id and item["filingId"] == filing_id for item in items)
    assert request("POST", base + "/sync/" + plan_id) == {"count": 2}
    current = base + "/" + next(item["id"] for item in items if item["severity"] == "medium")
    sibling = base + "/" + next(item["id"] for item in items if item["severity"] == "high")
    operator = items[0]["organizationName"]
    assigned, scope = check_business_delivery(current, "operator", operator, "open")
    initial_overview = request("GET", dashboard)
    before = initial_overview["remediation"]
    assert before == request("GET", base + "/workbench")
    assert set(before) == {"pendingPlanReview", "pendingReview", "dueWithin10Days", "overdue", "items", "pendingPlans"}
    assert before["dueWithin10Days"] >= 1 and before["overdue"] >= 1
    assert not any(item["id"] == plan_id for item in before["pendingPlans"])
    overdue = request("GET", base + "?" + urlencode({"planId": plan_id, "due": "overdue"}))["items"]
    assert [item["id"] for item in overdue] == [sibling.rsplit("/", 1)[1]]
    hazard_action(current, "submit", "等待证据", status=400)
    evidence = upload(assigned, "evidence", "verification.pdf", scope=scope)
    request("GET", sibling + "/attachments/" + evidence["id"], status=404)
    hazard_action(current, "submit", "已增加留存窗口", "pending_retest")
    pending = institution_retest(current)
    submitted = request("GET", dashboard)["remediation"]
    assert submitted["pendingReview"] == before["pendingReview"] + 1
    assert any(item["id"] == pending["id"] and item["status"] == "pending_review"
               for item in submitted["items"])
    hazard_action(current, "return_for_correction", status=400)
    hazard_action(current, "return_for_correction", "需附重新核验结果", "returned")
    check_business_delivery(current, "operator", operator, "returned")
    hazard_action(current, "close", status=409, version=pending["version"])
    hazard_action(current, "submit", "继续使用旧证据", status=400)
    upload(assigned, "evidence", "second-verification.pdf", scope=scope)
    new_version = request("GET", assigned + scope)["version"]
    reworked = request("POST", assigned + "/actions" + scope,
                       {"action": "submit", "expectedVersion": new_version, "comment": "已补充核验结果"})
    assert reworked["status"] == "pending_retest"
    institution_retest(current)
    hazard_action(current, "close", "确认整改完成", "closed")
    check_business_delivery(current, "operator", operator, "closed")
    detail = request("GET", current)
    assert detail["closedAt"] and detail["allowedActions"] == []
    assert request("GET", current + "/attachments/" + evidence["id"]) == DOCUMENT
    assert request("POST", base + "/sync/" + plan_id) == {"count": 2}
    assert request("GET", current)["status"] == "closed"
    history = request("GET", current + "/history")["items"]
    actions = [event["action"] for event in history if event["action"] in {"submit", "return_for_correction", "close"}]
    assert actions == ["close", "submit", "return_for_correction", "submit"], history
    final_overview = request("GET", dashboard)
    after = final_overview["remediation"]
    assert after == request("GET", base + "/workbench")
    assert final_overview["hazardClosed"] == initial_overview["hazardClosed"] + 1
    latest_ledger = request("GET", base + "?planId=" + plan_id)["items"]
    assert next(item for item in latest_ledger if item["id"] == detail["id"])["status"] == "closed"
    assert after["pendingReview"] == before["pendingReview"]
    assert after["dueWithin10Days"] == before["dueWithin10Days"] - 1 and after["overdue"] == before["overdue"]
    assert not any(item["id"] == detail["id"] for item in after["items"])
    print("PASS: 隐患幂等同步、证据归属、退回新增证据、复核销项及工作台待办")


def verify_knowledge():
    """验证知识条目的维护、中文检索和精简列表字段。
    参数 无，内容为本地虚构样例。
    返回值 无。
    """
    data = {"title": "隔离验收材料 " + RUN, "category": "standard", "source": "本地测试样例",
            "body": "中文检索样例：材料质量核对，溯源备注 " + RUN}
    item = request("POST", API + "/knowledge", data, 201)
    matches = request("GET", API + "/knowledge?" + urlencode({"q": "核对 " + RUN, "category": "standard"}))["items"]
    assert len(matches) == 1 and matches[0]["id"] == item["id"]
    assert set(matches[0]) == {"id", "title", "category", "source", "updatedAt", "tags"}
    assert matches[0]["tags"] == []
    path = API + "/knowledge/" + item["id"]
    detail = request("GET", path)
    assert detail["body"] == data["body"] and detail["version"] == 1
    tags = ["合成业务依据", "等保标签 " + RUN]
    assert request("PATCH", path + "/tags", {"tags": tags, "expectedVersion": 1}) == {"version": 2}
    corrected = {**data, "title": data["title"] + " 已修订", "source": "修订来源 " + RUN}
    assert request("PATCH", path, {**corrected, "expectedVersion": 2}) == {"version": 3}
    request("PATCH", path + "/tags", {"tags": [], "expectedVersion": 1}, 409)
    filters = {"q": tags[0], "source": corrected["source"], "category": "standard"}
    found = request("GET", API + "/knowledge?" + urlencode(filters))["items"]
    assert len(found) == 1 and found[0]["id"] == item["id"] and found[0]["tags"] == tags
    detail = request("GET", path)
    assert detail["title"] == corrected["title"] and detail["tags"] == tags and detail["version"] == 3
    revisions = request("GET", path + "/versions")
    assert [item["action"] for item in revisions["items"]] == ["edit", "tags", "create"]
    assert request("GET", path + "/versions/1")["title"] == data["title"]
    print("PASS: 知识录入、编辑、标签维护、版本冲突、来源与中文检索、历史版本")


def verify_knowledge_import():
    """通过隔离服务预检一份 UTF-8 CSV 并原子入库。
    参数 无，使用本轮独有的导入标题。
    返回值 无。
    """
    catalog = API + "/knowledge"
    route = catalog + "/import"
    assert b"title,category,source,body,tags" in request("GET", route + "/template")
    title = "本地批量知识-" + RUN
    content = f"title,category,source,body,tags\n{title},standard,隔离资料,验收正文,\n"
    boundary = "grade-import-" + RUN
    multipart = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="import.csv"\r\n'
                 f"Content-Type: text/csv\r\n\r\n{content}\r\n--{boundary}--\r\n").encode()
    headers = {"Content-Type": "multipart/form-data; boundary=" + boundary}
    candidate = request("POST", route + "/preview", multipart, headers=headers)
    assert candidate["total"] == 1 and candidate["issues"] == [] and candidate["items"][0]["title"] == title
    lookup = catalog + "?" + urlencode({"q": title})
    assert request("GET", lookup)["items"] == []
    result = request("POST", route, multipart, status=201, headers=headers)
    identifier = result["ids"][0]
    assert result["count"] == 1 and request("GET", lookup)["items"][0]["id"] == identifier
    assert request("GET", catalog + "/" + identifier + "/versions/1")["action"] == "import"
    request("POST", route, multipart, status=400, headers=headers)
    print("PASS: 知识 CSV 模板、预检无写入、入库快照和重复防护")


def verify_archives(filing_id, report_id, plan_id):
    """从单位画像查看同一备案、测评、规划和隐患来源。
    参数 filing_id、report_id 和 plan_id 为本轮已经办结的同链业务标识。
    返回值 无。
    """
    base = API + "/archives"
    system = request("GET", base + "/systems/" + filing_id)
    assert system["system"]["status"] == "cancelled" and system["system"]["closedHazardCount"] == 1
    assert report_id in {row["id"] for row in system["reports"]["items"]}
    assert plan_id in {row["id"] for row in system["remediations"]["items"]}
    assert {row["status"] for row in system["hazards"]["items"]} == {"open", "closed"}
    organization = system["system"]["organizationName"]
    groups = request("GET", base + "/units?" + urlencode({"q": organization}))["items"]
    group = next(row for row in groups if row["organizationName"] == organization)
    profile = request("GET", base + "/units/" + group["key"])
    assert filing_id in {row["id"] for row in profile["systems"]["items"]}
    assert profile["unit"]["latestReportAt"] and profile["unit"]["closedHazardCount"] >= 1
    print("PASS: 单位及系统档案聚合真实关系，注销和整改状态保留")


def verify_workbench_overview():
    """核对 V2 工作台引用的真实统计与待办。
    参数 无，使用本轮已登记的测试业务作为基线。
    返回值 无。
    """
    source = API + "/workbench/overview"
    request("GET", source + "?clientIp=192.0.2.4", status=400)
    result = request("GET", source)
    assert result["filingTotal"] >= 1
    assert result["filingTotal"] >= result["filingsThisYear"] >= 1
    assert result["implementationOpen"] >= 1
    assert result["hazardTotal"] >= result["hazardClosed"] >= 1
    assert len(result["tasks"]) <= 12
    for task in result["tasks"]:
        assert set(task) - {"deadline", "urgency"} == {"id", "resource", "label", "systemName", "number", "status"}
        resource = task["resource"]
        assert resource in {"filings", "reports", "implementations", "filing-applications", "remediations", "hazards", "alerts"}
        assert request("GET", f"{API}/{resource}/{task['id']}")["id"] == task["id"]
    print("PASS: V2 工作台真实统计、最小响应和待办直达")


def verify_review_windows(filing_id):
    """验证已启用的备案和报告审核阶段时限。
    参数 filing_id 为当前未注销的验收备案。
    返回值 无。
    """
    config = [int(os.getenv("GRADE_ACCEPTANCE_FILING_REVIEW_DAYS", "0")),
              int(os.getenv("GRADE_ACCEPTANCE_REPORT_REVIEW_DAYS", "0"))]
    filing_final = int(os.getenv("GRADE_ACCEPTANCE_FILING_FINAL_REVIEW_DAYS", "5"))
    if not any(config):
        return
    overview = API + "/workbench/overview"
    filing = request("POST", API + "/filings", {"number": "QA-DUE-F-" + RUN, "systemName": "审核时限验收",
                     "organizationName": "时限验收单位", "assessmentOrg": "时限验收机构", "level": "2"}, 201)
    report = request("POST", API + "/reports", {"number": "QA-DUE-R-" + RUN, "filingId": filing_id,
                     "assessmentOrg": "时限验收机构", "risk": "low"}, 201)
    for resource, record, days, kinds in (("filings", filing, config[0], ("filing_form", "classification_report")),
                                          ("reports", report, config[1], ("report_file",))):
        if not days:
            continue
        path = API + "/" + resource + "/" + record["id"]
        current_task = lambda: next((task for task in request("GET", overview)["tasks"]
                                    if task["id"] == record["id"]), None)
        for kind in kinds:
            upload(path, kind, kind + ".pdf")
        transition(path, "submit", "pending_initial")
        assert current_task()["deadline"] == review_workday(days)
        transition(path, "return_for_correction", "returned", "隔离时限测试退回")
        assert current_task() is None
        transition(path, "submit", "pending_initial")
        transition(path, "approve", "pending_final")
        final_days = filing_final if resource == "filings" else days
        assert current_task()["label"].endswith("终审")
        assert current_task().get("deadline") == review_workday(final_days)
        transition(path, "approve", "approved")
        assert current_task() is None
    assert set(request("GET", overview)["reviewDeadlines"]) == {"overdue", "near", "upcoming"}
    print("PASS: 上海日期的备案及测评时限随当前阶段计入和移除")


def verify_local_alerts(plan_id):
    """从未销项隐患生成并处置本地监管预警。
    参数 plan_id 为拥有本轮高风险未销项隐患的整改规划。
    返回值 可用于站内安全预警公告的预警标识。
    """
    base = API + "/alerts"
    hazards = request("GET", API + "/hazards?planId=" + plan_id)["items"]
    hazard = next(row for row in hazards if row["severity"] == "high" and row["status"] == "open")
    request("POST", base + "/sync")
    assert request("POST", base + "/sync") == {"created": 0}
    matches = request("GET", base + "?status=pending&severity=high")["items"]
    alert = next(row for row in matches if row["hazardId"] == hazard["id"] and row["type"] == "high_risk")
    path = base + "/" + alert["id"]
    checked = request("GET", path)
    action = {"action": "process", "expectedVersion": checked["version"], "comment": "验收已查阅隐患源记录"}
    handling = request("POST", path + "/actions", action)
    assert handling["status"] == "processing"
    request("POST", path + "/actions", action, status=409)
    result = request("POST", path + "/actions", {"action": "close", "expectedVersion": handling["version"],
                      "comment": "本地验收预警处置完成"})
    assert result["status"] == "closed" and result["closedAt"]
    assert [step["action"] for step in request("GET", path)["history"]][-2:] == ["process", "close"]
    assert request("GET", API + "/hazards/" + hazard["id"])["status"] == "open"
    summary = request("GET", base + "/summary")
    assert request("GET", API + "/workbench/overview")["activeAlerts"] == summary["pending"] + summary["processing"]
    print("PASS: 预警生成去重、处理关闭留痕、隐患独立状态和工作台同步")
    return alert["id"]


def verify_tag_catalog():
    """验证标签的使用计数、历史和停用约束。
    参数 无，标签与条目名称均使用本轮验收前缀。
    返回值 无。
    """
    directory = API + "/tag-catalog"
    name = "共享标签 " + RUN
    tag = request("POST", directory, {"name": name, "color": "#2255aa"}, 201)
    assert tag["active"] and tag["usageCount"] == 0
    knowledge = API + "/knowledge"
    data = {"title": "标签验收 " + RUN, "category": "case", "source": "隔离验收材料", "body": "知识目录示例"}
    identifier = request("POST", knowledge, data, 201)["id"]
    path = knowledge + "/" + identifier
    request("PATCH", path + "/tags", {"tags": [name], "expectedVersion": 1})
    query = "?" + urlencode({"q": name})
    assert request("GET", directory + query)["items"][0]["usageCount"] == 1
    updated = request("PATCH", directory + "/" + tag["id"],
                      {"color": "#3355aa", "active": False, "expectedVersion": tag["version"]})
    assert not updated["active"] and updated["usageCount"] == 1
    assert request("GET", path + "/versions/2")["tags"] == [name]
    other = request("POST", knowledge, {**data, "title": data["title"] + " 新条目"}, 201)["id"]
    failure = request("PATCH", knowledge + "/" + other + "/tags", {"tags": [name], "expectedVersion": 1}, 409)
    assert failure["code"] == "tag_disabled"
    print("PASS: 共享标签使用计数、停用限制与历史标签原样保留")


def verify_publications(alert_id):
    """验证监管公告发布、修订隔离、撤回及运营者只读访问。
    参数 alert_id 为本轮真实预警标识。
    返回值 已撤回的监管公告标识。
    """
    base = API + "/publications"
    payload = {"category": "security_advisory", "title": "隔离验收公告 " + RUN, "publisher": "本地监管测试",
               "body": "初次发布内容", "pinned": True, "alertId": alert_id}
    draft = request("POST", base, payload, 201)
    item = "/" + draft["id"]
    public = API + "/operator/publications" + item
    path = base + item
    assert draft["status"] == "draft" and draft["alertId"] == alert_id
    request("GET", public, status=404)
    published = request("POST", path + "/actions", {"action": "publish", "expectedVersion": draft["version"]})
    visible = request("GET", public)
    assert visible["body"] == payload["body"] and "alertId" not in visible and "allowedActions" not in visible
    request("PATCH", public, payload, status=404)
    assert request("GET", public) == visible
    revision = request("PATCH", path, {**payload, "body": "复核后的发布内容", "expectedVersion": published["version"]})
    assert request("GET", public)["body"] == payload["body"]
    updated = request("POST", path + "/actions", {"action": "publish", "expectedVersion": revision["version"]})
    assert request("GET", public)["body"] == revision["body"]
    request("POST", path + "/actions", {"action": "withdraw", "expectedVersion": updated["version"]})
    request("GET", public, status=404)
    events = request("GET", path + "/history")["items"]
    assert [row["action"] for row in events] == ["withdraw", "publish", "edit", "publish", "create"]
    print("PASS: 真实预警关联、站内发布、修订快照、运营者只读和撤回留痕")
    return draft["id"]


def verify_dashboard_and_audit(filing_id, publication_id):
    """验证驾驶舱真实统计和操作审计链接同源。
    参数 filing_id、publication_id 为本轮已办结备案和发布记录。
    返回值 无。
    """
    stats = request("GET", API + "/dashboard/statistics")
    assert stats["filings"]["cancelled"] >= 1 and stats["hazards"]["high"] >= 1
    assert any(row["filingId"] == filing_id and row["count"] >= 1 for row in stats["highRiskSystems"])
    assert stats["reportFirstReview"]["decided"] >= stats["reportFirstReview"]["approved"] >= 1
    rows = request("GET", API + "/audit?resource=publications&limit=10")["items"]
    event = next(row for row in rows if row["resourceId"] == publication_id and row["action"] == "withdraw")
    assert event["operator"] and event["occurredAt"] and event["href"] == "/monitor/publications?publication=" + publication_id
    assert "body" not in event and "ip" not in event
    print("PASS: 驾驶舱真实聚合及最近发布留痕直达")


if __name__ == "__main__":
    assert request("GET", "/readyz") == {"status": "ready"}
    filing_id = verify_filing()
    verify_filing_application(filing_id, "change")
    verify_implementation(filing_id)
    report_id = verify_report(filing_id)
    alternate_report = verify_extraction(filing_id, report_id)
    verify_report_history(report_id, alternate_report)
    plan_id = verify_remediation(report_id, alternate_report)
    verify_hazard_ledger(plan_id, filing_id, report_id)
    verify_review_windows(filing_id)
    verify_filing_application(filing_id, "cancel")
    verify_archives(filing_id, report_id, plan_id)
    assert request("GET", API + "/reports/" + report_id)["status"] == "approved"
    verify_knowledge()
    verify_knowledge_import()
    verify_tag_catalog()
    alert_id = verify_local_alerts(plan_id)
    publication_id = verify_publications(alert_id)
    verify_dashboard_and_audit(filing_id, publication_id)
    verify_workbench_overview()

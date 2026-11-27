"""在独立本地业务服务核验机构材料校验、重传和三端异常关闭。"""

from concurrent.futures import ThreadPoolExecutor
from io import BytesIO
import os
from urllib.parse import urlencode
from zipfile import ZipFile

from acceptance import API, DOCUMENT, RUN, check_business_delivery, precheck, request, transition, upload


INSTITUTION = "材料验收机构 " + RUN
ORGANIZATION = "材料验收运营单位 " + RUN
OUTSIDER = "材料验收不同归属 " + RUN
ISSUES = "/material-issues"
REPORTS = API + "/institution/reports"
REQUIRED_REPORT_KINDS = (
    "report_file", "remediation_plan", "assessment_conclusion",
    "issues_recommendations", "cover_basic_statement",
)


def scope(audience, outsider=False):
    """生成当前验收业务的机构或单位查询参数。
    参数 audience 是办理端，outsider 表示检查其他归属。
    返回值 查询字段及登记名称。
    """
    name = INSTITUTION if audience == "institution" else ORGANIZATION
    key = "institution" if audience == "institution" else "organization"
    return {key: OUTSIDER if outsider else name}


def inst_query():
    """生成当前机构的兼容材料操作查询参数。
    参数 无。
    返回值 编码后的机构范围。
    """
    return "?" + urlencode(scope("institution"))


def issue_rows(audience, outsider=False, **filters):
    """在指定一端读取待办或历史材料异常。
    参数 audience 为角色，outsider 表示其他收件方，filters 用于编号和状态筛选。
    返回值 当前响应页的异常条目。
    """
    scoped = scope(audience, outsider) if audience else {}
    prefix = "/" + audience if audience else ""
    query = urlencode({**scoped, **filters})
    return request("GET", API + prefix + ISSUES + ("?" + query if query else ""))["items"]


def submit_scoped(path, version=None, status=200):
    """按机构报告或备案的当前业务版本提交审核。
    参数 path 是机构资源地址，version 指定操作时版本，status 是预期返回码。
    返回值 业务状态或错误响应。
    """
    version = request("GET", path + inst_query())["version"] if version is None else version
    return request("POST", path + "/submit" + inst_query(), {"expectedVersion": version}, status)


def upload_material(path, kind, file_name, data, status, issue=None, outsider=False):
    """发起实际的机构 multipart 上传或定向重发。
    参数 path、kind、file_name、data 为材料输入，status 为预期响应，issue 可指定异常，outsider 验证归属。
    返回值 已登记材料结构或错误响应。
    """
    boundary = "material-recovery-" + RUN
    headers = {"Content-Type": "multipart/form-data; boundary=" + boundary, "X-Material-Kind": kind}
    prefix = f'--{boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\n{kind}\r\n'
    if issue:
        prefix += f'--{boundary}\r\nContent-Disposition: form-data; name="issueId"\r\n\r\n{issue}\r\n'
    part = (
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{file_name}"\r\n'
        'Content-Type: application/octet-stream\r\n\r\n'
    )
    body = (prefix + part).encode() + data + f"\r\n--{boundary}--\r\n".encode()
    query = "?" + urlencode(scope("institution", outsider))
    return request("POST", path + "/attachments" + query, body, status, headers)


def filing_package(*names):
    """构造带虚构 PDF 材料的备案 ZIP 包。
    参数 names 为压缩包内文件相对路径。
    返回值 合成 ZIP 字节。
    """
    payload = BytesIO()
    with ZipFile(payload, "w") as archive:
        for name in names:
            archive.writestr(name, DOCUMENT)
    return payload.getvalue()


def single_issue(case_kind, record_id, material_kind):
    """取出同一业务和材料类别唯一的待补正异常。
    参数 case_kind、record_id 指定业务，material_kind 指定失败材料类别。
    返回值 机构异常条目。
    """
    found = [
        row for row in issue_rows("institution", caseKind=case_kind)
        if row.get("caseId") == record_id and row["materialKind"] == material_kind
    ]
    assert len(found) == 1, found
    return found[0]


def assert_shared_issue(issue_id, expected, resolution=None):
    """核对三端读取同一异常及无关收件方的范围隔离。
    参数 issue_id 是异常标识，expected 是预期状态，resolution 是可选关闭结果。
    返回值 监管、机构和运营者查询结果依次组成的列表。
    """
    filters = {"id": issue_id, "status": "all"}
    rows = []
    for audience in (None, "institution", "operator"):
        page = issue_rows(audience, **filters)
        assert len(page) == 1, (audience, page)
        item = page[0]
        assert item["id"] == issue_id and item["status"] == expected, (audience, item)
        assert item.get("resolution") == resolution and bool(item.get("closedAt")) == bool(resolution), item
        if audience:
            assert issue_rows(audience, True, **filters) == [], audience
        else:
            assert item["organizationName"] == ORGANIZATION and item["institution"] == INSTITUTION, item
        if audience == "operator":
            private = {"organizationName", "fileName", "caseId", "caseVersion", "institution", "canRetry", "canDelete"}
            assert not private & item.keys(), item
        rows.append(item)
    assert len({(item["reference"], item["reasonCode"], item["openedAt"]) for item in rows}) == 1, rows
    return rows


def assert_notices(issue_id, expected, current):
    """确认两端及时收到各一次站内意见及可追踪的监管回执。
    参数 issue_id 是异常标识，expected 为事件类型序列，current 是异常当前状态。
    返回值 无。
    """
    for audience, recipient in (("institution", INSTITUTION), ("operator", ORGANIZATION)):
        query = "?" + urlencode(scope(audience))
        inbox = request("GET", API + "/" + audience + "/feedback" + query)["items"]
        matching = [item for item in inbox if item["resource"] == "material-issues" and item["resourceId"] == issue_id]
        assert [item["state"] for item in matching] == expected, (audience, matching)
        assert all(item["status"] == current for item in matching), (audience, matching)
        if audience == "operator":
            assert all(".pdf" not in item.get("comment", "") and ".zip" not in item.get("comment", "")
                       for item in matching), matching
        for state in expected:
            check_business_delivery(API + ISSUES + "/" + issue_id, audience, recipient, state, current)


def verify_filing_package():
    """验证不完整 ZIP 失败、三端站内预警、补正包上传后送审。
    参数 无，使用当前批次独有的机构及运营单位。
    返回值 经监管初终审通过的备案标识。
    """
    filings = API + "/institution/filings"
    draft = request("POST", filings + inst_query(), {
        "number": "MAT-F-" + RUN, "systemName": "材料验收系统 " + RUN,
        "organizationName": ORGANIZATION, "level": "3",
    }, status=201)
    path = filings + "/" + draft["id"]
    bad_zip = filing_package("材料包/备案表.pdf")
    good_zip = filing_package("材料包/备案表.pdf", "材料包/定级报告.pdf")
    for _ in range(2):
        error = upload_material(path, "filing_package", "缺少定级报告.zip", bad_zip, 400)
        assert error["code"] == "filing_package", error
    failure = single_issue("filing", draft["id"], "filing_package")
    assert "定级报告" in failure["reason"] and failure["canRetry"], failure
    issue_id = failure["id"]
    assert request("GET", API + "/workbench/overview")["openMaterialIssues"] >= 1
    admin_issue, _, operator = assert_shared_issue(issue_id, "open")
    assert admin_issue["fileName"] == "缺少定级报告.zip" and "定级报告" not in operator["reason"], operator
    assert_notices(issue_id, ["material_failed"], "open")
    assert request("GET", path + "/attachments" + inst_query())["items"] == []
    blocked = submit_scoped(path, status=409)
    assert blocked["code"] == "material_issues_open", blocked
    recovered = upload_material(path, "filing_package", "备案材料包.zip", good_zip, 201, issue_id)
    assert recovered["kind"] == "filing_package", recovered
    assert request("GET", path + "/attachments/" + recovered["id"] + inst_query()) == good_zip
    assert_shared_issue(issue_id, "closed", "corrected")
    assert_notices(issue_id, ["material_closed", "material_failed"], "closed")
    admin = API + "/filings/" + draft["id"]
    findings = precheck(admin)["findings"]
    assert not {"filing_form.missing", "classification_report.missing"} & {item["ruleId"] for item in findings}, findings
    assert submit_scoped(path)["status"] == "pending_initial"
    transition(admin, "approve", "pending_final")
    transition(admin, "approve", "approved")
    print("PASS: ZIP 缺项校验、重复失败去重、三端预警、补传关闭与机构送审")
    return draft["id"]


def report_draft(filing_id, suffix):
    """在机构端创建关联同一有效备案的材料测试报告。
    参数 filing_id 为有效备案标识，suffix 用来区分不同验收分支。
    返回值 报告编号、标识和版本等写入结果。
    """
    return request("POST", REPORTS + inst_query(), {
        "number": "MAT-R-" + RUN + suffix, "filingId": filing_id, "risk": "medium",
    }, status=201)


def fail_report_upload(path):
    """上传文件名合法但内容错误的报告，触发材料异常。
    参数 path 是机构报告记录地址。
    返回值 已核实的 HTTP 错误。
    """
    error = upload_material(path, "report_file", "损坏报告.pdf", b"Not a PDF", 400)
    assert error["code"] == "attachment_type", error
    return error


def interrupt_report_upload(path):
    """发送可识别材料类别的未完成 multipart 请求。
    参数 path 为机构报告地址，请求体省略文件后的结束边界。
    返回值 已核实的上传中断响应。
    """
    boundary = "material-interrupted-" + RUN
    headers = {
        "Content-Type": "multipart/form-data; boundary=" + boundary,
        "X-Material-Kind": "supporting",
    }
    body = (
        f'--{boundary}\r\nContent-Disposition: form-data; name="kind"\r\n\r\nsupporting\r\n'
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="part.pdf"\r\n'
        'Content-Type: application/octet-stream\r\n\r\n%PDF-1.4\r\n'
    )
    error = request("POST", path + "/attachments" + inst_query(), body.encode(), 400, headers)
    assert error["code"] == "attachment_interrupted", error
    return error


def retry_report_race(path, issue_id):
    """并发重传同一异常，验证只有一次有效材料登记。
    参数 path 为机构报告地址，issue_id 为待补正异常。
    返回值 唯一登记成功的材料。
    """
    def resend():
        """发起单次补传，记录异常已关闭时的并发冲突。
        参数 无，从测试上下文读报告和异常标识。
        返回值 本次成功登记的材料，或冲突时的空值。
        """
        try:
            return upload_material(path, "report_file", "修复报告.pdf", DOCUMENT, 201, issue_id)
        except AssertionError as error:
            assert error.args and isinstance(error.args[0], tuple) and error.args[0][2] == 409, error
            return None

    with ThreadPoolExecutor(max_workers=2) as pool:
        outcomes = [pool.submit(resend), pool.submit(resend)]
        saved = [material for task in outcomes if (material := task.result()) is not None]
    assert len(saved) == 1, saved
    return saved[0]


def verify_report_recovery(filing_id):
    """在同一报告上验证失败去重、重发并继续审核。
    参数 filing_id 是本轮实库通过的备案。
    返回值 已走到监管退回补正的报告地址。
    """
    draft = report_draft(filing_id, "-REC")
    path = REPORTS + "/" + draft["id"]
    with ThreadPoolExecutor(max_workers=4) as pool:
        list(pool.map(fail_report_upload, [path] * 4))
    failure = single_issue("report", draft["id"], "report_file")
    assert failure["fileName"] == "损坏报告.pdf" and failure["reason"] and failure["canDelete"], failure
    issue_id = failure["id"]
    assert_shared_issue(issue_id, "open")
    assert_notices(issue_id, ["material_failed"], "open")
    upload_material(path, "report_file", "修复报告.pdf", DOCUMENT, 404, issue_id, outsider=True)
    upload_material(path, "supporting", "修复报告.pdf", DOCUMENT, 409, issue_id)
    material = retry_report_race(path, issue_id)
    upload_material(path, "report_file", "修复报告.pdf", DOCUMENT, 409, issue_id)
    assert request("GET", path + "/attachments" + inst_query())["items"] == [material]
    assert_shared_issue(issue_id, "closed", "corrected")
    assert all(item["id"] != issue_id for item in issue_rows("institution", caseKind="report"))
    assert_notices(issue_id, ["material_closed", "material_failed"], "closed")
    for kind in REQUIRED_REPORT_KINDS[1:]:
        upload(path, kind, kind + ".pdf", scope=inst_query())
    submitted = submit_scoped(path)
    assert submitted["status"] == "pending_initial"
    request("DELETE", path + inst_query(), {"expectedVersion": submitted["version"]}, status=409)
    upload_material(path, "report_file", "损坏报告.pdf", b"Not a PDF", 409)
    transition(API + "/reports/" + draft["id"], "return_for_correction", "returned", "请补正报告正文")
    return path


def verify_report_deletion(filing_id, submitted_path):
    """核对草稿可按版本删除且已进入审核的报告受保护。
    参数 filing_id 是有效备案，submitted_path 是已退回的既有机构报告。
    返回值 无。
    """
    fail_report_upload(submitted_path)
    corrected_id = submitted_path.rsplit("/", 1)[1]
    required = single_issue("report", corrected_id, "report_file")
    assert required["canRetry"] and not required.get("canDelete"), required
    request("POST", API + "/institution" + ISSUES + "/" + required["id"] + "/discard" + inst_query(),
            {"expectedVersion": required["caseVersion"]}, status=409)
    document = DOCUMENT.replace(b"traceable", b"corrected")
    upload_material(submitted_path, "report_file", "补正报告.pdf", document, 201, required["id"])
    assert_shared_issue(required["id"], "closed", "corrected")
    assert submit_scoped(submitted_path)["status"] == "pending_initial"

    draft = report_draft(filing_id, "-DROP")
    path = REPORTS + "/" + draft["id"]
    fail_report_upload(path)
    interrupt_report_upload(path)
    issue = single_issue("report", draft["id"], "report_file")
    interrupted = single_issue("report", draft["id"], "supporting")
    assert interrupted["reasonCode"] == "attachment_interrupted", interrupted
    assert_notices(interrupted["id"], ["material_failed"], "open")
    discard = API + "/institution" + ISSUES + "/" + issue["id"] + "/discard"
    request("POST", discard + "?" + urlencode(scope("institution", True)),
            {"expectedVersion": issue["caseVersion"]}, status=404)
    request("POST", discard + inst_query(), {"expectedVersion": issue["caseVersion"] + 1}, status=409)
    request("POST", discard + inst_query(), {"expectedVersion": issue["caseVersion"]}, status=204)
    assert_shared_issue(issue["id"], "closed", "discarded")
    assert_shared_issue(interrupted["id"], "closed", "discarded")
    assert_notices(issue["id"], ["material_closed", "material_failed"], "closed")
    request("GET", path + inst_query(), status=404)
    request("GET", API + "/reports/" + draft["id"], status=404)
    assert all(item["id"] != draft["id"] for item in request("GET", REPORTS + inst_query())["items"])

    standalone = report_draft(filing_id, "-CLEAN")
    path = REPORTS + "/" + standalone["id"]
    request("DELETE", path + inst_query(), {"expectedVersion": standalone["version"]}, status=204)
    request("GET", path + inst_query(), status=404)
    print("PASS: 测评失败重发、并发提示去重、退回补正、草稿删除和三端关闭")


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请显式指定本轮隔离业务预览的 GRADE_ACCEPTANCE_PORT")
    filing_id = verify_filing_package()
    verify_report_deletion(filing_id, verify_report_recovery(filing_id))

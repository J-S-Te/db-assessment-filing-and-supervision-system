"""使用独立测试预览验收名录维护和跨端引用。"""

import os
from concurrent.futures import ThreadPoolExecutor
from uuid import uuid4
from urllib.parse import urlencode

from acceptance import API, RUN, business_date, request, transition, upload
from publication_receipts_acceptance import publish


DIRECTORY = API + "/directory"


def scope(name, **extra):
    """生成保留机构固定范围的查询。
    参数 name 为机构范围名，extra 为可选附加参数。
    返回值 可附到 URL 的编码查询。
    """
    return "?" + urlencode({"institution": name, **extra})


def institution(suffix):
    """登记一条用于本轮验收的测评机构。
    参数 suffix 为区分本批机构的测试后缀。
    返回值 真实已保存的机构档案。
    """
    return request("POST", DIRECTORY + "/institutions", {
        "name": "名录联验机构 " + RUN + suffix,
        "code": "DJCP-" + RUN + suffix,
        "qualification": "甲级",
        "validUntil": business_date(365),
    }, 201)


def credit_code():
    """生成本轮使用的十八位合成信用代码。
    参数 无。
    返回值 不与现有验收对象重复的合成代码。
    """
    return "9133" + uuid4().hex[:14].upper()


def register_unit(operator_scope):
    """在机构名录登记被测单位，并核对并发同码请求。
    参数 operator_scope 为测试机构的稳定范围名。
    返回值 只登记一次的被测单位。
    """
    body = {"name": "名录联验单位 " + RUN, "code": credit_code(),
            "classification": "企业", "district": "市本级"}
    path = API + "/institution/directory/units" + scope(operator_scope)

    def save_once(index):
        """发送同一代码的一次并发请求。
        参数 index 为并发分支标识。
        返回值 成功条目或预期的重复错误。
        """
        try:
            return request("POST", path, body, 201)
        except AssertionError as exc:
            if exc.args[0][2] != 409:
                raise
            return {"error": index}

    with ThreadPoolExecutor(max_workers=2) as pool:
        results = list(pool.map(save_once, (1, 2)))
    assert sum("id" in result for result in results) == 1, results
    return next(result for result in results if "id" in result)


def create_assessor(agency, suffix):
    """由机构登记本机构测评人员。
    参数 agency 为登记机构，suffix 区分唯一工号及证书。
    返回值 实际保存的测评人员。
    """
    url = API + "/institution/directory/people" + scope(agency["scopeName"])
    return request("POST", url, {
        "name": "测评师 " + RUN + suffix, "code": "STAFF-" + suffix,
        "qualification": "高级测评师", "certificateNo": "STAFF-CERT-" + RUN + suffix,
    }, 201)


def verify_registry_permissions(agency, other, unit, assessor):
    """核对机构资质、人员范围及共享单位的维护约束。
    参数 agency、other 为两个机构，unit 与 assessor 分别为机构的单位和人员。
    返回值 无。
    """
    people = API + "/institution/directory/people"
    own = request("GET", people + scope(agency["scopeName"]))["items"]
    foreign = request("GET", people + scope(other["scopeName"]))["items"]
    assert assessor["id"] in {row["id"] for row in own}
    assert assessor["id"] not in {row["id"] for row in foreign}
    profile = API + "/institution/directory/institutions/" + agency["id"]
    request("PATCH", profile + scope(agency["scopeName"]),
            {"expectedVersion": agency["version"], "qualification": "乙级"}, 403)
    unit_path = API + "/institution/directory/units/" + unit["id"]
    request("POST", unit_path + "/link" + scope(other["scopeName"]))
    request("POST", unit_path + "/link" + scope(other["scopeName"]))
    request("PATCH", unit_path + scope(agency["scopeName"]),
            {"expectedVersion": unit["version"], "district": "另一区"}, 403)
    history = request("GET", DIRECTORY + "/units/" + unit["id"] + "/history")["items"]
    assert [item["action"] for item in history].count("link") == 1, history
    print("PASS: 机构名录归属、并发重复登记、共享被测单位及监管资质职责")


def approved_filing(unit, agency):
    """用已登记的业务范围建立实际备案。
    参数 unit 和 agency 为所属单位及机构。
    返回值 已完成审核的备案地址。
    """
    record = request("POST", API + "/filings", {
        "number": "DIR-F-" + RUN, "systemName": "共用名录系统 " + RUN,
        "organizationName": unit["scopeName"], "assessmentOrg": agency["scopeName"], "level": "3",
    }, 201)
    path = API + "/filings/" + record["id"]
    for material in ("filing_form", "classification_report"):
        upload(path, material, material + ".pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return path


def active_business(filing, agency, assessor, foreign):
    """使用同一测评人员登记报告及实施报备，进入在审。
    参数 filing 为批准备案，agency、assessor 为业务归属，foreign 用于核对跨机构拒绝。
    返回值 本轮报备和报告的监管地址。
    """
    suffix = scope(agency["scopeName"])
    report_url = API + "/institution/reports"
    fields = {"number": "DIR-R-" + RUN, "filingId": filing.split("/")[-1], "risk": "low"}
    assert request("POST", report_url + suffix, fields, 400)["code"] == "directory_person_required"
    assert request("POST", report_url + suffix, {**fields, "assessmentPersonId": foreign["id"]},
                   400)["code"] == "directory_person_invalid"
    report = request("POST", report_url + suffix, {**fields, "assessmentPersonId": assessor["id"]}, 201)
    report_path = API + "/reports/" + report["id"]
    for material in ("report_file", "remediation_plan", "assessment_conclusion",
                     "issues_recommendations", "cover_basic_statement"):
        upload(report_url + "/" + report["id"], material, material + ".pdf", scope=suffix)
    transition(report_path, "submit", "pending_initial")
    impl_url = API + "/institution/implementations"
    implementation = request("POST", impl_url + suffix, {
        "filingId": fields["filingId"], "type": "routine", "reporterId": assessor["id"],
        "plannedStart": business_date(5), "plannedEnd": business_date(6), "plan": "隔离验收实施安排",
    }, 201)
    impl_path = API + "/implementations/" + implementation["id"]
    advance_implementation(impl_path, "submit", "submitted")
    return impl_path, report_path


def advance_implementation(path, action, target):
    """按当前版本办理真实实施报备。
    参数 path、action、target 为报备地址、动作与预期状态。
    返回值 已持久化的报备状态。
    """
    version = request("GET", path)["version"]
    result = request("POST", path + "/actions", {"action": action, "expectedVersion": version})
    assert result["status"] == target, result
    return result


def verify_business_history(agency, other, unit, assessor, foreign):
    """核对在审停用、办结后调动与报备、报告历史快照。
    参数 agency、other、unit、assessor、foreign 是本轮名录数据。
    返回值 无。
    """
    filing = approved_filing(unit, agency)
    implementation, report = active_business(filing, agency, assessor, foreign)
    path = DIRECTORY + "/people/" + assessor["id"]
    for item, collection in ((assessor, "people"), (unit, "units"), (agency, "institutions")):
        detail_path = DIRECTORY + "/" + collection + "/" + item["id"]
        version = request("GET", detail_path)["version"]
        error = request("PATCH", detail_path, {"expectedVersion": version, "status": "inactive"}, 409)
        assert error["code"] == "directory_in_review", error
    transition(report, "approve", "pending_final")
    transition(report, "approve", "approved")
    for action, target in (("accept", "pending_initial"), ("approve", "pending_final"),
                           ("approve", "pending_supervision"), ("approve", "approved")):
        advance_implementation(implementation, action, target)
    changed = request("PATCH", path, {"expectedVersion": assessor["version"],
                                      "institutionId": other["id"], "name": "新归属测评师 " + RUN})
    old = request("GET", path + "/history")["items"][0]
    assert old["action"] == "transfer" and old["before"]["institutionId"] == agency["id"], old
    assert changed["institutionId"] == other["id"], changed
    for endpoint in (implementation, report, implementation + "/history", report + "/review-history"):
        result = request("GET", endpoint)
        attributed = result if "items" not in result else result["items"][0]
        snapshot = attributed["directorySnapshot"]
        assert snapshot["person"]["name"] == assessor["name"]
        assert snapshot["person"]["institutionId"] == agency["id"]
        assert snapshot["unit"]["id"] == unit["id"]
        assert snapshot["institution"]["qualification"] == agency["qualification"]
    print("PASS: 报备和报告共用人员、在审停用保护、转机构后历史归属保持")


def verify_announcement_directory(unit, agency):
    """核对独立名录进入公告范围，更名与停用后按版本保留回执。
    参数 unit 为收件单位，agency 为同批机构。
    返回值 无。
    """
    standalone = request("POST", DIRECTORY + "/units", {
        "name": "无备案公告单位 " + RUN, "code": credit_code(),
    }, 201)
    recipient_lookup = API + "/publications/recipients?"
    candidates = request("GET", recipient_lookup + urlencode({"audience": "operator",
                                                               "q": standalone["name"]}))["items"]
    assert any(row["recipient"] == standalone["scopeName"] for row in candidates), candidates
    unit_path = DIRECTORY + "/units/" + unit["id"]
    version = request("GET", unit_path)["version"]
    renamed = request("PATCH", unit_path, {"expectedVersion": version, "name": "名录单位新名 " + RUN})
    targets = [{"audience": "operator", "recipient": renamed["scopeName"]},
               {"audience": "institution", "recipient": agency["scopeName"]}]
    body = {"category": "announcement", "title": "名录验证公告 " + RUN,
            "publisher": "隔离监管", "body": "核对稳定收件和显示名称", "pinned": False,
            "audience": "selected", "targets": targets}
    base = API + "/publications"
    draft = request("POST", base, body, 201)
    path = base + "/" + draft["id"]
    live = publish(path, draft["version"])
    operator_path = API + "/operator/publications/" + draft["id"]
    reader_scope = "?" + urlencode({"organization": renamed["name"]})
    request("POST", operator_path + "/acknowledge" + reader_scope, {"expectedVersion": live["version"]})
    revision = request("PATCH", path, {**body, "expectedVersion": live["version"]})
    request("PATCH", unit_path, {"expectedVersion": renamed["version"], "status": "inactive"})
    request("POST", path + "/actions", {"action": "publish", "expectedVersion": revision["version"]}, 400)
    old = request("GET", path + "/deliveries")
    delivery = next(row for row in old["items"] if row["audience"] == "operator")
    assert old["version"] == live["version"] and delivery["displayName"] == renamed["name"]
    assert delivery["acknowledgedAt"] and delivery["recipient"] == unit["scopeName"]
    candidates = request("GET", recipient_lookup + urlencode({"audience": "operator",
                                                               "q": unit["scopeName"]}))["items"]
    assert not candidates, candidates
    assert request("GET", operator_path + reader_scope)["acknowledgedAt"]
    print("PASS: 公告引用共用名录、独立登记可选、停用阻止后续投递及历史签收保留")


def main():
    """串起名录权限、业务引用和公告回执三组隔离业务。
    参数 无，端口通过 GRADE_ACCEPTANCE_PORT 明确提供。
    返回值 无，失败抛出验证异常。
    """
    if not os.getenv("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请指定独立测试预览的 GRADE_ACCEPTANCE_PORT")
    agency, other = institution("A"), institution("B")
    unit = register_unit(agency["scopeName"])
    assessor, foreign = create_assessor(agency, "A"), create_assessor(other, "B")
    verify_registry_permissions(agency, other, unit, assessor)
    verify_business_history(agency, other, unit, assessor, foreign)
    verify_announcement_directory(unit, agency)


if __name__ == "__main__":
    main()

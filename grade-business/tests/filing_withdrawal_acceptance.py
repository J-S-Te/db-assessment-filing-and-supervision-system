"""在隔离本地服务联验首报、变更注销的在审撤回及三端同步。"""

from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode
import json
import os

from acceptance import API, RUN, request, upload


ORGANIZATION = "备案撤回联验单位 " + RUN
INSTITUTION = "备案撤回联验机构 " + RUN
OPER_SCOPE = "?" + urlencode({"organization": ORGANIZATION})
INST_SCOPE = "?" + urlencode({"institution": INSTITUTION})
FOREIGN = "?" + urlencode({"organization": "其他单位 " + RUN})
FILING_FORM = {
    "number": "WITHDRAW-BA-" + RUN, "systemName": "备案撤回联验系统 " + RUN,
    "assessmentOrg": INSTITUTION, "level": "3",
}


def submit_filing(suffix=""):
    """建立一笔正常提交的隔离运营者首报。
    参数 suffix 为可选的本次脚本内部编号区分；材料统一使用测试 PDF。
    返回值 运营端、监管端、测评端地址及在审版本。
    """
    base = API + "/operator/filings"
    row = request("POST", base + OPER_SCOPE,
                  {**FILING_FORM, "number": FILING_FORM["number"] + suffix}, 201)
    operator = base + "/" + row["id"]
    regulator = API + "/filings/" + row["id"]
    institution = API + "/institution/filings/" + row["id"]
    for kind in ("filing_form", "classification_report"):
        upload(operator, kind, kind + ".pdf", scope=OPER_SCOPE)
    draft = request("GET", operator + OPER_SCOPE)
    submitted = request("POST", operator + "/submit" + OPER_SCOPE, {"expectedVersion": draft["version"]})
    assert submitted["status"] == "pending_initial"
    return operator, regulator, institution, submitted["version"]


def request_withdrawal(operator, version, reason):
    """发送两个完全相同的撤回请求以核对幂等。
    参数 operator 为运营者记录路径，version 为在审版本，reason 为撤回原因。
    返回值 两次请求共同得到的待确认撤回记录。
    """
    endpoint = operator + "/withdrawal"
    payload = {"expectedVersion": version, "reason": reason}
    request("POST", endpoint + FOREIGN, payload, 404)
    with ThreadPoolExecutor(max_workers=2) as workers:
        jobs = [workers.submit(request, "POST", endpoint + OPER_SCOPE, payload) for _ in range(2)]
        first, repeat = (job.result()["request"] for job in jobs)
    assert first == repeat and first["status"] == "pending"
    return first


def decide_withdrawal(regulator, row, decision):
    """执行监管决定及重复请求核对。
    参数 regulator 为监管记录路径，row 为收到的来件，decision 为同意或驳回。
    返回值 有经办人和确认时间的审批回执。
    """
    path = regulator + "/withdrawal/decision"
    payload = {
        "withdrawalId": row["id"], "expectedVersion": row["caseVersion"],
        "decision": decision, "comment": "按照隔离联验要求处理",
    }
    first = request("POST", path, payload)["request"]
    assert first == request("POST", path, payload)["request"]
    assert first["status"] == ("approved" if decision == "approve" else "rejected")
    assert first["decidedAt"] and first["decidedBy"]
    return first


def verify_initial_withdrawal():
    """确认撤回期间原审核暂停，归档和原材料保留且编号可续用。
    参数 无，隔离测试草稿自行创建。
    返回值 后续联验使用的新报送路径。
    """
    operator, regulator, institution, version = submit_filing()
    pending = request_withdrawal(operator, version, "更新定级材料")
    request("POST", institution + "/review-actions" + INST_SCOPE,
            {"expectedVersion": pending["caseVersion"], "decision": "approve"}, 409)
    assert request("GET", institution + "/withdrawal" + INST_SCOPE)["request"] == pending
    assert request("GET", regulator + "/withdrawal")["canDecide"] is True
    decide_withdrawal(regulator, pending, "approve")
    for path in (operator + OPER_SCOPE, institution + INST_SCOPE, regulator):
        assert request("GET", path)["status"] == "withdrawn"
    assert len(request("GET", operator + "/attachments" + OPER_SCOPE)["items"]) == 2
    assert request("GET", operator + "/receipt" + OPER_SCOPE)
    notice = request("GET", API + "/institution/feedback" + INST_SCOPE)["items"]
    assert any(item["resourceId"] == operator.split("/")[-1] and item["state"] == "withdrawal_approved"
               for item in notice)
    print("PASS: 首次备案在审撤回、并发重放、机构停止初审、通知送达和旧编号继续使用")
    return submit_filing()


def verify_application_withdrawal(operator_filing, regulator_filing, institution_filing, initial_version):
    """核对变更与注销申请的驳回、恢复初审、确认、材料回执及原备案状态。
    参数 四项为新首报的三端路径及初审版本。
    返回值 无。
    """
    history_action = {"expectedVersion": initial_version, "decision": "approve"}
    passed = request("POST", institution_filing + "/review-actions" + INST_SCOPE, history_action)
    filed = request("POST", regulator_filing + "/review-actions",
                    {"expectedVersion": passed["version"], "decision": "approve"})
    base = API + "/operator/filing-applications"
    for kind, material in (("change", "change_statement"), ("cancel", "cancellation_statement")):
        body = {"filingId": filed["id"], "type": kind, "reason": "隔离联验申请"}
        if kind == "change":
            body["changes"] = {"systemName": "备案撤回更名 " + RUN}
        row = request("POST", base + OPER_SCOPE, body, 201)
        operator, regulator = base + "/" + row["id"], API + "/filing-applications/" + row["id"]
        upload(operator, material, material + ".pdf", scope=OPER_SCOPE)
        submitted = request("POST", operator + "/submit" + OPER_SCOPE,
                            {"expectedVersion": row["version"] + 1})
        pending = request_withdrawal(operator, submitted["version"], "申请计划已变")
        rejected = decide_withdrawal(regulator, pending, "reject")
        pending = request_withdrawal(operator, rejected["caseVersion"], "再次补充说明请求撤回")
        decide_withdrawal(regulator, pending, "approve")
        assert request("GET", operator + OPER_SCOPE)["status"] == "withdrawn"
        assert request("GET", operator + "/withdrawal" + OPER_SCOPE)["previous"][0]["status"] == "rejected"
        assert request("GET", operator + "/receipt" + OPER_SCOPE)
    source = request("GET", operator_filing + OPER_SCOPE)
    assert source["status"] == "approved" and "cancelledAt" not in source and source["version"] == filed["version"]
    print("PASS: 备案变更注销在审撤回、驳回恢复、重新确认和申请占用释放")


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请指定独立的本地联验端口。")
    verify_application_withdrawal(*verify_initial_withdrawal())
    operator, regulator, institution, _ = submit_filing("-BROWSER")
    print(json.dumps({"organization": ORGANIZATION, "institution": INSTITUTION,
                      "uiFilingId": regulator.split("/")[-1]}, ensure_ascii=False))

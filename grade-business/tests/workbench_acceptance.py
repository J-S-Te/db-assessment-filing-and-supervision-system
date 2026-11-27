"""使用本地隔离预览核对三端待办交接、时限、催办及真实驾驶舱。"""

from concurrent.futures import ThreadPoolExecutor
from datetime import date, timedelta
from urllib.parse import urlencode

from acceptance import API, RUN, business_date, hazard_action, request, review_workday, transition, upload
from operator_rectification import OWNER, SCOPE, plan_hazards, seed_plan


def scoped(audience, name):
    """生成工作台所属单位或机构的有效查询范围。
    参数 audience 为端名称，name 为业务归属的登记名称。
    返回值 带前导问号的范围查询。
    """
    key = "institution" if audience == "institution" else "organization"
    return "?" + urlencode({key: name})


def queue_item(endpoint, identifier):
    """从当前快照定位办理队列中的一条记录。
    参数 endpoint 是完整工作台请求地址，identifier 为业务记录 ID。
    返回值 匹配任务或空值。
    """
    result = request("GET", endpoint)
    return next((item for item in result["tasks"] if item["id"] == identifier), None)


def verify_deadline_projection(identifier, partner_scope, other_scope, expected):
    """核对预警入口与机构工作台共用时限及收件范围。
    参数 identifier 为办理记录；partner_scope 和 other_scope 为两家机构；expected 为期限。
    返回值 无。
    """
    endpoint = API + "/institution/workbench/deadlines"
    request("GET", endpoint, status=400)
    request("GET", endpoint + partner_scope + "&urgency=unknown", status=400)
    response = request("GET", endpoint + partner_scope)
    task = next(item for item in response["items"] if item["id"] == identifier)
    assert task["deadline"] == expected, task
    response = request("GET", endpoint + partner_scope + "&urgency=" + task["urgency"])
    assert any(item["id"] == identifier for item in response["items"])
    assert request("GET", endpoint + other_scope)["items"] == []


def verify_filing_handoff():
    """核对备案进入机构初审、退回补正、重提和转监管终审的实时归属。
    参数 无，备案名称和业务归属由本批隔离数据生成。
    返回值 无。
    """
    owner = "工作台运营者 " + RUN
    institution = "工作台测评机构 " + RUN
    op_scope = scoped("operator", owner)
    inst_scope = scoped("institution", institution)
    other_scope = scoped("institution", "工作台其他机构 " + RUN)
    operator_board = API + "/operator/workbench"
    institution_board = API + "/institution/workbench"
    regulator_board = API + "/workbench/overview"
    request("GET", institution_board, status=400)
    request("GET", operator_board + op_scope + "&junk=1", status=400)
    draft = request("POST", API + "/operator/filings" + op_scope, {
        "number": "WORKBENCH-" + RUN, "systemName": "工作台办理链路",
        "assessmentOrg": institution, "level": "2",
    }, status=201)
    identifier = draft["id"]
    assert queue_item(operator_board + op_scope, identifier) is None
    operator = API + "/operator/filings/" + identifier
    partner = API + "/institution/filings/" + identifier
    admin = API + "/filings/" + identifier
    for kind in ("filing_form", "classification_report"):
        upload(operator, kind, kind + ".pdf", scope=op_scope)
    version = request("GET", operator + op_scope)["version"]
    submitted = request("POST", operator + "/submit" + op_scope, {"expectedVersion": version})
    task = queue_item(institution_board + inst_scope, identifier)
    assert task["status"] == "pending_initial" and task["deadline"] == review_workday(3)
    verify_deadline_projection(identifier, inst_scope, other_scope, task["deadline"])
    assert queue_item(institution_board + other_scope, identifier) is None
    assert queue_item(regulator_board, identifier) is None
    request("POST", partner + "/review-actions" + inst_scope, {
        "decision": "return_for_correction", "comment": "需要补正说明", "expectedVersion": submitted["version"],
    })
    assert queue_item(institution_board + inst_scope, identifier) is None
    assert queue_item(operator_board + op_scope, identifier)["status"] == "returned"
    upload(operator, "filing_form", "corrected.pdf", scope=op_scope)
    version = request("GET", operator + op_scope)["version"]
    resubmitted = request("POST", operator + "/submit" + op_scope, {"expectedVersion": version})
    assert queue_item(operator_board + op_scope, identifier) is None
    assert queue_item(institution_board + inst_scope, identifier)["deadline"] == review_workday(3)
    request("POST", partner + "/review-actions" + inst_scope, {
        "decision": "approve", "expectedVersion": resubmitted["version"],
    })
    assert queue_item(institution_board + inst_scope, identifier) is None
    task = queue_item(regulator_board, identifier)
    assert task["status"] == "pending_final" and task["deadline"] == review_workday(5)
    deadlines = request("GET", API + "/workbench/deadlines")
    assert any(item["id"] == identifier and item["deadline"] == task["deadline"]
               for item in deadlines["items"])
    transition(admin, "approve", "approved")
    assert queue_item(regulator_board, identifier) is None
    closed = request("GET", operator_board + op_scope)
    assert closed["recentSystems"][0]["status"] == "approved" and closed["counts"]["pending"] == 0
    print("PASS: 三端备案待办交接、退回重提、独立工作日时限、隔离范围及办结移出")


def verify_retest_reminder():
    """核对复测催办及时送达且并发只发一次，流程继续同步。
    参数 无，隔离整改记录按测试运行标识生成。
    返回值 无。
    """
    plan, filing = seed_plan()
    identifier = plan_hazards(plan, filing)["转办事项"]
    admin = API + "/hazards/" + identifier
    report = request("GET", admin)["reportId"]
    institution = request("GET", API + "/reports/" + report)["assessmentOrg"]
    inst_scope = scoped("institution", institution)
    op_board = API + "/operator/workbench"
    institution_board = API + "/institution/workbench"
    operator = API + "/operator/hazards/" + identifier
    partner = API + "/institution/hazards/" + identifier
    reminder = op_board + "/retest-reminders"
    assert queue_item(op_board + SCOPE, identifier)["status"] == "open"
    request("POST", reminder + SCOPE, {"id": identifier}, status=409)
    upload(operator, "evidence", "workbench-evidence.pdf", scope=SCOPE)
    expected = request("GET", operator + SCOPE)["version"]
    request("POST", operator + "/actions" + SCOPE, {
        "action": "submit", "expectedVersion": expected, "comment": "实际送复测",
    })
    assert queue_item(op_board + SCOPE, identifier)["canRemind"] is True
    assert queue_item(institution_board + inst_scope, identifier)["status"] == "pending_retest"
    projected = request("GET", op_board + "/deadlines" + SCOPE)["items"]
    assert any(item["id"] == identifier and item["canRemind"] for item in projected)
    before = request("GET", institution_board + inst_scope)["counts"]["unread"]
    request("POST", reminder + scoped("operator", OWNER + "其他"), {"id": identifier}, status=404)
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = [pool.submit(request, "POST", reminder + SCOPE, {"id": identifier}) for _ in range(4)]
        assert sorted(future.result()["sent"] for future in futures) == [False, False, False, True]
    feedback = API + "/institution/feedback"
    notices = request("GET", feedback + inst_scope)["items"]
    matches = [n for n in notices if n["state"] == "retest_reminder" and n["resourceId"] == identifier]
    assert len(matches) == 1 and matches[0]["status"] == "pending_retest"
    assert request("GET", institution_board + inst_scope)["counts"]["unread"] == before + 1
    request("POST", feedback + "/" + matches[0]["id"] + "/acknowledge" + inst_scope)
    assert request("GET", institution_board + inst_scope)["counts"]["unread"] == before
    upload(partner, "retest_report", "workbench-retest.pdf", scope=inst_scope)
    expected = request("GET", partner + inst_scope)["version"]
    request("POST", partner + "/actions" + inst_scope, {"action": "retest_pass", "expectedVersion": expected})
    assert queue_item(institution_board + inst_scope, identifier) is None
    assert queue_item(op_board + SCOPE, identifier) is None
    assert queue_item(API + "/workbench/overview", identifier)["status"] == "pending_review"
    request("POST", reminder + SCOPE, {"id": identifier}, status=409)
    hazard_action(admin, "close", "复核通过", "closed")
    assert queue_item(API + "/workbench/overview", identifier) is None
    print("PASS: 运营者催办四并发仅一条，机构及时收到和签收，复测到监管销项同步")


def verify_dashboard(before):
    """核验真实新建、销项数据进入大屏的当日序列。
    参数 before 为办理前真实大屏快照。
    返回值 无。
    """
    result = request("GET", API + "/dashboard/statistics")
    trend = result["trend"]
    assert len(trend) == 30 and trend[-1]["date"] == business_date(0)
    assert trend[0]["date"] == business_date(-29)
    assert all(date.fromisoformat(b["date"]) - date.fromisoformat(a["date"]) == timedelta(days=1)
               for a, b in zip(trend, trend[1:]))
    for field in ("filingsCreated", "reportsCreated", "hazardsClosed"):
        assert trend[-1][field] > before["trend"][-1][field], field
    assert result["updatedAt"] and set(result["openHazards"]) == {"high", "medium", "low"}
    print("PASS: 驾驶舱读取真实当日新建与销项变化，三十日序列连续")


if __name__ == "__main__":
    baseline = request("GET", API + "/dashboard/statistics")
    verify_filing_handoff()
    verify_retest_reminder()
    verify_dashboard(baseline)

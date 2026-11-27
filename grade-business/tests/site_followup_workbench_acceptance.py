"""在隔离本地环境运行现场跟进工作台、期限提醒及两端交接。"""

import json
import os
import sys
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from time import monotonic, sleep

from acceptance import API, DOCUMENT, RUN, business_date, request
from directory_acceptance import institution
from supervision_acceptance import action, upload_material
from supervision_batch_site_acceptance import audience_query, draft_for, registered_filing


RESOURCE = "supervision-site"
ADMIN = API + "/workbench/overview"
OPERATOR = API + "/operator/workbench"


def seed_site(agency, suffix):
    """通过真实路由初始化下发自查及已登记现场检查的样例。
    参数 agency、suffix 为合法机构及样例后缀。
    返回值 工作台所需范围、现场证据版及跟进日期。
    """
    filing = registered_filing(agency, suffix, "现场截止联验 " + RUN)
    draft = draft_for(filing["id"], "现场跟进期限联验 " + suffix + RUN)
    annual = API + "/supervisions/" + draft["id"]
    one = upload_material(annual, "", "inspection_template", draft["version"])
    two = upload_material(annual, "", "summary_template", one["version"])
    action(annual, "", "publish", two["version"])
    admin = annual + "/site-inspection"
    operator = API + "/operator" + admin.removeprefix(API)
    visit = (datetime.now(timezone.utc) + timedelta(days=2)).isoformat(timespec="seconds")
    plan = action(admin, "", "schedule", 0, visitAt=visit, location="本地联验地点", inspectors="现场核验组")
    evidence = upload_material(admin, "", "inspection_evidence", plan["version"])
    recorded = action(admin, "", "record", evidence["version"],
                      outcome="follow_up_required", comment="需要核验本轮整改")
    return {
        "id": draft["id"], "owner": filing["owner"], "number": filing["number"],
        "admin": admin, "operator": operator, "recorded": recorded["version"],
        "due": business_date(1), "scope": audience_query(filing["owner"]),
    }


def issue_followup(fixture):
    """用已登记的现场结果发出跟进，并返回独立办理阶段。
    参数 fixture 为已登记现场结果的合成样例。
    返回值 监管跟进动作回执。
    """
    return action(fixture["admin"], "", "follow_up", fixture["recorded"],
                  followUpDueOn=fixture["due"], comment="请提交本轮跟进证据")


def site_items(items, task):
    """提取同一任务下独立现场资源的工作台或提醒条目。
    参数 items 为响应列表，task 为任务标识。
    返回值 命中的现场条目。
    """
    return [item for item in items if item["resource"] == RESOURCE
            and item.get("resourceId", item.get("id")) == task]


def check_scope(fixture, foreign):
    """验证运营者现场待办、时限、监管及其他单位的边界。
    参数 fixture 为办理样例，foreign 为未收件单位范围。
    返回值 无。
    """
    task, due, scope = [fixture[k] for k in ("id", "due", "scope")]
    own = request("GET", OPERATOR + scope)
    work = site_items(own["tasks"], task)
    assert len(work) == 1
    assert work[0]["deadline"] == due and work[0]["urgency"] == "near"
    assert work[0]["status"] == "following_up" and own["counts"]["pending"] == 2
    assert not site_items(request("GET", ADMIN)["tasks"], task)
    assert not site_items(request("GET", OPERATOR + foreign)["tasks"], task)
    path = OPERATOR + "/deadlines/" + RESOURCE + "/" + task
    assert request("GET", path + scope) == {"deadline": due, "label": "现场检查跟进"}
    assert request("GET", path + foreign) == {}
    deadlines = site_items(request("GET", OPERATOR + "/deadlines" + scope)["items"], task)
    assert len(deadlines) == 1 and deadlines[0]["deadline"] == due


def check_reminder(fixture, foreign):
    """等待隔离后台真实扫描到临期提醒，并核对监管回执和错误收件方。
    参数 fixture、foreign 为任务样例和无权读取的单位范围。
    返回值 无。
    """
    task, scope = fixture["id"], fixture["scope"]
    path = API + "/operator/feedback"
    cutoff = monotonic() + 83
    while monotonic() < cutoff:
        rows = site_items(request("GET", path + scope)["items"], task)
        if rows:
            break
        sleep(0.75)
    else:
        raise AssertionError("现场临期消息未在真实轮询中送达")
    assert len(rows) == 1 and rows[0]["state"] == "deadline_due_soon"
    assert rows[0]["status"] == "following_up" and fixture["due"] in rows[0]["comment"]
    assert not site_items(request("GET", path + foreign)["items"], task)
    receipts = request("GET", API + "/review-feedback/" + RESOURCE + "/" + task)["items"]
    assert len(receipts) == 1 and receipts[0]["recipient"] == fixture["owner"]
    endpoint = path + "/" + rows[0]["id"] + "/acknowledge"
    request("POST", endpoint + foreign, status=404)
    acknowledged = request("POST", endpoint + scope)
    assert request("POST", endpoint + scope) == acknowledged


def verify_handoff(fixture, foreign, issued):
    """覆盖材料校验、并发重放、退补、提交移交与办结。
    参数 fixture、foreign、issued 是样例、其他单位和初轮版本。
    返回值 无。
    """
    adm, op, scope, task = [fixture[k] for k in ("admin", "operator", "scope", "id")]
    version = issued["version"]
    action(op, foreign, "submit_follow_up", version, 404)
    action(op, scope, "submit_follow_up", version, 422)
    upload_material(op, scope, "follow_up_evidence", version, b"fake", 415)
    evidence = upload_material(op, scope, "follow_up_evidence", version)
    with ThreadPoolExecutor(max_workers=2) as workers:
        pending = [workers.submit(action, op, scope, "submit_follow_up", evidence["version"]) for _ in range(2)]
        first, replay = [call.result() for call in pending]
    assert first == replay and first["status"] == "follow_up_submitted"
    assert not site_items(request("GET", OPERATOR + scope)["tasks"], task)
    review = site_items(request("GET", ADMIN)["tasks"], task)
    assert len(review) == 1 and "deadline" not in review[0]
    returned = action(adm, "", "return_follow_up", first["version"], comment="补交本轮证据")
    check_scope(fixture, foreign)
    action(op, scope, "submit_follow_up", returned["version"], 422)
    corrected_proof = DOCUMENT.replace(b"%%EOF", b"Corrected evidence\n%%EOF")
    fixed = upload_material(op, scope, "follow_up_evidence", returned["version"], corrected_proof)
    revised = action(op, scope, "submit_follow_up", fixed["version"])
    action(adm, "", "close", revised["version"], comment="证据核验完成")
    assert not site_items(request("GET", ADMIN)["tasks"], task)
    assert not site_items(request("GET", OPERATOR + scope)["tasks"], task)
    assert request("GET", op + scope)["status"] == "closed"


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请指定独立本地预览的 GRADE_ACCEPTANCE_PORT。")
    agency = institution("SITE-TODOS")["scopeName"]
    if "--fixtures" in sys.argv:
        browser = seed_site(agency, "BROWSER")
        issue_followup(browser)
        print(json.dumps(browser, ensure_ascii=False))
    else:
        fixture = seed_site(agency, "HTTP")
        issued = issue_followup(fixture)
        foreign = audience_query("未参与现场跟进 " + RUN)
        check_scope(fixture, foreign)
        check_reminder(fixture, foreign)
        print("PASS: 现场及自查并行、时限原单位隔离、后台真实提醒及签收回执")
        verify_handoff(fixture, foreign, issued)
        print("PASS: 材料校验、并发提交、监管交接、退补及办结移出工作台")

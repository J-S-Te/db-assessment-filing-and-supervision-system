"""在隔离预览上联验监管批量自查和现场检查闭环。"""

import json
import os
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from urllib.parse import urlencode
from uuid import uuid4

from acceptance import API, DOCUMENT, RUN, business_date, request, transition, upload
from directory_acceptance import DIRECTORY, credit_code, institution
from supervision_acceptance import OPERATOR, REGULATOR, action, upload_material


def registered_filing(agency, suffix, district, level="3"):
    """登记可明确归属的有效备案和名录。
    参数 agency、suffix、district、level 为机构、唯一后缀、区县和等级。
    返回值 实际有效备案和它的运营单位。
    """
    org = request("POST", DIRECTORY + "/units", {
        "name": "现场批量收件 " + RUN + suffix, "code": credit_code(),
        "classification": "联验单位", "district": district,
    }, 201)
    owner = org["scopeName"]
    number = "SB-" + RUN + suffix
    filed = request("POST", API + "/filings", {
        "number": number, "systemName": "批量自查现场系统 " + RUN + suffix,
        "organizationName": owner, "assessmentOrg": agency, "level": level,
    }, 201)
    path = API + "/filings/" + filed["id"]
    for kind in ("filing_form", "classification_report"):
        upload(path, kind, kind + ".pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return {**filed, "number": number, "owner": owner, "unit": org}


def draft_for(filing_id, title):
    """为实际备案创建年度自查草稿。
    参数 filing_id、title 是有效备案和任务标题。
    返回值 真实草稿与业务版本。
    """
    return request("POST", REGULATOR, {
        "requestId": str(uuid4()), "filingId": filing_id, "title": title, "dueOn": business_date(10),
    }, 201)


def audience_query(owner):
    """编码当前运营单位的精确接收范围。
    参数 owner 是已登记单位的稳定范围。
    返回值 对应查询字符串。
    """
    return "?" + urlencode({"organization": owner})


def publish_batch(agency, source, template_version):
    """核对实际名录筛选、名单变化、已有年度任务及并发重复下发。
    参数 agency、source、template_version 为测评机构和真实模板来源及版本。
    返回值 批次接收单位及它的有效任务。
    """
    district = "批量联验区 " + RUN
    target = registered_filing(agency, "A", district)
    existing = registered_filing(agency, "B", district)
    outside = registered_filing(agency, "C", district + "以外")
    registered_filing(agency, "D", district, "2")
    duplicate = draft_for(existing["id"], "已有年度任务 " + RUN)
    criteria = {
        "district": district, "levels": ["3"], "templateTaskId": source,
        "templateVersion": template_version, "title": "实际发送年度自查 " + RUN, "dueOn": business_date(10),
    }
    endpoint = REGULATOR + "/batches"
    preview = request("POST", endpoint + "/preview", criteria)
    assert preview["totalEligible"] == 1 and {entry["filingId"] for entry in preview["items"]} == {
        target["id"], existing["id"],
    }
    before = request("GET", OPERATOR + audience_query(target["owner"]))["items"]
    assert not before
    unit = outside["unit"]
    unit_path = DIRECTORY + "/units/" + unit["id"]
    changed = request("PATCH", unit_path, {"expectedVersion": unit["version"], "district": district})
    reply = {**criteria, "token": preview["token"]}
    request("POST", endpoint, reply, 409)
    assert request("GET", OPERATOR + audience_query(target["owner"]))["items"] == before
    request("PATCH", unit_path, {"expectedVersion": changed["version"], "district": district + "以外"})
    with ThreadPoolExecutor(max_workers=2) as workers:
        sends = [workers.submit(request, "POST", endpoint, reply, 201) for _ in range(2)]
        first, second = (send.result() for send in sends)
    assert first == second and first["publishedCount"] == 1
    by_filing = {entry["filingId"]: entry for entry in first["items"]}
    assert by_filing[existing["id"]] == {
        "filingId": existing["id"], "taskId": duplicate["id"], "status": "existing",
    }
    delivered = by_filing[target["id"]]["taskId"]
    op = OPERATOR + "/" + delivered
    own = audience_query(target["owner"])
    foreign = audience_query(outside["owner"])
    documents = request("GET", op + own)["attachments"]
    assert {item["kind"] for item in documents} == {"inspection_template", "summary_template"}
    for item in documents:
        link = op + "/attachments/" + item["id"]
        assert request("GET", link + own) == DOCUMENT
        request("GET", link + foreign, status=404)
    print("PASS: 按实际区县与等级核对收件，名单变化停止，已有年度任务跳过，确认重放与模板隔离")
    return delivered, target["owner"], outside["owner"]


def field_inspection(task, owner, wrong_owner):
    """验证两端现场证据范围、补正轨迹和重复办理。
    参数 task、owner、wrong_owner 是批次实际任务、本单位及无关单位。
    返回值 已办结的现场版本。
    """
    reg = REGULATOR + "/" + task + "/site-inspection"
    op = OPERATOR + "/" + task + "/site-inspection"
    own, wrong = audience_query(owner), audience_query(wrong_owner)
    visit = (datetime.now(timezone.utc) + timedelta(days=2)).isoformat(timespec="seconds")
    plan = {"visitAt": visit, "location": "联验现场会议室", "inspectors": "监管检查一组"}
    scheduled = action(reg, "", "schedule", 0, **plan)
    assert scheduled == action(reg, "", "schedule", 0, **plan)
    action(REGULATOR + "/" + task, "", "cancel", 2, 409, comment="存在现场办理")
    request("GET", op + wrong, status=404)
    assert request("GET", op + own)["status"] == "scheduled"
    version = scheduled["version"]
    action(reg, "", "record", version, 422, outcome="follow_up_required", comment="检查待补")
    upload_material(reg, "", "inspection_evidence", version, b"invalid document", 415)
    evidence = upload_material(reg, "", "inspection_evidence", version)
    file_path = op + "/attachments/" + evidence["attachment"]["id"]
    request("GET", file_path + own, status=404)
    recorded = action(reg, "", "record", evidence["version"], outcome="follow_up_required", comment="检查待补")
    assert request("GET", file_path + own) == DOCUMENT
    due = action(reg, "", "follow_up", recorded["version"], followUpDueOn=business_date(3), comment="送回改正结果")
    return finish_followup(reg, op, own, wrong, due)


def finish_followup(reg, op, own, wrong, due):
    """核验跟进提交、退回补正和新证据办结的实际权限。
    参数 reg、op、own、wrong、due 分别为两端接口、范围及已下发动作。
    返回值 已办结的版本。
    """
    proof = upload_material(op, own, "follow_up_evidence", due["version"])
    private = reg + "/attachments/" + proof["attachment"]["id"]
    request("GET", private, status=404)
    action(op, wrong, "submit_follow_up", proof["version"], 404)
    submitted = action(op, own, "submit_follow_up", proof["version"])
    assert request("GET", private) == DOCUMENT
    returned = action(reg, "", "return_follow_up", submitted["version"], comment="需补操作留痕")
    action(op, own, "submit_follow_up", returned["version"], 422)
    corrected = upload_material(op, own, "follow_up_evidence", returned["version"], DOCUMENT + b"\n")
    second = action(op, own, "submit_follow_up", corrected["version"])
    finished = action(reg, "", "close", second["version"], comment="现场跟进已核对")
    assert finished == action(reg, "", "close", second["version"], comment="现场跟进已核对")
    view = request("GET", op + own)
    assert view["status"] == "closed" and len(view["events"]) == 7
    assert request("GET", private) == DOCUMENT
    return finished


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请指定独立本地预览的 GRADE_ACCEPTANCE_PORT。")
    agency = institution("SB")["scopeName"]
    template = registered_filing(agency, "SRC", "样板区 " + RUN)
    draft = draft_for(template["id"], "监督模板样板 " + RUN)
    path = REGULATOR + "/" + draft["id"]
    first = upload_material(path, "", "inspection_template", draft["version"])
    second = upload_material(path, "", "summary_template", first["version"])
    target, owner, wrong_owner = publish_batch(agency, draft["id"], second["version"])
    finished = field_inspection(target, owner, wrong_owner)
    inbox = request("GET", API + "/operator/feedback" + audience_query(owner))["items"]
    states = [item["state"] for item in inbox if item["resourceId"] == target]
    assert states.count("site_scheduled") == 1 and states.count("site_closed") == 1
    print("PASS: 现场安排、材料校验、两端分阶段可见、跟进退补、办结及站内通知去重")
    browser_district = "浏览器批量区 " + RUN
    browser_filing = registered_filing(agency, "WEB", browser_district)
    print(json.dumps({
        "supervisionBatch": {
            "templateTaskId": draft["id"], "templateTitle": draft["title"], "district": browser_district,
            "owner": browser_filing["owner"], "number": browser_filing["number"], "dueOn": business_date(12),
        },
        "supervisionSite": {"taskId": finished["id"], "owner": owner},
    }, ensure_ascii=False))

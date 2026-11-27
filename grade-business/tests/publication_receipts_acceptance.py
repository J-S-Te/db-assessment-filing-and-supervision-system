"""在独立本地预览验证公告发布回执和机构预警实际办理。"""

from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode

from acceptance import API, RUN, business_date, request, transition, upload
from hazard_ledger_acceptance import registration_payload
from workbench_acceptance import queue_item, scoped


def seed_recipient(suffix=""):
    """创建有效备案和登记的运营单位及测评机构。
    参数 suffix 为同一验收批次内区分备案和归属的标记。
    返回值 有效备案 ID 与两端的准确名称。
    """
    operator = "公告验收运营单位 " + RUN + suffix
    institution = "公告验收机构 " + RUN + suffix
    system = "公告预警验收系统 " + RUN + suffix
    current = request("POST", API + "/filings", {
        "number": "MESSAGE-" + RUN + suffix, "systemName": system,
        "organizationName": operator, "assessmentOrg": institution, "level": "3",
    }, status=201)
    path = API + "/filings/" + current["id"]
    for kind in ("filing_form", "classification_report"):
        upload(path, kind, kind + ".pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return current["id"], operator, institution


def publish(path, version):
    """显式发布指定当前草稿并返回已发布快照。
    参数 path 为监管公告地址，version 为已读取的当前监管版本。
    返回值 新的监管公告详情。
    """
    return request("POST", path + "/actions", {"action": "publish", "expectedVersion": version})


def assert_receipts(admin, delivered, read, acknowledged, version=None):
    """核对一个真实发布版本的送达、已读、签收及接收方记录。
    参数 admin 为监管详情地址，三项计数为期望结果，version 用于指定历史版本。
    返回值 含各接收方细节的回执页面。
    """
    query = "" if version is None else "?" + urlencode({"version": version})
    body = request("GET", admin + "/deliveries" + query)
    assert body["counts"] == {"delivered": delivered, "read": read, "acknowledged": acknowledged}, body
    assert len(body["items"]) == delivered and all(item["deliveredAt"] for item in body["items"])
    return body


def verify_publication_receipts(operator, institution, unrelated):
    """验证固定接收范围、版本隔离、两端独立阅读及监管历史回执。
    参数 operator、institution 为目标收件方，unrelated 为无权浏览的机构。
    返回值 无。
    """
    targets = [{"audience": "institution", "recipient": institution},
               {"audience": "operator", "recipient": operator}]
    payload = {"category": "announcement", "title": "选定对象公告 " + RUN,
               "publisher": "隔离监管发布", "body": "仅送达两名登记收件方", "pinned": True,
               "audience": "selected", "targets": targets}
    registry = API + "/publications"
    picked = request("GET", registry + "/recipients?" + urlencode({"audience": "institution", "q": institution}))
    assert targets[0] in picked["items"] and all(item["audience"] == "institution" for item in picked["items"]), picked
    draft = request("POST", registry, payload, status=201)
    identifier = draft["id"]
    admin = registry + "/" + identifier
    operator_base = API + "/operator/publications"
    institution_base = API + "/institution/publications"
    operator_path = operator_base + "/" + identifier
    institution_path = institution_base + "/" + identifier
    op_scope = scoped("operator", operator)
    inst_scope = scoped("institution", institution)
    request("GET", institution_path + inst_scope, status=404)
    live = publish(admin, draft["version"])
    first = live["publishedVersion"]
    request("POST", admin + "/actions", {"action": "publish", "expectedVersion": draft["version"]}, status=409)
    request("GET", institution_path + scoped("institution", unrelated), status=404)
    request("GET", operator_path, status=404)
    assert request("GET", institution_path + inst_scope)["body"] == payload["body"]
    assert_receipts(admin, 2, 0, 0, first)
    mark_one_and_sign(operator_path, institution_path, op_scope, inst_scope, first)
    assert_receipts(admin, 2, 2, 1, first)
    revision = request("PATCH", admin, {**payload, "body": "已确认的修订正文", "expectedVersion": live["version"]})
    assert request("GET", operator_path + op_scope)["body"] == payload["body"]
    revised = publish(admin, revision["version"])
    second = revised["publishedVersion"]
    assert second > first and not request("GET", operator_path + op_scope).get("readAt")
    request("POST", institution_path + "/acknowledge" + inst_scope, {"expectedVersion": first}, status=409)
    assert_receipts(admin, 2, 0, 0, second)
    assert request("GET", operator_base + "/unread-count" + op_scope)["count"] == 1
    assert request("POST", operator_base + "/read-all" + op_scope, {})["updated"] == 1
    assert request("POST", operator_base + "/read-all" + op_scope, {})["updated"] == 0
    assert request("GET", operator_base + "/unread-count" + op_scope)["count"] == 0
    assert_receipts(admin, 2, 1, 0, second)
    request("POST", admin + "/actions", {"action": "withdraw", "expectedVersion": revised["version"]})
    request("GET", institution_path + inst_scope, status=404)
    assert assert_receipts(admin, 2, 1, 0)["version"] == second
    assert_receipts(admin, 2, 2, 1, first)
    print("PASS: 定向公告、单条与批量已读、显式签收、修订版本和撤回历史回执")


def mark_one_and_sign(operator_path, institution_path, op_scope, inst_scope, version):
    """并发重复执行已读及签收，核对原先时间戳稳定。
    参数 两个 path 和 scope 分别为运营单位、机构的公告地址与归属，version 为发布版本。
    返回值 无。
    """
    body = {"expectedVersion": version}
    with ThreadPoolExecutor(max_workers=4) as pool:
        futures = [pool.submit(request, "POST", operator_path + "/read" + op_scope, body) for _ in range(4)]
        times = [job.result() for job in futures]
    assert all(item.get("readAt") == times[0]["readAt"] and not item.get("acknowledgedAt") for item in times)
    url = institution_path + "/acknowledge" + inst_scope
    signed = request("POST", url, body)
    assert signed.get("readAt") and signed.get("acknowledgedAt")
    assert request("POST", url, body) == signed


def seed_institution_alert(filing, institution_scope):
    """从真实直接登记隐患生成一个机构可处理的高风险预警。
    参数 filing 为当前有效备案标识，institution_scope 为派单机构范围。
    返回值 预警和原隐患标识，供后续验收。
    """
    payload = registration_payload(filing, "major_risk")
    payload["title"] = "机构预警来源 " + RUN
    payload["deadline"] = business_date(7)
    item = request("POST", API + "/hazards", payload, status=201)
    request("POST", API + "/hazards/" + item["id"] + "/actions",
            {"action": "dispatch", "expectedVersion": item["version"]})
    for _ in range(2):
        request("POST", API + "/alerts/sync")
    page = request("GET", API + "/institution/alerts" + institution_scope)
    matches = [row for row in page["items"] if row["hazardId"] == item["id"] and row["type"] == "high_risk"]
    assert len(matches) == 1, matches
    return matches[0]["id"], item["id"]


def alert_transition(path, scope, action, comment="机构处置验收"):
    """使用最新预警版本执行机构或监管的一个动作。
    参数 path 是业务端详情地址，scope 为范围后缀，action、comment 为操作值和意见。
    返回值 动作结果。
    """
    current = request("GET", path + scope)
    return request("POST", path + "/actions" + scope,
                   {"action": action, "expectedVersion": current["version"], "comment": comment})


def assert_alert_notice(audience, scope, identifier, state, current):
    """核对预警的历史送达状态和公告栏呈现的当前状态。
    参数 audience、scope 为两端中的指定接收人，identifier、state、current 为预警、送达状态和当前状态。
    返回值 无。
    """
    messages = request("GET", API + "/" + audience + "/feedback" + scope)["items"]
    matches = [n for n in messages if n["resourceId"] == identifier and n["state"] == state]
    assert len(matches) == 1 and matches[0]["status"] == current, matches


def verify_institution_alerts(filing, operator, institution, unrelated):
    """走通机构接手、提交反馈、监管退回重提和关闭单一预警。
    参数 filing 为预警备案，operator、institution、unrelated 为实际访问范围。
    返回值 无。
    """
    inst_scope = scoped("institution", institution)
    op_scope = scoped("operator", operator)
    identifier, hazard = seed_institution_alert(filing, inst_scope)
    agency = API + "/institution/alerts/" + identifier
    admin = API + "/alerts/" + identifier
    op = API + "/operator/alerts/" + identifier
    institution_board = API + "/institution/workbench" + inst_scope
    request("GET", agency + scoped("institution", unrelated), status=404)
    assert_alert_notice("institution", inst_scope, identifier, "pending", "pending")
    assert queue_item(institution_board, identifier)["status"] == "pending"
    assert alert_transition(agency, inst_scope, "process")["status"] == "processing"
    assert request("GET", op + op_scope)["status"] == "processing"
    submitted = alert_transition(agency, inst_scope, "submit_feedback")
    assert submitted["disposition"]["status"] == "pending"
    assert submitted["disposition"]["requesterAudience"] == "institution"
    assert queue_item(institution_board, identifier) is None
    task = queue_item(API + "/workbench/overview", identifier)
    assert task["label"] == "机构预警反馈审批"
    rejected = alert_transition(admin, "", "reject", "请补充核实说明")
    assert rejected["status"] == "processing" and rejected["disposition"]["status"] == "rejected"
    assert_alert_notice("institution", inst_scope, identifier, "rejected", "processing")
    assert queue_item(institution_board, identifier)["status"] == "processing"
    assert alert_transition(agency, inst_scope, "submit_feedback", "已补充核实说明")["status"] == "processing"
    approved = alert_transition(admin, "", "approve", "核实材料已齐备")
    assert approved["status"] == "closed" and approved["disposition"]["status"] == "approved"
    for path in (agency + inst_scope, op + op_scope, admin):
        assert request("GET", path)["status"] == "closed"
    assert_alert_notice("institution", inst_scope, identifier, "approved", "closed")
    assert_alert_notice("operator", op_scope, identifier, "approved", "closed")
    assert queue_item(institution_board, identifier) is None
    assert request("GET", API + "/hazards/" + hazard)["status"] == "open"
    print("PASS: 机构预警收件、处置反馈、退回重提及监管关闭同步，关联隐患继续整改")


if __name__ == "__main__":
    filing, operator, institution = seed_recipient()
    _, _, unrelated = seed_recipient("-other")
    verify_publication_receipts(operator, institution, unrelated)
    verify_institution_alerts(filing, operator, institution, unrelated)

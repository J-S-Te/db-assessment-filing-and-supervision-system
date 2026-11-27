"""通过本机隔离服务验收单位合并、三端归属和历史回执。"""

import os
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode

from acceptance import API, DOCUMENT, RUN, request
from directory_acceptance import DIRECTORY, approved_filing, credit_code, institution, scope
from publication_receipts_acceptance import publish


MERGES = DIRECTORY + "/unit-merges"


def create_unit(label):
    """建立具有本次唯一名称和信用代码的单位。
    参数 label 为主档或来源单位的测试标签。
    返回值 监管档案接口返回的真实单位记录。
    """
    return request("POST", DIRECTORY + "/units", {
        "name": f"合并联验 {RUN} {label}", "code": credit_code(),
    }, 201)


def operator_scope(unit):
    """把已登记的单位稳定范围附到运营者接口。
    参数 unit 为监管档案响应。
    返回值 编码的单位查询。
    """
    return "?" + urlencode({"organization": unit["scopeName"]})


def preserve_publication(source):
    """向来源单位真实发出并签收一份公告，作为历史隔离证据。
    参数 source 为尚未合并的来源单位档案。
    返回值 公告监管地址和运营者地址。
    """
    draft = request("POST", API + "/publications", {
        "category": "announcement", "title": "单位合并联验公告 " + RUN,
        "publisher": "隔离监管", "body": "仅验证既有签收原归属。", "pinned": False,
        "audience": "selected", "targets": [{"audience": "operator", "recipient": source["scopeName"]}],
    }, 201)
    admin = API + "/publications/" + draft["id"]
    operator = API + "/operator/publications/" + draft["id"]
    released = publish(admin, draft["version"])
    request("POST", operator + "/acknowledge" + operator_scope(source), {
        "expectedVersion": released["version"],
    })
    return admin, operator


def confirm_once(main, source, filing):
    """核对精确迁移范围并同时确认两次，验证只发生一次写入。
    参数 main 和 source 为主从档案，filing 为受影响备案的监管地址。
    返回值 已持久化的合并记录。
    """
    body = {"targetId": main["id"], "sourceIds": [source["id"]]}
    request("POST", MERGES + "/preview", {**body, "sourceIds": [source["id"], source["id"]]}, 400)
    preview = request("POST", MERGES + "/preview", body)
    assert not preview["blockers"] and preview["membershipsToAdd"] == 1, preview
    assert [move["id"] for move in preview["moves"]] == [filing.split("/")[-1]], preview
    with ThreadPoolExecutor(max_workers=2) as workers:
        writes = [workers.submit(request, "POST", MERGES, {**body, "token": preview["token"]})
                  for _ in range(2)]
        first, second = [write.result() for write in writes]
    assert first["id"] == second["id"] and first["replayed"] != second["replayed"], (first, second)
    history = request("GET", MERGES + "/" + first["id"])
    assert history["preview"]["moves"] == preview["moves"] and "token" not in history["preview"], history
    assert history["movedCount"] == 1 and history["preview"]["target"]["id"] == main["id"], history
    return history


def verify_current_filing(filing, main, source, agency, original):
    """核对三端当前所属单位、材料读取范围和旧审核回执。
    参数 filing 为监管地址，main、source、agency 为相关档案，original 为迁移前的详情快照、材料、审核历史和回执。
    返回值 无，归属或权限错误时终止联验。
    """
    identifier = filing.split("/")[-1]
    operator = API + "/operator/filings/" + identifier
    partner = API + "/institution/filings/" + identifier
    migrated = request("GET", filing)
    assert migrated.get("directorySnapshot") == original["detail"].get("directorySnapshot"), migrated
    for detail in (migrated, request("GET", partner + scope(agency["scopeName"])),
                   request("GET", operator + operator_scope(main))):
        assert detail["organizationName"] == main["scopeName"] and detail["status"] == "approved", detail
    request("GET", operator + operator_scope(source), status=404)
    materials = request("GET", operator + "/attachments" + operator_scope(main))
    assert materials == original["materials"] and len(materials["items"]) == 2, materials
    attachment = operator + "/attachments/" + materials["items"][0]["id"]
    assert request("GET", attachment + operator_scope(main)) == DOCUMENT
    request("GET", attachment + operator_scope(source), status=404)
    receipt = request("GET", operator + "/receipt" + operator_scope(main))
    assert receipt == original["receipt"] and migrated["number"] in receipt.decode(), receipt
    assert request("GET", filing + "/review-history") == original["history"]
    rejected = request("POST", API + "/filings", {
        "number": "DIR-M-" + RUN, "systemName": "归并来源下的另一套系统",
        "organizationName": source["scopeName"], "assessmentOrg": agency["scopeName"], "level": "3",
    }, 409)
    assert rejected["code"] == "filing_unit_merged", rejected


def verify_retained_scope(main, source, agency, history, admin_note, operator_note):
    """核对源主档冻结、关联迁移及旧公告收件与签收留痕。
    参数 main、source、agency 为本批档案，history 为合并证据，admin_note、operator_note 为公告地址。
    返回值 无，发现新归属读取旧公告时终止联验。
    """
    source_path = DIRECTORY + "/units/" + source["id"]
    frozen = request("GET", source_path)
    assert frozen["mergeId"] == history["id"] and frozen["mergedIntoId"] == main["id"], frozen
    assert frozen["status"] == "inactive" and not frozen["canEdit"], frozen
    request("PATCH", source_path, {"expectedVersion": frozen["version"], "status": "active"}, 403)
    link = API + "/institution/directory/units/" + main["id"] + scope(agency["scopeName"])
    assert request("GET", link)["id"] == main["id"]
    receipts = request("GET", admin_note + "/deliveries")["items"]
    assert [(item["recipient"], bool(item["acknowledgedAt"])) for item in receipts] == [
        (source["scopeName"], True),
    ], receipts
    assert request("GET", operator_note + operator_scope(source))["acknowledgedAt"]
    request("GET", operator_note + operator_scope(main), status=404)


def main():
    """在用户明确声明的隔离本机端口完成合并联验。
    参数 无，使用 GRADE_ACCEPTANCE_PORT 指定端口。
    返回值 无，失败时抛出可复盘的断言。
    """
    if not os.getenv("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请指定独立测试预览的 GRADE_ACCEPTANCE_PORT")
    target, source = create_unit("主单位"), create_unit("来源单位")
    agency = institution("-UNIT-MERGE")
    link = API + "/institution/directory/units/" + source["id"] + "/link"
    request("POST", link + scope(agency["scopeName"]))
    filing = approved_filing(source, agency)
    source_filing = API + "/operator" + filing.removeprefix(API)
    original = {"detail": request("GET", filing), "history": request("GET", filing + "/review-history"),
                "receipt": request("GET", source_filing + "/receipt" + operator_scope(source)),
                "materials": request("GET", source_filing + "/attachments" + operator_scope(source))}
    admin_note, operator_note = preserve_publication(source)
    history = confirm_once(target, source, filing)
    verify_current_filing(filing, target, source, agency, original)
    verify_retained_scope(target, source, agency, history, admin_note, operator_note)
    print(f"PASS: 名录单位合并、并发幂等、三端归属、材料隔离和原公告签收（{RUN}）")


if __name__ == "__main__":
    main()

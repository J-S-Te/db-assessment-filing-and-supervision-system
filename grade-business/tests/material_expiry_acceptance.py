"""在隔离本机预览核对有据登记、到期提醒、续期资料和确认关闭。"""

import json
import os
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlencode
from uuid import uuid4

from acceptance import API, DOCUMENT, RUN, business_date, request, transition, upload


BASE = API + "/material-expiries"
OWNER = "材料有效期联验单位 " + RUN
AGENCY = "材料有效期联验机构 " + RUN
OWNER_QUERY = "?" + urlencode({"organization": OWNER})
AGENCY_QUERY = "?" + urlencode({"institution": AGENCY})
FOREIGN_QUERY = "?" + urlencode({"organization": OWNER + " 未收件"})


def prepare_case(suffix):
    """建立独立备案并真实上传和审核两类必传正式文件。
    参数 suffix 区分服务端回归及浏览器样本。
    返回值 已通过备案、登记编号和两份正式附件。
    """
    number = "EXPIRY-" + RUN + "-" + suffix
    created = request("POST", API + "/filings", {
        "number": number, "systemName": "有效期联验系统 " + suffix,
        "organizationName": OWNER, "assessmentOrg": AGENCY, "level": "3",
    }, 201)
    path = API + "/filings/" + created["id"]
    attachment = upload(path, "filing_form", "备案依据.pdf")
    upload(path, "classification_report", "定级依据.pdf")
    transition(path, "submit", "pending_initial")
    transition(path, "approve", "pending_final")
    transition(path, "approve", "approved")
    return path, number, attachment


def register_material(number, original):
    """以明确日期及文件证据并发登记，核对仅有一条当前记录。
    参数 number 为已生效备案号，original 为经过上传校验的原件。
    返回值 有效期登记摘要。
    """
    candidates = request("GET", BASE + "/source-options?" + urlencode({"q": number}))["items"]
    assert original["id"] in {item["attachmentId"] for item in candidates}, candidates
    data = {"requestId": str(uuid4()), "sourceAttachmentId": original["id"],
            "validUntil": business_date(-1), "evidence": "原件签注的明确有效期", "warningDays": 5}
    with ThreadPoolExecutor(max_workers=2) as executor:
        replies = [executor.submit(request, "POST", BASE, data, 201) for _ in range(2)]
        created, repeated = [job.result() for job in replies]
    assert created == repeated and created["dateStatus"] == "expired" and created["version"] == 1
    request("POST", BASE, {**data, "evidence": "同请求号变更条件"}, status=409)
    request("POST", BASE, {**data, "requestId": str(uuid4())}, status=409)
    return created


def renew_at(path, version, date, *, content=DOCUMENT, query=OWNER_QUERY, code=201):
    """上传续期文件并核对业务 HTTP 响应。
    参数 path、version、date 为记录、当前版本和拟续期限，content、query、code 为文件、范围和预期状态。
    返回值 实际续传响应。
    """
    boundary = "expiry-" + RUN
    parts = [
        f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'
        for name, value in {
            "expectedVersion": version, "validUntil": date, "evidence": "续期证明视同实际原件核验",
        }.items()
    ]
    prefix = "".join(parts) + (
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="续期证明.pdf"\r\n'
        "Content-Type: application/pdf\r\n\r\n"
    )
    return request("POST", path + "/renewals" + query,
                   prefix.encode() + content + f"\r\n--{boundary}--\r\n".encode(), code,
                   {"Content-Type": "multipart/form-data; boundary=" + boundary})


def assert_notices(identifier, state, *, current):
    """核对两端收件人、状态和幂等签收。
    参数 identifier、state 指消息归属及状态，current 指当前日期状态。
    返回值 无。
    """
    for audience, query in (("operator", OWNER_QUERY), ("institution", AGENCY_QUERY)):
        inbox = API + "/" + audience + "/feedback"
        rows = [row for row in request("GET", inbox + query)["items"] if
                row["resource"] == "material-expiries" and row["resourceId"] == identifier and row["state"] == state]
        assert len(rows) == 1 and rows[0]["status"] == current, rows
        action = inbox + "/" + rows[0]["id"] + "/acknowledge"
        assert request("POST", action + query) == request("POST", action + query)
    foreign = request("GET", API + "/operator/feedback" + FOREIGN_QUERY)["items"]
    assert not any(row["resourceId"] == identifier for row in foreign)


def verify_renewal(created):
    """核对文件格式、归属、待审与批准的权限、下载和历史。
    参数 created 为已经过期的材料登记。
    返回值 更新后的监管台账地址。
    """
    identifier = created["id"]
    owner = API + "/operator/material-expiries/" + identifier
    agency = API + "/institution/material-expiries/" + identifier
    admin = BASE + "/" + identifier
    assert_notices(identifier, "material_expired", current="expired")
    later = business_date(60)
    assert renew_at(owner, 1, later, content=b"INVALID", code=415)["code"] == "unsupported_attachment"
    renew_at(owner, 1, later, query=FOREIGN_QUERY, code=404)
    renew_at(owner, 1, created["validUntil"], code=422)
    with ThreadPoolExecutor(max_workers=2) as executor:
        attempts = [executor.submit(renew_at, owner, 1, later) for _ in range(2)]
        first, repeated = [job.result() for job in attempts]
    assert first == repeated and first["version"] == 2 and first["renewalPending"] is True
    detail = request("GET", admin)
    assert detail["dateStatus"] == "expired" and len(detail["renewals"]) == 1
    attachment = "/renewals/" + detail["renewals"][0]["id"] + "/download"
    assert request("GET", owner + attachment + OWNER_QUERY) == DOCUMENT
    request("GET", agency + attachment + AGENCY_QUERY, status=404)
    request("GET", owner + attachment + FOREIGN_QUERY, status=404)
    decision = {"action": "approve", "expectedVersion": 2, "comment": "已核验递延日期"}
    accepted = request("POST", admin + "/actions", decision)
    assert accepted == request("POST", admin + "/actions", decision)
    assert accepted["dateStatus"] == "valid" and accepted["validUntil"] == later and not accepted["renewalPending"]
    assert request("GET", agency + attachment + AGENCY_QUERY) == DOCUMENT
    assert_notices(identifier, "material_expired", current="valid")
    assert_notices(identifier, "material_renewal_approved", current="valid")
    assert not any(row["id"] == identifier for row in request("GET", BASE + "?status=expired")["items"])
    return admin


if __name__ == "__main__":
    if not os.environ.get("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请指定本机隔离预览端口。")
    source_path, number, source = prepare_case("HTTP")
    created = register_material(number, source)
    path = verify_renewal(created)
    assert request("GET", source_path + "/attachments/" + source["id"]) == DOCUMENT
    history = request("GET", path)["history"]
    assert [event["action"] for event in history] == ["approve", "submit", "register"]
    assert [event["cycle"] for event in history] == [2, 1, 1]
    print("PASS: 明确依据登记、真实日期及提醒去重、跨端续传审核、旧预警关闭和原件保留")
    _, browser_number, browser_source = prepare_case("BROWSER")
    print(json.dumps({"organization": OWNER, "institution": AGENCY, "expiryId": created["id"],
                      "expiryFilingNumber": browser_number,
                      "expirySourceAttachmentId": browser_source["id"]}, ensure_ascii=False))

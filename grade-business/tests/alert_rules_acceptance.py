"""使用本机隔离预览验收真实业务来源与预警规则去重。"""

import json
import os
from urllib.parse import urlencode
from uuid import uuid4

from acceptance import API, RUN, request, transition, upload
from directory_acceptance import DIRECTORY, credit_code
from hazard_ledger_acceptance import registration_payload

RULES = API + "/alerts/rules"


def seed_district_hazard(suffix, district):
    """登记合法单位、终审备案和实际已派发的高风险隐患。
    参数 suffix 区分同批记录，district 为名录登记区县。
    返回值 已创建的备案、收件范围和隐患标识。
    """
    organization = "预警规则联验单位 " + RUN + suffix
    request("POST", DIRECTORY + "/units", {
        "name": organization, "district": district, "code": credit_code(),
    }, 201)
    filing = request("POST", API + "/filings", {
        "number": "RULE-" + RUN + suffix, "systemName": "预警规则联验系统 " + RUN + suffix,
        "organizationName": organization, "assessmentOrg": "预警联验机构 " + RUN + suffix, "level": "3",
    }, 201)
    base = API + "/filings/" + filing["id"]
    for kind in ("filing_form", "classification_report"):
        upload(base, kind, kind + ".pdf")
    transition(base, "submit", "pending_initial")
    transition(base, "approve", "pending_final")
    transition(base, "approve", "approved")
    hazard = request("POST", API + "/hazards", registration_payload(filing["id"], "major_risk"), 201)
    request("POST", API + "/hazards/" + hazard["id"] + "/actions", {
        "action": "dispatch", "expectedVersion": hazard["version"],
    })
    return request("GET", base), organization, hazard["id"]


def verify_rule_matches():
    """核对预览、实际站内范围、规则重放、交叠去重和停用版本。
    参数 无，使用本次专用合成业务数据。
    返回值 浏览器复测用名录范围与样本。
    """
    district = "预警联验区 " + RUN
    filing, owner, hazard = seed_district_hazard("OWN", district)
    _, other, outside = seed_district_hazard("OUT", district + "其他")
    request("POST", API + "/alerts/sync")
    body = {"name": "区县规则 " + RUN, "trigger": "high_risk", "audience": "operator",
            "district": district, "level": "3", "enabled": True}
    sample = request("POST", RULES + "/preview", body)["items"]
    assert len(sample) == 1 and sample[0]["recipient"] == owner
    original = {"requestId": str(uuid4()), **body}
    first = request("POST", RULES, original, 201)
    assert request("POST", RULES, original, 201) == first
    second = request("POST", RULES, {
        **original, "requestId": str(uuid4()), "name": "同范围同收件规则 " + RUN,
    }, 201)
    request("POST", RULES + "/sync")
    request("POST", RULES + "/sync")
    relevant = [item for item in request("GET", RULES + "/matches")["items"]
                if item["ruleId"] in {first["id"], second["id"]}]
    assert len(relevant) == 2 and all(item["resourceId"] == sample[0]["resourceId"] for item in relevant)
    inbox = API + "/operator/feedback?"
    my_notices = request("GET", inbox + urlencode({"organization": owner}))["items"]
    foreign = request("GET", inbox + urlencode({"organization": other}))["items"]
    assert sum(item["resourceId"] == sample[0]["resourceId"] for item in my_notices) == 1
    assert all(item["resourceId"] != sample[0]["resourceId"] for item in foreign)
    updated = request("PATCH", RULES + "/" + first["id"], {
        **body, "enabled": False, "expectedVersion": first["version"],
    })
    assert not updated["enabled"] and updated["version"] == first["version"] + 1
    request("PATCH", RULES + "/" + first["id"], {
        **body, "enabled": True, "expectedVersion": first["version"],
    }, 409)
    assert outside != hazard
    return {"alertRuleDemo": {"district": district, "systemName": filing["systemName"], "owner": owner}}


if __name__ == "__main__":
    assert os.environ.get("GRADE_ACCEPTANCE_PORT"), "请指定隔离预览端口"
    result = verify_rule_matches()
    print("PASS: 本地定向预警预览、当前区县、交叠命中留痕、站内去重和规则版本")
    print(json.dumps(result, ensure_ascii=False))

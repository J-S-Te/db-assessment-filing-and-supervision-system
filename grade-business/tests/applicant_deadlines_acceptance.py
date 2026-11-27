"""在本机隔离预览核对首报申请方、初审机构、终审监管的时限同步。"""

import json
import os
from urllib.parse import urlencode

from acceptance import API, RUN, request, transition, upload


def verify_filing_deadline_handoff():
    """提交真实材料并核对审核阶段、报送方关注时限及归属。
    参数 无，使用独立命名的合成业务记录。
    返回值 可复用为浏览器联验的数据标识。
    """
    owner = "时限联验运营单位 " + RUN
    institution = "时限联验测评机构 " + RUN
    org_scope = "?" + urlencode({"organization": owner})
    inst_scope = "?" + urlencode({"institution": institution})
    wrong = "?" + urlencode({"organization": owner + "其他"})
    filing = request("POST", API + "/operator/filings" + org_scope, {
        "number": "DEADLINE-APPLICANT-" + RUN, "systemName": "三端时限联验系统 " + RUN,
        "assessmentOrg": institution, "level": "3",
    }, 201)
    identifier = filing["id"]
    operator = API + "/operator/filings/" + identifier
    partner = API + "/institution/filings/" + identifier
    scope_deadline = API + "/operator/workbench/deadlines"
    mine = scope_deadline + "/filings/" + identifier
    agency = API + "/institution/workbench/deadlines/filings/" + identifier + inst_scope
    assert request("GET", mine + org_scope) == {}
    for category in ("filing_form", "classification_report"):
        upload(operator, category, category + ".pdf", scope=org_scope)
    version = request("GET", operator + org_scope)["version"]
    current = request("POST", operator + "/submit" + org_scope, {"expectedVersion": version})
    applying = request("GET", mine + org_scope)
    acting = request("GET", agency)
    assert applying["waitingFor"] == "institution" and applying["deadline"] == acting["deadline"]
    assert set(applying) == {"label", "deadline", "waitingFor"}
    assert request("GET", mine + wrong) == {}
    assert not request("GET", scope_deadline + wrong)["items"]
    monitored = next(item for item in request("GET", scope_deadline + org_scope)["items"] if item["id"] == identifier)
    assert monitored["waitingFor"] == "institution" and "canRemind" not in monitored
    operator_tasks = request("GET", API + "/operator/workbench" + org_scope)["tasks"]
    assert all(item["id"] != identifier for item in operator_tasks)
    request("POST", partner + "/review-actions" + inst_scope, {
        "decision": "approve", "expectedVersion": current["version"],
    })
    applying = request("GET", mine + org_scope)
    acting = next(item for item in request("GET", API + "/workbench/deadlines")["items"] if item["id"] == identifier)
    assert applying["waitingFor"] == "regulator" and applying["deadline"] == acting["deadline"]
    transition(API + "/filings/" + identifier, "approve", "approved")
    assert request("GET", mine + org_scope) == {}
    return {"organization": owner, "institution": institution, "filingId": identifier}


if __name__ == "__main__":
    assert os.environ.get("GRADE_ACCEPTANCE_PORT"), "请指定本次隔离预览端口"
    result = verify_filing_deadline_handoff()
    print("PASS: 首报申请人、初审机构及终审监管时限同源，外单位隔离、办结移出")
    print(json.dumps(result, ensure_ascii=False))

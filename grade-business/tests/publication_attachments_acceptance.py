"""在明确指定的隔离预览核验公告附件及版本关联。"""

import io
import os
from concurrent.futures import ThreadPoolExecutor
from http.client import HTTPConnection
from urllib.parse import urlencode
from zipfile import ZipFile, ZIP_DEFLATED

from acceptance import API, DOCUMENT, ORIGIN, PORT, RUN, request
from publication_receipts_acceptance import seed_recipient, publish


REGISTRY = API + "/publications"
FIXTURE_PDF = DOCUMENT
VALID_DOCX = "联验公文.docx"
ORIGINAL_PDF = "监督检查公文.pdf"


def brief_word():
    """生成可通过 Word 容器内容校验的合成测试公文。
    参数 无。
    返回值 DOCX 原始字节。
    """
    buffer = io.BytesIO()
    with ZipFile(buffer, "w", ZIP_DEFLATED) as archive:
        archive.writestr("[Content_Types].xml",
                         '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">'
                         '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>'
                         '<Override PartName="/word/document.xml" '
                         'ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>'
                         '</Types>')
        archive.writestr("_rels/.rels",
                         '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">'
                         '<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/'
                         'relationships/officeDocument" Target="word/document.xml"/></Relationships>')
        archive.writestr("word/document.xml",
                         '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">'
                         '<w:body><w:p><w:r><w:t>隔离验收公文</w:t></w:r></w:p></w:body></w:document>')
    return buffer.getvalue()


def form(name, content, targets):
    """构造沿用站内发布合同的通知通报表单。
    参数 name、content 为公告标题与内容，targets 为选定收件对象。
    返回值 公告草稿所需字段。
    """
    return {"category": "bulletin", "title": name + RUN, "publisher": "隔离监管",
            "body": content, "pinned": False, "audience": "selected", "targets": targets}


def attach(path, version, items, expected=200):
    """将多份材料在同一个 multipart 请求中提交。
    参数 path、version、items、expected 依次为公告地址、预期版本、文件和预期状态码。
    返回值 更新详情或约定的业务错误。
    """
    boundary = "pub-attachment-" + RUN
    parts = [f'--{boundary}\r\nContent-Disposition: form-data; name="expectedVersion"\r\n\r\n{version}\r\n'.encode()]
    for name, value in items:
        parts.append((f'--{boundary}\r\nContent-Disposition: form-data; name="files"; '
                      f'filename="{name}"\r\nContent-Type: application/octet-stream\r\n\r\n').encode())
        parts.extend([value, b"\r\n"])
    parts.append(f"--{boundary}--\r\n".encode())
    return request("POST", path + "/attachments", b"".join(parts), expected,
                   {"Content-Type": "multipart/form-data; boundary=" + boundary})


def entry(path, item, version, **scope):
    """形成包含公告版本和明确范围的附件地址。
    参数 path、item、version 为公告地址、附件、版本，scope 为收件范围。
    返回值 可用于直接下载的完整 API 路径。
    """
    return path + "/attachments/" + item["id"] + "?" + urlencode({"version": version, **scope})


def confirm_download(path, document, partial=False):
    """核对实际下载字节、中文文件名附件头、私有缓存与部分下载行为。
    参数 path 为待下载地址，document 为期望内容，partial 表示覆盖 Range 响应。
    返回值 无。
    """
    connection = HTTPConnection(ORIGIN, PORT, timeout=10)
    try:
        connection.request("GET", path, headers={"Range": "bytes=0-7"} if partial else {})
        response = connection.getresponse()
        output = response.read()
        assert response.status == (206 if partial else 200), (path, response.status, output[:200])
        assert response.getheader("Content-Disposition").startswith("attachment;")
        assert "filename*=utf-8" in response.getheader("Content-Disposition").lower()
        assert response.getheader("X-Content-Type-Options") == "nosniff"
        assert "no-store" in response.getheader("Cache-Control")
        assert output == (document[:8] if partial else document)
        if partial:
            assert response.getheader("Content-Range") == "bytes 0-7/" + str(len(document))
    finally:
        connection.close()


def check_batch(path, created, word):
    """核对批量材料校验、原子保存、重复、限额与并发版本约束。
    参数 path、created 为监管公告地址及初稿，word 是实际 Word 文件字节。
    返回值 已保存的初始附件版本。
    """
    version = created["version"]
    error = attach(path, version, [("可以保存.pdf", FIXTURE_PDF),
                                  ("截断.pdf", b"%PDF-1.7\nNo EOF")], 400)
    assert error["code"] == "attachment_type", error
    assert not request("GET", path).get("attachments") and request("GET", path)["version"] == version
    err = attach(path, version, [("伪装的公文.docx", b"dummy text")], 400)
    assert err["code"] == "attachment_type", err
    saved = attach(path, version, [(ORIGINAL_PDF, FIXTURE_PDF),
                                   (VALID_DOCX, word), (ORIGINAL_PDF, FIXTURE_PDF)])
    assert saved["version"] == version + 1 and len(saved["attachments"]) == 2, saved
    assert all(set(row) == {"id", "fileName", "size", "uploadedAt"} for row in saved["attachments"]), saved
    repeated = attach(path, saved["version"], [(ORIGINAL_PDF, FIXTURE_PDF)])
    assert repeated["version"] == saved["version"] and repeated["attachments"] == saved["attachments"], (
        saved["version"], repeated["version"], saved["attachments"], repeated["attachments"])
    attach(path, version, [(ORIGINAL_PDF, FIXTURE_PDF)], 409)
    oversized = [(f"补充材料{index}.pdf", FIXTURE_PDF) for index in range(7)]
    limit = attach(path, saved["version"], oversized, 409)
    assert limit["code"] == "attachment_limit", limit
    assert request("GET", path)["attachments"] == saved["attachments"]
    return saved


def check_concurrent_upload(targets):
    """并发修改同一个草稿版本，只允许一个请求产生版本推进。
    参数 targets 为用于这一独立草稿的发布对象。
    返回值 无。
    """
    saved = request("POST", REGISTRY, form("并发公文草稿", "两次提交独立材料", targets), 201)
    path = REGISTRY + "/" + saved["id"]

    def upload_once(index):
        """从同一版本提交不同文件并保留真实成功或冲突结果。
        参数 index 为并发分支标识。
        返回值 是否创建了新版本。
        """
        try:
            attach(path, saved["version"], [(f"并发附件{index}.pdf", FIXTURE_PDF)])
            return True
        except AssertionError as exc:
            if exc.args[0][2] != 409:
                raise
            return False

    with ThreadPoolExecutor(max_workers=2) as pool:
        outcomes = list(pool.map(upload_once, [1, 2]))
    assert outcomes.count(True) == 1, outcomes
    current = request("GET", path)
    assert current["version"] == saved["version"] + 1 and len(current["attachments"]) == 1, current


def check_live_download(path, saved, operator, institution, unrelated, word):
    """发布后检查三端授权、二进制下载以及机构签收。
    参数 path、saved 为监管公告路径及草稿，operator、institution、unrelated 是不同范围，word 是附件。
    返回值 公告发布详情及两端地址。
    """
    uid = saved["id"]
    inst_path = API + "/institution/publications/" + uid
    op_path = API + "/operator/publications/" + uid
    items = {row["fileName"]: row for row in saved["attachments"]}
    request("GET", entry(inst_path, items[ORIGINAL_PDF], saved["version"], institution=institution), status=404)
    live = publish(path, saved["version"])
    public_version = live["publishedVersion"]
    query = "?" + urlencode({"institution": institution})
    assert request("GET", inst_path + query)["attachments"] == live["attachments"]
    confirm_download(entry(inst_path, items[VALID_DOCX], public_version, institution=institution), word)
    confirm_download(entry(op_path, items[ORIGINAL_PDF], public_version, organization=operator), FIXTURE_PDF, True)
    request("GET", entry(inst_path, items[ORIGINAL_PDF], public_version, institution=unrelated), status=404)
    request("GET", entry(op_path, items[ORIGINAL_PDF], public_version), status=400)
    request("GET", entry(op_path, items[ORIGINAL_PDF], public_version, organization=institution), status=404)
    request("POST", inst_path + "/acknowledge" + query, {"expectedVersion": public_version})
    return live, inst_path, op_path


def check_revision_withdrawal(path, live, inst_path, op_path, institution, operator):
    """修订和撤回后核对门户链接及监管历史原件回执。
    参数 path、live 为公告路径及最初发布，inst_path、op_path 为两端路径，后两项为归属。
    返回值 无。
    """
    first = live["publishedVersion"]
    original = next(row for row in live["attachments"] if row["fileName"] == ORIGINAL_PDF)
    old_url = entry(op_path, original, first, organization=operator)
    revised = request("DELETE", path + "/attachments/" + original["id"], {"expectedVersion": live["version"]})
    assert revised["publishedVersion"] == first and original not in revised["attachments"], revised
    confirm_download(old_url, FIXTURE_PDF)
    latest = publish(path, revised["version"])
    new_version = latest["publishedVersion"]
    request("GET", old_url, status=404)
    request("GET", entry(inst_path, original, new_version, institution=institution), status=404)
    request("POST", path + "/actions", {"action": "withdraw", "expectedVersion": latest["version"]})
    surviving = latest["attachments"][0]
    request("GET", entry(op_path, surviving, new_version, organization=operator), status=404)
    past = request("GET", path + "/deliveries?" + urlencode({"version": first}))
    assert past["attachments"] == live["attachments"] and past["counts"]["acknowledged"] == 1, past
    tail = request("GET", path + "/deliveries")
    assert tail["version"] == new_version and tail["attachments"] == latest["attachments"], tail
    history = request("GET", path + "/history")["items"]
    assert next(row for row in history if row["version"] == first)["attachments"] == live["attachments"], history
    confirm_download(entry(path, original, first), FIXTURE_PDF)
    request("GET", entry(path, original, new_version), status=404)


def check_legacy_broadcast(operator):
    """确认旧版无范围入口只保留公告正文，附件继续核对实际送达。
    参数 operator 为当前已有实际业务范围的运营单位。
    返回值 无。
    """
    base = {"category": "announcement", "title": "全体站内公告 " + RUN,
            "publisher": "隔离监管", "body": "有材料的全体公告", "pinned": False}
    draft = request("POST", REGISTRY, base, 201)
    path = REGISTRY + "/" + draft["id"]
    staged = attach(path, draft["version"], [(ORIGINAL_PDF, FIXTURE_PDF)])
    published = publish(path, staged["version"])
    reader = API + "/operator/publications/" + draft["id"]
    anonymous = request("GET", reader)
    assert anonymous["body"] == base["body"] and not anonymous.get("attachments"), anonymous
    row = published["attachments"][0]
    version = published["publishedVersion"]
    request("GET", entry(reader, row, version), status=400)
    confirm_download(entry(reader, row, version, organization=operator), FIXTURE_PDF)


def main():
    """分批执行附件校验和发布范围及历史回执验收。
    参数 无，测试端口需通过环境变量明确提供。
    返回值 无，业务断言失败时抛出异常。
    """
    if not os.getenv("GRADE_ACCEPTANCE_PORT"):
        raise SystemExit("请先指定隔离测试预览 GRADE_ACCEPTANCE_PORT")
    _, operator, institution = seed_recipient("附件定向")
    _, _, unrelated = seed_recipient("附件旁观")
    targets = [{"audience": "operator", "recipient": operator}, {"audience": "institution", "recipient": institution}]
    draft = request("POST", REGISTRY, form("公文附件联验", "请查收随附材料", targets), 201)
    path = REGISTRY + "/" + draft["id"]
    word = brief_word()
    staged = check_batch(path, draft, word)
    check_concurrent_upload(targets)
    print("PASS: PDF 与 DOCX 校验、批量原子写入、限额、重复上传及并发版本约束")
    live, inst_path, op_path = check_live_download(path, staged, operator, institution, unrelated, word)
    check_revision_withdrawal(path, live, inst_path, op_path, institution, operator)
    check_legacy_broadcast(operator)
    print("PASS: 两端下载、定向范围、修订与撤回隔离、监管历史附件及签收回执")


if __name__ == "__main__":
    main()

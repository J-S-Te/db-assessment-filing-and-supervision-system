package publications

import (
	"errors"
	"mime/multipart"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/cases"
)

// TestAttachmentUploadRequestBudget 验证同次请求中的每份材料都占用上传额度。
// 参数 t 为请求表单边界测试上下文。
// 返回值 无。
func TestAttachmentUploadRequestBudget(t *testing.T) {
	form := &multipart.Form{
		Value: map[string][]string{"expectedVersion": {"2"}},
		File: map[string][]*multipart.FileHeader{"files": {
			{Size: maxAttachmentSize}, {Size: maxAttachmentSize}, {Size: 1},
		}},
	}
	if _, _, err := attachmentUploadParts(form); !errors.Is(err, cases.ErrFileSize) {
		t.Fatalf("超过 40 MiB 的上传请求未被拒绝: %v", err)
	}
}

// TestPublicationAttachmentUploadRollback 验证格式失败和数据库拒绝时均不会留下部分附件。
// 参数 t 为隔离 PostgreSQL 与临时文件测试上下文。
// 返回值 无。
func TestPublicationAttachmentUploadRollback(t *testing.T) {
	pool, router, dir := attachmentFixture(t)
	path := createAttachmentTestDraft(t, router)
	file := attachmentPart{"可用公文.pdf", "%PDF-1.4\nnotice\n%%EOF\n"}
	bad := attachmentPart{"坏公文.pdf", "%PDF-1.4\nincomplete"}
	readAttachmentResponse(t, uploadAttachmentRequest(router, path, 1, file, bad), 400)
	assertAttachmentPersistence(t, pool, dir, 0)
	const rejectSQL = "ALTER TABLE publication_events ADD CONSTRAINT reject_attachment_revision CHECK (version<>2)"
	if _, err := pool.Exec(t.Context(), rejectSQL); err != nil {
		t.Fatal(err)
	}
	readAttachmentResponse(t, uploadAttachmentRequest(router, path, 1, file), 500)
	assertAttachmentPersistence(t, pool, dir, 0)
	current := pubRequest[attachmentTestDetail](t, router, "GET", path, nil, 200)
	if current.Version != 1 || len(current.Attachments) != 0 {
		t.Fatalf("失败的附件请求推进了工作草稿: %+v", current)
	}
}

// TestPublicationAttachmentUploadConcurrency 验证并发旧版本、按内容去重和未登记文件清理。
// 参数 t 为隔离版路由与临时存储测试上下文。
// 返回值 无。
func TestPublicationAttachmentUploadConcurrency(t *testing.T) {
	pool, router, dir := attachmentFixture(t)
	path := createAttachmentTestDraft(t, router)
	file := attachmentPart{"同步测试.pdf", "%PDF-1.4\nsame body\n%%EOF\n"}
	statuses := make(chan int, 2)
	for range 2 {
		go func() { statuses <- uploadAttachmentRequest(router, path, 1, file).Code }()
	}
	a, b := <-statuses, <-statuses
	if a+b != 609 || a != 409 && b != 409 {
		t.Fatalf("并发结果应为一个 200 和一个 409: %d, %d", a, b)
	}
	item := readAttachmentResponse(t, uploadAttachmentRequest(router, path, 2, file), 200)
	if item.Version != 2 || len(item.Attachments) != 1 {
		t.Fatalf("重复上传产生了新版本或文件: %+v", item)
	}
	assertAttachmentPersistence(t, pool, dir, 1)
	checkPortalAttachmentVisibility(t, router, path, item, file)
}

// checkPortalAttachmentVisibility 验证不提供门户范围时只公开公告正文。
// 参数 t 为测试上下文，router、path 为路由和监管访问地址，item、file 为当前附件及字节。
// 返回值 无。
func checkPortalAttachmentVisibility(t *testing.T, router http.Handler, path string, item attachmentTestDetail,
	file attachmentPart,
) {
	t.Helper()
	pubRequest[attachmentTestDetail](t, router, "PATCH", path, map[string]any{
		"category": "bulletin", "title": "广播公文", "publisher": "测试监管", "body": "发布正文",
		"pinned": false, "audience": "all", "expectedVersion": 2,
	}, 200)
	pubAction(t, router, path, "publish", 3, 200)
	portal := readerPath + "/" + item.ID
	broadcast := pubRequest[map[string]any](t, router, "GET", portal, nil, 200)
	if _, leaked := broadcast["attachments"]; leaked {
		t.Fatal("无范围的兼容广播暴露了附件信息")
	}
	assertAttachmentDownload(t, router, portal+"/attachments/"+item.Attachments[0].ID, 4, "", "", 400)
	assertAttachmentDownload(t, router, path+"/attachments/"+item.Attachments[0].ID, 4, "", file.body, 200)
}

// attachmentFixture 创建真实公告、存储及受控接收方的隔离测试环境。
// 参数 t 为承担数据库模式和文件目录回收的测试上下文。
// 返回值 专用连接池、实际 HTTP 处理器和暂存目录。
func attachmentFixture(t *testing.T) (*pgxpool.Pool, http.Handler, string) {
	t.Helper()
	pool, _ := publicationFixture(t)
	dir := t.TempDir()
	store, err := cases.New(pool, dir, "公告测试席位")
	if err != nil {
		t.Fatal(err)
	}
	router := http.NewServeMux()
	NewWithAttachments(pool, "公告测试席位", store).Register(router)
	seedPublicationRecipient(t, pool, "ATTACH-BASE", "收件单位", "收件机构", "approved", false)
	return pool, router, dir
}

// assertAttachmentPersistence 核对数据库和本地文件中的实际持久化结果。
// 参数 t、pool、dir 分别为测试上下文、数据连接和受控目录，want 为应保留的文件数。
// 返回值 无。
func assertAttachmentPersistence(t *testing.T, pool *pgxpool.Pool, dir string, want int) {
	t.Helper()
	var files, linked int
	err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM publication_attachments),
		(SELECT count(*) FROM publication_version_attachments)`).Scan(&files, &linked)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if files != want || len(onDisk) != want || linked != want {
		t.Fatalf("文件、清单和实际落盘状态不一致: %d, %d, %d", files, linked, len(onDisk))
	}
}

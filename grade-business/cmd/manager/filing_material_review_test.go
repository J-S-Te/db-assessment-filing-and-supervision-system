package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
	"grade-business/internal/cases"
	"grade-business/internal/precheck"
	"grade-business/migrations"
)

// TestFilingMaterialReviewScope 验证备案材料逐项复核的辖区、版本与持久化。
// 参数 t 提供隔离数据库及真实管理端路由的验证上下文。
// 返回值 无。
func TestFilingMaterialReviewScope(t *testing.T) {
	dsn := os.Getenv("GRADE_ACCESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要单独隔离的 GRADE_ACCESS_TEST_DSN")
	}
	db := accessTestSchema(t, dsn)
	if err := migrations.Apply(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	first, second, org, _ := accessSubjects(t, db)
	if _, err := db.Exec(t.Context(), `UPDATE directory_parties SET district=CASE id
		WHEN $1 THEN '330802' ELSE '衢江区' END WHERE id IN ($1,$2)`, first, second); err != nil {
		t.Fatal(err)
	}
	own := accessFiling(t, db, "MATERIAL-OWN", "权限单位甲", "权限机构甲", first, org)
	other := accessFiling(t, db, "MATERIAL-OTHER", "权限单位乙", "权限机构甲", second, org)
	hit := filingReviewRoutes(t, db, own)
	for _, code := range []string{"330802", "330800"} {
		_, err := access.Provision(t.Context(), db, access.AccountInput{
			Username: "audit-" + code, Name: "测试监管员", Role: access.Regulator,
			AreaCode: code, PersonKey: "audit-" + code, Password: testInitialPassword,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	county := accessLogin(t, hit, "audit-330802", "330802")
	city := accessLogin(t, hit, "audit-330800", "330800")
	assertFilingMaterialReview(t, hit, county, city, own, other)
	assertFilingContentPrecheck(t, hit, county, own, other)
	if _, err := db.Exec(t.Context(), "UPDATE business_cases SET material_revision=material_revision+1 WHERE id=$1", own); err != nil {
		t.Fatal(err)
	}
	if result := hit(county, "GET", "/api/v1/filings/"+own+"/prechecks/review", nil, 200); strings.Contains(result.Body.String(), "请补充盖章后的材料") {
		t.Fatal("新版材料沿用了旧版人工意见")
	}
}

// filingReviewRoutes 组装带本地正文读取和后台任务的实际备案路由。
// 参数 t、db、filingID 为测试、隔离库和合成原件所属备案。
// 返回值 可发起真实请求的端点。
func filingReviewRoutes(t *testing.T, db *pgxpool.Pool, filingID string) requestAccess {
	t.Helper()
	directory := t.TempDir()
	key := strings.Repeat("a", 48)
	payload := filingAuditWord(t, "盖章状态：未盖章", "承载数据：测试工单")
	if err := os.WriteFile(filepath.Join(directory, key), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `INSERT INTO case_attachments(case_id,kind,file_name,file_key,size_bytes)
		VALUES($1,'classification_report','定级报告.docx',$2,$3)`, filingID, key, len(payload)); err != nil {
		t.Fatal(err)
	}
	records, err := cases.New(db, directory, "辖区测试")
	if err != nil {
		t.Fatal(err)
	}
	checks := precheck.New(db, materialSource(records), materialContent(records, false))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); checks.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	cfg := settings{address: "127.0.0.1:18549"}
	return accessRequest(t, routes(cfg, db, records, checks, http.NotFoundHandler()))
}

// filingAuditWord 生成带指定正文的最小合成 DOCX 原件。
// 参数 t 为测试，lines 为需要由真实解析器读出的段落。
// 返回值 关闭并完成写入的 DOCX 字节。
func filingAuditWord(t *testing.T, lines ...string) []byte {
	t.Helper()
	var content bytes.Buffer
	archive := zip.NewWriter(&content)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	var xmlDoc bytes.Buffer
	xmlDoc.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, text := range lines {
		xmlDoc.WriteString("<w:p><w:r><w:t>")
		if err := xml.EscapeText(&xmlDoc, []byte(text)); err != nil {
			t.Fatal(err)
		}
		xmlDoc.WriteString("</w:t></w:r></w:p>")
	}
	xmlDoc.WriteString("</w:body></w:document>")
	if _, err := entry.Write(xmlDoc.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return content.Bytes()
}

// assertFilingContentPrecheck 等待实际文档预审并检查 11 项与原件定位。
// 参数 t、hit、county、own、other 为测试上下文、会话和不同辖区备案。
// 返回值 无。
func assertFilingContentPrecheck(t *testing.T, hit requestAccess, county *http.Cookie, own, other string) {
	t.Helper()
	base := "/api/v1/filings/"
	var job precheck.Job
	for range 25 {
		result := hit(county, "GET", base+own+"/prechecks/latest", nil, 200)
		if err := json.Unmarshal(result.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "succeeded" || job.Status == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if job.Status != "succeeded" || len(job.Checks) != 11 || len(job.Findings) != 2 {
		t.Fatalf("预审未按原件完成 11 项和原有缺件检查: %s %d %d", job.Status, len(job.Checks), len(job.Findings))
	}
	seal := job.Checks[3]
	if seal.Status != "fail" || len(seal.Evidence) != 1 || seal.Evidence[0].FileName != "定级报告.docx" ||
		seal.Evidence[0].Location != "第 1 段" || seal.Evidence[0].Quote != "盖章状态：未盖章" {
		t.Fatalf("真实正文未形成对应的用章异常及段落位置: %+v", seal)
	}
	hit(county, "GET", base+other+"/prechecks/"+job.ID, nil, 404)
}

// assertFilingMaterialReview 检查真实接口保存、拒绝跨区和并发旧版本写入。
// 参数 t、hit 为测试环境，county、city 为会话，own、other 为不同辖区备案。
// 返回值 无。
func assertFilingMaterialReview(t *testing.T, hit requestAccess, county, city *http.Cookie, own, other string) {
	t.Helper()
	path := "/api/v1/filings/" + own + "/prechecks/review"
	outside := "/api/v1/filings/" + other + "/prechecks/review"
	hit(nil, "GET", path, nil, 401)
	hit(county, "GET", outside, nil, 404)
	hit(city, "GET", outside, nil, 200)
	result := hit(county, "GET", path, nil, 200)
	var review struct {
		MaterialRevision int64 `json:"materialRevision"`
		Items            []struct {
			RuleID string `json:"ruleId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &review); err != nil || len(review.Items) != 11 {
		t.Fatalf("未返回完整的 11 项检查: %v", err)
	}
	rule := "/" + review.Items[3].RuleID
	body := map[string]any{"status": "fail", "remark": "请补充盖章后的材料",
		"materialRevision": review.MaterialRevision, "expectedVersion": 0}
	hit(county, "PUT", outside+rule, body, 404)
	hit(county, "PUT", path+rule, body, 200)
	hit(city, "PUT", path+rule, body, 409)
	persisted := hit(city, "GET", path, nil, 200)
	if !strings.Contains(persisted.Body.String(), "请补充盖章后的材料") {
		t.Fatal("辖区内另一审核席位未读取到已保存的意见")
	}
	hit(county, "POST", "/api/v1/filings/"+own+"/prechecks", nil, 202)
	hit(county, "POST", "/api/v1/filings/"+other+"/prechecks", nil, 404)
}

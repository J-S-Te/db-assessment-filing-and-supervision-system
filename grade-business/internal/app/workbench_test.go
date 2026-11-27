package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWorkbenchOverviewRejectsUnexpectedQuery 验证总览接口只接受声明的输入。
// 参数 t 为测试结果接收器。
// 返回值 无。
func TestWorkbenchOverviewRejectsUnexpectedQuery(t *testing.T) {
	mux := http.NewServeMux()
	RegisterWorkbenchOverview(mux, nil, ReviewWindows{})
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/api/v1/workbench/overview?unused=true", nil))
	if out.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", out.Code)
	}
}

package app

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/migrations"
)

// TestReviewDeadlinesUseCurrentStage 验证跨日分档、补正重提和终审起算。
// 参数 t 为测试上下文，使用专门创建的隔离 schema。
// 返回值 无。
func TestReviewDeadlinesUseCurrentStage(t *testing.T) {
	ctx := context.Background()
	db := openWorkbenchTestDB(t, ctx)
	seedReviewDeadlines(t, ctx, db)
	got, err := readOverview(ctx, db, ReviewWindows{FilingDays: 3, FilingFinalDays: 3, ReportDays: 5})
	if err != nil {
		t.Fatal(err)
	}
	if want := (reviewDeadlineCounts{Overdue: 1, Near: 2, Upcoming: 2}); got.ReviewDeadlines == nil ||
		*got.ReviewDeadlines != want {
		t.Fatalf("审核分档 = %+v, 期望 %+v", got.ReviewDeadlines, want)
	}
	if len(got.Tasks) != 5 || got.Tasks[0].Number != "F-OVER" || got.Tasks[1].Number != "F-RESUBMIT" {
		t.Fatalf("未按当前阶段截止日期排序: %+v", got.Tasks)
	}
	disabled, err := readOverview(ctx, db, ReviewWindows{})
	if err != nil || disabled.ReviewDeadlines != nil {
		t.Fatalf("关闭审核期限仍有汇总: %+v, %v", disabled.ReviewDeadlines, err)
	}
	for _, task := range disabled.Tasks {
		if task.Deadline != "" {
			t.Fatalf("关闭审核期限仍返回截止日期: %+v", task)
		}
	}
}

// openWorkbenchTestDB 为工作台数据库用例创建短期独立 schema。
// 参数 t 为测试对象，ctx 控制迁移和初始化。
// 返回值 仅指向当前测试 schema 的连接池。
func openWorkbenchTestDB(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("GRADE_BUSINESS_TEST_DSN")
	if dsn == "" {
		t.Skip("需要明确配置 GRADE_BUSINESS_TEST_DSN")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != "grade_business_test" {
		t.Fatal("须使用明确隔离的 grade_business_test 数据库")
	}
	root, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.Close)
	name := fmt.Sprintf("workbench_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	config.ConnConfig.RuntimeParams["search_path"] = name
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

// seedReviewDeadlines 写入覆盖相邻日期边界的人工验收事件。
// 参数 t 为测试对象，ctx 控制查询，db 为当前测试 schema。
// 返回值 无。
func seedReviewDeadlines(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	_, err := db.Exec(ctx, `
		INSERT INTO workflow_calendar_days(day,is_workday)
		SELECT (timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date + offset_days),true
		FROM generate_series(-30,40) AS offset_days;
		INSERT INTO business_cases(kind,number,assessment_org,level,status,version)
		  VALUES ('filing','F-OVER','测试机构','2','pending_initial',2),
		         ('filing','F-RESUBMIT','测试机构','2','pending_initial',4),
		         ('filing','F-NEAR','测试机构','2','pending_final',3),
		         ('filing','F-UPCOMING','测试机构','2','pending_initial',2),
		         ('filing','F-RETURNED','测试机构','2','returned',3);
		INSERT INTO business_cases(kind,number,assessment_org,filing_id,risk,status,version)
		  SELECT 'report','R-UPCOMING','测试机构',id,'low','pending_final',3
		  FROM business_cases WHERE number='F-UPCOMING';
		INSERT INTO case_review_events(case_id,action,from_status,to_status,operator,version,occurred_at)
		  SELECT c.id,e.action,e.before,e.after,'审查测试',e.version,
		    ((timezone('Asia/Shanghai',CURRENT_TIMESTAMP)::date + e.day_offset)+TIME '23:30')
		      AT TIME ZONE 'Asia/Shanghai'
		  FROM (VALUES
		    ('F-OVER','submit','draft','pending_initial',2,-4),
		    ('F-RESUBMIT','submit','draft','pending_initial',2,-20),
		    ('F-RESUBMIT','return_for_correction','pending_initial','returned',3,-10),
		    ('F-RESUBMIT','submit','returned','pending_initial',4,-3),
		    ('F-NEAR','approve','pending_initial','pending_final',3,7),
		    ('F-UPCOMING','submit','draft','pending_initial',2,8),
		    ('F-RETURNED','submit','draft','pending_initial',2,-8),
		    ('R-UPCOMING','approve','pending_initial','pending_final',3,25)
		  ) e(number,action,before,after,version,day_offset) JOIN business_cases c ON c.number=e.number;
	`)
	if err != nil {
		t.Fatal(err)
	}
}

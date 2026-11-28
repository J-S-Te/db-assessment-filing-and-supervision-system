package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
	"grade-business/internal/alerts"
	"grade-business/internal/app"
	"grade-business/internal/archives"
	"grade-business/internal/assessmentdue"
	"grade-business/internal/audittrail"
	"grade-business/internal/cases"
	"grade-business/internal/compliance"
	"grade-business/internal/dashboard"
	"grade-business/internal/directory"
	"grade-business/internal/filingaudit"
	"grade-business/internal/hazards"
	"grade-business/internal/httpx"
	"grade-business/internal/implementations"
	"grade-business/internal/knowledge"
	"grade-business/internal/materialexpiry"
	"grade-business/internal/precheck"
	"grade-business/internal/publications"
	"grade-business/internal/remediations"
	"grade-business/internal/reportextract"
	"grade-business/internal/reporthistory"
	"grade-business/internal/reviewinbox"
	"grade-business/internal/supervision"
	"grade-business/internal/tagcatalog"
	"grade-business/internal/workflowreminders"
	"grade-business/migrations"
)

type settings struct {
	address, dsn, assets, attachments, operator string
	tlsCert, tlsKey                             string
	reviewWindows                               app.ReviewWindows
	lan                                         netip.Prefix
	filingAI                                    *filingaudit.DeepSeek
	accessOptions                               access.Options
}

// main 启动管理端业务服务，可使用本机回环或经配置的服务器内网地址。
// 参数 无，配置通过业务版环境变量传入。
// 返回值 无，启动或运行失败时退出非零状态。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg, err := loadSettings()
	if err == nil {
		err = serve(ctx, cfg)
	}
	if err != nil {
		slog.Error("业务管理端启动失败", "reason", err)
		os.Exit(1)
	}
}

// loadSettings 读取部署所需的应用、数据库、材料和内网配置。
// 参数 无。
// 返回值 已校验的配置或缺少必要地址的提示。
func loadSettings() (settings, error) {
	cfg := settings{address: env("GRADE_BUSINESS_ADDR", "127.0.0.1:18444"), dsn: os.Getenv("GRADE_BUSINESS_DSN"),
		assets: env("GRADE_BUSINESS_WEB_DIR", "./web/dist"), attachments: env("GRADE_BUSINESS_ATTACHMENT_DIR", "./data/attachments"),
		operator: env("GRADE_BUSINESS_OPERATOR", "本地业务测试")}
	host, port, err := net.SplitHostPort(cfg.address)
	number, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || number < 1 || number > 65535 ||
		!net.ParseIP(host).IsLoopback() {
		return settings{}, errors.New("请为 GRADE_BUSINESS_ADDR 配置回环地址与有效端口")
	}
	if cidr := strings.TrimSpace(os.Getenv("GRADE_BUSINESS_LAN_CIDR")); cidr != "" {
		prefix, parseErr := netip.ParsePrefix(cidr)
		if parseErr != nil || !privateIPv4Subnet(prefix) {
			return settings{}, errors.New("内网访问需要服务器私有 IPv4 地址及其网段前缀")
		}
		cfg.address = net.JoinHostPort(prefix.Addr().String(), port)
		cfg.lan = prefix.Masked()
	}
	if strings.TrimSpace(cfg.dsn) == "" {
		return settings{}, errors.New("请配置 GRADE_BUSINESS_DSN")
	}
	cfg.accessOptions.SecureCookies, err = strconv.ParseBool(env("GRADE_BUSINESS_COOKIE_SECURE", "false"))
	if err != nil {
		return settings{}, errors.New("GRADE_BUSINESS_COOKIE_SECURE 必须是布尔值")
	}
	cfg.tlsCert = strings.TrimSpace(os.Getenv("GRADE_BUSINESS_TLS_CERT"))
	cfg.tlsKey = strings.TrimSpace(os.Getenv("GRADE_BUSINESS_TLS_KEY"))
	if (cfg.tlsCert == "") != (cfg.tlsKey == "") {
		return settings{}, errors.New("业务 HTTPS 需同时配置 GRADE_BUSINESS_TLS_CERT 与 GRADE_BUSINESS_TLS_KEY")
	}
	if proxy := strings.TrimSpace(os.Getenv("GRADE_BUSINESS_AUTH_TRUSTED_PROXY")); proxy != "" {
		addr, parseErr := netip.ParseAddr(proxy)
		if parseErr != nil || addr.Zone() != "" || addr.IsUnspecified() {
			return settings{}, errors.New("GRADE_BUSINESS_AUTH_TRUSTED_PROXY 必须是单一反代 IP 地址")
		}
		cfg.accessOptions.TrustedProxy = addr.Unmap()
	}
	idleMinutes, idleErr := strconv.Atoi(env("GRADE_BUSINESS_SESSION_IDLE_MINUTES", "30"))
	if idleErr != nil || idleMinutes < 1 || idleMinutes > 720 {
		return settings{}, errors.New("GRADE_BUSINESS_SESSION_IDLE_MINUTES 需为 1 至 720 之间的整数")
	}
	cfg.accessOptions.IdleTimeout = time.Duration(idleMinutes) * time.Minute
	cfg.reviewWindows, err = loadReviewWindows()
	if err != nil {
		return settings{}, err
	}
	cfg.filingAI, err = filingaudit.LoadDeepSeek(os.Getenv("GRADE_BUSINESS_FILING_AI_CONFIG"),
		os.Getenv("DEEPSEEK_API_KEY"))
	return cfg, err
}

// privateIPv4Subnet 校验部署网段完全包含在 IPv4 私网地址段中。
// 参数 network 为含服务器地址及子网长度的前缀。
// 返回值 前缀完全位于 RFC 1918 私网时为真。
func privateIPv4Subnet(network netip.Prefix) bool {
	for _, cidr := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"} {
		private := netip.MustParsePrefix(cidr)
		if network.Bits() >= private.Bits() && private.Contains(network.Addr()) {
			return true
		}
	}
	return false
}

// loadReviewWindows 读取各阶段可配置的工作日数及本地测试缺省值。
// 参数 无，从应用启动配置读取。
// 返回值 已校验的审核工作日数，或仅包含无效配置项名称的错误。
func loadReviewWindows() (app.ReviewWindows, error) {
	var windows app.ReviewWindows
	for _, field := range []struct {
		name, fallback string
		destination    *int
	}{
		{"GRADE_BUSINESS_FILING_REVIEW_DAYS", "3", &windows.FilingDays},
		{"GRADE_BUSINESS_FILING_FINAL_REVIEW_DAYS", "5", &windows.FilingFinalDays},
		{"GRADE_BUSINESS_REPORT_REVIEW_DAYS", "5", &windows.ReportDays},
		{"GRADE_BUSINESS_IMPLEMENTATION_REVIEW_DAYS", "3", &windows.ImplementationDays},
		{"GRADE_BUSINESS_REMEDIATION_REVIEW_DAYS", "3", &windows.RemediationDays},
	} {
		value, err := strconv.Atoi(env(field.name, field.fallback))
		if err != nil || value < 0 || value > 365 {
			return app.ReviewWindows{}, fmt.Errorf("%s 需为 0 至 365 之间的整数", field.name)
		}
		*field.destination = value
	}
	return windows, nil
}

// env 获取部署配置或测试环境默认值。
// 参数 key 为环境变量名，fallback 为缺省值。
// 返回值 当前采用的配置值。
func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

// serve 迁移业务库，挂载业务接口并启动 HTTP 服务。
// 参数 ctx 控制应用生命周期，cfg 为本地业务配置。
// 返回值 数据库、存储、构建产物或监听错误。
func serve(ctx context.Context, cfg settings) error {
	pool, err := connect(ctx, cfg.dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	migrateCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	err = migrations.Apply(migrateCtx, pool)
	cancel()
	if err != nil {
		slog.Error("业务库迁移发生错误", "error_type", fmt.Sprintf("%T", err))
		return errors.New("业务库迁移失败，请核对迁移配置")
	}
	records, err := cases.New(pool, cfg.attachments, cfg.operator)
	if err != nil {
		return errors.New("业务材料存储初始化失败")
	}
	pages, err := app.Frontend(os.DirFS(cfg.assets))
	if err != nil {
		return errors.New("请先构建前端产物，确认 GRADE_BUSINESS_WEB_DIR")
	}
	checks := precheck.New(pool, materialSource(records), materialContent(records, cfg.filingAI.UsesImages()))
	if cfg.filingAI != nil {
		checks.WithAssessor(cfg.filingAI)
	}
	handler := routes(cfg, pool, records, checks, pages)
	worker, cancelWorker := context.WithCancel(ctx)
	stopped := make(chan struct{})
	alertsStopped := make(chan struct{})
	expiriesStopped := make(chan struct{})
	assessmentDueStopped := make(chan struct{})
	remindersStopped := make(chan struct{})
	go func() { defer close(stopped); checks.Run(worker) }()
	localAlerts := alerts.NewWithRuleWindows(pool, "本地预警任务", alerts.RuleWindows{
		InitialDays: cfg.reviewWindows.FilingDays, FinalDays: cfg.reviewWindows.FilingFinalDays,
	})
	go func() { defer close(alertsStopped); localAlerts.Run(worker) }()
	expiries := materialexpiry.New(pool, records, cfg.operator)
	go func() { defer close(expiriesStopped); expiries.Run(worker) }()
	assessmentDues := assessmentdue.New(pool, cfg.operator)
	go func() { defer close(assessmentDueStopped); assessmentDues.Run(worker) }()
	go func() {
		defer close(remindersStopped)
		windows := cfg.reviewWindows
		workflowreminders.Run(worker, pool, workflowreminders.Windows{
			FilingInitial: windows.FilingDays, FilingFinal: windows.FilingFinalDays,
			Report: windows.ReportDays, Implementation: windows.ImplementationDays,
			Remediation: windows.RemediationDays,
		})
	}()
	defer func() {
		cancelWorker()
		<-stopped
		<-alertsStopped
		<-expiriesStopped
		<-assessmentDueStopped
		<-remindersStopped
	}()
	return listen(ctx, cfg, handler)
}

// connect 建立本地业务数据库连接池并检查连通性。
// 参数 ctx 控制连接期限，dsn 为单独配置的业务库地址。
// 返回值 正常的数据库连接池或不含凭据的连接错误。
func connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("业务库地址配置无效")
	}
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.MaxConns = 12
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("业务库连接池初始化失败")
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(probe); err != nil {
		pool.Close()
		return nil, errors.New("无法连接业务库")
	}
	return pool, nil
}

// materialSource 将备案持久化材料映射成最小预审输入。
// 参数 records 为处理备案及附件的业务服务。
// 返回值 按备案标识读取版本和所需证据的函数。
func materialSource(records *cases.Service) precheck.Source {
	return func(ctx context.Context, id string) (precheck.Snapshot, error) {
		snapshot, err := records.FilingMaterials(ctx, id)
		if errors.Is(err, cases.ErrNotFound) {
			return precheck.Snapshot{}, precheck.ErrNotFound
		}
		if err != nil {
			return precheck.Snapshot{}, err
		}
		materials := make([]precheck.Material, 0, len(snapshot.Attachments))
		for _, file := range snapshot.Attachments {
			materials = append(materials, precheck.Material{ID: file.ID, Kind: file.Kind, FileName: file.FileName})
		}
		return precheck.Snapshot{Revision: snapshot.Revision, Materials: materials}, nil
	}
}

// materialContent 组装备案例后台预审使用的文字及可选原件图像提取。
// 参数 records、includeImages 为材料服务及是否提供图像。
// 返回值 只读取预审快照原件的内容来源。
func materialContent(records *cases.Service, includeImages bool) precheck.ContentSource {
	return func(ctx context.Context, id string, materials []precheck.Material) ([]filingaudit.Document, filingaudit.Unit, error) {
		imageCount, imageBytes := 0, 0
		return records.FilingAuditContent(ctx, id, materials, func(ctx context.Context,
			name string, source io.ReaderAt, size int64,
		) ([]filingaudit.Line, []filingaudit.Image, error) {
			lines, err := filingaudit.Parse(ctx, name, source, size, reportextract.FilingPDFText)
			if err != nil || !includeImages || imageCount >= 10 || imageBytes >= 8<<20 {
				return lines, nil, err
			}
			var selected []filingaudit.Image
			for _, image := range filingVisuals(ctx, name, source, size) {
				if imageCount < 10 && imageBytes+len(image.Data) <= 8<<20 {
					selected = append(selected, image)
					imageCount++
					imageBytes += len(image.Data)
				}
			}
			return lines, selected, err
		})
	}
}

// filingVisuals 从支持的原件提取可定位的预审图像。
// 参数 ctx、name、source、size 为期限、文件名、实际附件和已知大小。
// 返回值 Word 内嵌图像或 PDF 页面。
func filingVisuals(ctx context.Context, name string, source io.ReaderAt, size int64) []filingaudit.Image {
	switch {
	case strings.HasSuffix(strings.ToLower(name), ".docx"):
		return filingaudit.WordImages(source, size)
	case strings.HasSuffix(strings.ToLower(name), ".pdf"):
		return reportextract.FilingPDFImages(ctx, io.NewSectionReader(source, 0, size))
	default:
		return nil
	}
}

// routes 装配同源页面、业务路由和就绪检查。
// 参数 cfg 为本地服务和审核期限配置，pool 为业务库，records 和 checks 为业务服务，pages 为前端资源。
// 返回值 应用了本机访问边界的完整 HTTP 处理器。
func routes(cfg settings, pool *pgxpool.Pool, records *cases.Service,
	checks *precheck.Service, pages http.Handler,
) http.Handler {
	mux := http.NewServeMux()
	records.Register(mux)
	records.RegisterOperatorFilingRoutes(mux)
	records.RegisterOperatorApplicationRoutes(mux)
	implementation := implementations.New(pool, cfg.operator)
	implementation.Register(mux)
	knowledge.New(pool).Register(mux)
	checks.Register(mux)
	plans := remediations.New(pool, records, cfg.operator)
	plans.Register(mux)
	plans.RegisterOperator(mux)
	ledger := hazards.New(pool, records, cfg.operator)
	ledger.Register(mux)
	ledger.RegisterWorkbench(mux)
	ledger.RegisterOperator(mux)
	ledger.RegisterInstitution(mux)
	reportextract.New(pool, records, cfg.operator).Register(mux)
	reporthistory.NewWithAttachments(pool, records, cfg.operator).Register(mux)
	compliance.New(pool, cfg.operator).Register(mux)
	archives.Register(mux, pool)
	directory.Register(mux, pool)
	alertService := alerts.NewWithRuleWindows(pool, cfg.operator, alerts.RuleWindows{
		InitialDays: cfg.reviewWindows.FilingDays, FinalDays: cfg.reviewWindows.FilingFinalDays,
	})
	alertService.Register(mux)
	alertService.RegisterOperator(mux)
	alertService.RegisterInstitution(mux)
	materialexpiry.New(pool, records, cfg.operator).Register(mux)
	assessmentdue.New(pool, cfg.operator).Register(mux)
	publications.NewWithAttachments(pool, cfg.operator, records).Register(mux)
	supervision.New(pool, records, cfg.operator).Register(mux)
	dashboard.Register(mux, pool, workflowreminders.Windows{
		FilingInitial: cfg.reviewWindows.FilingDays, FilingFinal: cfg.reviewWindows.FilingFinalDays,
		Report: cfg.reviewWindows.ReportDays, Implementation: cfg.reviewWindows.ImplementationDays,
		Remediation: cfg.reviewWindows.RemediationDays,
	})
	audittrail.New(pool).Register(mux)
	tagcatalog.Register(mux, pool)
	reviewinbox.Register(mux, pool, receiptDistrictScope)
	workflowreminders.Register(mux, pool)
	app.RegisterWorkbenchOverview(mux, pool, cfg.reviewWindows)
	app.RegisterWorkdayCalendar(mux, pool, cfg.operator)
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) { ready(w, r, pool) })
	app.RegisterAIReview(mux, os.DirFS(filepath.Join(cfg.assets, "review")))
	mux.Handle("/", pages)
	return app.LocalBoundary(cfg.address, cfg.lan, access.New(pool, cfg.accessOptions).Protect(mux))
}

// receiptDistrictScope 将审核回执绑定在当前监管员的可信辖区内。
// 参数 ctx 为请求身份，filing 为受控备案查询，param 为授权区县数组参数。
// 返回值 可用的 SQL 条件和授权辖区，非监管席位返回 nil 范围。
func receiptDistrictScope(ctx context.Context, filing, param string) (string, []string) {
	districts := access.Districts(ctx)
	if districts == nil || filing == "" {
		return "", districts
	}
	return access.FilingDistrictScope(filing, param, false), districts
}

// ready 提供仅含业务服务就绪状态的探针。
// 参数 w 为响应对象，r 为探活请求，pool 为业务库。
// 返回值 无。
func ready(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		httpx.Error(w, 503, "not_ready", "业务库暂不可用")
		return
	}
	httpx.JSON(w, 200, struct {
		Status string `json:"status"`
	}{"ready"})
}

// listen 在指定的本机地址提供业务 HTTP 并等待平滑停止。
// 配置了 GRADE_BUSINESS_TLS_CERT 与 GRADE_BUSINESS_TLS_KEY 时直接以 HTTPS 提供，会话 Cookie 自动携带 Secure。
// 参数 ctx 控制运行，cfg 为含监听地址与可选证书的业务配置，handler 为业务应用入口。
// 返回值 监听、服务或关停错误。
func listen(ctx context.Context, cfg settings, handler http.Handler) error {
	socket, err := net.Listen("tcp", cfg.address)
	if err != nil {
		return errors.New("本机业务端口无法使用，请选择空闲端口")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 2 * time.Minute,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() {
		if cfg.tlsCert != "" {
			done <- server.ServeTLS(socket, cfg.tlsCert, cfg.tlsKey)
			return
		}
		done <- server.Serve(socket)
	}()
	scheme := "http"
	if cfg.tlsCert != "" {
		scheme = "https"
	}
	slog.Info("本地业务管理端可访问", "address", scheme+"://"+cfg.address)
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		return server.Shutdown(shut)
	}
}

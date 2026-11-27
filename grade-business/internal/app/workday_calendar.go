package app

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"grade-business/internal/httpx"
)

const calendarAPI = "/api/v1/workday-calendar"
const calendarPageSize = 20

var errCalendarVersion = errors.New("calendar version or request changed")
var errCalendarNoChange = errors.New("calendar setting already effective")

type calendarService struct {
	db       *pgxpool.Pool
	operator string
}

type calendarDay struct {
	Day       string `json:"day"`
	IsWorkday bool   `json:"isWorkday"`
	Override  bool   `json:"override"`
	Version   int64  `json:"version"`
}

type calendarFlags struct {
	IsWorkday bool `json:"isWorkday"`
	Override  bool `json:"override"`
}

type calendarEvent struct {
	calendarDay
	Mode      string        `json:"mode"`
	Reason    string        `json:"reason"`
	Operator  string        `json:"operator"`
	ChangedAt time.Time     `json:"changedAt"`
	Previous  calendarFlags `json:"previous"`
}

type calendarUpdate struct {
	RequestID       string `json:"requestId"`
	ExpectedVersion *int64 `json:"expectedVersion"`
	Mode            string `json:"mode"`
	Reason          string `json:"reason"`
}

// RegisterWorkdayCalendar 挂载本地监管工作日历的读取、设置和留痕接口。
// 参数 mux 和 db 为路由、业务库，operator 是本机监管操作标记。
// 返回值 无。
func RegisterWorkdayCalendar(mux *http.ServeMux, db *pgxpool.Pool, operator string) {
	s := calendarService{db, operator}
	mux.HandleFunc("GET "+calendarAPI, s.monthHTTP)
	mux.HandleFunc("PATCH "+calendarAPI+"/{day}", s.updateHTTP)
	mux.HandleFunc("GET "+calendarAPI+"/{day}/history", s.historyHTTP)
}

// monthHTTP 返回所选月份的实际工作日和已有覆盖版本。
// 参数 w、r 为响应和仅包含月份的查询。
// 返回值 无。
func (s calendarService) monthHTTP(w http.ResponseWriter, r *http.Request) {
	month, valid := parseCalendarMonth(r.URL.RawQuery)
	if !valid {
		calendarInvalid(w)
		return
	}
	items, err := s.readMonth(r.Context(), month)
	if err != nil {
		calendarFailure(w, err)
		return
	}
	httpx.JSON(w, 200, struct {
		Month string        `json:"month"`
		Items []calendarDay `json:"items"`
	}{month, items})
}

// updateHTTP 核对写入字段并按页面已读版本保存覆盖和对应留痕。
// 参数 w、r 为响应与自然日、请求号和版本。
// 返回值 无。
func (s calendarService) updateHTTP(w http.ResponseWriter, r *http.Request) {
	rawDay := r.PathValue("day")
	day, err := time.Parse(time.DateOnly, rawDay)
	var input calendarUpdate
	if err != nil || day.Year() < 1 || r.URL.RawQuery != "" ||
		httpx.Decode(w, r, &input) != nil || !normalizeCalendarUpdate(&input) {
		calendarInvalid(w)
		return
	}
	event, err := s.save(r.Context(), day, input)
	if err != nil {
		calendarFailure(w, err)
		return
	}
	httpx.JSON(w, 200, event)
}

// historyHTTP 读取所选自然日的一页变更审计。
// 参数 w、r 为响应及日期路径、续页版本。
// 返回值 无。
func (s calendarService) historyHTTP(w http.ResponseWriter, r *http.Request) {
	rawDay := r.PathValue("day")
	day, err := time.Parse(time.DateOnly, rawDay)
	before, valid := parseCalendarVersion(r.URL.RawQuery)
	if err != nil || day.Year() < 1 || !valid {
		calendarInvalid(w)
		return
	}
	items, err := s.readHistory(r.Context(), rawDay, before)
	if err != nil {
		calendarFailure(w, err)
		return
	}
	var next string
	if len(items) > calendarPageSize {
		items = items[:calendarPageSize]
		next = strconv.FormatInt(items[len(items)-1].Version, 10)
	}
	httpx.JSON(w, 200, struct {
		Items      []calendarEvent `json:"items"`
		NextBefore string          `json:"nextBefore,omitempty"`
	}{items, next})
}

// calendarQuery 接受单个已知查询字段，并区分未指定和显式空值。
// 参数 raw 为原始查询，key 为当前端点接受的唯一参数。
// 返回值 查询值和合法标志。
func calendarQuery(raw, key string) (string, bool) {
	if raw == "" {
		return "", true
	}
	values, err := url.ParseQuery(raw)
	if err != nil || len(values) != 1 || len(values[key]) != 1 || values.Get(key) == "" {
		return "", false
	}
	return values.Get(key), true
}

// parseCalendarMonth 读取有效自然月，缺省使用中国时区的当前月份。
// 参数 raw 为月查询。
// 返回值 标准年月和合法标志。
func parseCalendarMonth(raw string) (string, bool) {
	value, valid := calendarQuery(raw, "month")
	if valid && value == "" {
		value = time.Now().In(time.FixedZone("UTC+8", 8*60*60)).Format("2006-01")
	}
	parsed, err := time.Parse("2006-01", value)
	return value, valid && err == nil && parsed.Year() > 0
}

// parseCalendarVersion 解析变更历史下一页的独占版本边界。
// 参数 raw 为历史查询。
// 返回值 最早已显示版本，零表示第一页，及合法标志。
func parseCalendarVersion(raw string) (int64, bool) {
	value, valid := calendarQuery(raw, "before")
	if value == "" {
		return 0, valid
	}
	before, err := strconv.ParseInt(value, 10, 64)
	return before, valid && err == nil && before > 0
}

// normalizeCalendarUpdate 校验请求号、所见版本、日期模式和可审计理由。
// 参数 input 为解码后的更新请求。
// 返回值 字段合法时返回真，并标准化请求号及理由。
func normalizeCalendarUpdate(input *calendarUpdate) bool {
	var id pgtype.UUID
	input.RequestID = strings.ToLower(input.RequestID)
	input.Reason = strings.TrimSpace(input.Reason)
	modeAllowed := input.Mode == "workday" || input.Mode == "restday" || input.Mode == "default"
	return len(input.RequestID) == 36 && id.Scan(input.RequestID) == nil && id.Valid &&
		id.String() == input.RequestID &&
		input.ExpectedVersion != nil && *input.ExpectedVersion >= 0 && *input.ExpectedVersion < math.MaxInt64 &&
		modeAllowed && utf8.RuneCountInString(input.Reason) >= 1 &&
		utf8.RuneCountInString(input.Reason) <= 300 && !strings.ContainsFunc(input.Reason, unicode.IsControl)
}

// calendarInvalid 写出通用输入错误并隐藏解析细节。
// 参数 w 为 HTTP 响应。
// 返回值 无。
func calendarInvalid(w http.ResponseWriter) {
	httpx.Error(w, 400, "invalid_input", "请核对日期、查询条件、变更类型和操作理由。")
}

// calendarFailure 将日历的可处理冲突和数据库错误转为安全响应。
// 参数 w 为 HTTP 响应，err 为操作或读取错误。
// 返回值 无。
func calendarFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errCalendarVersion):
		httpx.Error(w, 409, "calendar_conflict", "日历版本或请求内容已变化，请刷新后重新核对。")
	case errors.Is(err, errCalendarNoChange):
		httpx.Error(w, 409, "calendar_unchanged", "该日期已使用所选设置。")
	default:
		slog.Error("监管工作日历操作失败", "error_type", fmt.Sprintf("%T", err))
		httpx.Error(w, 500, "internal_error", "暂时无法处理工作日历，请稍后重试。")
	}
}

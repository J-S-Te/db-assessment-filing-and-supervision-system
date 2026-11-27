package app

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const effectiveCalendarSQL = `
	SELECT to_char(w.day,'YYYY-MM-DD'),COALESCE(d.is_workday,EXTRACT(ISODOW FROM w.day) BETWEEN 1 AND 5),
	    d.day IS NOT NULL,COALESCE(event.version,0)
	FROM selected_days w LEFT JOIN workflow_calendar_days d USING(day)
	LEFT JOIN LATERAL (SELECT version FROM workflow_calendar_events
	    WHERE day=w.day ORDER BY version DESC LIMIT 1) event ON true
`

const calendarMonthSQL = `
	WITH selected_days AS (
	    SELECT $1::date + n AS day
	    FROM generate_series(0,($1::date+INTERVAL '1 month')::date-$1::date-1) n
	)
` + effectiveCalendarSQL + " ORDER BY w.day"

const calendarEventValues = `
	to_char(day,'YYYY-MM-DD'),is_workday,is_override,version,mode,reason,operator,changed_at,
	before_is_workday,before_override
`

// readMonth 读取当前工作日规则计算出的整月自然日和版本。
// 参数 ctx 控制查询，month 是已校验的北京时间自然月。
// 返回值 从首日到末日排列的日期列表或查询错误。
func (s calendarService) readMonth(ctx context.Context, month string) ([]calendarDay, error) {
	rows, err := s.db.Query(ctx, calendarMonthSQL, month+"-01")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[calendarDay])
}

// readHistory 读取日期的稳定版本序列，额外读取一条判断剩余记录。
// 参数 ctx 为查询上下文，day 和 before 为自然日和续页位置。
// 返回值 变更留痕或读取错误。
func (s calendarService) readHistory(ctx context.Context, day string, before int64) ([]calendarEvent, error) {
	rows, err := s.db.Query(ctx, "SELECT "+calendarEventValues+`
		FROM workflow_calendar_events WHERE day=$1 AND ($2::bigint=0 OR version<$2)
		ORDER BY version DESC LIMIT $3`, day, before, calendarPageSize+1)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanCalendarEvent)
}

// scanCalendarEvent 从 PostgreSQL 读取审计投影中的前后状态和理由。
// 参数 row 是由已知字段声明形成的单行读取。
// 返回值 精简审计信息或扫描错误。
func scanCalendarEvent(row pgx.CollectableRow) (calendarEvent, error) {
	var e calendarEvent
	err := row.Scan(&e.Day, &e.IsWorkday, &e.Override, &e.Version, &e.Mode, &e.Reason, &e.Operator,
		&e.ChangedAt, &e.Previous.IsWorkday, &e.Previous.Override)
	return e, err
}

// save 序列化本 schema 内的日历写入，同事务更新覆盖与审计。
// 参数 ctx 控制写入，day 和 input 为日期及经校验的确认请求。
// 返回值 实际写入事件、历史回执重放或版本冲突。
func (s calendarService) save(ctx context.Context, day time.Time, input calendarUpdate) (calendarEvent, error) {
	var event calendarEvent
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext(current_schema()), 415378)"); err != nil {
			return err
		}
		currentDay := day.Format(time.DateOnly)
		replay, err := readCalendarReplay(ctx, tx, currentDay, input, &event)
		if err != nil || replay {
			return err
		}
		var previous calendarDay
		err = tx.QueryRow(ctx, "WITH selected_days AS (SELECT $1::date AS day) "+effectiveCalendarSQL,
			currentDay).Scan(&previous.Day, &previous.IsWorkday, &previous.Override, &previous.Version)
		if err != nil {
			return err
		}
		if previous.Version != *input.ExpectedVersion {
			return errCalendarVersion
		}
		flags := desiredCalendarFlags(day, input.Mode)
		if flags == (calendarFlags{previous.IsWorkday, previous.Override}) {
			return errCalendarNoChange
		}
		event, err = applyCalendarUpdate(ctx, tx, previous, flags, input, s.operator)
		return err
	})
	return event, err
}

// desiredCalendarFlags 依据工作日覆盖选项计算落库状态或默认周内状态。
// 参数 day 为选中自然日，mode 为已校验覆盖模式。
// 返回值 写入后生效的标签。
func desiredCalendarFlags(day time.Time, mode string) calendarFlags {
	if mode == "default" {
		return calendarFlags{IsWorkday: day.Weekday() >= time.Monday && day.Weekday() <= time.Friday}
	}
	return calendarFlags{IsWorkday: mode == "workday", Override: true}
}

// readCalendarReplay 仅允许请求号完全对应最初日历操作时重放该回执。
// 参数 ctx、tx 为写环境，day、input 定位操作，event 用于输出原回执。
// 返回值 是否找到原请求及匹配或数据库错误。
func readCalendarReplay(ctx context.Context, tx pgx.Tx, day string, input calendarUpdate,
	event *calendarEvent,
) (bool, error) {
	match, err := scanCalendarSingle(ctx, tx, "SELECT "+calendarEventValues+
		" FROM workflow_calendar_events WHERE request_id=$1", input.RequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if match.Day != day || match.Version != *input.ExpectedVersion+1 ||
		match.Mode != input.Mode || match.Reason != input.Reason {
		return false, errCalendarVersion
	}
	*event = match
	return true, nil
}

// applyCalendarUpdate 按模式覆盖或恢复自然日，并写入同一审计事务。
// 参数 ctx、tx 为事务，before、flags、input、operator 提供变更快照。
// 返回值 写入后回执或数据库错误。
func applyCalendarUpdate(ctx context.Context, tx pgx.Tx, before calendarDay, flags calendarFlags,
	input calendarUpdate, operator string,
) (calendarEvent, error) {
	sql := `DELETE FROM workflow_calendar_days WHERE day=$1`
	params := []any{before.Day}
	if flags.Override {
		sql = `INSERT INTO workflow_calendar_days(day,is_workday) VALUES($1,$2)
		    ON CONFLICT(day) DO UPDATE SET is_workday=excluded.is_workday`
		params = append(params, flags.IsWorkday)
	}
	if _, err := tx.Exec(ctx, sql, params...); err != nil {
		return calendarEvent{}, err
	}
	return scanCalendarSingle(ctx, tx, `INSERT INTO workflow_calendar_events
	    (request_id,day,version,mode,reason,operator,before_is_workday,before_override,is_workday,is_override)
	    VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+calendarEventValues,
		input.RequestID, before.Day, before.Version+1, input.Mode, input.Reason, operator,
		before.IsWorkday, before.Override, flags.IsWorkday, flags.Override)
}

// scanCalendarSingle 复用审计投影并要求数据库最多返回单条操作。
// 参数 ctx、tx 为事务，sql 和 params 为固定查询及数据参数。
// 返回值 唯一审计记录或查询错误。
func scanCalendarSingle(ctx context.Context, tx pgx.Tx, sql string, params ...any) (calendarEvent, error) {
	rows, err := tx.Query(ctx, sql, params...)
	if err != nil {
		return calendarEvent{}, err
	}
	return pgx.CollectExactlyOneRow(rows, scanCalendarEvent)
}

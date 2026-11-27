package cases

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Service 持有备案、测评和附件的持久化条件。
type Service struct {
	db            *pgxpool.Pool
	attachmentDir string
	operator      string
}

// New 创建备案和测评业务服务。
// 参数 pool 为业务数据库连接池，attachmentDir 为附件目录，operator 为本地留痕操作人。
// 返回值 可注册 HTTP 路由的服务，或者无效配置及目录初始化错误。
func New(pool *pgxpool.Pool, attachmentDir, operator string) (*Service, error) {
	operator = strings.TrimSpace(operator)
	if pool == nil || strings.TrimSpace(attachmentDir) == "" || !validText(operator, 100) {
		return nil, errors.New("business cases require a database, attachment directory and operator")
	}
	dir, err := filepath.Abs(attachmentDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return &Service{db: pool, attachmentDir: dir, operator: operator}, nil
}

// validID 验证请求中的记录或材料标识。
// 参数 id 为客户端或数据库提供的 UUID。
// 返回值 UUID 格式有效时为真。
func validID(id string) bool {
	if len(id) != 36 {
		return false
	}
	var parsed pgtype.UUID
	return parsed.Scan(id) == nil && parsed.Valid
}

// validText 判断用于展示的业务文字是否非空并满足约定长度。
// 参数 value 为已去除首尾空白的文字，limit 为最大 Unicode 字符数。
// 返回值 文字长度和编码均有效时为真。
func validText(value string, limit int) bool {
	return value != "" && utf8.ValidString(value) &&
		utf8.RuneCountInString(value) <= limit && !strings.ContainsRune(value, 0)
}

// constraintError 将已知数据库约束映射到稳定业务错误。
// 参数 err 为写入返回的错误。
// 返回值 编号重复、单位已归并错误或原始错误。
func constraintError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "business_cases_kind_number":
			return ErrDuplicate
		case "filing_unit_merged":
			return errHistoryMergedUnit
		}
	}
	return err
}

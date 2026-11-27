package hazards

import (
	"grade-business/internal/cases"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service 提供隐患台账、证据和销号办理能力。
type Service struct {
	db       *pgxpool.Pool
	records  *cases.Service
	operator string
}

// New 构造使用业务库和通用材料存储的隐患台账服务。
// 参数 db 为业务连接池，records 为材料存储服务，operator 为留痕操作人。
// 返回值 可挂载到管理端的服务。
func New(db *pgxpool.Pool, records *cases.Service, operator string) *Service {
	return &Service{db: db, records: records, operator: operator}
}

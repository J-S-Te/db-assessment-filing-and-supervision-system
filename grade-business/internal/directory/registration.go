package directory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrRegistrationUnitConflict 表示申请资料不能用于新建唯一的单位主档。
var ErrRegistrationUnitConflict = errors.New("注册单位与现有名录冲突")

// CreateRegistrationUnit 在审批事务中按现有名录规则登记单位及初始历史。
// 参数 ctx、tx 为审批上下文和事务，name、code、district 为已核验的单位名称、信用代码和辖区。
// 返回值 新单位标识、名录名称冲突或数据库错误。
func CreateRegistrationUnit(ctx context.Context, tx pgx.Tx, name, code, district string) (string, error) {
	item, err := prepareCreate(ctx, tx, unit, "", entry{
		Name: name, Code: code, District: district, Status: active,
	})
	if errors.Is(err, errDuplicate) || errors.Is(err, errInvalid) {
		return "", ErrRegistrationUnitConflict
	}
	if err != nil {
		return "", err
	}
	id, err := insertEntry(ctx, tx, unit, item)
	if err != nil {
		return "", err
	}
	item, err = readEntry(ctx, tx, unit, "", id)
	if err != nil {
		return "", err
	}
	if err := recordEvent(ctx, tx, unit, "", "create", nil, item); err != nil {
		return "", err
	}
	return id, nil
}

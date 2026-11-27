package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"grade-business/internal/access"
)

// main 由具备业务数据库操作授权的人员开通基础角色账号。
// 参数 CLI 标志指定角色与归属，标准输入接收临时密码。
// 返回值 无，创建失败时以非零状态退出。
func main() {
	id, err := provision()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(id)
}

// provision 校验参数并在已完成迁移的业务库登记首登账号。
// 参数 无，读取命令行、标准输入和应用 DSN。
// 返回值 账号标识或安全的错误提示。
func provision() (string, error) {
	var input access.AccountInput
	var role string
	flag.StringVar(&input.Username, "username", "", "登录账号")
	flag.StringVar(&input.Name, "name", "", "负责人或个人名称")
	flag.StringVar(&role, "role", "", "角色编码")
	flag.StringVar(&input.PersonKey, "person-key", "", "管理人员的唯一内部编号")
	flag.StringVar(&input.AreaCode, "area-code", "", "监管区划代码")
	flag.StringVar(&input.SubjectID, "subject-id", "", "已登记机构或单位 UUID")
	flag.Parse()
	dsn := strings.TrimSpace(os.Getenv("GRADE_BUSINESS_DSN"))
	if dsn == "" {
		return "", errors.New("请指定已完成权限迁移的 GRADE_BUSINESS_DSN")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 259))
	if err != nil || len(raw) > 258 {
		return "", errors.New("无法读取有效的临时密码")
	}
	input.Role = access.Role(role)
	input.Password = strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return "", errors.New("无法连接指定的业务数据库")
	}
	defer db.Close()
	return access.Provision(ctx, db, input)
}

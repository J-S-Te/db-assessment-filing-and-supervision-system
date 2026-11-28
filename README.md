# 网络安全等级保护备案与监管系统

面向监管部门、测评机构和网络运营单位，覆盖等保备案、实施报备、测评报告、整改复测和监督检查。系统按账号所属角色、监管辖区或业务主体提供对应的工作台和数据范围。

## 业务入口

| 工作台 | 入口 | 主要业务 |
| --- | --- | --- |
| 管理端 | `/filings` | 备案审核、测评报告监管与后续监督。系统管理员另有名录、全局规则和审计管理权限。 |
| 测评机构端 | `/institution/workbench` | 办理机构范围内的备案协作、实施报备、测评报告、补正和复测。 |
| 运营者端 | `/operator/workbench` | 办理本单位的备案、变更和注销，查看整改、监督检查和公告。 |

统一登录入口为 `/login`。材料审阅支持原文定位、AI 预审和人工审核。材料预审配置与独立 AI 智能审核入口分别见下方文档。

## 本地启动

需要 Go 1.26、Node.js 24、pnpm、Docker Compose 和 OpenSSL。pnpm 版本见 [前端包配置](grade-business/web/package.json)。以下命令从仓库根目录运行，创建一个本地测试库。

```sh
cd grade-business
umask 077
if [ ! -f deploy/.env ]; then
  printf 'GRADE_BUSINESS_DB_PASSWORD=%s\n' "$(openssl rand -hex 24)" > deploy/.env
fi

docker compose --env-file deploy/.env -f deploy/compose.yaml up -d --wait
pnpm --dir web install --frozen-lockfile
pnpm --dir web build

grade_business_db_password=$(sed -n 's/^GRADE_BUSINESS_DB_PASSWORD=//p' deploy/.env)
export GRADE_BUSINESS_DSN="postgres://grade_business:$grade_business_db_password@127.0.0.1:${GRADE_BUSINESS_DB_PORT:-15444}/grade_business_test?sslmode=disable"
go run ./cmd/manager
```

启动后访问 [本地登录页](http://127.0.0.1:18444/login)。应用启动时自动执行数据库迁移，可通过 `/readyz` 检查就绪状态。系统需要先开通账号，临时口令账号首次登录须改密。开户命令、PDF 和 OCR 工具、附件目录与 AI 预审设置见 [业务模块运行文档](grade-business/README.md)。

## 开发与测试

前端使用 Vue 3、TypeScript、Vite 和 Vue Router，后端使用 Go、`net/http`、pgx 和 PostgreSQL。运行下列命令前，先进入 `grade-business/` 并安装前端依赖。

```sh
go test ./...
pnpm --dir web test
pnpm --dir web typecheck
pnpm --dir web build
```

数据库集成测试使用独立测试库。相关环境变量和真实数据库测试命令见 [执行验收](grade-business/README.md#执行验收) 和 [GitHub Actions 配置](.github/workflows/ci-cd.yml)。

## 部署

- GitHub Actions 在 PR 上运行 Go 和 Vue 检查。`main` 更新后，流水线先验证构建，再发布镜像并部署到 GitHub `test` 环境。[CI/CD 配置](.github/workflows/ci-cd.yml)
- 内网 Ubuntu 由 Gitea Runner 发布，使用独立的业务数据库与附件目录。[内网发布说明](grade-business/README.md#gitea-发布到-ubuntu)

## 仓库与文档

| 位置 | 内容 |
| --- | --- |
| [`grade-business/cmd/`](grade-business/cmd/) | Go 服务入口和账号开通工具 |
| [`grade-business/internal/`](grade-business/internal/) | 业务服务、数据范围校验和授权 |
| [`grade-business/web/`](grade-business/web/) | 三端 Vue 前端和驾驶舱 |
| [`grade-business/migrations/`](grade-business/migrations/) | PostgreSQL 数据库迁移 |
| [`grade-business/deploy/`](grade-business/deploy/) | Docker Compose 与发布脚本 |
| [原型](原型/) · [定级材料模板](定级材料模版/) | 页面设计稿与定级备案材料 |

- [三端业务操作手册](grade-business/THREE_PORTAL_BUSINESS_MANUAL.md)
- [角色与权限清单](角色与权限清单.md)
- [OpenAPI 接口契约](grade-business/api/openapi.yaml)
- [备案材料 AI 预审配置](grade-business/README.md#备案详情的-deepseek-预审) · [独立 AI 智能审核接入](grade-business/AI_REVIEW_INTEGRATION.md)

# M8 AI 智能审核接入

本期把管理端 M8 从“待接入”改为同源 `/review/` 页面，复用现有 `grade_business_session`、首登改密、账号启停与监管辖区过滤，不使用 DBReportReviewer 的本地登录或用户表。原平台是身份及报告归属的唯一来源，没有账号复制、跨系统密码传递或客户端角色断言。

## 范围及运行

用户已明确授权：`ROLE_SYS_ADMIN` 和 `ROLE_REGULATOR` 可访问 M8；机构、运营者、未知角色不新增权限。`GET /review`、`GET /review/` 和固定静态资源全部经过 `access.Protect`；未登录返回原 `/login?redirect=/review/`，首登未改密返回原改密页。`GET /api/v1/ai-review/context` 同样经过该网关，返回服务器当前会话视图。伪造 X-User-ID、X-Role 不产生身份。页面只读，采用原 `/api/v1/reports`、详情和附件接口，每次读取仍由原平台检查范围；监管员不能从 M8 串读其他辖区。

独立审核源码位于同级 `DBReportReview-web` 的 `integrated.html`、`review-platform.mjs`；平台只承载构建产物。部署时不指向 `index.html` 合成原型，也不跳转 `portal.html` 的第二套本地账户。返回原平台办理继续使用已有审核权限和流程。页面当前提供真实报告列表、查询、游标分页、材料读取及返回原办理页；AI 审核执行和结果工件尚未接入，页面明确提示，不将办理状态当作 AI 结论。

在平台前端构建前生成发布包：

```sh
node ./DBReportReview-web/tools/package-platform-review.mjs \
  ./db-assessment-filing-and-supervision-system/grade-business/web/public/review
```

上述相对路径示例从三仓库共同父目录执行，或直接使用绝对路径。脚本只打包六个固定资源和摘要清单，拒绝覆盖非本工具生成的现有目录；生成的 `web/public/review/` 被 Git 忽略。随后按平台原有步骤构建 `web/dist`、启动 manager。Node、Vue、数据库与 Cookie 均沿用现有组件，不添加身份依赖或数据库迁移。只用静态 HTTP 服务不能模拟真实平台会话。

## 测试服发布

现有 GitHub Actions 的镜像 job 增加前端检出及打包步骤。平台仓库变量 `AI_REVIEW_WEB_REF` 必须是 `J-S-Te/DBReportReview-web` 中包含本次集成文件的完整 40 位提交 SHA。流水线拒绝空值或分支名，不使用漂移的 main 作为页面依赖。先提交前端，再固定该变量，最后提交平台代码触发原测试镜像发布；回滚使用原流水线的上一镜像。没有数据库迁移或线上用户授权批量修改。

本次已验证的前端提交为 `af46ab2ffcc5a7b79879f9b24ab7ef8355b68982`。用户已授权提交推送及通过既有流水线发布测试服；远端是否运行本版以对应 GitHub Actions 发布回执与在线入口为准，本机浏览器验收不等于远端已更新。

## 实际验证

- 隔离 PostgreSQL 的 M8/原角色 `-race` 定向验证共 8 个测试通过、无跳过：原平台账号登录、首登限制、四角色范围、匿名/伪造身份拒绝、退出及账号冻结、跨区报告/附件拒绝，以及静态白名单和缺少构建产物的 503。
- `go vet ./internal/access ./internal/app ./cmd/manager` 通过；Vue `typecheck`、构建及原前端 30 组回归通过。构建保留原有图表 chunk 大小提示。
- 审核前端 5 组路径、查询、响应及发布包测试通过；原权限模型 12 组回归按独立原型解释。
- 浏览器用本轮合成账号从原平台 M8 菜单进入，无二次登录；验证列表、查询空态、材料空态、返回原平台、退出后跳回原登录页、重新登录返回 M8 和 390×844 无文档横向溢出。

曾因本机热重建导致旧 index 引用已替换的动态模块，重启预览进程载入对应入口后复验通过；正式镜像统一打包入口与资源。关键词使用原接口 `q` 参数，客户端不发送辖区或单位过滤断言。

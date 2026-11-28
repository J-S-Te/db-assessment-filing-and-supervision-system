# 等保管理端业务版

业务版可在本机或内网使用，登录后进入账号所属的工作台。包含备案管理、实施报备、测评报告、整改规划、隐患台账和知识库。前端采用 Vue 3、TypeScript、Vite 和 Vue Router。Go 使用 `net/http`、pgx 和 OpenAPI，PostgreSQL 与应用独立运行。

新增通用功能优先复用现有框架、数据库或浏览器能力。地图路径与拓扑布局等专用算法采用开源组件，随对应页面或功能加载。归属、审核、材料校验及统计口径由业务模块维护。

[业务接口契约](api/openapi.yaml) · [管理端原型 V2](../原型/管理端/index.html) · [页面对照与核验](FRONTEND_V2_ALIGNMENT.md) · [业务流核对](BUSINESS_GAPS.md) · [测评意见回传](BUSINESS_FEEDBACK.md) · [功能清单](../软件开发功能清单（原型基线版）.md) · [整改闭环目标](BUSINESS_PHASE_3.md) · [运营者备案和换机构复测](BUSINESS_OPERATOR_FILING.md) · [运营者整改单和预警处置](BUSINESS_OPERATOR_RECTIFICATION.md) · [机构复测及初终审](BUSINESS_INSTITUTION_REVIEW.md) · [三端工作台与驾驶舱](BUSINESS_WORKBENCH.md) · [测评比对、运营者资料及资产指标](BUSINESS_REPORT_RESOURCES_ASSETS.md) · [本地验收记录](TASK_STATE.md)

M8 AI 智能审核的同源页面、原会话复用、已授权角色范围与固定前端发布依赖见 [接入说明](AI_REVIEW_INTEGRATION.md)。

## Gitea 发布到 Ubuntu

推送到 `main` 后，`ubuntu-vm` Runner 构建触发该次发布的提交。前端使用 `web/package.json` 声明的 pnpm 版本和锁文件，和 Go 二进制一起发布。Runner 需要 Git、Go、curl、iproute2、flock 和 systemd 操作权限。缺少 Node.js 24 或 Corepack 时，流水线下载并校验固定版本的 Node.js 构建包，解压到临时工作目录。首次部署使用 Docker Compose v2 和 OpenSSL 初始化数据库。

下载不稳定的 Runner 可将对应架构的 Node.js 官方归档放在 `/opt/grade-business/.ci-cache/node`。流水线使用与在线下载相同的 SHA-256 校验，校验通过后才复用缓存。

首次部署自动创建独立 PostgreSQL 的 `grade_business` 数据库和 `grade-business-release_business-data` 数据卷，默认数据库端口为本机 `15445`。应用凭据写入 `/opt/grade-business/bootstrap.env`，权限为 `0600`，重试沿用原文件及数据卷。已有 systemd 服务使用原数据库、附件目录和端口。只安装了服务文件、缺少数据库配置的实例进入首次初始化。应用启动时执行数据库迁移。

流水线优先使用 Ubuntu 到 Gitea 路由选择的私有 IPv4 和网段，首次默认应用端口为 `18444`。客户端从同一网段用发布日志中的地址访问。内网入口沿用同源限制。业务 API 需要账号会话，部署探活与前端静态资源可独立访问。单独执行本机开发命令时仍使用回环地址。

发布日志在新进程、`/readyz`、前端首页和入口脚本均通过检查后打印访问地址。检查失败时恢复原应用二进制和前端配置。数据库卷和附件目录在重试及应用回退期间持续保留。

## 本机运行

在 `grade-business/` 中执行。需要 Go 1.26、Node.js 24、pnpm 和 Docker Compose。数据库配置仅用于独立的业务测试库。

```sh
umask 077
if [ ! -f deploy/.env ]; then
  printf 'GRADE_BUSINESS_DB_PASSWORD=%s\n' "$(openssl rand -hex 24)" > deploy/.env
fi
docker compose --env-file deploy/.env -f deploy/compose.yaml up -d --wait
pnpm --dir web install --frozen-lockfile
grade_business_preview_dir=$(mktemp -d "${TMPDIR:-/tmp}/grade-business-web.XXXXXXXX")
pnpm --dir web exec vite build --outDir "$grade_business_preview_dir"
export GRADE_BUSINESS_WEB_DIR="$grade_business_preview_dir"
grade_business_password=$(sed -n 's/^GRADE_BUSINESS_DB_PASSWORD=//p' deploy/.env)
export GRADE_BUSINESS_DSN="postgres://grade_business:$grade_business_password@127.0.0.1:15444/grade_business_test?sslmode=disable"
go run ./cmd/manager
```

已有业务测试库时，保留首次使用的 `deploy/.env`，继续使用原来的口令。浏览器访问 `http://127.0.0.1:18444/`。服务启动时自动应用业务库迁移，材料默认写入项目的 `data/attachments`，数据库使用独立的 Compose 数据卷。可以通过 `GRADE_BUSINESS_ATTACHMENT_DIR` 修改材料目录，通过 `GRADE_BUSINESS_ADDR` 设置可用的回环端口。`GRADE_BUSINESS_OPERATOR` 是本地测试留痕所用的经办人标签。

### 角色与权限底座

启动后访问 `/login`，系统根据登录角色进入管理、测评机构或网络运营者工作台。没有预设账号。应用首次启动并完成迁移后，可由具备业务数据库管理授权的人员用 `cmd/access-account` 登记账号。

登录页按发布的驾驶舱地图提供市本级和区县选项。监管员须选择账号登记的辖区，市级账号请选择对应的「市本级」。系统管理员、测评机构和运营单位请选择「非辖区账号」。直接调用登录接口时，监管员提交对应的 areaCode，其他角色可省略。

登录页的安防盾牌支持鼠标和触屏拖动旋转。聚焦盾牌后可用方向键旋转，按 Home 复位。生物识别登录目前暂未开通。

```sh
go run ./cmd/access-account --username demo-admin --name '测试管理员' \
  --role ROLE_SYS_ADMIN --person-key DEMO-EMP-001 < /path/to/initial-password
```

以上是隔离测试示例。密码由受控文件经标准输入提供，至少 12 位。首次登录需改密并重新登录。监管员使用 `ROLE_REGULATOR`，填写人员唯一编号及 `--area-code`（例如 `331002`）。机构管理员使用 `ROLE_ORG_ADMIN`，运营单位管理员使用 `ROLE_OPERATOR`，两者均以 `--subject-id` 引用相应名录 UUID。同一人员或主体只能建一个账号。机构资质过期时，登录和已有会话均暂停使用，名录续期后恢复。

监管员按备案关联的可信单位辖区办理备案、变更与注销、实施报备、测评报告、整改规划、隐患和预警处置、监督检查及回执查询。区级账号仅覆盖本区县，市本级账号覆盖所属区县。未绑定可信辖区的记录由系统管理员核对单位主档。机构可管理自有名录的业务字段，辖区和已核定单位的信用代码由系统管理员维护。全局规则配置、预警全量同步和安全审计由系统管理员办理，监管员可只读访问知识库。

备案、备案申请、测评报告和实施报备的审核留痕保存经办账号和角色。实施报备的初审与终审须由不同账号办理，系统管理员代审遵守同样约束。缺少可信审核身份的历史待终审或待复核报备须填写原因并重新进入初审。系统管理员可通过「业务审计」按「账号与权限」查看登录、退出、改密、账号开通、注册审批以及访问拒绝的受控记录。

`GRADE_ACCESS_TEST_DSN` 用于真实路由权限集成测试，必须指向独立测试数据库。运行 `go test ./cmd/manager -run 'Test(RoleAccess|FourRoleWorkflow)OnIsolatedDB' -v` 可验证四角色、首登改密、主体权限及从备案到实施报备、测评、整改、复测、监督检查和注销的业务链。测试在连接的数据库中创建并清理专用 schema。

### 机构用户开通

系统管理员从「系统管理 → 机构用户」选择资质有效的在册测评机构，填写登录账号和机构负责人姓名。每家机构只能绑定一个 `ROLE_ORG_ADMIN` 账号。开户成功后本页一次性展示登录地址、账号和临时密码，由监管人员自行邮件联系机构负责人，平台内仅完成开户和留痕。机构首次登录须修改临时密码。

使用独立测试数据库和 `GRADE_ACCESS_TEST_DSN` 执行 `go test ./cmd/manager -run TestInstitutionAccountProvisionOnIsolatedDB -v` 验证开户权限、机构归属、唯一性及首登改密。

### 运营单位注册与审批

启动时自动应用注册相关迁移。系统支持台州与衢州监管辖区，衢州注册页面的行政归属由迁移 `090` 保存。

- 运营单位从登录页提交单位名称、统一社会信用代码、行政归属、管理员姓名、手机号、登录账号及自设密码。行政归属沿用衢州智慧大屏的市本级和六个区县。通过格式校验的申请统一受理，提交频率按连接来源限制。私密查询码仅在受理时展示，可查看进度和公开处理提示。
- 系统管理员可办理全部申请，监管员可在授权辖区内通过或驳回申请。新单位按填写的行政归属进入审核队列，已登记单位沿用名录核定辖区。两者不一致时审核端同时展示。存量申请和旧客户端继续沿用原归属，无法确认辖区的申请由系统管理员核实。
- 待审列表按统一社会信用代码归并，组内可逐份核验和办理。
- 审核员人工核实单位资料、联系人身份和授权后通过。通过时在同一事务中关联或建立单位名录、开通唯一 `ROLE_OPERATOR` 账号并保存审核留痕。驳回必须提供原因，申请人可修改后重新提交。待审和被驳回的申请不能用来登录，审批通过后直接使用自设密码。
- 使用独立测试数据库执行 `go test ./cmd/manager -run '^TestOperatorRegistration' -v`，沿用 `GRADE_ACCESS_TEST_DSN` 和专用 schema。

短信验证码和审核结果短信待接入。本机回环可使用 HTTP。正式登录前应配置 HTTPS 并核对账号与辖区名录。通过受控 HTTPS 反向代理终止 TLS 时，在应用进程设置 `GRADE_BUSINESS_COOKIE_SECURE=true`，并限制用户只能通过代理访问应用。应用直连 TLS 时自动标记会话 Cookie 为 `Secure`：在应用进程配置 `GRADE_BUSINESS_TLS_CERT` 与 `GRADE_BUSINESS_TLS_KEY`（服务器已有证书时填证书和私钥路径，两个必须同时提供）即可直接提供 HTTPS。旧版免登录的 HTTP 验收脚本需接入测试账号会话后才能运行。

会话在 30 分钟无请求后失效，可用 `GRADE_BUSINESS_SESSION_IDLE_MINUTES`（1–720）调整，登录后的总有效期仍为 8 小时。新设口令（开户临时口令、注册、改密）至少 12 位，并含大写字母、小写字母、数字、符号四类中的三类；存量账号口令不受影响，下次修改时才要求复杂度。

反向代理还应覆盖 `X-Real-IP` 后再转发请求，例如 Nginx 使用 `proxy_set_header X-Real-IP $remote_addr;`。应用通过 `GRADE_BUSINESS_AUTH_TRUSTED_PROXY` 配置直接连接它的单一代理 IP，只有该对端发送的合法单一地址参与客户端限流。未配置代理时使用 TCP 连接来源。

PDF 文字提取需要应用进程的 `PATH` 包含 Poppler 的 `pdfinfo`、`pdftotext`。扫描页还需 `pdftoppm`、Tesseract 的 `tesseract` 以及 `chi_sim`、`eng` 语言包。可以使用 `tesseract --list-langs` 核对语言。资料专区发布 PDF 前也使用 `pdfinfo` 检查页面结构。DOCX 提取随 Go 服务提供。

审核期限按上海日期从进入当前阶段后的首个工作日开始计数，退回、通过或撤销后停止该阶段的计时。默认周一至周五，监管可在系统管理的「工作日历」配置休息日、补班或恢复默认，并查变更留痕。整改截止日沿用审批结果。

| 启动设置 | 本地测试缺省工作日 |
| --- | ---: |
| `GRADE_BUSINESS_FILING_REVIEW_DAYS` | 3 |
| `GRADE_BUSINESS_FILING_FINAL_REVIEW_DAYS` | 5 |
| `GRADE_BUSINESS_REPORT_REVIEW_DAYS` | 5 |
| `GRADE_BUSINESS_IMPLEMENTATION_REVIEW_DAYS` | 3 |
| `GRADE_BUSINESS_REMEDIATION_REVIEW_DAYS` | 3 |

取值为 0–365，0 关闭对应环节的审核期限。三端工作台共读当前办理状态，临期包含当天和下一个工作日的截止事项。后台启动时及每分钟扫描时限，生成临期、到期和逾期站内提醒，重试和并发投递去重。运营者每个上海日期可对同一隐患催办复测一次。

本地预警以已登记隐患为来源，每分钟同步一次，预警中心也可手动同步。
每个隐患的每种规则只登记一个预警，预警处置和隐患销项分别留痕。
公告确认发布时固定该版本的收件范围和公文附件。机构和运营者查收公告及下载附件，监管可按版本查询原文件、送达、已读和签收。运营者可在同一公告页打开「政策标准与常用资料」，从已送达的资料卡下载附件。机构可提交预警处置反馈，由监管审核并同步处理结果。[公告回执验收](BUSINESS_PUBLICATION_RECEIPTS.md) · [公文附件验收](BUSINESS_PUBLICATION_ATTACHMENTS.md)

并行运行本地版本时，分别为实例配置回环端口、静态构建目录、测试 schema 和附件目录。

## 业务流程和安全边界

- 备案可创建草稿、上传材料、发起异步材料预审、提交初审、完成终审或退回补正。权限网关启用时，运营者提交的首报、变更和注销由管理端办理审核。备案表和定级报告为首报最低提交材料。监管可进一步按事项、附件类型和文件名配置材料规则。备案草稿可保存 GB/T 22240-2020 定级自评和依据，正式备案等级仍由业务流程确认。
- 监管可以在备案列表导入历史材料。将清单和两类材料打成 ZIP，先预检再确认。通过校验的记录保存为草稿，重复和冲突逐行留痕，同一包重传沿用原批次。[剩余业务规则与联验](BUSINESS_REMAINING_CLOSURE.md)
- 测评报告从已保存备案中选择关联对象，上传报告后提交审核。备案和测评都在同一事务中保存审核状态与审核记录。版本冲突返回 `409`。
- 测评退回和终审向报告登记机构生成站内意见。机构端可查收、签收并对退回报告补传材料重提。监管端查看送达和签收时间。
- 单位、机构资质和测评人员共用名录。机构维护本机构人员和被测单位，监管维护主档。实施报备、测评报告及公告收件使用统一范围，并保留办理或发布时的快照。[业务与验收](BUSINESS_DIRECTORY.md)
- 监管按预览合并重复单位。复杂模式逐笔列出可接续、原范围留存和需先处理的事项。符合条件的在审备案、年度自查、现场检查和带证据隐患可交给承接单位，原件和历史回执按原范围保留。[复杂单位归并与档案分析](BUSINESS_MERGE_ARCHIVE_ANALYTICS.md)
- 机构可上传备案 ZIP，包内需包含标准命名的备案表与定级报告。测评报告失败后可从「未成功」查看原因、重传，或删除未送审的草稿。材料异常和补正结果同步到三端预警及站内提醒。[业务与验收](BUSINESS_MATERIAL_RECOVERY.md)
- 机构可复制备案为独立草稿，按状态和版本批量删除或送审，并按筛选导出测评台账。监管可按区县和保护等级预览并批量下发自查，跟踪现场安排、证据、补正和办结。材料有效期支持原件登记、三端续传审核和站内提醒，监管可预览和配置定向预警。[业务规则及验收](BUSINESS_BATCH_AND_VALIDITY.md)
- 现场跟进和监管复核会按办理阶段进入统一工作台。原运营单位可从截止提醒直达现场检查。材料到期登记支持筛选和分页下载待登记的正式原件，并在落库前核验历史归属。[现场待办与原件补录](BUSINESS_SITE_FOLLOWUP.md)
- 已通过且有效的备案可发起变更或注销。同一备案处理一份未办结申请，草稿或退回申请可撤回并保留留痕。变更和注销在终审通过时生效，历史申请、备案及已关联的报告可追溯。
- 运营者可为在审首报、变更和注销申请撤回。监管确认后释放在审占用，驳回则恢复原审核阶段和期限。原材料和回执按原归属可查。[三端办理闭环](BUSINESS_REVIEW_CLOSURE.md)
- 实施报备支持常规报备和变更报备，经过受理、初审、终审及监管复核。变更以首次提交时的北京时间自然年计算，每份备案最多三次有效变更。补正重提沿用原额度。实施计划修改截至计划开始日北京时间零点。开始前，首次提交 24 小时内且未受理可撤销。到达开始日后，发起人仍可撤销未办结报备。撤销释放额度，同一备案满 15 天后可重新提交。
- 整改规划关联有效备案下已通过的测评报告。录入风险、措施、责任和期限后，提交需附整改规划方案、责任分工表及实施计划表。首次上传材料后锁定关联报告。支持期限筛选、统计、退回补正和重新审核。
- 整改审批通过后按风险项登记隐患台账。历史已通过的规划可在详情手动同步，重复同步保留现有记录。隐患需提交本轮证据和说明，由测评机构上传本轮复测报告并通过复测，随后监管端复核销项。退回后须补传证据，销项后仍保留历史和证据。
- 监管可在有效备案下直接登记系统漏洞和重大风险并派单。重大风险支持向当前整改或复测方发站内督办，同一事项按上海日期去重。两类台账均支持筛选后导出 CSV。[业务与验收](BUSINESS_HAZARD_LEDGER.md)
- 整改待办展示待审核规划、待复核隐患和临期、逾期条目，可从工作台进入详情。期限按中国时区计算，已销项的隐患退出未完成统计。
- 三端预警中心可按各自归属查看业务时限。监管从有效备案登记、修订或办结系统测评到期事项，填写实际到期日期和依据。系统向当前运营者和测评机构发送临期与到期站内通知，并保留签收记录。运营者按上海关闭日期导出本单位处置台账，监管监督线索记录仍超期的隐患并保留历史。驾驶舱按有效备案所属单位名录区县展示未销项风险。「全息档案 → 系统档案」维护资产和备案覆盖清册，驾驶舱从清册计算全局和区县覆盖率，并从有效备案统计已登记资产。
- 单位档案维护统一供应商主档，系统档案从登记关系生成拓扑。驾驶舱可导入本机区县 GeoJSON 查看风险分布，并按已登记的行业和问题分类统计。[档案和驾驶舱口径](BUSINESS_MERGE_ARCHIVE_ANALYTICS.md)
- 驾驶舱对外提供**同比环比**：`/dashboard/district-response` 返回 `comparison`，按当前有效备案范围，在当前窗口、紧邻的等长上期窗口和去年同期的等长窗口之间给出**真实计算**的对照。流量型指标（新增备案、新增测评报告、触发预警、隐患销项）按事件发生时间落入窗口计数；存量型指标（未销项隐患、活跃预警、综合风险指数、整改完成率）按窗口末时点重放，即"该时点已创建且尚未销项"的隐患与"该时点已触发且尚未关闭"的预警。变化率、上期为 0 时改按数量比较；去年同期窗口没有数据时返回 `hasYearAgo=false`，界面显示"同比 —"而不编造数字。窗口天数可用 `window` 查询参数指定（1–365，缺省 30）。
- 驾驶舱对外提供**综合风险指数**与**当前范围待办汇总**：前者按逾期 40%、高风险 40%、待机构复测 20% 加权并封顶 100 分，落在 `risk-by-district` 的每个区县与 `district-response` 的 `risk` 字段；后者汇总当前范围的待办总数、四类待办计数、最重区县与中文结论，落在 `district-response` 的 `duty` 字段。驾驶舱界面据此把区县按风险指数浮起成三维沙盘，并在顶部给出可直接投屏的态势结论。
- 机构和运营者可在单据详情与业务时限跟踪报送后正在其他端办理的审核，使用相同阶段及日历。监督检查可以按备案或真实超期线索下发，指定运营单位上传两类材料，监管退回补正或办结，原证据及收件历史可追溯。
- 报告提取支持 DOCX、文本 PDF 及扫描 PDF 的「资产清单」「漏洞详情」「风险等级」「整改建议」。DOCX 保留段落，PDF 保留物理页、文本块位置和提取来源。校对形成独立版本和留痕。
- 监管依据终审通过报告的材料登记并留存结构化得分、结论和三类风险项数。运营者按可见范围看年度趋势，两期可对比结果和已批整改问题。
- 运营者可下载有权查看的终审报告正文和随附整改规划。复杂归并会封存原可交付范围，使原单位仍能查看已有报告与对应的整改要求。
- 知识库支持五类条目的录入、编辑、查看和标签维护，可以录入正文或导入 UTF-8 TXT、Markdown 文本。关键词匹配标题、来源、正文和标签，可与分类、来源组合查询。编辑和标签维护共享条目版本。
- 备案详情的材料规则预审核对必传材料，并按 11 项展示内容结论与原件依据。监管员按辖区逐项保存结论和备注。材料、必传规则或模型参数变动后，旧预审标记过时。
- 整改方案、责任分工表和实施计划表可从当前范围的规划下载本地参考模板。实施报备可预检字段、日期、备案材料、名录和变更额度。监管在知识条目中维护法规和标准条款。测评合规根据明确题名和条款号列候选，也可人工选条款。登记保留证据、知识及条款版本。[办理规则及验收](BUSINESS_DAILY_OPERATIONS.md)
- 业务 API 通过账号会话及角色校验，登录和运营单位注册入口可匿名访问。本机模式限制回环地址。部署启用内网模式时，服务使用配置地址和网段检查连接。两种模式都限制 Host 和请求来源。业务接口采用专用 DTO，附件返回业务地址和文件字段，未知写入字段被拒绝。

## 备案详情的 DeepSeek 预审

服务从运行环境的 `DEEPSEEK_API_KEY` 读取密钥，默认使用内置模型参数。如需调整参数，可复制 `config/filing-audit.deepseek.example.json` 为 `config/filing-audit.deepseek.local.json`，或用 `GRADE_BUSINESS_FILING_AI_CONFIG` 指定自定义参数文件的绝对路径。GitHub 测试部署通过同名仓库 Actions Secret 注入密钥。

- `model` 使用 `deepseek-flash`，对应 DeepSeek-V4.1-Flash。
- `reasoning_effort` 默认 `high`，支持 `none`、`low`、`high` 和 `max`。
- `include_images` 默认关闭。开启后，发起预审时会随摘录发送选定的 PDF 页图和 DOCX 内嵌图片。无法从送检材料确定的签章、签名或拓扑由监管员核实。
- 进程启动时读取参数。修改后重启，再从备案详情发起预审。运行环境提供密钥后优先调用 DeepSeek，模型不可用时自动完成本地规则审核，并在结果中显示来源和原因。
- 备案、名录、规则和模型输入一致时，重复发起预审可复用已核验的 DeepSeek 结果。补报材料或恢复中断任务时，系统续用仍有效的逐件结果，再核对整套材料。需要显式重新推理时可调用 `POST /api/v1/filings/{id}/prechecks?force=true`。
- 数据库表 `filing_precheck_ai_usage` 按任务和审核阶段保存实际模型请求、用量、缓存细分和结果状态。按 `job_id` 查询并汇总可估算该批次费用。供应商未上报用量的请求通过 `usage_reported` 标记，费用应计为未知。

## 执行验收

```sh
GRADE_BUSINESS_TEST_DSN="$GRADE_BUSINESS_DSN" go test -race -count=1 ./...
go vet ./...
pnpm --dir web typecheck
pnpm --dir web test
grade_business_verify_dir=$(mktemp -d "${TMPDIR:-/tmp}/grade-business-verify.XXXXXXXX")
pnpm --dir web exec vite build --outDir "$grade_business_verify_dir"
GRADE_ACCEPTANCE_PORT=18444 python3 tests/acceptance.py
```

集成测试在名称为 `grade_business_test` 的专用 PostgreSQL 库创建独立 schema，完成后清理该测试 schema。接口验收脚本访问已运行的应用，创建可识别的 `QA-` 业务记录用于核对持久化和附件下载。

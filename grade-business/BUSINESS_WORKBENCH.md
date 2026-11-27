# 三端工作台、时限提醒与指挥驾驶舱

- 工作台以业务表当前状态和最新流转记录投影待办。监管看审核、复核及待决预警，机构看本机构初审、复测及补正，运营者看本单位补正、整改与预警。运营者可跟踪待机构复测并催办。不同席位沿用本地业务范围。
- 审核阶段自进入该阶段之日后首个工作日计数。退回时退出审核计时，补正重提进入新阶段。整改截止日沿用审批结果。默认周一至周五，支持数据库日历覆盖补班和休息日。正式节假日由部署方提供，当前样例使用可配置测试窗口。
- 阶段截止前、到期和逾期站内提醒从当前任务生成，按阶段去重。运营者手动催复测按隐患和上海日期去重。站内任务与原办理入口保持一致。
- 驾驶舱采用独立全屏、同屏关键指标和真实业务趋势，沿用现有统计、预警、区县和行业数据。区县边界可以从本机导入。
- 依据三端原型完成前端后，统一验证跨端接手与办结、假日和补班边界、迟到与重复提醒、数据刷新以及全屏布局。登录和 HTTPS 暂缓。

## 实现与运行口径

- 迁移 029 建立当前待办投影和工作日日历，迁移 030 扩展同一站内收件箱的投递约束。监管和两端工作台直接使用当前办理记录，备案与变更注销列表沿用详情的审核席位文案。
- 每个审核阶段自进入后首个工作日计数。调休日期优先使用 `workflow_calendar_days` 覆盖。本地缺省备案初审 3、终审 5、测评 5、实施与整改规划 3 个工作日，均可配置。整改截止日按已审批日期。
- 自动提醒启动时和每分钟扫描一次，临期、到期和逾期分别按阶段、截止日、接收方与类型去重。监管经办的时限在监管工作台显示，并在站内提醒发起方关注进度。手动催办在事务内复核隐患状态和收件机构，同一上海日期只发送一次。
- 起始页为 `http://127.0.0.1:18460/`，机构和运营者入口分别为 `/institution/workbench`、`/operator/workbench`。两端可见页面每 10 秒刷新，监管工作台和驾驶舱每 30 秒更新。
- 大屏入口为 `/dashboard`。Vue、CSS 和浏览器 Fullscreen API 提供 16:9 态势总览，窄屏按区块排列。顶部关键指标、中央区县风险、两侧图表及底栏监管指标沿用现有业务接口。
- ECharts 6.1.0 按需引入折线、环图和 Canvas 渲染模块，图表独立延迟加载。趋势支持 7／14／30 日、指针和键盘查看。辖区 GeoJSON 随前端构建发布后，态势总览默认显示地图，也可切换排名。未配置辖区边界时默认显示排名。
- 风险分析、实时预警、统计分析保留原有入口。区县热力、系统档案、待办、预警等可以转到对应页面。手动刷新同时更新当前显示的区县统计。

## 区县处置态势实施

- [x] 沿用有效备案和已派发未销项的区县范围，增加北京时间逾期数和稳定排序。根据选中区县查重点事项、风险系统和最新预警。
- [x] 中部默认显示全域。排名和地图共用当前区县范围，联动处置详情、风险系统和预警。自动刷新保留所选范围，未归区独立查看。
- [x] 用隔离数据库核对到期边界、区县匹配、跨区联动和下钻，再用浏览器复测本地入口。

区县分布接口返回系统数、高中低风险数和逾期数。处置接口在全域或一个区县范围内读取同一快照，返回待机构复测和待监管复核的实际数量，并提供至多 6 项重点事项、10 个高风险系统和 20 条预警。全域含未归区，区县按有效备案所属单位划分。重点事项显示运营单位、系统、状态和截止日期，可进入隐患详情。

总览右侧监管待办默认显示全域业务计数，可进入对应筛选列表。选中区县后，逾期隐患、待监管复核、待审核整改规划和待处理预警一并切换为区县范围，重点隐患和最新预警提供逐项详情入口。待处理预警计数按该区县完整数据计算。新增的活跃高风险预警在总览短暂标记，首屏加载和范围切换只建立比对基线。

选中区县时，顶部指标、备案覆盖、趋势、分类、备案状态、风险分布和底栏与处置详情使用同一个区县查询快照。备案按当前单位名录归属区县，测评、报备、隐患、预警和资产通过对应业务关联归属。地图着色保留全市九区县风险对照。切换驾驶舱页签和刷新保留当前区县。点击全域恢复总体统计，选择未归区或无记录区县时，各面板仍按该范围显示。

### 台州市底图

默认底图覆盖浙江省台州市（331000）的椒江区、黄岩区、路桥区、三门县、天台县、仙居县、温岭市、临海市和玉环市。区县名单与[台州市自然资源和规划局公开材料](https://zrzy.zjtz.gov.cn/module/download/downfile.jsp?classid=0&filename=7b69d06c902b4a069f2389d6be93ecba.pdf)核对。

底图使用 [CTAmap v2.0](https://www.shengshixian.com/pages/d35ae5/) 的 2026 年 1 月 1 日县级边界快照，原作者为 Cheng, R.、Xiao, C.、Wang, Y. 和 Hu, Y.，数据 DOI 为 [10.5281/zenodo.22179207](https://doi.org/10.5281/zenodo.22179207)，许可为 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)。从 `T2026_county.gpkg` 按省级码 `330000`、地级码 `331000` 提取九个区县面，再转为 WGS84 GeoJSON，保留原始几何和坐标。

构建从同源提供 [district-boundaries.geojson](web/src/assets/district-boundaries.geojson)。地图用 `properties.name` 与单位名录区县全称精确匹配，未归区单列，无法匹配边界的记录可在排名查看。地理边界用于驾驶舱态势展示，业务数量和待办实时读取业务接口。

## 网络安全主题底图

使用内置图像生成工具制作原创数字城市，压缩为 [cockpit-cyber-city.webp](web/src/assets/cockpit-cyber-city.webp)，随同源前端资源加载。城市图仅用于表现网络安全主题，页面另外从业务接口读取实际名称和数量。生成提示词如下。

```text
Create one sophisticated network security technology style background artwork for the central panel of a cyber defense operations dashboard. Wide landscape 3:2 image safe for a much wider crop of the middle. Cybersecurity digital twin of a modern city: realistic oblique aerial 3D environment with dense angular navy-black glass towers, fine neon-blue edges, central prominent tower, glowing cyan fiber optic lines embedded in street grid and selectively ascending small data links, thin geometric connected mesh nodes distributed subtly through the skyline, premium cinematic cyberspace atmosphere with blue volumetric color. Include a restrained elegant wireframe hexagonal shield motif integrated within distant central upper atmosphere, faint and anonymous, one only, no invented law enforcement identity. Hero focus reaches across the middle band. Rich detail and high contrast flowing in the central streets, relatively dark and clean outer edges, dark lower edge for separate HTML statistic cards. Deep navy, electric cyan and royal blue with minimal amber lights. Crisp technically sophisticated restrained public-sector cybersecurity feel, realistic texture and no vaporwave. Background only. No letters, no text or numbers, no charts, no dashboard UI, no logos, no captions, no map location pins, no markers for actual data, no advertising.
```

## 统一验收

- 隔离 PostgreSQL 的 Go 全包竞态测试、`go vet ./...`、管理端二进制构建通过。总语句覆盖率 79.3%。覆盖节假日与补班、阶段换手、范围隔离、重复投递和并发销项等。
- Vue 类型检查、8 项前端单测和生产构建通过。OpenAPI 1.15.0 的 212 个操作及 564 处组件引用校验通过。
- `tests/workbench_acceptance.py` 新增 3 组业务接口验收通过，34 组既有回归通过。隔离预览的后台任务另实际投递并可查询一条运营者逾期提醒。
- Chrome 完成工作台自动出现新任务、初审及终审交接、办理后退出队列。运营者发起和重复催办后，机构实际查到一条提醒并直达隐患。驾驶舱验证 1920×1080 单屏、原生全屏进出、真实趋势切换和返回工作台。

正式节假日需要维护日历覆盖。实际区县热力使用对应的正式区县边界，行业和区县统计取自系统登记。

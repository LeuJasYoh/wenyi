# wenyi-multi 开发日志（跨会话进度档案）

> 本文件是跨上下文压缩的持久进度档案。每个阶段完成/中断时更新。
> 规格 materials: D:\Projects\wenyi-spec-export\（主规格=唯一真源；冲突裁决顺序：主规格 > 分册 > 经验；验收方法以任务书 §7 为最高优先）

## 环境

- Go 1.26.5 / Node v24.14.0 (npm 11.9.0) / .NET 10.0.400 @ Windows
- 工作区：D:\Projects\wenyi-multi（Go module 名 `wenyi`）
- Go deps 已取：cobra、modernc.org/sqlite、golang.org/x/text、golang.org/x/sync、（待加 gopkg.in/yaml.v3）

## 关键实现决策（累积）

- 逐字文本（prompt 模板 / 默认 config.yaml）用脚本从分册 markdown 代码围栏提取为 .txt 文件，Go 侧 go:embed 嵌入——保证字节保真，不手工转录。
- Python len(str)=码点数 → Go 用 utf8.RuneCountInString；Node 用 [...s].length（不能直接 .length，UTF-16 单位 ≠ 码点）。
- JSON 键序：审校协议需要（list(data)[-2:] 校验），Go 侧用 json.Decoder token 流自建有ordered map（阶段4）。events.jsonl/usage.json 输出必须 SetEscapeHTML(false)，键需排序（Go map marshal 自动排序；但 ordered 场景需手写）。
- Go safe_substitute：实现 Python string.Template 语义（$$ 转义、$id、${id}、未知占位原样保留）。
- 仓库根 config.yaml（与内嵌差异：fast.thinking=true + 注释差异，仅 delta 已知）→ 交付时按已知 delta 生成并登记已知差异清单。
- testdata 夹具：node/scripts/gen-fixtures.mjs 从 sample_data.py 内容生成（语言无关迁移），产物提交到 testdata/。
- slugify：`re.sub(r"[^\w一-鿿぀-ヿ-]+","_",name).strip("_") or "book"`（\w 含数字字母下划+Unicode 字母）。

## 阶段进度

### 阶段 0（完成 ✅ 2026-08-20）
- [x] 三语言骨架 + go mod init + deps（cobra/modernc sqlite/x-text/x-sync/yaml.v3）
- [x] Go: config（嵌入默认 YAML 逐字，scripts/extract-verbatim.mjs 提取）/jsonx（保序 JSON）/llm(base+fake+jsonparser)/agents(prompts 27 模板嵌入+langprofile+base+Translator)/checks
- [x] Go 测试: config_test（5 用例）+ translator_test（8 用例）全绿；三组件 --help/--version 冒烟通过
- [x] Node: package.json（cheerio/jszip/jschardet/iconv-lite/fastify/yaml；**better-sqlite3 移除——Node24 原生编译失败，阶段 7 解决：试 v12+ 预编译或改用 node:sqlite**）+ cli.js 骨架 + node:test 冒烟
- [x] .NET: Wenyi.Pdf（QuestPDF 2025.7.0）+ xUnit 冒烟
- [x] testdata: gen-fixtures.mjs 生成 11 个 EPUB + sample.txt
- 已知差异登记：render_glossary 格式主规格（嵌套括号）vs 分册05（扁平逗号）无测试可裁决 → 按主规格嵌套实现

### 阶段 1（完成 ✅ 2026-08-20）
- Node src/doc：dom.js（domhandler 工具层+自写 bs4 风格序列化器——cheerio $.html() 丢自闭合斜杠不可用）/models/errors/encoding（BOM→decl→jschardet→utf8replace）/epub-toc/epub-reader（annotate 全套：内联保留/注释 point+range/空白折叠 boundary_map/目标选择/OPF/逻辑章切分/TopLevelTocStrategy）/text-reader/fb2-reader/html-reader/pdf-reader/pdf-to-html(MinerU)/segmenter/cmd-parse
- 测试：node/test/ ingest-text(10)/ingest-fb2(7)/ingest-epub(31)/ingest-pdf(4)/cmd-parse(6) + cli-smoke(2) = 60 全绿
- 关键坑修复：① JS \d 只匹配 ASCII → 全部正则改 \p{Nd}+u（Python \d=Unicode Nd）；② stripMarkup 遍历 findAllElements 只含元素 → 改全节点遍历；③ iconv.normalizeEncoding 不是公开 API；④ insideNavigationList 需含元素自身（li 本身也保护）；⑤ Python .split() 无参 ≠ JS split(/\s+/)（前导空串）
- test_pdf_support.py 其余用例（orchestrator/cli/assemble/pdf导出）→ 阶段 3/5 迁移（已登记对照表）
- 夹具修正：nested-ncx-custom-filename.epub 的 NCX 文件名须为 toc.xml（对齐 Python 测试参数）

### 阶段 2（完成 ✅ 2026-08-20，Go 118 测试全绿）
- llm：usage（tracker/delta/merge，None sample 完全跳过）/tiers（回退链）/retrying（HTTPError/TransportError 分类、x-should-retry、retry-after 链、CallWithRetry+事件）/jsonparser（字符级修复器：内引号转义+尾随括号忽略+EOF 补全）/fake（完整快照）
- providers：RequestKwargs 结构（等价 Python kwargs dict）+ BaseRequestKwargs/DeepMerge/ResolveProviderTiers[generic]/httpChatCaller（可注入 stub）；deepseek/openai/openrouter/openai-compatible+ollama+vllm 方言；gemini 独立实现（消息转换/用量/thinking 互斥/安全拦截）；factory 在 internal/llmfactory（避免 import cycle）
- agents：analyzer（analyze/seed/style_brief）/synopsis（digest 8000/map-reduce 12000）/polisher（保守回退）/consistency（手工拼 user）/extractor（从 glossary 包移入——避免 agents↔glossary cycle；recurring 过滤/history 回查/首次译文校准）/annotationaligner（⟪⟫ 标记协议全套）
- glossary：modernc sqlite（DSN _txlock=immediate+busy_timeout+WAL）；schema 逐字（含 DROP translation_memory）；匹配（NFKC+casefold、ASCII/\w/CJK 三档边界、称谓只按 source、区间合并计数）；upsert 三态/resolve/只读快照
- pipeline：RollingContext（context.go）
- punct：normalizeZH/NormalizeZHSegments（cont 状态传递修正：状态流入 cont=True 段自身）
- 关键坑：① ⟪ 剥离须 rune 切片（3字节）；② aligner payload 须 {"items": [...]} 包装；③ CompatClient 用 *llm.Core 内嵌（跨包未导出问题）
- 已知差异登记：render_glossary 嵌套括号 vs 分册扁平；extractor summary 第 7 键身份未知（按 6 键实现）；punct 6 正则顺序/单引号第 4 分支未随包（按主规格 3 分支）
- 阶段 2 测试迁移：test_llm(22)/retrying(11→阶段3还1)/usage(12→阶段3还4)/gemini(12)/glossary(10)/glossary_agents(11)/annotation_aligner(12)/newfeatures·punct+honorific(9→阶段3还1)

### 阶段 3（完成 ✅ 2026-08-24，累计 Go 155 + Node 60 测试全绿）
- ingest（Go）：spawn Node `doc parse`（临时目录→doc.json→按 title 定位 run_dir；PDF 直接文件名 slug）；meta 数字归一 int64（防 float 破坏偏移）
- pipeline：runstore（manifest/chapters/context/analysis/usage/report/events.jsonl+缓存/batch 检查点/锁 Windows LockFile+POSIX flock+进程内全局互斥/slugify）；orchestrator（prepare/run/RunLocked/BuildUnderstanding goroutine 池/TranslateChapter 主循环/断点复用/章末标点+兜底抽取+回译/usage flush/进度计数）；annotation 接线；titles（anchor 复用/spine 回退/分批≤40·≤4000/失败不降级）；steps（RunSteps/RunAll/QA/BuildReport/AssembleFunc+RunReviewFunc 注入点——阶段4/5 填充）
- cli：完整命令集（translate/prepare/review/qa/status/report/assemble/glossary list·conflicts·resolve）；--config 预解析+自动建配置；API 预检（豁免 assemble/glossary/report/status；--version/--help 跳过）；退出码 0/1/2（chapter 冲突=1 ValueError 语义，format/engine=2）
- fakellm：routing_handler 逐语言等价
- 关键坑：① ResumeBatches 忘在 raw 批边界 flush（合并成一批→断点全失效）；② jsonx.Marshal 分隔符须 ", "/"： "（Python json.dumps 默认）；③ ⟪ payload rune 切片；④ requestedIndices append 丢失；⑤ CLI 内建 FakeClient 无 handler（自创测试删除）
- 测试迁移：test_orchestrator 24 个（Review/assemble 相关 22 个待阶段4/5）+ test_cli 13 + test_newfeatures 5 + test_usage 剩余 2 + RollingContext 3
- 待办登记：_detect_language_ai system 提示词为已知缺口（自拟文本，登记差异清单）

### 阶段 6（完成 ✅ 2026-08-24）
- pdf/Wenyi.Pdf：QuestPDF 2025.7（RegisterFontWithCustomName "WenyiCJK"）；A5+18/16/20mm+页脚页码；h1 分页（首元素除外）；双语段落对（order）；cont 续段并回；字体发现（--font→TRANS_NOVEL_PDF_FONT→Windows msyh/simsun→mac→linux）；CLI render + --version；Program.Run 可测入口（InternalsVisibleTo）
- 测试：RenderSmokeTests 4/4（真实渲染 %PDF 头 + 缺状态 exit 1）
- 待阶段5 落地后：Go AssembleFunc 的 pdf 格式分支 spawn dotnet（CLI 约定 "OUTPUT: <path>"）

### 阶段 4/5（进行中 — 后台子代理并行实现）
- agent1（Go review 域）：internal/review 包 + pipeline 接线 + test_review_agent/test_review_polish/ReviewReporting 迁移
- agent2（Node assemble 域）：writer.js 全套 + cmd-assemble + Go AssembleFunc spawn 接线 + test_assemble/test_bilingual 迁移
- 完成后我方验收：go test ./... + node --test 全绿、抽查规格符合度、补 RunAll 集成测试（test_newfeatures::TestRunAll）

### 阶段 5b（完成 ✅ 2026-08-26）
- internal/pipeline/assemble_node.go：AssembleFunc init 接线（spawn node doc assemble，解析 OUTPUT: 行）；pdf → dotnet Wenyi.Pdf.dll spawn
- 引擎 CLI 修复：result["outputs"] = toAnySlice（[]string → []any，CLI 输出路径恢复）；runall.test.js 3 例绿（RunAll 全流程/subset assemble 幂等/pdf 路由）

### 阶段 7（完成 ✅ 2026-08-26，WebUI 17+ 测试绿 + 前端构建通过）
- 后端（node/src/web/）：serve.js（分册 10 全端点：系统/书架/章节/任务/术语直连/审校/QA/导出/事件+SSE 游标/config/上传）+ spawn.js（JobRegistry/--progress-line 解析/argv 映射表）+ slugify.js
- 引擎增量：--progress-line（JSON Lines 进度行，默认关闭）+ --state-dir（PersistentPreRun 覆盖）——分册 10 §1.8/§1.1 契约
- 前端（node/ui/）：React18+TS+Vite+Tailwind3；设计 token（tailwind.config 单一真源）；组件库（Button/Card/Skeleton/Empty/Error/Progress/Badge/Spinner）；App.tsx 全页面（Dashboard/NewTask/BookView 7 标签页：Translate 实时双 SSE/Glossary 冲突裁决/Review/QA/Export/Usage/Events/Settings/Ctrl+K 命令面板/双主题）；构建产物 → ui-dist/
- 测试：web-api.test.js 14 例（映射表逐行/slug·Host/游标/错误信封/分页/上传闭环）+ web-e2e.test.js 3 例（全流程闭环含 review/取消幂等/事件透传）全绿
- 关键坑：① spawn 不传 env → fake 无路由（engineEnv 注入修复）；② cobra PersistentPreRun 时机（--state-dir 在 flags 解析后才可用）；③ glossary aliases NULL 容错（safeParseList）；④ Go sqlite 驱动需在 main 侧 import

## 最终测试汇总（288 例全绿）
- Go 189（config/llm/providers/factory/glossary/agents/review/pipeline/punct/cli）
- Node 95（ingest 48 + assemble 15 + pdf 4 + runall 3 + web 17 + 冒烟 8）
- .NET 4（QuestPDF 渲染冒烟）

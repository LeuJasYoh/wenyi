# wenyi-multi

Wenyi（trans-novel）多语言复刻版：Go + Node.js + .NET 10 三组件 + **WebUI 产品主界面**。
行为复刻自 Python 原版 v0.4.1（GitHub: BigDawnGhost/wenyi），验收方式为测试断言迁移（见《测试迁移对照表.md》）。

## 运行时前置

- **Go 1.26+**：编译引擎（静态单二进制，无运行时依赖）
- **Node.js ≥ 20**：摄取/组装组件 + WebUI 后端与前端
- **.NET 10 Runtime**：仅 PDF 导出功能需要（QuestPDF）

## 快速开始（开发环境）

```bash
# 1. 构建三组件
make build-go     # → dist/wenyi.exe（Go 引擎 + CLI）
cd node && npm install && cd ui && npm install && npm run build && cd ../..   # Node 依赖 + WebUI 前端
cd pdf/Wenyi.Pdf && dotnet publish -c Release --no-self-contained -o ../../dist/wenyi-pdf && cd ../..

# 2. WebUI（产品主界面）
cd node && WENYI_ENGINE=../dist/wenyi.exe node cli.js web serve --port 8731 --no-open
# 浏览器打开 http://127.0.0.1:8731

# 3. 无头 CLI（脚本接口）
dist/wenyi.exe translate book.epub        # 断点续跑
dist/wenyi.exe status book.epub
dist/wenyi.exe --help
```

## 组件结构

```
├── cmd/wenyi/            # Go 引擎入口（编排/智能体/LLM/术语/Review/状态存储）
├── internal/             # Go：config/llm/providers/agents/glossary/review/pipeline/punct/jsonx/cli
├── node/
│   ├── cli.js            # Node 入口：doc parse|assemble / web serve
│   ├── src/doc/          # 摄取（EPUB/FB2/HTML/TXT/PDF）+ 组装（EPUB 回填/双语/HTML/TXT/MD）
│   ├── src/web/          # WebUI 后端（fastify + SSE）+ 引擎 spawn 管理
│   ├── ui/               # WebUI 前端源码（React 18 + TS + Vite + Tailwind）
│   └── ui-dist/          # 前端构建产物（随包分发，运行时零构建）
├── pdf/                  # .NET 10 PDF 组件（QuestPDF，A5 + CJK）
├── testdata/             # 语言无关测试夹具
└── dist/                 # 构建产物（wenyi.exe / wenyi-pdf/）
```

## 测试

```bash
make test-go     # Go 全量（189 用例）
make test-node   # Node 全量（95 用例，含 WebUI API/端到端）
make test-pdf    # .NET 冒烟（4 用例）
```

## 文档

- 《测试迁移对照表.md》——18 个 Python 测试文件 → Go/Node 测试的逐项映射
- 《已知差异清单.md》——与 Python 原版的全部行为差异声明
- 《DEVLOG.md》——开发过程日志

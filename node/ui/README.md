# WebUI 前端

Wenyi 的浏览器端界面：书架/新建任务/书籍详情（翻译、术语裁决、审校、QA、导出、用量、事件流）/设置（API Key、环节 × 模型档位、JSON 源码模式），书卷纸感设计系统（纸色底 + 墨色字 + 朱砂强调；衬线标题 + tabular-nums 数据，无网络字体依赖）。

## 技术栈

- React 19 + TypeScript + Vite + Tailwind CSS 3，无其他运行时依赖
- 路由为内置 hash 路由（`useHashRoute`），状态就地管理

## 源码结构

- `src/App.tsx`——页面与书籍详情 Tab、命令面板、App 外壳
- `src/config-form.tsx`——设置页表单
- `src/components.tsx`——通用组件（Button/Card/Skeleton/Empty/Progress 等）
- `src/api.ts`——REST / SSE 封装
- `src/icons.tsx`、`src/tokens.ts`——SVG 图标与设计令牌
- `tailwind.config.js`——设计 token 单一真源

## 构建与开发

- `npm run build`——产物输出到 `../ui-dist/`，由仓库根 `make webui` 复制进 `internal/webserver/static/` 并 go:embed 内嵌进单 exe；fresh clone 无产物时引擎回落占位页
- `npm run dev`——Vite 热更新开发态，`/api` 代理到 `127.0.0.1:8731`，配合 `dist/wenyi.exe web` 使用；后端另支持 `WENYI_UI_DIR` 指定磁盘前端目录覆盖内嵌版本
- `npm run lint`——oxlint

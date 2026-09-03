// App.tsx —— WebUI 页面全集（侧边栏布局 + 现代化视觉，分册 10 §7 页面→端点映射）。
import React, { useEffect, useState, useCallback } from "react";
import { createRoot } from "react-dom/client";
import { api, subscribeJob, subscribeBook, type BookItem, type Job } from "./api";
import { Button, Card, SkeletonList, EmptyState, ErrorState, Progress, Badge, Spinner } from "./components";
import { SettingsForm } from "./config-form";
import "./index.css";

// ---- 主题 ----
function useTheme() {
  const [theme, setTheme] = useState<"light" | "dark">(() =>
    localStorage.getItem("wenyi-theme") === "dark" ? "dark" : "light");
  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark");
    localStorage.setItem("wenyi-theme", theme);
  }, [theme]);
  return { theme, toggle: () => setTheme((t) => (t === "light" ? "dark" : "light")) };
}

// ---- 路由 ----
function useHashRoute() {
  const [route, setRoute] = useState(() => window.location.hash.slice(1) || "/");
  useEffect(() => {
    const on = () => { setRoute(window.location.hash.slice(1) || "/"); window.scrollTo(0, 0); };
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

const nav = (to: string) => { window.location.hash = to; };

// ---- 书架 Dashboard ----
function Dashboard() {
  const [books, setBooks] = useState<BookItem[] | null>(null);
  const [error, setError] = useState("");
  const [meta, setMeta] = useState<any>(null);
  const load = useCallback(() => api.books().then((r) => setBooks(r.items)).catch((e) => setError(e.message)), []);
  useEffect(() => { load(); api.meta().then(setMeta).catch(() => {}); const t = setInterval(load, 15000); return () => clearInterval(t); }, [load]);
  if (error) return <ErrorState message={error} onRetry={load} />;
  if (!books) return <div className="space-y-4"><Card className="h-24 animate-pulse" /><Card className="h-64 animate-pulse" /></div>;
  const translating = books.filter((b) => b.status === "translating").length;
  const done = books.filter((b) => b.status === "done").length;
  return (
    <div className="space-y-6">
      {/* 欢迎横幅 */}
      <div className="relative overflow-hidden rounded-2xl bg-gradient-to-br from-brand-600 via-brand-600 to-indigo-700 px-6 py-7 text-white shadow-md">
        <div className="pointer-events-none absolute -right-8 -top-8 text-[10rem] opacity-10 select-none" aria-hidden>📖</div>
        <h1 className="text-xl font-semibold">开始你的第一次翻译</h1>
        <p className="mt-1 max-w-lg text-sm leading-relaxed text-white/80">
          上传一本 EPUB / TXT / HTML / FB2 / PDF 小说，Wenyi 会自动解析章节、维护术语表并分批翻译——中途关闭也不会丢失进度。
        </p>
        <div className="mt-4 flex flex-wrap gap-2.5">
          <Button variant="soft" className="!bg-white !text-brand-700 hover:!bg-white/90" onClick={() => nav("/new")}>＋ 新建翻译任务</Button>
          {meta && !meta.engineAvailable && <span className="self-center text-xs text-white/70">⚠ 引擎不可用，请检查设置</span>}
        </div>
      </div>
      {/* 统计条 */}
      <div className="grid grid-cols-3 gap-3">
        {[["📚 总书籍", books.length], ["⏳ 翻译中", translating], ["✅ 已完成", done]].map(([label, n]) => (
          <Card key={String(label)} className="p-4 text-center">
            <p className="text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">{String(n)}</p>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{label}</p>
          </Card>
        ))}
      </div>
      {/* 书籍网格 */}
      {books.length === 0 ? (
        <Card><EmptyState icon="📚" title="书架还是空的" hint="点击上方「新建翻译任务」，上传一本小说开始" action={<Button className="mt-2" onClick={() => nav("/new")}>＋ 新建任务</Button>} /></Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {books.map((b) => (
            <Card key={b.slug} className="group cursor-pointer p-0 transition-all hover:-translate-y-0.5 hover:shadow-md"
              onClick={() => nav(`/book/${b.slug}`)} role="link" tabIndex={0}
              onKeyDown={(e) => { if (e.key === "Enter") nav(`/book/${b.slug}`); }}>
              <div className="flex gap-4 p-4">
                {/* 书脊装饰 */}
                <div className={`flex h-20 w-14 shrink-0 items-end justify-center rounded-md pb-2 text-lg shadow-inner ${spineColor(b.slug)}`} aria-hidden>
                  <span className="writing-vertical text-xs font-bold tracking-widest text-white/90">{(b.title ?? "").slice(0, 6)}</span>
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-start justify-between gap-2">
                    <h2 className="line-clamp-2 text-sm font-semibold text-gray-900 group-hover:text-brand-600 dark:text-gray-50">{b.title}</h2>
                    <Badge tone={b.status === "done" ? "green" : b.status === "translating" ? "blue" : "gray"}>
                      {b.status === "done" ? "已完成" : b.status === "translating" ? "翻译中" : b.status === "prepared" ? "待翻译" : "未初始化"}
                    </Badge>
                  </div>
                  <div className="mt-2"><Progress done={b.chaptersDone} total={b.chaptersTotal} label={`${b.chaptersDone}/${b.chaptersTotal} 章`} /></div>
                  <p className="mt-2 text-xs text-gray-400">{b.fmt} · {b.sourceLang}→{b.targetLang}{b.updatedAt && ` · ${timeAgo(b.updatedAt)}`}</p>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

function spineColor(slug: string): string {
  const colors = ["bg-gradient-to-b from-rose-400 to-rose-600", "bg-gradient-to-b from-amber-400 to-amber-600", "bg-gradient-to-b from-emerald-400 to-emerald-600", "bg-gradient-to-b from-sky-400 to-sky-600", "bg-gradient-to-b from-violet-400 to-violet-600"];
  let h = 0; for (const c of slug) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return colors[h % colors.length];
}

function timeAgo(iso: string): string {
  const s = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

// ---- 新建任务向导 ----
function NewTask() {
  const [file, setFile] = useState<File | null>(null);
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [uploaded, setUploaded] = useState<{ sourcePath: string; slug: string } | null>(null);
  const [error, setError] = useState("");
  const [preparing, setPreparing] = useState(false);
  const [cfg, setCfg] = useState<any>(null);
  useEffect(() => { api.config().then((c) => setCfg(c.parsed)).catch(() => {}); }, []);
  const langLabel = (code: string) => LANG_NAMES[code] ?? code ?? "?";
  async function start() {
    setError("");
    if (!file) return;
    setUploading(true);
    try { setUploaded(await api.upload(file)); }
    catch (e: any) { setError(e.message); } finally { setUploading(false); }
  }
  async function prepare() {
    if (!uploaded) return;
    setPreparing(true);
    try {
      const { job } = await api.prepare(uploaded.slug, uploaded.sourcePath);
      subscribeJob(job.id, { onStatus: (d) => { if (d.status !== "running") nav(`/book/${uploaded.slug}`); } });
    } catch (e: any) { setError(e.message); setPreparing(false); }
  }
  return (
    <div className="mx-auto max-w-xl space-y-5">
      <div>
        <h1 className="text-xl font-semibold">新建翻译任务</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">两步开始：选文件 → 确认启动</p>
      </div>
      {/* 步骤指示 */}
      <div className="flex items-center gap-2 text-xs">
        {[["1", "选择文件", !!file], ["2", "上传解析", !!uploaded], ["3", "开始翻译", preparing]].map(([n, label, active], i) => (
          <React.Fragment key={String(n)}>
            {i > 0 && <div className={`h-px flex-1 ${active ? "bg-brand-500" : "bg-gray-200 dark:bg-gray-700"}`} />}
            <span className={`flex items-center gap-1.5 ${active ? "text-brand-600" : "text-gray-400"}`}>
              <span className={`flex h-5 w-5 items-center justify-center rounded-full text-[10px] font-bold ${active ? "bg-brand-600 text-white" : "bg-gray-200 text-gray-500 dark:bg-gray-700"}`}>{n}</span>{label}
            </span>
          </React.Fragment>
        ))}
      </div>
      <Card className="p-6">
        <div
          className={`flex cursor-pointer flex-col items-center gap-3 rounded-xl border-2 border-dashed p-10 text-center transition-colors ${dragOver ? "border-brand-500 bg-brand-50/50" : "border-gray-300 hover:border-brand-400 dark:border-gray-600"}`}
          onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(e) => { e.preventDefault(); setDragOver(false); const f = e.dataTransfer.files?.[0]; if (f) { setFile(f); setUploaded(null); } }}
          onClick={(e) => (e.currentTarget.querySelector("input") as HTMLInputElement)?.click()}
          role="button" aria-label="选择或拖入文件">
          <span className="text-4xl" aria-hidden>{file ? "📄" : "⬆️"}</span>
          {file ? (
            <div>
              <p className="text-sm font-medium text-gray-900 dark:text-gray-50">{file.name}</p>
              <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{(file.size / 1024).toFixed(0)} KB · 点击重新选择</p>
            </div>
          ) : (
            <div>
              <p className="text-sm font-medium text-gray-700 dark:text-gray-200">拖入文件，或点击选择</p>
              <p className="mt-1 text-xs text-gray-400">支持 EPUB / TXT / Markdown / HTML / FB2 / PDF，最大 200MB</p>
            </div>
          )}
          <input type="file" className="hidden" accept=".epub,.txt,.md,.html,.fb2,.pdf"
            onChange={(e) => { setFile(e.target.files?.[0] ?? null); setUploaded(null); }} />
        </div>
        {/* 当前配置摘要 */}
        {cfg && (
          <div className="mt-4 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <Badge tone="brand">{langLabel(cfg?.language?.source)} → {langLabel(cfg?.language?.target)}</Badge>
            <Badge>{cfg?.llm?.provider === "fake" ? "离线体验模式" : cfg?.llm?.provider}</Badge>
            <button className="text-brand-600 hover:underline" onClick={() => nav("/settings")}>修改设置 →</button>
          </div>
        )}
        {cfg?.llm?.provider === "fake" && (
          <p className="mt-2 text-xs text-amber-600">⚠ 当前为离线体验模式，译文是假数据。要翻译真实书籍请先到 <button className="underline" onClick={() => nav("/settings")}>设置</button> 配置模型。</p>
        )}
        {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
        {uploaded && (
          <div className="mt-4 rounded-lg bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
            ✓ 解析就绪：<b>{uploaded.slug}</b>。点击开始后 Wenyi 会分析全书风格并建立术语表。
          </div>
        )}
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="secondary" onClick={() => nav("/")}>取消</Button>
          {uploaded ? (
            <Button size="lg" onClick={prepare} disabled={preparing}>{preparing ? <><Spinner /> 正在准备…</> : "🚀 开始翻译"}</Button>
          ) : (
            <Button size="lg" onClick={start} disabled={!file || uploading}>{uploading ? <><Spinner /> 上传中…</> : "上传文件"}</Button>
          )}
        </div>
      </Card>
    </div>
  );
}

const LANG_NAMES: Record<string, string> = { auto: "自动检测", ja: "日语", en: "英语", ko: "韩语", ru: "俄语", fr: "法语", de: "德语", es: "西班牙语", it: "意大利语", pt: "葡萄牙语", zh: "中文" };

// ---- 书籍详情 ----
const TABS = [
  ["translate", "翻译"], ["glossary", "术语"], ["review", "审校"], ["qa", "QA"],
  ["export", "导出"], ["usage", "用量"], ["events", "事件"],
] as const;
type Tab = (typeof TABS)[number][0];

function BookView({ slug }: { slug: string }) {
  const [tab, setTab] = useState<Tab>("translate");
  const [book, setBook] = useState<any>(null);
  const [error, setError] = useState("");
  useEffect(() => { api.book(slug).then(setBook).catch((e) => setError(e.message)); }, [slug]);
  if (error) return <ErrorState message={error} onRetry={() => nav("/")} />;
  if (!book) return <div className="space-y-4"><Card className="h-16 animate-pulse" /><Card className="h-96 animate-pulse" /></div>;
  const m = book.manifest;
  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="ghost" size="sm" onClick={() => nav("/")}>← 书架</Button>
        <h1 className="min-w-0 flex-1 truncate text-lg font-semibold">{m.title}</h1>
        <Badge>{m.fmt}</Badge>
        <Badge tone="gray">{m.source_lang}→{m.target_lang}</Badge>
      </div>
      <div className="flex gap-1 overflow-x-auto rounded-xl border border-gray-200 bg-gray-50 p-1 dark:border-gray-800 dark:bg-gray-900" role="tablist">
        {TABS.map(([t, label]) => (
          <button key={t} role="tab" aria-selected={tab === t}
            className={`whitespace-nowrap rounded-lg px-4 py-1.5 text-sm font-medium transition-all ${tab === t ? "bg-white text-brand-600 shadow-sm dark:bg-gray-800" : "text-gray-500 hover:text-gray-800 dark:hover:text-gray-200"}`}
            onClick={() => setTab(t)}>{label}</button>
        ))}
      </div>
      {tab === "translate" && <TranslateTab slug={slug} />}
      {tab === "glossary" && <GlossaryTab slug={slug} />}
      {tab === "review" && <ReviewTab slug={slug} />}
      {tab === "qa" && <ReportTab slug={slug} />}
      {tab === "export" && <ExportTab slug={slug} />}
      {tab === "usage" && <UsageTab slug={slug} />}
      {tab === "events" && <EventsTab slug={slug} />}
    </div>
  );
}

// ---- 实时翻译 ----
function TranslateTab({ slug }: { slug: string }) {
  const [status, setStatus] = useState<any>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [logs, setLogs] = useState("");
  const [events, setEvents] = useState<any[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(() => api.bookStatus(slug).then(setStatus).catch((e) => setError(e.message)), [slug]);
  useEffect(() => { load(); }, [load]);
  useEffect(() => {
    if (!job) return;
    const unsub = subscribeJob(job.id, {
      onStatus: (d) => { setJob((j) => (j ? { ...j, status: d.status, exitCode: d.exitCode } : j)); if (d.status !== "running") load(); },
      onProgress: (p) => setJob((j) => (j ? { ...j, progress: p } : j)),
      onLog: (l) => setLogs((prev) => (prev + l.chunk).slice(-8000)),
    });
    return unsub;
  }, [job?.id, load]);
  useEffect(() => subscribeBook(slug, (ev) => setEvents((prev) => [...prev.slice(-200), ev])), [slug]);
  async function start(kind: "translate" | "review" | "qa" | "report") {
    setBusy(true); setError(""); setLogs("");
    try { const { job: j } = await (api as any)[kind](slug, {}); setJob(j); }
    catch (e: any) { setError(e.message); } finally { setBusy(false); }
  }
  const running = job?.status === "running";
  const chapters = status?.chapters ?? [];
  const done = chapters.filter((c: any) => c.status === "done").length;
  return (
    <div className="grid grid-cols-1 gap-5 xl:grid-cols-3">
      <div className="space-y-4">
        <Card className="p-5">
          <h3 className="mb-1 text-sm font-semibold">翻译操作</h3>
          <p className="mb-4 text-xs leading-relaxed text-gray-500 dark:text-gray-400">中断随时可续——已翻译的章节和批次不会重做。</p>
          <div className="space-y-2">
            <Button className="w-full" size="lg" onClick={() => start("translate")} disabled={busy || running}>
              {running ? <Spinner /> : "▶"} {done > 0 ? "继续翻译" : "开始翻译"}
            </Button>
            <div className="grid grid-cols-2 gap-2">
              <Button variant="secondary" onClick={() => start("review")} disabled={busy || running}>🔍 审校</Button>
              <Button variant="secondary" onClick={() => start("qa")} disabled={busy || running}>✓ 一致性 QA</Button>
            </div>
            {running && <Button variant="danger" className="w-full" onClick={() => job && api.cancel(job.id)}>■ 取消任务</Button>}
          </div>
          {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
        </Card>
        {job && job.progress && running && (
          <Card className="p-5"><Progress {...job.progress} /></Card>
        )}
        {job && !running && (
          <Card className={`p-4 text-sm ${job.status === "succeeded" ? "text-emerald-600" : job.status === "failed" ? "text-red-600" : "text-gray-500 dark:text-gray-400"}`}>
            {job.status === "succeeded" ? "✓ 任务完成" : job.status === "failed" ? `✗ 任务失败（退出码 ${job.exitCode}）` : "已取消"}
            {job.status === "failed" && <button className="ml-2 text-xs underline" onClick={() => api.job(job.id).then((r) => setLogs(r.job.logTail ?? ""))}>查看日志</button>}
          </Card>
        )}
        <Card className="p-5">
          <h3 className="mb-3 text-sm font-semibold">章节进度 <span className="ml-1 text-xs font-normal text-gray-400">{done}/{chapters.length}</span></h3>
          <div className="max-h-80 space-y-0.5 overflow-y-auto">
            {chapters.map((c: any) => (
              <div key={c.index} className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm">
                <span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[10px] ${c.status === "done" ? "bg-emerald-100 text-emerald-600 dark:bg-emerald-900/40" : "bg-gray-100 text-gray-400 dark:bg-gray-800"}`}>
                  {c.status === "done" ? "✓" : c.index + 1}
                </span>
                <span className={`truncate ${c.status === "done" ? "text-gray-700 dark:text-gray-200" : "text-gray-400"}`}>{c.title || `章节 ${c.index + 1}`}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>
      <div className="space-y-4 xl:col-span-2">
        <Card className="flex h-72 flex-col p-0">
          <div className="flex items-center justify-between border-b border-gray-100 px-5 py-3 dark:border-gray-800">
            <h3 className="text-sm font-semibold">实时事件流</h3>
            <Badge tone={running ? "green" : "gray"}>{running ? "● 运行中" : "空闲"}</Badge>
          </div>
          <div className="flex-1 space-y-1 overflow-y-auto p-3" aria-live="polite">
            {events.length === 0 ? <EmptyState icon="📡" title="等待事件…" hint="翻译过程中的每一步（批次完成、术语抽取等）会实时出现在这里" /> : events.slice().reverse().map((ev, i) => (
              <div key={i} className="rounded-md bg-gray-50 px-2.5 py-1.5 font-mono text-xs dark:bg-gray-800/60">
                <span className="font-semibold text-brand-600">{EVENT_NAMES[ev.event] ?? ev.event}</span>
                {ev.chapter != null && <span className="ml-2 text-gray-400">第 {Number(ev.chapter) + 1} 章</span>}
                {ev.detail && <span className="ml-2 text-gray-500 dark:text-gray-400">{String(ev.detail).slice(0, 90)}</span>}
              </div>
            ))}
          </div>
        </Card>
        <Card className="p-0">
          <div className="border-b border-gray-100 px-5 py-3 dark:border-gray-800"><h3 className="text-sm font-semibold">任务日志</h3></div>
          <pre className="max-h-56 overflow-y-auto whitespace-pre-wrap p-4 text-xs leading-relaxed text-gray-600 dark:text-gray-300">{logs || "（暂无输出）"}</pre>
        </Card>
      </div>
    </div>
  );
}

const EVENT_NAMES: Record<string, string> = {
  run_initialized: "初始化完成", run_resumed: "续跑恢复", analysis_saved: "风格分析完成",
  language_detected: "语言识别", language_detection_failed: "语言识别失败",
  book_understanding_skipped: "跳过预读", book_understanding_chapter_digest_started: "开始章节梗概",
  book_understanding_chapter_digest_saved: "章节梗概完成", book_synopsis_saved: "全书概览完成",
  translate_run_started: "翻译开始", translate_run_finished: "翻译结束",
  batch_translated: "批次翻译完成", batch_skipped: "批次跳过（已译）",
  batch_glossary_extracted: "批次术语抽取", chapter_glossary_extracted: "章节术语抽取",
  chapter_backtranslation_checked: "回译抽检", chapter_done: "章节完成", chapter_skipped: "空章节跳过",
  titles_translated: "标题翻译完成", titles_skipped: "标题跳过",
  titles_translation_failed: "标题翻译失败", titles_translation_rejected: "标题翻译被拒",
  annotation_alignment_completed: "注释对齐完成", annotation_alignment_failed: "注释对齐失败", annotation_alignment_skipped: "注释对齐跳过",
  usage_summary: "用量统计", review_started: "审校开始", review_finished: "审校结束",
  run_steps_started: "步骤开始", run_steps_finished: "步骤结束",
  consistency_qa_finished: "QA 完成", report_saved: "报告保存", assembled: "导出完成",
  translation_prepared: "准备完成", llm_retry_wait: "模型重试等待", llm_retry_exhausted: "模型重试耗尽",
};

// ---- 术语工作台 ----
function GlossaryTab({ slug }: { slug: string }) {
  const [q, setQ] = useState("");
  const [data, setData] = useState<any>(null);
  const [conflicts, setConflicts] = useState<any>(null);
  const [error, setError] = useState("");
  const [resolving, setResolving] = useState<string | null>(null);
  const load = useCallback(() => {
    api.glossary(slug, q || undefined).then(setData).catch((e) => setError(e.message));
    api.conflicts(slug).then(setConflicts).catch(() => {});
  }, [slug, q]);
  useEffect(() => { const t = setTimeout(load, 200); return () => clearTimeout(t); }, [load]);
  async function resolve(source: string, target: string) {
    setResolving(source);
    try { await api.resolve(slug, source, target); load(); } catch (e: any) { alert(e.message); } finally { setResolving(null); }
  }
  if (error) return <ErrorState message={error} onRetry={load} />;
  return (
    <div className="space-y-5">
      {(conflicts?.items?.length ?? 0) > 0 && (
        <Card className="border-amber-200 p-5 dark:border-amber-800">
          <h3 className="mb-1 text-sm font-semibold text-amber-700 dark:text-amber-300">⚠ 需要你裁决的译法冲突（{conflicts.items.length}）</h3>
          <p className="mb-4 text-xs leading-relaxed text-gray-500 dark:text-gray-400">同一术语在不同章节被译成了不同写法。选择一个全书统一的版本：</p>
          <div className="space-y-2.5">
            {conflicts.items.map((c: any) => (
              <div key={c.id} className="flex flex-col gap-2.5 rounded-xl border border-amber-100 bg-amber-50/60 p-4 dark:border-amber-900/50 dark:bg-amber-900/20 sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="font-mono text-sm font-semibold">{c.source}</p>
                  <div className="mt-1.5 flex flex-wrap items-center gap-2 text-sm">
                    <span className="rounded-md bg-white px-2.5 py-1 shadow-sm dark:bg-gray-800"><b>{c.existing_target}</b> <span className="text-xs text-gray-400">现有</span></span>
                    <span className="text-gray-400">vs</span>
                    <span className="rounded-md bg-white px-2.5 py-1 shadow-sm dark:bg-gray-800"><b>{c.proposed_target}</b> <span className="text-xs text-gray-400">新提议</span></span>
                  </div>
                  <p className="mt-1 text-xs text-gray-400">出现在第 {(c.chapter ?? 0) + 1} 章</p>
                </div>
                <div className="flex shrink-0 gap-2">
                  <Button size="sm" variant="secondary" disabled={resolving === c.source} onClick={() => resolve(c.source, c.existing_target)}>保留现有</Button>
                  <Button size="sm" disabled={resolving === c.source} onClick={() => resolve(c.source, c.proposed_target)}>采用新提议</Button>
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}
      <Card className="p-0">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-3.5 dark:border-gray-800">
          <h3 className="text-sm font-semibold">术语表 <Badge tone="brand">{data?.stats?.terms ?? 0} 条</Badge></h3>
          <div className="relative">
            <input className="w-60 rounded-lg border border-gray-200 bg-gray-50 py-2 pl-9 pr-3 text-sm placeholder-gray-400 focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/20 dark:border-gray-700 dark:bg-gray-950"
              placeholder="搜索原文或译文…" value={q} onChange={(e) => setQ(e.target.value)} aria-label="搜索术语" />
            <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" aria-hidden>🔍</span>
          </div>
        </div>
        {!data ? <SkeletonList rows={6} /> : data.items.length === 0 ? (
          <EmptyState icon="🔤" title={q ? "没有匹配的术语" : "术语表还是空的"} hint={q ? "换个关键词试试" : "翻译过程中会自动从书中抽取人名、地名等术语"} />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-gray-100 text-left text-xs text-gray-400 dark:border-gray-800">
                <th className="px-5 py-2.5 font-medium">原文</th><th className="py-2.5 pr-4 font-medium">译文</th>
                <th className="py-2.5 pr-4 font-medium">类型</th><th className="py-2.5 pr-5 font-medium">状态</th>
              </tr></thead>
              <tbody>
                {data.items.map((t: any) => (
                  <tr key={t.source} className="border-b border-gray-50 transition-colors last:border-0 hover:bg-gray-50/60 dark:border-gray-800/60 dark:hover:bg-gray-800/40">
                    <td className="px-5 py-2.5 font-mono font-medium">{t.source}</td>
                    <td className="py-2.5 pr-4">{t.target}</td>
                    <td className="py-2.5 pr-4 text-gray-500 dark:text-gray-400">{t.type}{t.gender ? ` · ${t.gender}` : ""}{t.aliases?.length ? ` · 又名 ${t.aliases.join("/")}` : ""}</td>
                    <td className="py-2.5 pr-5"><Badge tone={t.status === "ok" ? "green" : "amber"}>{t.status === "ok" ? "✓" : "⚠"} {t.status === "ok" ? "正常" : "冲突"}</Badge></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}

// ---- 审校中心 ----
function ReviewTab({ slug }: { slug: string }) {
  const [reviews, setReviews] = useState<any[] | null>(null);
  const [selected, setSelected] = useState<any>(null);
  useEffect(() => { api.reviews(slug).then((r) => setReviews(r.items)).catch(() => setReviews([])); }, [slug]);
  return (
    <div className="grid grid-cols-1 gap-5 lg:grid-cols-3">
      <Card className="lg:col-span-1 p-0">
        <div className="border-b border-gray-100 px-4 py-3.5 dark:border-gray-800"><h3 className="text-sm font-semibold">审校历史</h3></div>
        {!reviews ? <SkeletonList /> : reviews.length === 0 ? <EmptyState icon="🔍" title="尚无审校记录" hint="在「翻译」页点击「审校」按钮开始全书审校" /> :
          <div className="max-h-[30rem] overflow-y-auto">
            {reviews.map((r) => (
              <button key={r.id} className={`block w-full border-b border-gray-50 px-4 py-3 text-left transition-colors last:border-0 hover:bg-gray-50 dark:border-gray-800/60 dark:hover:bg-gray-800 ${selected?.review_id === r.id ? "bg-brand-50/50 dark:bg-brand-900/20" : ""}`}
                onClick={() => api.reviewResult(slug, r.id).then(setSelected)}>
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono text-xs text-gray-500 dark:text-gray-400">{r.id.slice(7, 23)}</span>
                  <Badge tone={r.status === "completed" ? "green" : "red"}>{r.status === "completed" ? "完成" : "失败"}</Badge>
                </div>
                <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">{TERM_NAMES[r.termination] ?? r.termination} · {r.issueCount} 问题 · {r.changeCount} 修改建议</p>
              </button>
            ))}
          </div>}
      </Card>
      <Card className="p-5 lg:col-span-2">
        {!selected ? <EmptyState icon="👈" title="选择一次审校查看详情" hint="问题清单与修改建议会显示在这里" /> : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone="blue">{TERM_NAMES[selected.termination] ?? selected.termination}</Badge>
              {selected.summary && <>
                <Badge>🔍 {selected.summary.review_round_count ?? "?"} 轮审校</Badge>
                <Badge>🩹 {selected.summary.patch_count ?? 0} 个补丁</Badge>
                <Badge>🔁 {selected.summary.fix_round_count ?? 0} 轮修复</Badge>
                <Badge>⏱ {selected.summary.clean_streak ?? 0} 连续干净</Badge>
              </>}
            </div>
            {[
              ["issues", "发现的问题", (selected.issues ?? []).length, "每条问题附带了修改建议，可按建议手动修订译文"],
              ["changes", "修改建议", (selected.changes ?? []).length, "审校生成的建议替换——目前为只读建议，不自动改动正文"],
            ].map(([key, title, count, hint]) => (
              <div key={String(key)}>
                <h4 className="mb-1.5 text-sm font-semibold">{title} <span className="ml-1 text-xs font-normal text-gray-400">{count}</span></h4>
                <p className="mb-2 text-xs text-gray-400">{hint}</p>
                <div className="max-h-56 overflow-y-auto rounded-lg border border-gray-100 dark:border-gray-800">
                  {count === 0 ? <p className="px-4 py-6 text-center text-xs text-gray-400">无</p> :
                    (selected[key] ?? []).slice(0, 60).map((item: any, i: number) => (
                      <div key={i} className="border-b border-gray-50 px-4 py-2.5 text-sm last:border-0 dark:border-gray-800/60">
                        <span className="mr-2 text-xs text-gray-400">第 {Number(item.chapter) + 1} 章 · 段 {item.index}{item.type && ` · ${item.type}`}</span>
                        <p className="mt-0.5">{item.detail ?? item.suggested_target}</p>
                        {item.suggestion && item.detail && <p className="mt-0.5 text-xs text-brand-600">建议：{item.suggestion}</p>}
                      </div>
                    ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

const TERM_NAMES: Record<string, string> = {
  clean_confirmed: "✓ 连续确认通过", max_rounds: "达到轮数上限", no_progress: "无进展",
  cycle_detected: "检测到振荡", issues_reported: "存在问题", unresolved_fixes: "有未修复问题", error: "出错",
  not_started: "未开始",
};

// ---- QA 报告 ----
function ReportTab({ slug }: { slug: string }) {
  const [report, setReport] = useState<any>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  useEffect(() => { setLoading(true); api.reportJson(slug).then(setReport).catch((e) => setError(e.message)).finally(() => setLoading(false)); }, [slug]);
  if (loading) return <div className="space-y-4"><Card className="h-28 animate-pulse" /><Card className="h-48 animate-pulse" /></div>;
  if (error) return <ErrorState message={error} onRetry={() => setError("")} />;
  const s = report.summary ?? {};
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {[["📚", "章节", `${s.chapters_done}/${s.chapters_total}`], ["🔤", "术语", s.terms ?? 0],
          ["ⓘ", "空译文", s.empty_targets ?? 0], ["🔁", "回译问题", s.backtranslation_issues ?? 0]].map(([icon, label, value]) => (
          <Card key={String(label)} className="p-4">
            <p className="text-xs text-gray-400">{icon} {label}</p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-gray-900 dark:text-white">{String(value)}</p>
          </Card>
        ))}
      </div>
      {report.review && (
        <Card className="flex flex-wrap items-center gap-3 p-4">
          <Badge tone="blue">最近审校</Badge>
          <span className="text-sm">{TERM_NAMES[report.review.termination] ?? report.review.termination}</span>
          <span className="text-xs text-gray-400">{report.review.issue_count} 问题 · {report.review.change_count} 修改建议 · <button className="text-brand-600 hover:underline" onClick={() => { /* 切到审校页由父组件处理 */ }}>查看详情</button></span>
        </Card>
      )}
      <Card className="p-5">
        <h3 className="mb-3 text-sm font-semibold">跨章一致性问题 <Badge tone="amber">{(report.consistency_issues ?? []).length}</Badge></h3>
        {(report.consistency_issues ?? []).length === 0 ? <EmptyState icon="✅" title="未发现一致性问题" hint="全书术语译法与人称保持一致" /> :
          (report.consistency_issues ?? []).map((iss: any, i: number) => (
            <div key={i} className="border-b border-gray-50 py-2.5 text-sm last:border-0 dark:border-gray-800/60">
              <Badge tone="amber">{iss.type}</Badge> <span className="ml-1">{iss.detail}</span>
              {iss.where && <span className="ml-2 text-xs text-gray-400">{iss.where}</span>}
            </div>
          ))}
      </Card>
    </div>
  );
}

// ---- 导出中心 ----
function ExportTab({ slug }: { slug: string }) {
  const [exports, setExports] = useState<any[] | null>(null);
  const [form, setForm] = useState("epub");
  const [bilingual, setBilingual] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(() => api.exports(slug).then((r) => setExports(r.items)).catch((e) => setError(e.message)), [slug]);
  useEffect(() => { load(); }, [load]);
  async function assemble() {
    setBusy(true); setError("");
    try {
      const { job } = await api.assemble(slug, { format: form, bilingual: bilingual ? true : null, mono: bilingual ? null : true });
      subscribeJob(job.id, { onStatus: (d) => { if (d.status !== "running") { load(); setBusy(false); } } });
    } catch (e: any) { setError(e.message); setBusy(false); }
  }
  return (
    <div className="space-y-5">
      <Card className="p-5">
        <h3 className="mb-1 text-sm font-semibold">生成导出文件</h3>
        <p className="mb-4 text-xs text-gray-500 dark:text-gray-400">从当前翻译状态组装产物，不会调用模型。</p>
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex rounded-lg border border-gray-200 p-0.5 dark:border-gray-700" role="radiogroup" aria-label="格式">
            {["epub", "txt", "html", "markdown"].map((f) => (
              <button key={f} role="radio" aria-checked={form === f}
                className={`rounded-md px-3.5 py-1.5 text-sm font-medium transition-all ${form === f ? "bg-brand-600 text-white shadow-sm" : "text-gray-500 hover:text-gray-800 dark:hover:text-gray-200"}`}
                onClick={() => setForm(f)}>{f.toUpperCase()}</button>
            ))}
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
            <input type="checkbox" className="h-4 w-4 rounded accent-brand-600" checked={bilingual} onChange={(e) => setBilingual(e.target.checked)} />
            双语对照版
          </label>
          <Button onClick={assemble} disabled={busy}>{busy ? <><Spinner /> 生成中…</> : "📦 生成文件"}</Button>
        </div>
        {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
      </Card>
      <Card className="p-0">
        <div className="border-b border-gray-100 px-5 py-3.5 dark:border-gray-800">
          <h3 className="text-sm font-semibold">已生成的文件 <Badge tone="brand">{exports?.length ?? 0}</Badge></h3>
        </div>
        {!exports ? <SkeletonList rows={2} /> : exports.length === 0 ? <EmptyState icon="📦" title="暂无导出文件" hint="点击上方「生成文件」创建第一个产物" /> :
          <div>
            {exports.map((e) => (
              <a key={e.name} href={`/api/v1/books/${slug}/exports/${encodeURIComponent(e.name)}`}
                className="flex items-center gap-4 border-b border-gray-50 px-5 py-3.5 text-sm transition-colors last:border-0 hover:bg-gray-50 dark:border-gray-800/60 dark:hover:bg-gray-800/40">
                <span className="flex h-10 w-10 items-center justify-center rounded-lg bg-brand-50 text-lg dark:bg-brand-900/40" aria-hidden>
                  {e.format === "epub" ? "📗" : e.format === "pdf" ? "📕" : e.format === "txt" ? "📄" : "🌐"}
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">{e.name}</p>
                  <p className="text-xs text-gray-400">{(e.size / 1024).toFixed(0)} KB · {new Date(e.mtime).toLocaleString("zh-CN")}</p>
                </div>
                {e.bilingual && <Badge tone="blue">双语</Badge>}
                <span className="text-xs font-medium text-brand-600">下载 ↓</span>
              </a>
            ))}
          </div>}
      </Card>
    </div>
  );
}

// ---- 用量统计 ----
function UsageTab({ slug }: { slug: string }) {
  const [usage, setUsage] = useState<any>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => { setLoading(true); api.usage(slug).then(setUsage).catch(() => setUsage(null)).finally(() => setLoading(false)); }, [slug]);
  if (loading) return <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">{[0, 1, 2, 3].map((i) => <Card key={i} className="h-24 animate-pulse" />)}</div>;
  if (!usage) return <Card><EmptyState icon="📊" title="暂无用量数据" hint="翻译完成后可在此查看 token 消耗统计" /></Card>;
  const t = usage.totals ?? {};
  const stages = Object.entries(usage.by_stage ?? {}).sort((a: any, b: any) => (b[1].total_tokens ?? 0) - (a[1].total_tokens ?? 0));
  const maxTokens = Math.max(...stages.map(([, v]: any) => v.total_tokens ?? 0), 1);
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {[
          ["📞", "总调用次数", (t.calls ?? 0).toLocaleString()],
          ["⬆️", "输入 tokens", (t.prompt_tokens ?? 0).toLocaleString()],
          ["⬇️", "输出 tokens", (t.completion_tokens ?? 0).toLocaleString()],
          ["⚡", "缓存命中率", `${((t.cache_hit_rate ?? 0) * 100).toFixed(1)}%`],
        ].map(([icon, label, value]) => (
          <Card key={label} className="p-4">
            <p className="text-xs text-gray-400">{icon} {label}</p>
            <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
          </Card>
        ))}
      </div>
      <Card className="p-5">
        <h3 className="mb-4 text-sm font-semibold">按环节分布</h3>
        {stages.length === 0 ? <p className="text-xs text-gray-400">暂无数据</p> : stages.map(([stage, v]: any) => (
          <div key={stage} className="mb-3.5 last:mb-0">
            <div className="mb-1 flex items-baseline justify-between text-xs">
              <span className="font-medium">{STAGE_NAMES[stage] ?? stage}</span>
              <span className="tabular-nums text-gray-400">{(v.total_tokens ?? 0).toLocaleString()} tokens · {v.calls ?? 0} 次</span>
            </div>
            <div className="h-2.5 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
              <div className="h-full rounded-full bg-gradient-to-r from-brand-400 to-brand-600 transition-all duration-700" style={{ width: `${Math.max(((v.total_tokens ?? 0) / maxTokens) * 100, 2)}%` }} />
            </div>
          </div>
        ))}
      </Card>
    </div>
  );
}

const STAGE_NAMES: Record<string, string> = {
  Translator: "翻译", Polisher: "润色", Reviewer: "审校", Analyzer: "风格分析", Synopsizer: "全书梗概",
  GlossaryExtractor: "术语抽取", AnnotationAligner: "注释对齐", ReviewAgentLoop: "审校取证", ReviewConflictArbiter: "冲突仲裁",
  ReviewFixer: "审校修订", BackTranslator: "回译抽检", ConsistencyChecker: "一致性检查", language_detect: "语言识别",
  title_translate: "标题翻译",
};

// ---- 事件日志 ----
function EventsTab({ slug }: { slug: string }) {
  const [events, setEvents] = useState<any[] | null>(null);
  const [filter, setFilter] = useState("");
  useEffect(() => {
    let cursor = 0; let alive = true;
    (async function loadAll() {
      try {
        const r = await api.events(slug, cursor, 500);
        if (!alive) return;
        setEvents((prev) => [...(prev ?? []), ...r.items]);
        cursor = r.nextCursor;
        if (cursor < r.total) loadAll();
      } catch { setEvents([]); }
    })();
    const unsub = subscribeBook(slug, (ev) => setEvents((prev) => [...(prev ?? []), ev]));
    return () => { alive = false; unsub(); };
  }, [slug]);
  const filtered = (events ?? []).filter((e) => !filter || (e.event ?? "").includes(filter));
  return (
    <Card className="p-0">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-3.5 dark:border-gray-800">
        <h3 className="text-sm font-semibold">事件日志 <Badge tone="brand">{filtered.length}</Badge></h3>
        <div className="relative">
          <input className="w-56 rounded-lg border border-gray-200 bg-gray-50 py-2 pl-9 pr-3 text-sm placeholder-gray-400 focus:border-brand-500 focus:outline-none dark:border-gray-700 dark:bg-gray-950"
            placeholder="过滤事件名…" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="过滤事件" />
          <span className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" aria-hidden>🔍</span>
        </div>
      </div>
      {!events ? <SkeletonList rows={8} /> : filtered.length === 0 ? <EmptyState icon="📜" title={filter ? "无匹配事件" : "暂无事件"} /> : (
        <div className="max-h-[34rem] overflow-y-auto">
          {filtered.slice().reverse().map((e, i) => (
            <div key={i} className="flex items-baseline gap-3 border-b border-gray-50 px-5 py-2 font-mono text-xs last:border-0 dark:border-gray-800/60">
              <span className="shrink-0 text-gray-300 dark:text-gray-600">{e.ts ?? ""}</span>
              <span className="shrink-0 font-semibold text-brand-600">{EVENT_NAMES[e.event] ?? e.event}</span>
              <span className="min-w-0 truncate text-gray-500 dark:text-gray-400">{JSON.stringify({ ...e, ts: undefined, event: undefined, line: undefined }).slice(0, 140)}</span>
            </div>
          ))}
        </div>
      )}
    </Card>
  );
}

// ---- 命令面板（Ctrl+K）----
function CommandPalette({ onClose, books }: { onClose: () => void; books: BookItem[] }) {
  const [q, setQ] = useState("");
  const actions = [
    { icon: "📚", label: "打开书架", run: () => nav("/") },
    { icon: "➕", label: "新建翻译任务", run: () => nav("/new") },
    { icon: "⚙️", label: "设置", run: () => nav("/settings") },
    ...books.map((b) => ({ icon: "📖", label: `打开《${b.title}》`, run: () => nav(`/book/${b.slug}`) })),
  ].filter((a) => !q || a.label.toLowerCase().includes(q.toLowerCase()));
  const [idx, setIdx] = useState(0);
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/50 pt-[15vh] backdrop-blur-sm" onClick={onClose} role="dialog" aria-label="命令面板">
      <div className="w-[30rem] max-w-[92vw] overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl dark:border-gray-700 dark:bg-gray-900"
        onClick={(e) => e.stopPropagation()}>
        <input autoFocus className="w-full border-b border-gray-100 bg-transparent px-4 py-3.5 text-sm outline-none placeholder:text-gray-400 dark:border-gray-800"
          placeholder="搜索命令或书名…" value={q}
          onChange={(e) => { setQ(e.target.value); setIdx(0); }}
          onKeyDown={(e) => {
            if (e.key === "Escape") onClose();
            if (e.key === "ArrowDown") { e.preventDefault(); setIdx((i) => Math.min(i + 1, actions.length - 1)); }
            if (e.key === "ArrowUp") { e.preventDefault(); setIdx((i) => Math.max(i - 1, 0)); }
            if (e.key === "Enter" && actions[idx]) { actions[idx].run(); onClose(); }
          }} />
        <div className="max-h-72 overflow-y-auto p-2">
          {actions.length === 0 ? <p className="px-3 py-6 text-center text-xs text-gray-400">没有匹配项</p> : actions.map((a, i) => (
            <button key={i} className={`flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-left text-sm ${i === idx ? "bg-brand-50 text-brand-700 dark:bg-brand-900/30 dark:text-brand-300" : "hover:bg-gray-50 dark:hover:bg-gray-800"}`}
              onMouseEnter={() => setIdx(i)} onClick={() => { a.run(); onClose(); }}>
              <span aria-hidden>{a.icon}</span>{a.label}
            </button>
          ))}
        </div>
        <div className="border-t border-gray-100 px-4 py-2 text-[10px] text-gray-400 dark:border-gray-800">↑↓ 选择 · Enter 确认 · Esc 关闭</div>
      </div>
    </div>
  );
}

// ---- App 根：侧边栏布局 ----
function App() {
  const route = useHashRoute();
  const { theme, toggle } = useTheme();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [books, setBooks] = useState<BookItem[]>([]);
  const [meta, setMeta] = useState<any>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  useEffect(() => { api.books().then((r) => setBooks(r.items)).catch(() => {}); api.meta().then(setMeta).catch(() => {}); }, [route]);
  useEffect(() => {
    const on = (e: KeyboardEvent) => { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setPaletteOpen((o) => !o); } };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);
  const slug = route.startsWith("/book/") ? decodeURIComponent(route.slice(6)) : null;
  const current = slug ? books.find((b) => b.slug === slug) : null;
  const navItems = [
    { icon: "📚", label: "书架", to: "/", active: route === "/" },
    { icon: "➕", label: "新建任务", to: "/new", active: route === "/new" },
    { icon: "⚙️", label: "设置", to: "/settings", active: route === "/settings" },
    ...(current ? [{ icon: "📖", label: current.title.slice(0, 8), to: `/book/${slug}`, active: true }] : []),
  ];
  return (
    <div className="flex min-h-screen bg-gray-50 text-gray-900 dark:bg-gray-950 dark:text-gray-100">
      {/* 侧边栏 */}
      <aside className={`fixed inset-y-0 left-0 z-40 flex w-60 flex-col border-r border-gray-200 bg-white transition-transform dark:border-gray-800 dark:bg-gray-900 lg:sticky lg:top-0 lg:h-screen lg:translate-x-0 ${sidebarOpen ? "translate-x-0" : "-translate-x-full"}`}>
        <button className="flex items-center gap-2.5 px-5 py-5 text-left" onClick={() => { nav("/"); setSidebarOpen(false); }}>
          <span className="flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-brand-500 to-indigo-600 text-lg text-white shadow-sm" aria-hidden>📖</span>
          <div><p className="text-[15px] font-bold leading-tight">Wenyi</p><p className="text-[10px] text-gray-400">小说翻译工作台</p></div>
        </button>
        <nav className="flex-1 space-y-1 overflow-y-auto px-3" aria-label="主导航">
          {navItems.map((item) => (
            <button key={item.to}
              className={`flex w-full items-center gap-3 rounded-lg px-3 py-2.5 text-sm font-medium transition-colors ${item.active ? "bg-brand-50 text-brand-700 dark:bg-brand-900/30 dark:text-brand-300" : "text-gray-600 hover:bg-gray-50 hover:text-gray-900 dark:text-gray-300 dark:hover:bg-gray-800 dark:hover:text-white"}`}
              onClick={() => { nav(item.to); setSidebarOpen(false); }}>
              <span aria-hidden>{item.icon}</span><span className="truncate">{item.label}</span>
            </button>
          ))}
        </nav>
        <div className="space-y-2 border-t border-gray-100 p-3 dark:border-gray-800">
          <button className="flex w-full items-center justify-between rounded-lg px-3 py-2 text-xs text-gray-500 hover:bg-gray-50 dark:hover:bg-gray-800"
            onClick={() => setPaletteOpen(true)}>
            <span>搜索…</span><kbd className="rounded border border-gray-200 px-1.5 py-0.5 font-mono text-[10px] dark:border-gray-700">Ctrl K</kbd>
          </button>
          <div className="flex items-center justify-between rounded-lg px-3 py-2">
            <span className="flex items-center gap-1.5 text-xs text-gray-500 dark:text-gray-400">
              <span className={`h-2 w-2 rounded-full ${meta?.engineAvailable ? "bg-emerald-500" : "bg-red-400"}`} aria-hidden />
              引擎 {meta?.engineVersion ?? "…"}
            </span>
            <button onClick={toggle} className="rounded-lg p-1.5 text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800" aria-label="切换深浅主题">
              {theme === "dark" ? "☀️" : "🌙"}
            </button>
          </div>
        </div>
      </aside>
      {/* 移动端遮罩 */}
      {sidebarOpen && <div className="fixed inset-0 z-30 bg-black/40 lg:hidden" onClick={() => setSidebarOpen(false)} aria-hidden />}
      {/* 主内容 */}
      <div className="min-w-0 flex-1">
        <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-gray-200/70 bg-gray-50/80 px-4 py-3 backdrop-blur dark:border-gray-800/70 dark:bg-gray-950/80 lg:hidden">
          <button className="rounded-lg p-1.5 text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800" onClick={() => setSidebarOpen(true)} aria-label="打开菜单">☰</button>
          <span className="text-sm font-semibold">Wenyi</span>
        </header>
        <main className="mx-auto max-w-6xl px-4 py-6 lg:px-8">
          {slug ? <BookView slug={slug} /> : route === "/new" ? <NewTask /> : route === "/settings" ? <SettingsForm /> : <Dashboard />}
        </main>
      </div>
      {paletteOpen && <CommandPalette onClose={() => setPaletteOpen(false)} books={books} />}
    </div>
  );
}

export { App };
createRoot(document.getElementById("root")!).render(<App />);

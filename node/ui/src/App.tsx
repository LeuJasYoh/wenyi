// App.tsx —— WebUI 页面全集（墨色侧栏 + 书卷纸感版面，分册 10 §7 页面→端点映射）。
import React, { useEffect, useState, useCallback } from "react";
import { createRoot } from "react-dom/client";
import { api, subscribeJob, subscribeBook, type BookItem, type Job } from "./api";
import { Button, Card, SkeletonList, EmptyState, ErrorState, Progress, Badge, Spinner } from "./components";
import { SettingsForm } from "./config-form";
import {
  BookOpen, Book, Plus, Sliders, Search, Upload, FileText, Check, CheckCircle, Play, Stop,
  ChevronLeft, Sun, Moon, Eye, Shield, Package, BarChart, ListIcon,
  Command, Menu, Download, AlertTriangle, Bookmark, Activity,
} from "./icons";
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
    <div className="space-y-7">
      {/* 纸面欢迎卡 */}
      <section className="relative overflow-hidden rounded-xl border border-ink-200/80 bg-paper-50 px-7 py-7 shadow-card dark:border-ink-800 dark:bg-ink-900">
        <span aria-hidden className="writing-vertical pointer-events-none absolute right-14 top-1/2 hidden h-64 -translate-y-1/2 select-none font-serif text-[40px] leading-none tracking-[.35em] text-ink-200/70 dark:text-ink-800 sm:block">落纸云烟</span>
        <span className="inline-flex items-center gap-1.5 rounded-full border border-seal-200 bg-seal-50 px-2.5 py-0.5 text-xs text-seal-700 dark:border-seal-500/30 dark:bg-seal-500/10 dark:text-seal-300">
          <BookOpen size={12} /> 小说翻译工作台
        </span>
        <h1 className="mt-3 font-serif text-2xl font-semibold text-ink-900 dark:text-paper-100">开始你的第一次翻译</h1>
        <p className="mt-1.5 max-w-lg text-sm leading-relaxed text-ink-500 dark:text-ink-300">
          上传一本 EPUB / TXT / HTML / FB2 / PDF 小说，Wenyi 会自动解析章节、维护术语表并分批翻译——中途关闭也不会丢失进度。
        </p>
        <div className="mt-5 flex flex-wrap items-center gap-3">
          <Button size="lg" onClick={() => nav("/new")}><Plus size={15} /> 新建翻译任务</Button>
          {meta && !meta.engineAvailable && (
            <span className="inline-flex items-center gap-1.5 text-xs text-seal-600 dark:text-seal-300"><AlertTriangle size={13} /> 引擎不可用，请检查设置</span>
          )}
        </div>
        <span aria-hidden className="absolute bottom-5 right-5 flex h-9 w-9 rotate-3 select-none items-center justify-center rounded bg-seal-600 font-serif text-lg text-paper-50 shadow-raised">译</span>
      </section>
      {/* 藏书统计（编辑部式一行） */}
      <div className="flex divide-x divide-ink-200/80 border-y border-ink-200/80 dark:divide-ink-800 dark:border-ink-800">
        {[["藏书", books.length], ["翻译中", translating], ["已完成", done]].map(([label, n], i) => (
          <div key={String(label)} className={`flex flex-1 items-baseline gap-2.5 py-3.5 ${i > 0 ? "pl-6" : ""}`} aria-label={String(label)}>
            <span className="text-[26px] leading-none font-semibold tabular-nums text-ink-800 dark:text-paper-100">{String(n)}</span>
            <span className="text-xs text-ink-400">{label}</span>
          </div>
        ))}
      </div>
      {/* 书籍网格 */}
      {books.length === 0 ? (
        <Card><EmptyState icon={<Book size={22} />} title="书架还是空的" hint="点击上方「新建翻译任务」，上传一本小说开始" action={<Button className="mt-2" size="sm" onClick={() => nav("/new")}><Plus size={14} /> 新建任务</Button>} /></Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {books.map((b) => (
            <Card key={b.slug} className="group cursor-pointer p-0 transition-all hover:-translate-y-0.5 hover:shadow-raised"
              onClick={() => nav(`/book/${b.slug}`)} role="link" tabIndex={0}
              onKeyDown={(e) => { if (e.key === "Enter") nav(`/book/${b.slug}`); }}>
              <div className="flex gap-4 p-4">
                {/* 书脊 */}
                <div className="flex h-20 w-12 shrink-0 items-end justify-center rounded-[3px] pb-2 shadow-inner" style={{ backgroundColor: spineColor(b.slug) }} aria-hidden>
                  <span className="writing-vertical text-xs font-bold tracking-widest text-white/85">{(b.title ?? "").slice(0, 6)}</span>
                </div>
                <div className="min-w-0 flex-1">
                  <div className="flex items-start justify-between gap-2">
                    <h2 className="line-clamp-2 font-serif text-[15px] font-semibold leading-snug text-ink-900 group-hover:text-seal-700 dark:text-paper-100 dark:group-hover:text-seal-300">{b.title}</h2>
                    <Badge tone={b.status === "done" ? "green" : b.status === "translating" ? "brand" : "gray"}>
                      {b.status === "done" ? "已完成" : b.status === "translating" ? "翻译中" : b.status === "prepared" ? "待翻译" : "未初始化"}
                    </Badge>
                  </div>
                  <div className="mt-2.5"><Progress done={b.chaptersDone} total={b.chaptersTotal} label={`${b.chaptersDone}/${b.chaptersTotal} 章`} /></div>
                  <p className="mt-2.5 text-xs text-ink-400">{b.fmt} · {b.sourceLang}→{b.targetLang}{b.updatedAt && ` · ${timeAgo(b.updatedAt)}`}</p>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

// 书脊染布色（绀青/赭石/竹青/胭脂/苔色，低饱和，避免刺眼）
const SPINES = ["#5b6b8c", "#8a5a44", "#5d7a5f", "#9c4f5c", "#6f7049"];
function spineColor(slug: string): string {
  let h = 0; for (const c of slug) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return SPINES[h % SPINES.length];
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
        <h1 className="font-serif text-xl font-semibold text-ink-900 dark:text-paper-100">新建翻译任务</h1>
        <p className="mt-1 text-sm text-ink-400">两步开始：选文件 → 确认启动</p>
      </div>
      {/* 步骤指示 */}
      <div className="flex items-center gap-2 text-xs">
        {[["1", "选择文件", !!file], ["2", "上传解析", !!uploaded], ["3", "开始翻译", preparing]].map(([n, label, active], i) => (
          <React.Fragment key={String(n)}>
            {i > 0 && <div className={`h-px flex-1 ${active ? "bg-seal-500" : "bg-ink-200 dark:bg-ink-700"}`} />}
            <span className={`flex items-center gap-1.5 ${active ? "text-seal-600 dark:text-seal-300" : "text-ink-400"}`}>
              <span className={`flex h-5 w-5 items-center justify-center rounded-full text-[10px] font-bold ${active ? "bg-seal-600 text-paper-50" : "bg-ink-200 text-ink-500 dark:bg-ink-700 dark:text-ink-300"}`}>{n}</span>{label}
            </span>
          </React.Fragment>
        ))}
      </div>
      <Card className="p-6">
        <div
          className={`flex cursor-pointer flex-col items-center gap-3 rounded-lg border border-dashed p-10 text-center transition-colors ${dragOver ? "border-seal-500 bg-seal-50/50" : "border-ink-300 bg-paper-100/40 hover:border-seal-400 dark:border-ink-600 dark:bg-ink-950/40"}`}
          onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
          onDragLeave={() => setDragOver(false)}
          onDrop={(e) => { e.preventDefault(); setDragOver(false); const f = e.dataTransfer.files?.[0]; if (f) { setFile(f); setUploaded(null); } }}
          onClick={(e) => (e.currentTarget.querySelector("input") as HTMLInputElement)?.click()}
          role="button" aria-label="选择或拖入文件">
          <span className="text-seal-600 dark:text-seal-300" aria-hidden>{file ? <FileText size={34} /> : <Upload size={34} />}</span>
          {file ? (
            <div>
              <p className="text-sm font-medium text-ink-800 dark:text-paper-100">{file.name}</p>
              <p className="mt-1 text-xs text-ink-400">{(file.size / 1024).toFixed(0)} KB · 点击重新选择</p>
            </div>
          ) : (
            <div>
              <p className="text-sm font-medium text-ink-700 dark:text-ink-200">拖入文件，或点击选择</p>
              <p className="mt-1 text-xs text-ink-400">支持 EPUB / TXT / Markdown / HTML / FB2 / PDF，最大 200MB</p>
            </div>
          )}
          <input type="file" className="hidden" accept=".epub,.txt,.md,.html,.fb2,.pdf"
            onChange={(e) => { setFile(e.target.files?.[0] ?? null); setUploaded(null); }} />
        </div>
        {/* 当前配置摘要 */}
        {cfg && (
          <div className="mt-4 flex flex-wrap items-center gap-2 text-xs text-ink-400">
            <Badge tone="brand">{langLabel(cfg?.language?.source)} → {langLabel(cfg?.language?.target)}</Badge>
            <Badge>{cfg?.llm?.provider === "fake" ? "离线体验模式" : cfg?.llm?.provider}</Badge>
            <button className="text-seal-600 hover:underline dark:text-seal-300" onClick={() => nav("/settings")}>修改设置 →</button>
          </div>
        )}
        {cfg?.llm?.provider === "fake" && (
          <p className="mt-2 flex items-start gap-1.5 text-xs leading-relaxed text-[#8a6116] dark:text-[#d8b45e]">
            <AlertTriangle size={13} className="mt-0.5 shrink-0" />
            <span>当前为离线体验模式，译文是假数据。要翻译真实书籍请先到 <button className="underline" onClick={() => nav("/settings")}>设置</button> 配置模型。</span>
          </p>
        )}
        {error && <p className="mt-3 text-sm text-seal-700 dark:text-seal-300">{error}</p>}
        {uploaded && (
          <div className="mt-4 flex items-start gap-2 rounded-md bg-moss-50 p-3 text-sm leading-relaxed text-moss-700 dark:bg-moss-500/15 dark:text-moss-100">
            <Check size={14} className="mt-1 shrink-0" />
            <span>解析就绪：<b>{uploaded.slug}</b>。点击开始后 Wenyi 会分析全书风格并建立术语表。</span>
          </div>
        )}
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="secondary" onClick={() => nav("/")}>取消</Button>
          {uploaded ? (
            <Button size="lg" onClick={prepare} disabled={preparing}>{preparing ? <><Spinner /> 正在准备…</> : <><Play size={14} /> 开始翻译</>}</Button>
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
        <Button variant="ghost" size="sm" onClick={() => nav("/")}><ChevronLeft size={14} /> 书架</Button>
        <h1 className="min-w-0 flex-1 truncate font-serif text-xl font-semibold text-ink-900 dark:text-paper-100">{m.title}</h1>
        <Badge>{m.fmt}</Badge>
        <Badge tone="gray">{m.source_lang}→{m.target_lang}</Badge>
      </div>
      {/* 报头式页签 */}
      <div className="flex gap-6 overflow-x-auto border-b border-ink-200/80 dark:border-ink-800" role="tablist">
        {TABS.map(([t, label]) => (
          <button key={t} role="tab" aria-selected={tab === t}
            className={`-mb-px whitespace-nowrap border-b-2 px-0.5 pb-2.5 pt-1 text-sm transition-colors ${tab === t ? "border-seal-600 font-medium text-seal-700 dark:border-seal-400 dark:text-seal-300" : "border-transparent text-ink-400 hover:text-ink-700 dark:hover:text-ink-200"}`}
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
          <h3 className="mb-1 font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">翻译操作</h3>
          <p className="mb-4 text-xs leading-relaxed text-ink-400">中断随时可续——已翻译的章节和批次不会重做。</p>
          <div className="space-y-2">
            <Button className="w-full" size="lg" onClick={() => start("translate")} disabled={busy || running}>
              {running ? <Spinner /> : <Play size={14} />} {done > 0 ? "继续翻译" : "开始翻译"}
            </Button>
            <div className="grid grid-cols-2 gap-2">
              <Button variant="secondary" onClick={() => start("review")} disabled={busy || running}><Eye size={14} /> 审校</Button>
              <Button variant="secondary" onClick={() => start("qa")} disabled={busy || running}><Shield size={14} /> 一致性 QA</Button>
            </div>
            {running && <Button variant="danger" className="w-full" onClick={() => job && api.cancel(job.id)}><Stop size={13} /> 取消任务</Button>}
          </div>
          {error && <p className="mt-3 text-sm text-seal-700 dark:text-seal-300">{error}</p>}
        </Card>
        {job && job.progress && running && (
          <Card className="p-5"><Progress {...job.progress} /></Card>
        )}
        {job && !running && (
          <Card className={`flex items-center gap-2 p-4 text-sm ${job.status === "succeeded" ? "text-moss-700 dark:text-moss-100" : job.status === "failed" ? "text-seal-700 dark:text-seal-300" : "text-ink-400"}`}>
            {job.status === "succeeded" ? <><Check size={15} /> 任务完成</> : job.status === "failed" ? <><AlertTriangle size={15} /> 任务失败（退出码 {job.exitCode}）</> : "已取消"}
            {job.status === "failed" && <button className="ml-1 inline-flex items-center gap-0.5 text-xs underline" onClick={() => api.job(job.id).then((r) => setLogs(r.job.logTail ?? ""))}><ListIcon size={12} /> 查看日志</button>}
          </Card>
        )}
        <Card className="p-5">
          <h3 className="mb-3 font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">章节进度 <span className="ml-1 text-xs font-normal tabular-nums text-ink-400">{done}/{chapters.length}</span></h3>
          <div className="max-h-80 space-y-0.5 overflow-y-auto">
            {chapters.map((c: any) => (
              <div key={c.index} className="flex items-center gap-2.5 rounded-md px-2 py-1.5 text-sm">
                <span className={`flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[10px] ${c.status === "done" ? "bg-moss-100 text-moss-700 dark:bg-moss-500/30 dark:text-moss-100" : "bg-ink-100 text-ink-400 dark:bg-ink-800"}`}>
                  {c.status === "done" ? <Check size={11} /> : c.index + 1}
                </span>
                <span className={`truncate ${c.status === "done" ? "text-ink-600 dark:text-ink-200" : "text-ink-400"}`}>{c.title || `章节 ${c.index + 1}`}</span>
              </div>
            ))}
          </div>
        </Card>
      </div>
      <div className="space-y-4 xl:col-span-2">
        <Card className="flex h-72 flex-col p-0">
          <div className="flex items-center justify-between border-b border-ink-200/70 px-5 py-3 dark:border-ink-800">
            <h3 className="flex items-center gap-2 font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100"><Activity size={14} className="text-seal-600 dark:text-seal-400" /> 实时事件流</h3>
            <Badge tone={running ? "green" : "gray"}>{running ? "运行中" : "空闲"}</Badge>
          </div>
          <div className="flex-1 divide-y divide-ink-100 overflow-y-auto px-5 dark:divide-ink-800/70" aria-live="polite">
            {events.length === 0 ? <EmptyState icon={<Activity size={20} />} title="等待事件…" hint="翻译过程中的每一步（批次完成、术语抽取等）会实时出现在这里" /> : events.slice().reverse().map((ev, i) => (
              <div key={i} className="flex items-baseline gap-2 py-2 text-xs leading-relaxed">
                <span className="shrink-0 font-medium text-seal-700 dark:text-seal-300">{EVENT_NAMES[ev.event] ?? ev.event}</span>
                {ev.chapter != null && <span className="shrink-0 tabular-nums text-ink-400">第 {Number(ev.chapter) + 1} 章</span>}
                {ev.detail && <span className="min-w-0 truncate text-ink-500 dark:text-ink-400">{String(ev.detail).slice(0, 90)}</span>}
              </div>
            ))}
          </div>
        </Card>
        <Card className="overflow-hidden p-0">
          <div className="border-b border-ink-200/70 px-5 py-3 dark:border-ink-800"><h3 className="font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">任务日志</h3></div>
          <pre className="max-h-56 overflow-y-auto whitespace-pre-wrap bg-ink-950 p-4 font-mono text-xs leading-relaxed text-ink-100 dark:bg-black/40">{logs || "（暂无输出）"}</pre>
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
        <Card className="border-[#e3c98f] p-5 dark:border-[#5a4a22]">
          <h3 className="mb-1 flex items-center gap-1.5 font-serif text-[15px] font-semibold text-[#8a6116] dark:text-[#d8b45e]"><AlertTriangle size={14} /> 需要你裁决的译法冲突（{conflicts.items.length}）</h3>
          <p className="mb-4 text-xs leading-relaxed text-ink-400">同一术语在不同章节被译成了不同写法。选择一个全书统一的版本：</p>
          <div className="space-y-2.5">
            {conflicts.items.map((c: any) => (
              <div key={c.id} className="flex flex-col gap-2.5 rounded-lg border border-[#e9d9ae] bg-[#faf5e6] p-4 dark:border-[#4a3d1e] dark:bg-[#2a2413] sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="font-mono text-sm font-semibold text-ink-800 dark:text-paper-100">{c.source}</p>
                  <div className="mt-1.5 flex flex-wrap items-center gap-2 text-sm">
                    <span className="rounded bg-paper-50 px-2.5 py-1 shadow-card dark:bg-ink-800"><b>{c.existing_target}</b> <span className="text-xs text-ink-400">现有</span></span>
                    <span className="text-ink-300">vs</span>
                    <span className="rounded bg-paper-50 px-2.5 py-1 shadow-card dark:bg-ink-800"><b>{c.proposed_target}</b> <span className="text-xs text-ink-400">新提议</span></span>
                  </div>
                  <p className="mt-1 text-xs text-ink-400">出现在第 {(c.chapter ?? 0) + 1} 章</p>
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
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-ink-200/70 px-5 py-3.5 dark:border-ink-800">
          <h3 className="font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">术语表 <Badge tone="brand">{data?.stats?.terms ?? 0} 条</Badge></h3>
          <div className="relative">
            <input className="w-60 rounded-md border border-ink-200 bg-paper-100/60 py-2 pl-8 pr-3 text-sm placeholder-ink-300 focus:border-seal-500 focus:outline-none focus:ring-2 focus:ring-seal-500/15 dark:border-ink-700 dark:bg-ink-950"
              placeholder="搜索原文或译文…" value={q} onChange={(e) => setQ(e.target.value)} aria-label="搜索术语" />
            <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-ink-400" aria-hidden><Search size={13} /></span>
          </div>
        </div>
        {!data ? <SkeletonList rows={6} /> : data.items.length === 0 ? (
          <EmptyState icon={<Bookmark size={20} />} title={q ? "没有匹配的术语" : "术语表还是空的"} hint={q ? "换个关键词试试" : "翻译过程中会自动从书中抽取人名、地名等术语"} />
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b border-ink-200/70 text-left text-xs tracking-wide text-ink-400 dark:border-ink-800">
                <th className="px-5 py-2.5 font-medium">原文</th><th className="py-2.5 pr-4 font-medium">译文</th>
                <th className="py-2.5 pr-4 font-medium">类型</th><th className="py-2.5 pr-5 font-medium">状态</th>
              </tr></thead>
              <tbody className="divide-y divide-ink-100 dark:divide-ink-800/70">
                {data.items.map((t: any) => (
                  <tr key={t.source} className="transition-colors hover:bg-paper-100/60 dark:hover:bg-ink-800/50">
                    <td className="px-5 py-2.5 font-mono font-medium">{t.source}</td>
                    <td className="py-2.5 pr-4">{t.target}</td>
                    <td className="py-2.5 pr-4 text-ink-400">{t.type}{t.gender ? ` · ${t.gender}` : ""}{t.aliases?.length ? ` · 又名 ${t.aliases.join("/")}` : ""}</td>
                    <td className="py-2.5 pr-5"><Badge tone={t.status === "ok" ? "green" : "amber"}>{t.status === "ok" ? "正常" : "冲突"}</Badge></td>
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
      <Card className="p-0 lg:col-span-1">
        <div className="border-b border-ink-200/70 px-4 py-3.5 dark:border-ink-800"><h3 className="font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">审校历史</h3></div>
        {!reviews ? <SkeletonList /> : reviews.length === 0 ? <EmptyState icon={<Eye size={20} />} title="尚无审校记录" hint="在「翻译」页点击「审校」按钮开始全书审校" /> :
          <div className="max-h-[30rem] overflow-y-auto">
            {reviews.map((r) => (
              <button key={r.id} className={`block w-full border-b border-ink-100 px-4 py-3 text-left transition-colors last:border-0 hover:bg-paper-100/70 dark:border-ink-800/70 dark:hover:bg-ink-800 ${selected?.review_id === r.id ? "bg-seal-50/60 dark:bg-seal-500/10" : ""}`}
                onClick={() => api.reviewResult(slug, r.id).then(setSelected)}>
                <div className="flex items-center justify-between gap-2">
                  <span className="font-mono text-xs text-ink-400">{r.id.slice(7, 23)}</span>
                  <Badge tone={r.status === "completed" ? "green" : "red"}>{r.status === "completed" ? "完成" : "失败"}</Badge>
                </div>
                <p className="mt-1 text-xs text-ink-400">{TERM_NAMES[r.termination] ?? r.termination} · {r.issueCount} 问题 · {r.changeCount} 修改建议</p>
              </button>
            ))}
          </div>}
      </Card>
      <Card className="p-5 lg:col-span-2">
        {!selected ? <EmptyState icon={<Eye size={20} />} title="选择一次审校查看详情" hint="问题清单与修改建议会显示在这里" /> : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <Badge tone="blue">{TERM_NAMES[selected.termination] ?? selected.termination}</Badge>
              {selected.summary && <>
                <Badge>{selected.summary.review_round_count ?? "?"} 轮审校</Badge>
                <Badge>{selected.summary.patch_count ?? 0} 个补丁</Badge>
                <Badge>{selected.summary.fix_round_count ?? 0} 轮修复</Badge>
                <Badge>{selected.summary.clean_streak ?? 0} 连续干净</Badge>
              </>}
            </div>
            {[
              ["issues", "发现的问题", (selected.issues ?? []).length, "每条问题附带了修改建议，可按建议手动修订译文"],
              ["changes", "修改建议", (selected.changes ?? []).length, "审校生成的建议替换——目前为只读建议，不自动改动正文"],
            ].map(([key, title, count, hint]) => (
              <div key={String(key)}>
                <h4 className="mb-1.5 font-serif text-sm font-semibold text-ink-800 dark:text-paper-100">{title} <span className="ml-1 text-xs font-normal text-ink-400">{count}</span></h4>
                <p className="mb-2 text-xs text-ink-400">{hint}</p>
                <div className="max-h-56 divide-y divide-ink-100 overflow-y-auto rounded-md border border-ink-200/70 dark:divide-ink-800/70 dark:border-ink-800">
                  {count === 0 ? <p className="px-4 py-6 text-center text-xs text-ink-400">无</p> :
                    (selected[key] ?? []).slice(0, 60).map((item: any, i: number) => (
                      <div key={i} className="px-4 py-2.5 text-sm">
                        <span className="mr-2 text-xs text-ink-400">第 {Number(item.chapter) + 1} 章 · 段 {item.index}{item.type && ` · ${item.type}`}</span>
                        <p className="mt-0.5">{item.detail ?? item.suggested_target}</p>
                        {item.suggestion && item.detail && <p className="mt-0.5 text-xs text-seal-700 dark:text-seal-300">建议：{item.suggestion}</p>}
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
        {[["章节", `${s.chapters_done}/${s.chapters_total}`], ["术语", s.terms ?? 0],
          ["空译文", s.empty_targets ?? 0], ["回译问题", s.backtranslation_issues ?? 0]].map(([label, value]) => (
          <Card key={String(label)} className="p-4">
            <p className="text-xs text-ink-400">{label}</p>
            <p className="mt-1 text-2xl font-semibold tabular-nums text-ink-900 dark:text-paper-100">{String(value)}</p>
          </Card>
        ))}
      </div>
      {report.review && (
        <Card className="flex flex-wrap items-center gap-3 p-4">
          <Badge tone="blue">最近审校</Badge>
          <span className="text-sm">{TERM_NAMES[report.review.termination] ?? report.review.termination}</span>
          <span className="text-xs text-ink-400">{report.review.issue_count} 问题 · {report.review.change_count} 修改建议</span>
        </Card>
      )}
      <Card className="p-5">
        <h3 className="mb-3 flex items-center gap-2 font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">跨章一致性问题 <Badge tone="amber">{(report.consistency_issues ?? []).length}</Badge></h3>
        {(report.consistency_issues ?? []).length === 0 ? <EmptyState icon={<CheckCircle size={20} />} title="未发现一致性问题" hint="全书术语译法与人称保持一致" /> :
          (report.consistency_issues ?? []).map((iss: any, i: number) => (
            <div key={i} className="border-b border-ink-100 py-2.5 text-sm last:border-0 dark:border-ink-800/70">
              <Badge tone="amber">{iss.type}</Badge> <span className="ml-1">{iss.detail}</span>
              {iss.where && <span className="ml-2 text-xs text-ink-400">{iss.where}</span>}
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
        <h3 className="mb-1 font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">生成导出文件</h3>
        <p className="mb-4 text-xs text-ink-400">从当前翻译状态组装产物，不会调用模型。</p>
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex rounded-md border border-ink-200 p-0.5 dark:border-ink-700" role="radiogroup" aria-label="格式">
            {["epub", "txt", "html", "markdown"].map((f) => (
              <button key={f} role="radio" aria-checked={form === f}
                className={`rounded px-3.5 py-1.5 text-sm font-medium transition-all ${form === f ? "bg-seal-600 text-paper-50 shadow-card" : "text-ink-400 hover:text-ink-700 dark:hover:text-ink-200"}`}
                onClick={() => setForm(f)}>{f.toUpperCase()}</button>
            ))}
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-sm text-ink-500 dark:text-ink-300">
            <input type="checkbox" className="h-4 w-4 rounded accent-seal-600" checked={bilingual} onChange={(e) => setBilingual(e.target.checked)} />
            双语对照版
          </label>
          <Button onClick={assemble} disabled={busy}>{busy ? <><Spinner /> 生成中…</> : <><Package size={14} /> 生成文件</>}</Button>
        </div>
        {error && <p className="mt-3 text-sm text-seal-700 dark:text-seal-300">{error}</p>}
      </Card>
      <Card className="p-0">
        <div className="border-b border-ink-200/70 px-5 py-3.5 dark:border-ink-800">
          <h3 className="font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">已生成的文件 <Badge tone="brand">{exports?.length ?? 0}</Badge></h3>
        </div>
        {!exports ? <SkeletonList rows={2} /> : exports.length === 0 ? <EmptyState icon={<Package size={20} />} title="暂无导出文件" hint="点击上方「生成文件」创建第一个产物" /> :
          <div className="divide-y divide-ink-100 dark:divide-ink-800/70">
            {exports.map((e) => (
              <a key={e.name} href={`/api/v1/books/${slug}/exports/${encodeURIComponent(e.name)}`}
                className="flex items-center gap-4 px-5 py-3.5 text-sm transition-colors hover:bg-paper-100/60 dark:hover:bg-ink-800/50">
                <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-md border border-ink-200/80 bg-paper-100 text-ink-500 dark:border-ink-700 dark:bg-ink-800 dark:text-ink-300" aria-hidden>
                  <FileText size={17} />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">{e.name}</p>
                  <p className="text-xs text-ink-400">{(e.size / 1024).toFixed(0)} KB · {new Date(e.mtime).toLocaleString("zh-CN")}</p>
                </div>
                {e.bilingual && <Badge tone="blue">双语</Badge>}
                <span className="inline-flex items-center gap-1 text-xs font-medium text-seal-700 dark:text-seal-300"><Download size={12} /> 下载</span>
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
  if (!usage) return <Card><EmptyState icon={<BarChart size={20} />} title="暂无用量数据" hint="翻译完成后可在此查看 token 消耗统计" /></Card>;
  const t = usage.totals ?? {};
  const stages = Object.entries(usage.by_stage ?? {}).sort((a: any, b: any) => (b[1].total_tokens ?? 0) - (a[1].total_tokens ?? 0));
  const maxTokens = Math.max(...stages.map(([, v]: any) => v.total_tokens ?? 0), 1);
  return (
    <div className="space-y-5">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        {[
          ["总调用次数", (t.calls ?? 0).toLocaleString()],
          ["输入 tokens", (t.prompt_tokens ?? 0).toLocaleString()],
          ["输出 tokens", (t.completion_tokens ?? 0).toLocaleString()],
          ["缓存命中率", `${((t.cache_hit_rate ?? 0) * 100).toFixed(1)}%`],
        ].map(([label, value]) => (
          <Card key={label} className="p-4">
            <p className="text-xs text-ink-400">{label}</p>
            <p className="mt-1 text-xl font-semibold tabular-nums text-ink-900 dark:text-paper-100">{value}</p>
          </Card>
        ))}
      </div>
      <Card className="p-5">
        <h3 className="mb-4 font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">按环节分布</h3>
        {stages.length === 0 ? <p className="text-xs text-ink-400">暂无数据</p> : stages.map(([stage, v]: any) => (
          <div key={stage} className="mb-3.5 last:mb-0">
            <div className="mb-1 flex items-baseline justify-between text-xs">
              <span className="font-medium">{STAGE_NAMES[stage] ?? stage}</span>
              <span className="tabular-nums text-ink-400">{(v.total_tokens ?? 0).toLocaleString()} tokens · {v.calls ?? 0} 次</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-ink-200/70 dark:bg-ink-800">
              <div className="h-full rounded-full bg-seal-500/85 transition-all duration-700" style={{ width: `${Math.max(((v.total_tokens ?? 0) / maxTokens) * 100, 2)}%` }} />
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
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-ink-200/70 px-5 py-3.5 dark:border-ink-800">
        <h3 className="font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">事件日志 <Badge tone="brand">{filtered.length}</Badge></h3>
        <div className="relative">
          <input className="w-56 rounded-md border border-ink-200 bg-paper-100/60 py-2 pl-8 pr-3 text-sm placeholder-ink-300 focus:border-seal-500 focus:outline-none dark:border-ink-700 dark:bg-ink-950"
            placeholder="过滤事件名…" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="过滤事件" />
          <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-ink-400" aria-hidden><Search size={13} /></span>
        </div>
      </div>
      {!events ? <SkeletonList rows={8} /> : filtered.length === 0 ? <EmptyState icon={<ListIcon size={20} />} title={filter ? "无匹配事件" : "暂无事件"} /> : (
        <div className="max-h-[34rem] overflow-y-auto">
          {filtered.slice().reverse().map((e, i) => (
            <div key={i} className="flex items-baseline gap-3 border-b border-ink-100 px-5 py-2 font-mono text-xs last:border-0 dark:border-ink-800/70">
              <span className="shrink-0 text-ink-300 dark:text-ink-500">{e.ts ?? ""}</span>
              <span className="shrink-0 font-semibold text-seal-700 dark:text-seal-300">{EVENT_NAMES[e.event] ?? e.event}</span>
              <span className="min-w-0 truncate text-ink-400">{JSON.stringify({ ...e, ts: undefined, event: undefined, line: undefined }).slice(0, 140)}</span>
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
    { icon: <Book size={15} />, label: "打开书架", run: () => nav("/") },
    { icon: <Plus size={15} />, label: "新建翻译任务", run: () => nav("/new") },
    { icon: <Sliders size={15} />, label: "设置", run: () => nav("/settings") },
    ...books.map((b) => ({ icon: <BookOpen size={15} />, label: `打开《${b.title}》`, run: () => nav(`/book/${b.slug}`) })),
  ].filter((a) => !q || a.label.toLowerCase().includes(q.toLowerCase()));
  const [idx, setIdx] = useState(0);
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-ink-950/50 pt-[15vh] backdrop-blur-sm" onClick={onClose} role="dialog" aria-label="命令面板">
      <div className="w-[30rem] max-w-[92vw] overflow-hidden rounded-xl border border-ink-200 bg-paper-50 shadow-raised dark:border-ink-700 dark:bg-ink-900"
        onClick={(e) => e.stopPropagation()}>
        <input autoFocus className="w-full border-b border-ink-200/70 bg-transparent px-4 py-3.5 text-sm text-ink-800 outline-none placeholder:text-ink-300 dark:border-ink-800 dark:text-ink-100"
          placeholder="搜索命令或书名…" value={q}
          onChange={(e) => { setQ(e.target.value); setIdx(0); }}
          onKeyDown={(e) => {
            if (e.key === "Escape") onClose();
            if (e.key === "ArrowDown") { e.preventDefault(); setIdx((i) => Math.min(i + 1, actions.length - 1)); }
            if (e.key === "ArrowUp") { e.preventDefault(); setIdx((i) => Math.max(i - 1, 0)); }
            if (e.key === "Enter" && actions[idx]) { actions[idx].run(); onClose(); }
          }} />
        <div className="max-h-72 overflow-y-auto p-2">
          {actions.length === 0 ? <p className="px-3 py-6 text-center text-xs text-ink-400">没有匹配项</p> : actions.map((a, i) => (
            <button key={i} className={`flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm ${i === idx ? "bg-seal-50 text-seal-800 dark:bg-seal-500/15 dark:text-seal-200" : "text-ink-600 hover:bg-paper-100 dark:text-ink-200 dark:hover:bg-ink-800"}`}
              onMouseEnter={() => setIdx(i)} onClick={() => { a.run(); onClose(); }}>
              <span className="text-ink-400" aria-hidden>{a.icon}</span>{a.label}
            </button>
          ))}
        </div>
        <div className="border-t border-ink-200/70 px-4 py-2 text-[10px] text-ink-400 dark:border-ink-800">↑↓ 选择 · Enter 确认 · Esc 关闭</div>
      </div>
    </div>
  );
}

// ---- App 根：墨色侧栏 + 纸面主区 ----
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
    { icon: <BookOpen size={15} />, label: "书架", to: "/", active: route === "/" },
    { icon: <Plus size={15} />, label: "新建任务", to: "/new", active: route === "/new" },
    { icon: <Sliders size={15} />, label: "设置", to: "/settings", active: route === "/settings" },
    ...(current ? [{ icon: <Book size={15} />, label: current.title.slice(0, 8), to: `/book/${slug}`, active: true }] : []),
  ];
  return (
    <div className="flex min-h-screen">
      {/* 墨色侧栏 */}
      <aside className={`fixed inset-y-0 left-0 z-40 flex w-56 flex-col bg-ink-950 text-ink-200 transition-transform lg:sticky lg:top-0 lg:h-screen lg:translate-x-0 ${sidebarOpen ? "translate-x-0" : "-translate-x-full"}`}>
        <button className="flex items-center gap-2.5 px-5 py-5 text-left" onClick={() => { nav("/"); setSidebarOpen(false); }}>
          <span className="flex h-9 w-9 select-none items-center justify-center rounded bg-seal-600 font-serif text-lg text-paper-50 shadow-card" aria-hidden>译</span>
          <div><p className="font-serif text-[15px] font-bold tracking-wide text-paper-50">Wenyi</p><p className="text-[10px] text-ink-400">小说翻译工作台</p></div>
        </button>
        <nav className="flex-1 space-y-0.5 overflow-y-auto px-3" aria-label="主导航">
          {navItems.map((item) => (
            <button key={item.to}
              className={`flex w-full items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors ${item.active ? "bg-seal-600 font-medium text-paper-50" : "text-ink-300 hover:bg-white/5 hover:text-paper-100"}`}
              onClick={() => { nav(item.to); setSidebarOpen(false); }}>
              <span aria-hidden>{item.icon}</span><span className="truncate">{item.label}</span>
            </button>
          ))}
        </nav>
        <div className="space-y-1 border-t border-white/5 p-3">
          <button className="flex w-full items-center justify-between rounded-md px-3 py-2 text-xs text-ink-400 hover:bg-white/5 hover:text-ink-200"
            onClick={() => setPaletteOpen(true)}>
            <span className="flex items-center gap-1.5"><Search size={12} /> 搜索…</span>
            <kbd className="flex items-center gap-0.5 rounded border border-white/10 px-1.5 py-0.5 font-mono text-[10px]"><Command size={9} /> K</kbd>
          </button>
          <div className="flex items-center justify-between rounded-md px-3 py-2">
            <span className="flex items-center gap-1.5 text-xs text-ink-400">
              <span className={`h-2 w-2 rounded-full ${meta?.engineAvailable ? "bg-moss-500" : "bg-seal-500"}`} aria-hidden />
              引擎 {meta?.engineVersion ?? "…"}
            </span>
            <button onClick={toggle} className="rounded-md p-1.5 text-ink-400 hover:bg-white/5 hover:text-paper-100" aria-label="切换深浅主题">
              {theme === "dark" ? <Sun size={14} /> : <Moon size={14} />}
            </button>
          </div>
        </div>
      </aside>
      {/* 移动端遮罩 */}
      {sidebarOpen && <div className="fixed inset-0 z-30 bg-ink-950/50 lg:hidden" onClick={() => setSidebarOpen(false)} aria-hidden />}
      {/* 主内容 */}
      <div className="min-w-0 flex-1">
        <header className="sticky top-0 z-20 flex items-center gap-3 border-b border-ink-200/70 bg-paper-100/85 px-4 py-3 backdrop-blur dark:border-ink-800 dark:bg-ink-950/85 lg:hidden">
          <button className="rounded-md p-1.5 text-ink-500 hover:bg-ink-100 dark:hover:bg-ink-800" onClick={() => setSidebarOpen(true)} aria-label="打开菜单"><Menu size={16} /></button>
          <span className="font-serif text-sm font-semibold">Wenyi</span>
        </header>
        <main className="mx-auto max-w-6xl px-4 py-7 lg:px-8">
          {slug ? <BookView slug={slug} /> : route === "/new" ? <NewTask /> : route === "/settings" ? <SettingsForm /> : <Dashboard />}
        </main>
      </div>
      {paletteOpen && <CommandPalette onClose={() => setPaletteOpen(false)} books={books} />}
    </div>
  );
}

export { App };
createRoot(document.getElementById("root")!).render(<App />);

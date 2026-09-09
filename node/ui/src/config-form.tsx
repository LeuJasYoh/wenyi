// 设置页：可视化配置编辑器（面向不熟悉配置文件的用户）+ 高级源码模式。
// 表单保存以「快照 + 受管字段覆盖」生成 config.json，未识别的键原样保留。
import { useEffect, useState } from "react";
import { api } from "./api";
import { Button, Card, Section, Field, Input, Select, Toggle, RadioCard, Spinner, Badge } from "./components";
import { Globe, Cpu, Sliders, Package, Check, Key, Activity, AlertTriangle, Eye } from "./icons";

// 厂商头像字（衬线单字，替代 emoji 图标）
function Monogram({ ch }: { ch: string }) {
  return (
    <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-ink-200/80 bg-paper-100 font-serif text-[13px] text-ink-600 dark:border-ink-700 dark:bg-ink-800 dark:text-ink-200" aria-hidden>
      {ch}
    </span>
  );
}

// ---- 语言选项（对应主规格 §15.1 别名表）----
const LANGS: { value: string; label: string }[] = [
  { value: "auto", label: "自动检测（由模型识别）" },
  { value: "ja", label: "日语" },
  { value: "en", label: "英语" },
  { value: "ko", label: "韩语" },
  { value: "ru", label: "俄语" },
  { value: "fr", label: "法语" },
  { value: "de", label: "德语" },
  { value: "es", label: "西班牙语" },
  { value: "it", label: "意大利语" },
  { value: "pt", label: "葡萄牙语" },
  { value: "zh", label: "中文" },
];

// ---- Provider 预设（含默认地址/密钥环境变量/推荐档位模型）----
interface ProviderPreset {
  id: string; icon: string; name: string; desc: string; badge?: string;
  baseUrl?: string; apiKeyEnv?: string; requiresKey: boolean;
  tiers: { strong?: string; cheap?: string; fast?: string };
}

const PRESETS: ProviderPreset[] = [
  { id: "deepseek", icon: "深", name: "DeepSeek", badge: "推荐", desc: "性价比高，对中文小说效果好", baseUrl: "https://api.deepseek.com", apiKeyEnv: "DEEPSEEK_API_KEY", requiresKey: true, tiers: { strong: "deepseek-v4-pro", cheap: "deepseek-v4-flash", fast: "deepseek-v4-flash" } },
  { id: "openai", icon: "O", name: "OpenAI", desc: "GPT 系列，效果稳定", baseUrl: "https://api.openai.com/v1", apiKeyEnv: "OPENAI_API_KEY", requiresKey: true, tiers: {} },
  { id: "openrouter", icon: "R", name: "OpenRouter", desc: "一个密钥聚合多家模型", baseUrl: "https://openrouter.ai/api/v1", apiKeyEnv: "OPENROUTER_API_KEY", requiresKey: true, tiers: {} },
  { id: "gemini", icon: "G", name: "Google Gemini", desc: "Google 官方 Gemini API", apiKeyEnv: "GEMINI_API_KEY", requiresKey: true, tiers: { strong: "gemini-3.6-flash", cheap: "gemini-3.6-flash", fast: "gemini-3.6-flash" } },
  { id: "ollama", icon: "L", name: "本地 Ollama", desc: "完全离线，数据不出本机", baseUrl: "http://localhost:11434/v1", requiresKey: false, tiers: {} },
  { id: "vllm", icon: "V", name: "本地 vLLM", desc: "自建推理服务，速度快", baseUrl: "http://localhost:8000/v1", requiresKey: false, tiers: {} },
  { id: "openai-compatible", icon: "兼", name: "其他兼容服务", desc: "中转站或任意 OpenAI 兼容端点，需手动填地址", requiresKey: false, tiers: {} },
  { id: "fake", icon: "试", name: "离线体验模式", desc: "不调用任何模型，用假数据跑通全流程（试用/演示用）", requiresKey: false, tiers: { strong: "demo", cheap: "demo", fast: "demo" } },
];

// ---- 环节档位定义（难度说明面向用户；key 与引擎 pipeline.stage_tiers 对齐）----
interface StageDef { key: string; name: string; def: Tier; desc: string }
type Tier = "strong" | "cheap" | "fast";

const TIER_LABELS: Record<Tier, string> = { strong: "质量档", cheap: "均衡档", fast: "省钱档" };
const TIER_NOTES: Record<Tier, string> = {
  strong: "用主力模型，质量最好、费用最高",
  cheap: "用辅助模型，质量与费用均衡",
  fast: "用快速模型，最便宜",
};

const STAGE_DEFS: StageDef[] = [
  { key: "translator", name: "正文翻译", def: "strong", desc: "全书章节的核心翻译，长上下文 + 文学性要求最高，直接决定译文质量上限" },
  { key: "polisher", name: "译文润色", def: "strong", desc: "对译文做二次润色改写，文笔要求高，建议与正文翻译同档" },
  { key: "title_translate", name: "标题翻译", def: "strong", desc: "书名与章节标题翻译，量小但影响目录观感，用质量档不贵" },
  { key: "analyzer", name: "风格分析", def: "strong", desc: "翻译前通读全书提炼叙事风格，决定全书的语气基调" },
  { key: "reviewer", name: "章节审校", def: "cheap", desc: "逐章找出漏译、误译与术语不一致，均衡档通常足够" },
  { key: "annotationaligner", name: "注释对齐", def: "cheap", desc: "把 EPUB 脚注/尾注链接对回正确位置，结构化任务，均衡档即可" },
  { key: "consistency_checker", name: "一致性检查", def: "cheap", desc: "全书译完后扫描跨章术语与人称问题，均衡档即可" },
  { key: "language_detect", name: "语言识别", def: "cheap", desc: "判断源文本是什么语言，任务很小" },
  { key: "synopsizer", name: "梗概生成", def: "fast", desc: "章节梗概与全书概览，只为让译文保持连贯，省钱档足够" },
  { key: "glossary_extractor", name: "术语抽取", def: "fast", desc: "从原文抽取人名、地名等术语，结构化抽取任务；追求零漏译可升均衡档" },
  { key: "back_translator", name: "回译抽检", def: "fast", desc: "抽样回译以发现离谱误译，省钱档足够" },
];

// ---- 表单状态（与引擎 config schema 对齐，缺省值 = 引擎默认值）----
interface FormState {
  sourceLang: string; targetLang: string;
  provider: string; baseUrl: string; apiKeyEnv: string; apiKey: string;
  timeout: number; maxRetries: number;
  tierStrong: string; tierCheap: string; tierFast: string;
  maxBatch: number; maxSegment: number;
  polish: boolean; review: boolean; qa: boolean; bookUnderstanding: boolean;
  annotationAlignment: boolean; honorific: string;
  reviewAgentTier: Tier;
  stageTiers: Partial<Record<string, Tier>>;
  mono: boolean; bilingual: boolean; bilingualOrder: string; aboutPage: boolean;
}

const DEFAULTS: FormState = {
  sourceLang: "auto", targetLang: "zh",
  provider: "deepseek", baseUrl: "https://api.deepseek.com", apiKeyEnv: "DEEPSEEK_API_KEY", apiKey: "",
  timeout: 600, maxRetries: 4,
  tierStrong: "deepseek-v4-pro", tierCheap: "deepseek-v4-flash", tierFast: "deepseek-v4-flash",
  maxBatch: 1800, maxSegment: 1200,
  polish: true, review: false, qa: false, bookUnderstanding: true,
  annotationAlignment: true, honorific: "keep_style",
  reviewAgentTier: "strong",
  stageTiers: {},
  mono: true, bilingual: false, bilingualOrder: "target_first", aboutPage: true,
};

/** 从 GET /config 的 parsed 对象填表单。 */
function fromParsed(p: any): FormState {
  const f = { ...DEFAULTS, stageTiers: { ...DEFAULTS.stageTiers } };
  const lang = p?.language ?? {};
  if (typeof lang.source === "string") f.sourceLang = lang.source;
  if (typeof lang.target === "string") f.targetLang = lang.target;
  const llm = p?.llm ?? {};
  if (typeof llm.provider === "string") f.provider = llm.provider;
  if (typeof llm.base_url === "string" && llm.base_url) f.baseUrl = llm.base_url;
  else { const preset = PRESETS.find((x) => x.id === f.provider); f.baseUrl = preset?.baseUrl ?? ""; }
  if (typeof llm.api_key === "string") f.apiKey = llm.api_key;
  if (typeof llm.api_key_env === "string" && llm.api_key_env) f.apiKeyEnv = llm.api_key_env;
  else { const preset = PRESETS.find((x) => x.id === f.provider); f.apiKeyEnv = preset?.apiKeyEnv ?? ""; }
  if (typeof llm.timeout === "number") f.timeout = llm.timeout;
  if (typeof llm.max_retries === "number") f.maxRetries = llm.max_retries;
  const tiers = llm.tiers ?? {};
  f.tierStrong = tiers?.strong?.model ?? PRESETS.find((x) => x.id === f.provider)?.tiers.strong ?? "";
  f.tierCheap = tiers?.cheap?.model ?? PRESETS.find((x) => x.id === f.provider)?.tiers.cheap ?? "";
  f.tierFast = tiers?.fast?.model ?? tiers?.cheap?.model ?? PRESETS.find((x) => x.id === f.provider)?.tiers.fast ?? "";
  const seg = p?.segment ?? {};
  if (typeof seg.max_chars_per_batch === "number") f.maxBatch = seg.max_chars_per_batch;
  if (typeof seg.max_chars_per_segment === "number") f.maxSegment = seg.max_chars_per_segment;
  const pl = p?.pipeline ?? {};
  if (typeof pl.polish === "boolean") f.polish = pl.polish;
  if (typeof pl.review === "boolean") f.review = pl.review;
  if (typeof pl.consistency_qa === "boolean") f.qa = pl.consistency_qa;
  if (typeof pl.book_understanding === "boolean") f.bookUnderstanding = pl.book_understanding;
  if (typeof pl.annotation_alignment === "boolean") f.annotationAlignment = pl.annotation_alignment;
  if (pl.review_agent_tier === "strong" || pl.review_agent_tier === "cheap" || pl.review_agent_tier === "fast") {
    f.reviewAgentTier = pl.review_agent_tier;
  }
  const st = pl.stage_tiers ?? {};
  for (const def of STAGE_DEFS) {
    if (st[def.key] === "strong" || st[def.key] === "cheap" || st[def.key] === "fast") {
      f.stageTiers[def.key] = st[def.key];
    }
  }
  const out = p?.output ?? {};
  if (typeof out.mono === "boolean") f.mono = out.mono;
  if (typeof out.bilingual === "boolean") f.bilingual = out.bilingual;
  if (typeof out.bilingual_order === "string") f.bilingualOrder = out.bilingual_order;
  if (typeof out.about_page === "boolean") f.aboutPage = out.about_page;
  const hon = p?.honorific ?? {};
  if (typeof hon.strategy === "string") f.honorific = hon.strategy;
  return f;
}

/** 表单 + 服务器快照 → config.json 全文。
 *  以快照为底做受管字段覆盖：未在表单中暴露的键（如 reasoning_style、并发数）原样保留。 */
function toConfigJson(f: FormState, snapshot: any): string {
  const out: any = snapshot && typeof snapshot === "object" ? structuredClone(snapshot) : {};
  out.language = { ...(out.language ?? {}), source: f.sourceLang, target: f.targetLang };
  const llm = { ...(out.llm ?? {}) };
  llm.provider = f.provider;
  if (f.baseUrl) llm.base_url = f.baseUrl; else delete llm.base_url;
  llm.api_key = f.apiKey; // 直存（空串 = 未配置，回落环境变量）
  if (f.apiKeyEnv) llm.api_key_env = f.apiKeyEnv; else delete llm.api_key_env;
  llm.timeout = f.timeout;
  llm.max_retries = f.maxRetries;
  const tiers = { ...(llm.tiers ?? {}) };
  const slots: [string, string][] = [["strong", f.tierStrong], ["cheap", f.tierCheap], ["fast", f.tierFast]];
  for (const [slot, model] of slots) {
    if (model) tiers[slot] = { ...(tiers[slot] ?? {}), model };
    else delete tiers[slot];
  }
  llm.tiers = tiers;
  out.llm = llm;
  out.segment = { ...(out.segment ?? {}), max_chars_per_batch: f.maxBatch, max_chars_per_segment: f.maxSegment };
  const pl = { ...(out.pipeline ?? {}) };
  pl.review = f.review;
  pl.polish = f.polish;
  pl.consistency_qa = f.qa;
  pl.book_understanding = f.bookUnderstanding;
  pl.annotation_alignment = f.annotationAlignment;
  pl.review_agent_tier = f.reviewAgentTier;
  const st: Record<string, string> = {};
  for (const def of STAGE_DEFS) {
    const t = f.stageTiers[def.key];
    if (t) st[def.key] = t;
  }
  if (Object.keys(st).length > 0) pl.stage_tiers = st; else delete pl.stage_tiers;
  out.pipeline = pl;
  out.honorific = { ...(out.honorific ?? {}), strategy: f.honorific };
  out.output = {
    ...(out.output ?? {}),
    mono: f.mono, bilingual: f.bilingual, bilingual_order: f.bilingualOrder, about_page: f.aboutPage,
  };
  return JSON.stringify(out, null, 2) + "\n";
}

export function SettingsForm() {
  const [form, setForm] = useState<FormState>(DEFAULTS);
  const [snapshot, setSnapshot] = useState<any>(null); // 服务器 parsed 快照（保留未知键）
  const [loaded, setLoaded] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [srcText, setSrcText] = useState("");
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<{ line: number | null; message: string }[]>([]);
  const [savedAt, setSavedAt] = useState("");
  const [configIsJson, setConfigIsJson] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [meta, setMeta] = useState<any>(null);

  async function reload() {
    const c = await api.config();
    setForm(fromParsed(c.parsed));
    setSnapshot(c.parsed ?? null);
    setSrcText(c.raw);
    setConfigIsJson((c.path ?? "").endsWith(".json"));
    setLoaded(true);
    if (c.parsed == null && c.raw.trim() !== "") setAdvanced(true);
  }

  useEffect(() => {
    reload().catch(() => setLoaded(true));
    api.meta().then(setMeta).catch(() => {});
  }, []);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) => {
    setForm((f) => ({ ...f, [key]: value }));
    setDirty(true);
  };

  function applyPreset(preset: ProviderPreset) {
    setForm((f) => ({
      ...f,
      provider: preset.id,
      baseUrl: preset.baseUrl ?? f.baseUrl,
      apiKeyEnv: preset.apiKeyEnv ?? "",
      tierStrong: preset.tiers.strong ?? "",
      tierCheap: preset.tiers.cheap ?? "",
      tierFast: preset.tiers.fast ?? "",
    }));
    setDirty(true);
  }

  const currentPreset = PRESETS.find((p) => p.id === form.provider);

  async function save() {
    setSaving(true); setErrors([]); setSavedAt("");
    const content = advanced ? srcText : toConfigJson(form, snapshot);
    try {
      await api.saveConfig(content);
      setSavedAt(new Date().toLocaleTimeString("zh-CN"));
      setDirty(false);
      await reload(); // 刷新快照与活动路径（YAML→JSON 迁移后 path 变化）
    } catch (e: any) {
      setErrors(e.details?.errors ?? [{ line: null, message: e.message }]);
    } finally { setSaving(false); }
  }

  async function resetDefault() {
    if (!confirm("确定恢复默认配置？将清除当前全部设置（含已保存的 API Key）。")) return;
    await fetch("/api/v1/config", { method: "DELETE" });
    setForm(DEFAULTS);
    setSnapshot(null);
    setSrcText("");
    setDirty(false);
    setSavedAt("已恢复默认（下次运行时生成默认文件）");
    await reload().catch(() => {});
  }

  if (!loaded) {
    return <div className="space-y-4">{[0, 1, 2].map((i) => <Card key={i} className="h-40 animate-pulse" />)}</div>;
  }

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      {/* 顶部：保存状态条 */}
      <div className="sticky top-0 z-20 -mx-1 flex items-center justify-between gap-3 rounded-lg border border-ink-200/80 bg-paper-50/90 px-4 py-3 backdrop-blur dark:border-ink-800 dark:bg-ink-900/90">
        <div className="flex items-center gap-2 text-sm">
          {saving ? <><Spinner /> 正在保存…</>
            : savedAt ? <><Check size={14} className="text-moss-600" /> 已保存 {dirty && <span className="text-[#8a6116] dark:text-[#d8b45e]">· 有未保存修改</span>}</>
            : dirty ? <span className="flex items-center gap-1.5 text-[#8a6116] dark:text-[#d8b45e]"><span className="inline-block h-1.5 w-1.5 rounded-full bg-seal-500" aria-hidden /> 有未保存的修改</span>
            : <span className="text-ink-400">配置无改动</span>}
        </div>
        <div className="flex items-center gap-2">
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-ink-400" title="直接编辑配置源码（保存时自动迁移为 JSON）">
            <input type="checkbox" checked={advanced} onChange={(e) => { setAdvanced(e.target.checked); if (!e.target.checked) setSrcText(toConfigJson(form, snapshot)); }} />
            源码模式
          </label>
          <Button variant="secondary" size="sm" onClick={resetDefault}>恢复默认</Button>
          <Button size="sm" onClick={save} disabled={saving}>{saving ? <Spinner /> : "保存配置"}</Button>
        </div>
      </div>

      {errors.map((e, i) => (
        <div key={i} className="rounded-md border border-seal-200 bg-seal-50 px-3 py-2 text-xs text-seal-700 dark:border-seal-500/40 dark:bg-seal-500/15 dark:text-seal-300">
          {e.line != null ? `第 ${e.line} 行：` : ""}{e.message}
        </div>
      ))}

      {advanced ? (
        <Card className="p-4">
          <p className="mb-2 text-xs font-medium text-ink-500 dark:text-ink-300">
            配置源码{configIsJson ? "（config.json）" : "（当前为 YAML，保存后将自动迁移为 config.json，旧文件改名 .bak）"}
          </p>
          <textarea className="h-[32rem] w-full rounded-md border border-ink-200 bg-paper-100/60 p-3 font-mono text-xs leading-relaxed text-ink-700 dark:border-ink-700 dark:bg-ink-950 dark:text-ink-200"
            value={srcText} onChange={(e) => { setSrcText(e.target.value); setDirty(true); }} spellCheck={false} aria-label="配置源码" />
          <p className="mt-2 text-xs text-ink-400">保存时做语法 + 语义校验；退出源码模式会以表单内容重新生成（保留未识别的键）。</p>
        </Card>
      ) : (
        <>
          {/* ① 翻译方向 */}
          <Section icon={<Globe size={14} />} title="翻译方向" desc="原文语言与译文语言">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="原文语言" hint="不确定就选「自动检测」">
                <Select value={form.sourceLang} onChange={(e) => set("sourceLang", e.target.value)}>
                  {LANGS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
                </Select>
              </Field>
              <Field label="译文语言">
                <Select value={form.targetLang} onChange={(e) => set("targetLang", e.target.value)}>
                  <option value="zh">简体中文</option>
                </Select>
              </Field>
            </div>
          </Section>

          {/* ② 模型服务 */}
          <Section icon={<Cpu size={14} />} title="模型服务" desc="选择翻译用的 AI 服务商并配置密钥">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {PRESETS.map((p) => (
                <RadioCard key={p.id} icon={<Monogram ch={p.icon} />} title={p.name} desc={p.desc} badge={p.badge}
                  active={form.provider === p.id} onClick={() => applyPreset(p)} />
              ))}
            </div>
            <div className="mt-4 space-y-4">
              {form.provider === "openai-compatible" && (
                <Field label="服务地址（base_url）" required hint="例如中转站 https://api.example.com/v1">
                  <Input value={form.baseUrl} onChange={(e) => set("baseUrl", e.target.value)} placeholder="https://…" />
                </Field>
              )}
              {currentPreset?.requiresKey && (
                <Field label="API Key" hint={`明文保存在本机 config.json 中，保存后下一次任务即生效；留空则回落环境变量 ${form.apiKeyEnv || "（未设置）"}`}>
                  <div className="relative">
                    <input type="password" autoComplete="off"
                      className="w-full rounded-md border border-ink-200 bg-paper-50 px-3 py-2 pr-3 font-mono text-sm text-ink-800 placeholder-ink-300 transition-colors focus:border-seal-500 focus:outline-none focus:ring-2 focus:ring-seal-500/15 dark:border-ink-700 dark:bg-ink-950 dark:text-ink-100"
                      value={form.apiKey} onChange={(e) => set("apiKey", e.target.value)}
                      placeholder="sk-…" aria-label="API Key" />
                  </div>
                </Field>
              )}
              {currentPreset?.requiresKey && form.apiKey && (
                <div className="flex items-center gap-2 text-xs text-moss-700 dark:text-moss-100">
                  <Check size={13} /> 已填写 Key（{form.apiKey.length} 字符）· 保存后写入 config.json
                </div>
              )}
              {currentPreset?.requiresKey && !form.apiKey && (
                <div className="rounded-md border border-[#e3c98f] bg-[#faf5e6] px-3.5 py-3 text-xs leading-relaxed text-[#6d4d10] dark:border-[#5a4a22] dark:bg-[#2a2413] dark:text-[#d8b45e]">
                  <span className="flex items-start gap-1.5">
                    <Key size={13} className="mt-0.5 shrink-0" aria-hidden />
                    <span>
                      未填写 Key：引擎将从环境变量 <code className="rounded bg-[#efe4c2] px-1 dark:bg-black/30">{form.apiKeyEnv || "（未设置）"}</code> 读取。
                      建议直接在上方输入框填写，免去命令行设置环境变量的步骤。
                      {form.provider !== "deepseek" && (
                        <span className="mt-1.5 flex items-center gap-1.5">
                          环境变量名：
                          <input className="rounded border border-[#d9c48a] bg-white/70 px-1.5 py-0.5 font-mono text-[11px] dark:border-[#5a4a22] dark:bg-ink-900"
                            value={form.apiKeyEnv} onChange={(e) => set("apiKeyEnv", e.target.value)} />
                        </span>
                      )}
                    </span>
                  </span>
                </div>
              )}
              {form.provider === "fake" && (
                <div className="flex items-start gap-1.5 rounded-md border border-ink-200 bg-paper-100 px-3.5 py-2.5 text-xs leading-relaxed text-ink-500 dark:border-ink-700 dark:bg-ink-800 dark:text-ink-300">
                  <Activity size={13} className="mt-0.5 shrink-0" aria-hidden />
                  <span>体验模式不调用真实模型，译文为占位假数据，用于熟悉流程。要翻译真实书籍请切换到其他服务商。</span>
                </div>
              )}
              <details className="group">
                <summary className="cursor-pointer text-xs font-medium text-ink-400 hover:text-ink-600 dark:hover:text-ink-200">模型档位设置（质量档/均衡档/省钱档各用哪个模型）</summary>
                <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <Field label="主力模型（质量档）" hint="翻译/润色/分析默认使用">
                    <Input value={form.tierStrong} onChange={(e) => set("tierStrong", e.target.value)} placeholder="模型名" />
                  </Field>
                  <Field label="辅助模型（均衡档）" hint="审校类任务默认使用">
                    <Input value={form.tierCheap} onChange={(e) => set("tierCheap", e.target.value)} placeholder="模型名" />
                  </Field>
                  <Field label="快速模型（省钱档）" hint="梗概/抽取类任务默认使用">
                    <Input value={form.tierFast} onChange={(e) => set("tierFast", e.target.value)} placeholder="模型名" />
                  </Field>
                </div>
                <p className="mt-2 text-xs text-ink-400">留空的档位会自动回落到更高级的模型。{meta?.engineAvailable ? `当前引擎 v${meta.engineVersion}。` : ""}</p>
              </details>
            </div>
          </Section>

          {/* ②b 环节 × 模型分配 */}
          <Section icon={<Eye size={14} />} title="环节 × 模型分配" desc="按任务难度把每个环节指到不同档位的模型——模型更新换代时在这里重新分配即可">
            <p className="mb-4 text-xs leading-relaxed text-ink-400">
              每个环节标注了任务难度与推荐档位；「跟随推荐」即使用「模型档位设置」中的默认分配。保存后对下一次任务生效。
            </p>
            <div className="divide-y divide-ink-100 dark:divide-ink-800">
              {STAGE_DEFS.map((s) => (
                <div key={s.key} className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-ink-800 dark:text-paper-100">
                      {s.name}
                      <span className="ml-2 inline-flex items-center gap-1 rounded border border-ink-200 px-1.5 py-0.5 text-[10px] text-ink-400 dark:border-ink-700">
                        推荐 {TIER_LABELS[s.def]}
                      </span>
                    </p>
                    <p className="mt-0.5 text-xs leading-relaxed text-ink-400">{s.desc}</p>
                  </div>
                  <div className="flex shrink-0 rounded-md border border-ink-200 p-0.5 dark:border-ink-700" role="radiogroup" aria-label={`${s.name} 档位`}>
                    {([null, "strong", "cheap", "fast"] as (Tier | null)[]).map((t) => {
                      const active = (form.stageTiers[s.key] ?? null) === t;
                      return (
                        <button key={String(t)} type="button" role="radio" aria-checked={active}
                          className={`rounded px-2.5 py-1 text-xs font-medium transition-all ${active ? "bg-seal-600 text-paper-50" : "text-ink-400 hover:text-ink-700 dark:hover:text-ink-200"}`}
                          title={t ? TIER_NOTES[t] : `默认 ${TIER_LABELS[s.def]}（${TIER_NOTES[s.def]}）`}
                          onClick={() => {
                            const next = { ...form.stageTiers };
                            if (t) next[s.key] = t as Tier; else delete next[s.key];
                            set("stageTiers", next);
                          }}>
                          {t ? TIER_LABELS[t] : "跟随推荐"}
                        </button>
                      );
                    })}
                  </div>
                </div>
              ))}
              {/* 审校智能体三环节共用 review_agent_tier */}
              <div className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium text-ink-800 dark:text-paper-100">
                    审校智能体
                    <span className="ml-2 inline-flex items-center gap-1 rounded border border-ink-200 px-1.5 py-0.5 text-[10px] text-ink-400 dark:border-ink-700">
                      推荐 质量档
                    </span>
                  </p>
                  <p className="mt-0.5 text-xs leading-relaxed text-ink-400">审校取证 / 冲突仲裁 / 修订三个子环节共用一个档位；需要引用原文做复杂判断，建议质量档</p>
                </div>
                <div className="flex shrink-0 rounded-md border border-ink-200 p-0.5 dark:border-ink-700" role="radiogroup" aria-label="审校智能体档位">
                  {(["strong", "cheap", "fast"] as Tier[]).map((t) => (
                    <button key={t} type="button" role="radio" aria-checked={form.reviewAgentTier === t}
                      className={`rounded px-2.5 py-1 text-xs font-medium transition-all ${form.reviewAgentTier === t ? "bg-seal-600 text-paper-50" : "text-ink-400 hover:text-ink-700 dark:hover:text-ink-200"}`}
                      title={TIER_NOTES[t]}
                      onClick={() => set("reviewAgentTier", t)}>
                      {TIER_LABELS[t]}
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </Section>

          {/* ③ 翻译流程 */}
          <Section icon={<Sliders size={14} />} title="翻译流程" desc="按需开关各环节——关得越多越便宜，开得越多质量越好">
            <div className="divide-y divide-ink-100 dark:divide-ink-800">
              <Toggle checked={form.bookUnderstanding} onChange={(v) => set("bookUnderstanding", v)}
                label="全书预读" desc="翻译前先通读全书生成概览，译文更连贯（略微增加少量 token 开销）" />
              <Toggle checked={form.polish} onChange={(v) => set("polish", v)}
                label="译文润色" desc="用主力模型对译文二次润色，文笔更好，但费用约增加一倍" />
              <Toggle checked={form.review} onChange={(v) => set("review", v)}
                label="全书审校" desc="翻译完成后自动做一轮全文审校，找出漏译/误译/术语不一致" />
              <Toggle checked={form.qa} onChange={(v) => set("qa", v)}
                label="跨章一致性检查" desc="全书译完后扫描术语译法、人称等跨章不一致问题" />
              <Toggle checked={form.annotationAlignment} onChange={(v) => set("annotationAlignment", v)}
                label="EPUB 脚注对齐" desc="让译文中的脚注/尾注链接回到正确位置（EPUB 书籍建议开启）" />
            </div>
            <details className="mt-3">
              <summary className="cursor-pointer text-xs font-medium text-ink-400 hover:text-ink-600 dark:hover:text-ink-200">高级：切分与敬称</summary>
              <div className="mt-3 grid grid-cols-1 gap-4 sm:grid-cols-2">
                <Field label="每批字符数" hint="一次送翻的文本量上限（默认 1800）">
                  <Input type="number" value={form.maxBatch} min={200} max={8000} onChange={(e) => set("maxBatch", Number(e.target.value))} />
                </Field>
                <Field label="超长段拆分阈值" hint="超过此长度的段落自动按句拆分（默认 1200）">
                  <Input type="number" value={form.maxSegment} min={100} max={8000} onChange={(e) => set("maxSegment", Number(e.target.value))} />
                </Field>
                <Field label="敬称处理（日语）" hint="如何处理「ちゃん」「さん」等称呼">
                  <Select value={form.honorific} onChange={(e) => set("honorific", e.target.value)}>
                    <option value="keep_style">保留风格（小X、X君，推荐）</option>
                    <option value="normalize">统一规则</option>
                    <option value="drop">省略敬称</option>
                  </Select>
                </Field>
              </div>
            </details>
          </Section>

          {/* ④ 输出 */}
          <Section icon={<Package size={14} />} title="导出产物" desc="翻译完成后自动生成哪些文件">
            <div className="divide-y divide-ink-100 dark:divide-ink-800">
              <Toggle checked={form.mono} onChange={(v) => set("mono", v)} label="单语中文版" desc="只含译文的 EPUB（.zh.epub）" />
              <Toggle checked={form.bilingual} onChange={(v) => set("bilingual", v)} label="双语对照版" desc="原文+译文对照的 EPUB（.zh-bi.epub），原文灰色显示" />
              <Toggle checked={form.aboutPage} onChange={(v) => set("aboutPage", v)} label="书末附「关于此翻译」页" desc="注明本书由 Wenyi 自动翻译生成" />
            </div>
            {form.bilingual && (
              <div className="mt-3">
                <Field label="双语排列顺序">
                  <Select value={form.bilingualOrder} onChange={(e) => set("bilingualOrder", e.target.value)}>
                    <option value="target_first">译文在上（默认）</option>
                    <option value="source_first">原文在上</option>
                  </Select>
                </Field>
              </div>
            )}
            {!form.mono && !form.bilingual && (
              <p className="mt-2 flex items-center gap-1.5 text-xs text-[#8a6116] dark:text-[#d8b45e]">
                <AlertTriangle size={12} aria-hidden /> 两者都关闭时引擎会自动生成单语版，保证至少有一个产物。
              </p>
            )}
          </Section>

          <p className="pb-4 text-center text-xs text-ink-400">
            <Badge tone="gray">配置文件</Badge> 保存在程序同目录 <code className="mx-1">config.json</code>（旧 config.yaml 保存时自动迁移）
          </p>
        </>
      )}
    </div>
  );
}

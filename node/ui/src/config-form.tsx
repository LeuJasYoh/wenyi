// 设置页：可视化配置编辑器（面向不熟悉 YAML 的用户）+ 高级 YAML 模式。
import { useEffect, useState } from "react";
import { api } from "./api";
import { Button, Card, Section, Field, Input, Select, Toggle, RadioCard, Spinner } from "./components";

// ---- 语言选项（对应主规格 §15.1 别名表）----
const LANGS: { value: string; label: string }[] = [
  { value: "auto", label: "🌐 自动检测（由模型识别）" },
  { value: "ja", label: "🇯🇵 日语" },
  { value: "en", label: "🇬🇧 英语" },
  { value: "ko", label: "🇰🇷 韩语" },
  { value: "ru", label: "🇷🇺 俄语" },
  { value: "fr", label: "🇫🇷 法语" },
  { value: "de", label: "🇩🇪 德语" },
  { value: "es", label: "🇪🇸 西班牙语" },
  { value: "it", label: "🇮🇹 意大利语" },
  { value: "pt", label: "🇵🇹 葡萄牙语" },
  { value: "zh", label: "🇨🇳 中文" },
];

// ---- Provider 预设（含默认地址/密钥环境变量/推荐档位模型）----
interface ProviderPreset {
  id: string; icon: string; name: string; desc: string; badge?: string;
  baseUrl?: string; apiKeyEnv?: string; requiresKey: boolean;
  tiers: { strong?: string; cheap?: string; fast?: string };
}

const PRESETS: ProviderPreset[] = [
  { id: "deepseek", icon: "🐳", name: "DeepSeek", badge: "推荐", desc: "性价比高，对中文小说效果好", baseUrl: "https://api.deepseek.com", apiKeyEnv: "DEEPSEEK_API_KEY", requiresKey: true, tiers: { strong: "deepseek-v4-pro", cheap: "deepseek-v4-flash", fast: "deepseek-v4-flash" } },
  { id: "openai", icon: "🌀", name: "OpenAI", desc: "GPT 系列，效果稳定", baseUrl: "https://api.openai.com/v1", apiKeyEnv: "OPENAI_API_KEY", requiresKey: true, tiers: {} },
  { id: "openrouter", icon: "🛰️", name: "OpenRouter", desc: "一个密钥聚合多家模型", baseUrl: "https://openrouter.ai/api/v1", apiKeyEnv: "OPENROUTER_API_KEY", requiresKey: true, tiers: {} },
  { id: "gemini", icon: "✨", name: "Google Gemini", desc: "Google 官方 Gemini API", apiKeyEnv: "GEMINI_API_KEY", requiresKey: true, tiers: { strong: "gemini-3.6-flash", cheap: "gemini-3.6-flash", fast: "gemini-3.6-flash" } },
  { id: "ollama", icon: "🏠", name: "本地 Ollama", desc: "完全离线，数据不出本机", baseUrl: "http://localhost:11434/v1", requiresKey: false, tiers: {} },
  { id: "vllm", icon: "⚡", name: "本地 vLLM", desc: "自建推理服务，速度快", baseUrl: "http://localhost:8000/v1", requiresKey: false, tiers: {} },
  { id: "openai-compatible", icon: "🔌", name: "其他兼容服务", desc: "中转站或任意 OpenAI 兼容端点，需手动填地址", requiresKey: false, tiers: {} },
  { id: "fake", icon: "🧪", name: "离线体验模式", desc: "不调用任何模型，用假数据跑通全流程（试用/演示用）", requiresKey: false, tiers: { strong: "demo", cheap: "demo", fast: "demo" } },
];

// ---- 表单状态（与引擎 config schema 对齐，缺省值 = 引擎默认值）----
interface FormState {
  sourceLang: string; targetLang: string;
  provider: string; baseUrl: string; apiKeyEnv: string;
  timeout: number; maxRetries: number;
  tierStrong: string; tierCheap: string; tierFast: string;
  maxBatch: number; maxSegment: number;
  polish: boolean; review: boolean; qa: boolean; bookUnderstanding: boolean;
  annotationAlignment: boolean; honorific: string;
  mono: boolean; bilingual: boolean; bilingualOrder: string; aboutPage: boolean;
}

const DEFAULTS: FormState = {
  sourceLang: "auto", targetLang: "zh",
  provider: "deepseek", baseUrl: "https://api.deepseek.com", apiKeyEnv: "DEEPSEEK_API_KEY",
  timeout: 600, maxRetries: 4,
  tierStrong: "deepseek-v4-pro", tierCheap: "deepseek-v4-flash", tierFast: "deepseek-v4-flash",
  maxBatch: 1800, maxSegment: 1200,
  polish: true, review: false, qa: false, bookUnderstanding: true,
  annotationAlignment: true, honorific: "keep_style",
  mono: true, bilingual: false, bilingualOrder: "target_first", aboutPage: true,
};

/** 从 GET /config 的 parsed 对象填表单。 */
function fromParsed(p: any): FormState {
  const f = { ...DEFAULTS };
  const lang = p?.language ?? {};
  if (typeof lang.source === "string") f.sourceLang = lang.source;
  if (typeof lang.target === "string") f.targetLang = lang.target;
  const llm = p?.llm ?? {};
  if (typeof llm.provider === "string") f.provider = llm.provider;
  if (typeof llm.base_url === "string" && llm.base_url) f.baseUrl = llm.base_url;
  else { const preset = PRESETS.find((x) => x.id === f.provider); f.baseUrl = preset?.baseUrl ?? ""; }
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
  const out = p?.output ?? {};
  if (typeof out.mono === "boolean") f.mono = out.mono;
  if (typeof out.bilingual === "boolean") f.bilingual = out.bilingual;
  if (typeof out.bilingual_order === "string") f.bilingualOrder = out.bilingual_order;
  if (typeof out.about_page === "boolean") f.aboutPage = out.about_page;
  const hon = p?.honorific ?? {};
  if (typeof hon.strategy === "string") f.honorific = hon.strategy;
  return f;
}

function yamlStr(v: string): string {
  return /^[\w.\-/:]+$/.test(v) ? v : `"${v.replace(/"/g, '\\"')}"`;
}

/** 表单 → config.yaml 全文（键序/注释对齐引擎默认文件的写法习惯）。 */
function toYaml(f: FormState): string {
  const L: string[] = [];
  L.push("# Wenyi 配置（由设置页生成）");
  L.push("language:");
  L.push(`  source: ${f.sourceLang}`);
  L.push(`  target: ${f.targetLang}`);
  L.push("");
  L.push("llm:");
  L.push(`  provider: ${f.provider}`);
  if (f.baseUrl) L.push(`  base_url: ${yamlStr(f.baseUrl)}`);
  if (f.apiKeyEnv) L.push(`  api_key_env: ${f.apiKeyEnv}`);
  L.push(`  timeout: ${f.timeout}`);
  L.push(`  max_retries: ${f.maxRetries}`);
  L.push("  tiers:");
  L.push("    strong:");
  L.push(`      model: ${yamlStr(f.tierStrong)}`);
  L.push("    cheap:");
  L.push(`      model: ${yamlStr(f.tierCheap)}`);
  L.push("    fast:");
  L.push(`      model: ${yamlStr(f.tierFast)}`);
  L.push("");
  L.push("segment:");
  L.push(`  max_chars_per_batch: ${f.maxBatch}`);
  L.push(`  max_chars_per_segment: ${f.maxSegment}`);
  L.push("");
  L.push("pipeline:");
  L.push(`  review: ${f.review}`);
  L.push(`  polish: ${f.polish}`);
  L.push(`  consistency_qa: ${f.qa}`);
  L.push(`  book_understanding: ${f.bookUnderstanding}`);
  L.push(`  annotation_alignment: ${f.annotationAlignment}`);
  L.push("");
  L.push("honorific:");
  L.push(`  strategy: ${f.honorific}`);
  L.push("");
  L.push("output:");
  L.push(`  mono: ${f.mono}`);
  L.push(`  bilingual: ${f.bilingual}`);
  L.push(`  bilingual_order: ${f.bilingualOrder}`);
  L.push(`  about_page: ${f.aboutPage}`);
  L.push("");
  return L.join("\n");
}

export function SettingsForm() {
  const [form, setForm] = useState<FormState>(DEFAULTS);
  const [loaded, setLoaded] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [yamlText, setYamlText] = useState("");
  const [saving, setSaving] = useState(false);
  const [errors, setErrors] = useState<{ line: number | null; message: string }[]>([]);
  const [savedAt, setSavedAt] = useState("");
  const [dirty, setDirty] = useState(false);
  const [meta, setMeta] = useState<any>(null);

  useEffect(() => {
    api.config().then((c) => {
      setForm(fromParsed(c.parsed));
      setYamlText(c.raw);
      setLoaded(true);
      if (c.parsed == null && c.raw.trim() !== "") setAdvanced(true);
    }).catch(() => setLoaded(true));
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
    const content = advanced ? yamlText : toYaml(form);
    try {
      await api.saveConfig(content);
      setSavedAt(new Date().toLocaleTimeString("zh-CN"));
      setDirty(false);
      if (advanced) setForm(fromParsed((await api.config()).parsed));
    } catch (e: any) {
      setErrors(e.details?.errors ?? [{ line: null, message: e.message }]);
    } finally { setSaving(false); }
  }

  async function resetDefault() {
    if (!confirm("确定恢复默认配置？将清除当前全部设置。")) return;
    await fetch("/api/v1/config", { method: "DELETE" });
    setForm(DEFAULTS);
    setYamlText("");
    setDirty(false);
    setSavedAt("已恢复默认（下次运行引擎时生成默认文件）");
  }

  if (!loaded) {
    return <div className="space-y-4">{[0, 1, 2].map((i) => <Card key={i} className="h-40 animate-pulse" />)}</div>;
  }

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      {/* 顶部：保存状态条 */}
      <div className="sticky top-0 z-20 -mx-1 flex items-center justify-between gap-3 rounded-xl border border-gray-200/80 bg-white/85 px-4 py-3 backdrop-blur dark:border-gray-800 dark:bg-gray-900/85">
        <div className="flex items-center gap-2 text-sm">
          {saving ? <><Spinner /> 正在保存…</>
            : savedAt ? <><span className="text-green-600">✓</span> 已保存 {dirty && <span className="text-amber-600">· 有未保存修改</span>}</>
            : dirty ? <span className="text-amber-600">● 有未保存的修改</span>
            : <span className="text-gray-400">配置无改动</span>}
        </div>
        <div className="flex items-center gap-2">
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-gray-500" title="直接编辑 YAML 源文件">
            <input type="checkbox" checked={advanced} onChange={(e) => { setAdvanced(e.target.checked); if (!e.target.checked) setYamlText(toYaml(form)); }} />
            YAML 模式
          </label>
          <Button variant="secondary" size="sm" onClick={resetDefault}>恢复默认</Button>
          <Button size="sm" onClick={save} disabled={saving}>{saving ? <Spinner /> : "保存配置"}</Button>
        </div>
      </div>

      {errors.map((e, i) => (
        <div key={i} className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-800 dark:bg-red-900/30 dark:text-red-300">
          {e.line != null ? `第 ${e.line} 行：` : ""}{e.message}
        </div>
      ))}

      {advanced ? (
        <Card className="p-4">
          <p className="mb-2 text-xs font-medium text-gray-600 dark:text-gray-300">config.yaml 源文件（保存前会做语法校验）</p>
          <textarea className="h-[32rem] w-full rounded-lg border border-gray-200 bg-gray-50 p-3 font-mono text-xs leading-relaxed text-gray-800 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-200"
            value={yamlText} onChange={(e) => { setYamlText(e.target.value); setDirty(true); }} spellCheck={false} aria-label="config.yaml 源码" />
          <p className="mt-2 text-xs text-gray-400">提示：退出 YAML 模式会以表单内容覆盖此处文本。</p>
        </Card>
      ) : (
        <>
          {/* ① 翻译方向 */}
          <Section icon="🌐" title="翻译方向" desc="原文语言与译文语言">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="原文语言" hint="不确定就选「自动检测」">
                <Select value={form.sourceLang} onChange={(e) => set("sourceLang", e.target.value)}>
                  {LANGS.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
                </Select>
              </Field>
              <Field label="译文语言">
                <Select value={form.targetLang} onChange={(e) => set("targetLang", e.target.value)}>
                  <option value="zh">🇨🇳 简体中文</option>
                </Select>
              </Field>
            </div>
          </Section>

          {/* ② 模型服务 */}
          <Section icon="🤖" title="模型服务" desc="选择翻译用的 AI 服务商">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              {PRESETS.map((p) => (
                <RadioCard key={p.id} icon={p.icon} title={p.name} desc={p.desc} badge={p.badge}
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
                <div className="rounded-lg border border-amber-200 bg-amber-50 px-3.5 py-3 text-xs leading-relaxed text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
                  🔑 <b>{currentPreset.name}</b> 需要密钥。引擎将从环境变量 <code className="rounded bg-amber-100 px-1 dark:bg-amber-900/50">{form.apiKeyEnv || "（未设置）"}</code> 读取 API Key。<br />
                  设置方法：启动服务前在终端运行 <code className="rounded bg-amber-100 px-1 dark:bg-amber-900/50">set {form.apiKeyEnv}=你的密钥</code>（Windows）或写入系统环境变量。
                  {form.provider !== "deepseek" && (
                    <span className="mt-1.5 flex items-center gap-1.5">
                      环境变量名：
                      <input className="rounded border border-amber-300 bg-white/70 px-1.5 py-0.5 font-mono text-[11px] dark:border-amber-700 dark:bg-gray-900"
                        value={form.apiKeyEnv} onChange={(e) => set("apiKeyEnv", e.target.value)} />
                    </span>
                  )}
                </div>
              )}
              {form.provider === "fake" && (
                <div className="rounded-lg border border-blue-200 bg-blue-50 px-3.5 py-2.5 text-xs leading-relaxed text-blue-800 dark:border-blue-800 dark:bg-blue-900/20 dark:text-blue-200">
                  🧪 体验模式不调用真实模型，译文为占位假数据，用于熟悉流程。要翻译真实书籍请切换到其他服务商。
                </div>
              )}
              <details className="group">
                <summary className="cursor-pointer text-xs font-medium text-gray-500 hover:text-gray-700 dark:hover:text-gray-300">模型档位设置（一般保持默认即可）</summary>
                <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <Field label="主力模型（翻译）" hint="质量优先">
                    <Input value={form.tierStrong} onChange={(e) => set("tierStrong", e.target.value)} placeholder="模型名" />
                  </Field>
                  <Field label="辅助模型（审校）" hint="均衡">
                    <Input value={form.tierCheap} onChange={(e) => set("tierCheap", e.target.value)} placeholder="模型名" />
                  </Field>
                  <Field label="快速模型（梗概）" hint="省钱">
                    <Input value={form.tierFast} onChange={(e) => set("tierFast", e.target.value)} placeholder="模型名" />
                  </Field>
                </div>
                <p className="mt-2 text-xs text-gray-400">留空的档位会自动回落到更高级的模型。{meta?.engineAvailable ? `当前引擎 v${meta.engineVersion}。` : ""}</p>
              </details>
            </div>
          </Section>

          {/* ③ 翻译流程 */}
          <Section icon="⚙️" title="翻译流程" desc="按需开关各环节——关得越多越便宜，开得越多质量越好">
            <div className="divide-y divide-gray-100 dark:divide-gray-800">
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
              <summary className="cursor-pointer text-xs font-medium text-gray-500 hover:text-gray-700 dark:hover:text-gray-300">高级：切分与敬称</summary>
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
          <Section icon="📦" title="导出产物" desc="翻译完成后自动生成哪些文件">
            <div className="divide-y divide-gray-100 dark:divide-gray-800">
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
              <p className="mt-2 text-xs text-amber-600">⚠ 两者都关闭时引擎会自动生成单语版，保证至少有一个产物。</p>
            )}
          </Section>
        </>
      )}
    </div>
  );
}

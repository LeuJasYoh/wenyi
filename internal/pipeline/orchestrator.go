package pipeline

// orchestrator.go 编排核心（主规格 §15 + 分册 07 §4）。

import (
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"wenyi/internal/agents"
	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/ingest"
	"wenyi/internal/jsonx"
	"wenyi/internal/llm"
	"wenyi/internal/punct"
)

// ProgressFn (done, total, label)；total==0 表示滚动模式。
type ProgressFn func(done, total int, label string)

func callProgress(p ProgressFn, done, total int, label string) {
	if p != nil {
		p(done, total, label)
	}
}

var langAliases = map[string]string{
	"japanese": "ja", "日语": "ja", "日文": "ja", "jp": "ja", "jpn": "ja",
	"english": "en", "英语": "en", "英文": "en", "eng": "en",
	"russian": "ru", "俄语": "ru", "俄文": "ru", "rus": "ru",
	"chinese": "zh", "中文": "zh", "汉语": "zh", "zh-cn": "zh", "zho": "zh",
	"korean": "ko", "韩语": "ko", "韩文": "ko", "kor": "ko",
	"french": "fr", "法语": "fr", "法文": "fr",
	"german": "de", "德语": "de", "德文": "de",
	"spanish": "es", "西班牙语": "es", "西班牙文": "es",
	"italian": "it", "意大利语": "it", "意大利文": "it",
	"portuguese": "pt", "葡萄牙语": "pt", "葡萄牙文": "pt",
}

var langUnknown = map[string]bool{"auto": true, "unknown": true, "und": true, "uncertain": true, "mixed": true, "多语言": true, "未知": true}

var asciiLetterRe = regexp.MustCompile(`^[a-zA-Z]{2}$`)

// NormalizeLang 语言归一（主规格 §15.1）。
func NormalizeLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" || langUnknown[c] {
		return ""
	}
	if v, ok := langAliases[c]; ok {
		return v
	}
	if len([]rune(c)) >= 2 && asciiLetterRe.MatchString(string([]rune(c)[:2])) {
		return string([]rune(c)[:2])
	}
	return ""
}

// Orchestrator 全书流程驱动器。
type Orchestrator struct {
	Config             *config.Config
	Client             llm.LLMClient
	usageCheckpoint    map[string]any
	Analyzer           *agents.Analyzer
	Synopsizer         *agents.Synopsizer
	Translator         *agents.Translator
	Polisher           *agents.Polisher
	Extractor          *agents.GlossaryExtractor
	AnnotationAligner  *agents.AnnotationAligner
	Backtrans          *agents.BackTranslator

	rngMu sync.Mutex
	rng   *rand.Rand
}

// NewOrchestrator 构造（client 为 nil 时按 config 构建）。
func NewOrchestrator(cfg *config.Config, client llm.LLMClient) (*Orchestrator, error) {
	if client == nil {
		var err error
		client, err = buildClientFor(cfg)
		if err != nil {
			return nil, err
		}
	}
	o := &Orchestrator{
		Config: cfg,
		Client: client,
		rng:    rand.New(rand.NewSource(rand.Int63())),
	}
	o.usageCheckpoint = client.UsageSummary()
	o.Analyzer = agents.NewAnalyzer(client, cfg)
	o.Synopsizer = agents.NewSynopsizer(client, cfg)
	o.Translator = agents.NewTranslator(client, cfg)
	o.Polisher = agents.NewPolisher(client, cfg)
	o.Extractor = agents.NewExtractor(client, cfg)
	o.AnnotationAligner = agents.NewAnnotationAligner(client, cfg)
	o.Backtrans = agents.NewBackTranslator(client, cfg)
	return o, nil
}

// bindLLMEvents 把重试事件写入书籍日志。
func (o *Orchestrator) BindLLMEvents(store *RunStore) {
	o.Client.SetEventSink(func(event string, data map[string]any) {
		_ = store.LogEvent(event, data)
	})
}

// PunctuationEnabled 标点规范化开关。
func (o *Orchestrator) PunctuationEnabled() bool {
	target := strings.ReplaceAll(strings.ToLower(o.Config.TargetLang), "_", "-")
	return o.Config.PunctuationNormalize && (target == "zh" || strings.HasPrefix(target, "zh-"))
}

// FlushUsage 增量落盘用量（无新增 calls 不落盘）。
func (o *Orchestrator) FlushUsage(store *RunStore, scope string) (map[string]any, error) {
	current := o.Client.UsageSummary()
	increment := llm.UsageDelta(current, o.usageCheckpoint)
	o.usageCheckpoint = current
	accumulated, err := store.LoadUsage()
	if err != nil {
		return nil, err
	}
	if accumulated == nil {
		accumulated = map[string]any{"totals": map[string]any{}, "by_tier": map[string]any{}, "by_stage": map[string]any{}}
	}
	cumulative := llm.MergeUsageSummaries(accumulated, increment)
	totals, _ := increment["totals"].(map[string]any)
	if totals == nil || slotInt(totals, "calls") == 0 {
		return cumulative, nil
	}
	if err := store.SaveUsage(cumulative); err != nil {
		return nil, err
	}
	_ = store.LogEvent("usage_summary", map[string]any{
		"scope": scope, "increment": increment, "cumulative": cumulative,
	})
	return cumulative, nil
}

func slotInt(m map[string]any, key string) int64 {
	switch t := m[key].(type) {
	case int64:
		return t
	case int:
		return int64(t)
	case float64:
		return int64(t)
	}
	return 0
}

// ApplyLanguage 归一校验并同步 agent 源语言。
func (o *Orchestrator) ApplyLanguage(lang string) error {
	resolved := lang
	if resolved == "" {
		resolved = o.Config.SourceLang
	}
	source := NormalizeLang(resolved)
	target := NormalizeLang(o.Config.TargetLang)
	if source != "" && source == target {
		return fmt.Errorf("源语言与目标语言相同（%s），无需翻译；请修改 config.yaml 中的 language.source 或 language.target。", source)
	}
	o.Config.SourceLang = resolved
	for _, ag := range []interface{ SetSrc(string) }{
		o.Analyzer, o.Synopsizer, o.Translator, o.Polisher, o.Extractor, o.AnnotationAligner, o.Backtrans,
	} {
		ag.SetSrc(resolved)
	}
	return nil
}

// DetectLanguageAI 模型语言检测（失败返回 ""）。
func (o *Orchestrator) DetectLanguageAI(doc *ingest.Document) string {
	sample := SampleText(doc, false)
	runes := []rune(sample)
	if len(runes) > 1500 {
		sample = string(runes[:1500])
	}
	if sample == "" {
		return ""
	}
	system := "你是语言识别器。判断给定文本的源语言，仅输出 JSON：{\"language\": \"ISO 639-1 代码\"}；无法判断时 language 为空字符串。"
	data, err := o.Client.CompleteJSON([]llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: sample},
	}, "cheap", nil, "language_detect")
	if err != nil {
		return ""
	}
	if om, ok := data.(*jsonx.OMap); ok {
		if v, ok := om.Get("language"); ok {
			if s, ok := v.(string); ok {
				return NormalizeLang(s)
			}
		}
	}
	return ""
}

// SampleText 三点采样（labeled=true 带【开头/中部/结尾样章】标注）。
func SampleText(doc *ingest.Document, labeled bool) string {
	var texts []string
	for _, ch := range doc.Chapters {
		var parts []string
		for _, s := range ch.TextSegments() {
			parts = append(parts, s.Source)
		}
		t := strings.Join(parts, "\n")
		if len([]rune(t)) > 200 {
			texts = append(texts, t)
		}
	}
	if len(texts) == 0 {
		var parts []string
		count := 0
		for _, ch := range doc.Chapters {
			if count >= 2 {
				break
			}
			for _, s := range ch.TextSegments() {
				parts = append(parts, s.Source)
			}
			count++
		}
		t := strings.Join(parts, "\n")
		runes := []rune(t)
		if len(runes) > 6000 {
			t = string(runes[:6000])
		}
		return t
	}
	if !labeled {
		t := texts[0]
		runes := []rune(t)
		if len(runes) > 6000 {
			t = string(runes[:6000])
		}
		return t
	}
	type pick struct{ idx int; tag string }
	picks := []pick{
		{0, "开头样章"},
		{len(texts) / 2, "中部样章"},
		{len(texts) - 1, "结尾样章"},
	}
	seen := map[int]bool{}
	var blocks []string
	for _, p := range picks {
		if seen[p.idx] {
			continue
		}
		seen[p.idx] = true
		runes := []rune(texts[p.idx])
		var chunk string
		if p.tag == "结尾样章" && len(runes) > 2800 {
			chunk = string(runes[len(runes)-2800:])
		} else if len(runes) > 2800 {
			chunk = string(runes[:2800])
		} else {
			chunk = string(runes)
		}
		blocks = append(blocks, fmt.Sprintf("【%s】\n%s", p.tag, chunk))
	}
	return strings.Join(blocks, "\n\n")
}

// runDirFor 按输入解析标题定位状态目录（PDF 用文件名）。
func (o *Orchestrator) runDirFor(inputPath string, progress ProgressFn, forPDF bool) (*ingest.Document, string, error) {
	if forPDF {
		base := filepath.Base(inputPath)
		stem := base
		if i := strings.LastIndex(base, "."); i > 0 {
			stem = base[:i]
		}
		runDir := filepath.Join(o.Config.StateDir, Slugify(stem))
		if err := ensureDir(runDir); err != nil {
			return nil, "", err
		}
		doc, err := ingest.LoadDocument(inputPath, o.Config.SourceLang, o.Config.TargetLang, 0, runDir)
		if err != nil {
			return nil, "", err
		}
		return doc, runDir, nil
	}
	callProgress(progress, 0, 0, "查找翻译进度…")
	doc, runDir, err := o.parseToTemp(inputPath)
	if err != nil {
		return nil, "", err
	}
	return doc, runDir, nil
}

// parseToTemp 解析到临时目录再定位 run_dir（非 PDF：title 未知前无法确定目录）。
func (o *Orchestrator) parseToTemp(inputPath string) (*ingest.Document, string, error) {
	tmp, err := mkdtemp("wenyi-parse-")
	if err != nil {
		return nil, "", err
	}
	defer removeAll(tmp)
	doc, err := ingest.LoadDocument(inputPath, o.Config.SourceLang, o.Config.TargetLang,
		o.Config.Segment.MaxCharsPerSegment, tmp)
	if err != nil {
		return nil, "", err
	}
	return doc, filepath.Join(o.Config.StateDir, Slugify(doc.Title)), nil
}

// LocateExistingStore 定位既有状态（不创建）。
func (o *Orchestrator) LocateExistingStore(inputPath string, progress ProgressFn) (*RunStore, error) {
	forPDF := strings.EqualFold(filepath.Ext(inputPath), ".pdf")
	if forPDF {
		base := filepath.Base(inputPath)
		stem := base
		if i := strings.LastIndex(base, "."); i > 0 {
			stem = base[:i]
		}
		runDir := filepath.Join(o.Config.StateDir, Slugify(stem))
		store, err := NewRunStore(runDir, false)
		if err != nil {
			return nil, err
		}
		if !store.Exists() {
			return nil, errors.New("尚无翻译进度。请先运行 translate。")
		}
		o.BindLLMEvents(store)
		return store, nil
	}
	_, runDir, err := o.runDirFor(inputPath, progress, false)
	if err != nil {
		return nil, err
	}
	store, err := NewRunStore(runDir, false)
	if err != nil {
		return nil, err
	}
	if !store.Exists() {
		return nil, errors.New("尚无翻译进度。请先运行 translate。")
	}
	o.BindLLMEvents(store)
	return store, nil
}

// Prepare 解析 + 初始化（幂等，续跑直接返回）。
func (o *Orchestrator) Prepare(inputPath string, progress ProgressFn) (*RunStore, error) {
	forPDF := strings.EqualFold(filepath.Ext(inputPath), ".pdf")
	var doc *ingest.Document
	var runDir string
	var err error
	if forPDF {
		callProgress(progress, 0, 0, "解析文档…")
		doc, runDir, err = o.runDirFor(inputPath, progress, true)
	} else {
		callProgress(progress, 0, 0, "解析文档…")
		doc, runDir, err = o.parseToTemp(inputPath)
	}
	if err != nil {
		return nil, err
	}
	store, err := NewRunStore(runDir, true)
	if err != nil {
		return nil, err
	}
	o.BindLLMEvents(store)
	unlock, err := store.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return o.prepareLocked(doc, store, inputPath, progress)
}

func (o *Orchestrator) prepareLocked(doc *ingest.Document, store *RunStore, inputPath string, progress ProgressFn) (*RunStore, error) {
	if store.Exists() {
		_ = store.LogEvent("run_resumed", map[string]any{"input_path": inputPath, "run_dir": store.RunDir})
		return store, nil
	}
	if o.Config.SourceLang == "auto" || o.Config.SourceLang == "" {
		callProgress(progress, 0, 0, "识别语言…")
		detected := o.DetectLanguageAI(doc)
		if detected == "" {
			_ = store.LogEvent("language_detection_failed", map[string]any{"source_lang": doc.SourceLang})
			return nil, errors.New("自动识别源语言失败：请检查模型配置，或在 config.yaml 的 language.source 指定 ISO 639-1 语言代码（如 ja/en/ko/ru/fr/de/es）。")
		}
		doc.SourceLang = detected
		_ = store.LogEvent("language_detected", map[string]any{"source_lang": detected})
	}
	if err := o.ApplyLanguage(doc.SourceLang); err != nil {
		return nil, err
	}
	manifest, err := store.StageDocument(doc)
	if err != nil {
		return nil, err
	}
	glossaryStore, err := glossary.OpenStore(store.GlossaryPath())
	if err != nil {
		return nil, err
	}
	defer glossaryStore.Close()
	callProgress(progress, 0, 0, "分析全书风格…")
	analysis := map[string]any{}
	sample := SampleText(doc, true)
	if sample != "" {
		om, err := o.Analyzer.Analyze(sample)
		if err != nil {
			return nil, err
		}
		analysis = omapToMap(om)
	}
	hasAnalysis := len(analysis) > 0
	if hasAnalysis {
		if _, err := o.Analyzer.SeedGlossary(glossaryStore, mapToOMap(analysis)); err != nil {
			return nil, err
		}
	}
	if err := store.SaveAnalysis(analysis); err != nil {
		return nil, err
	}
	_ = store.LogEvent("analysis_saved", map[string]any{"has_analysis": hasAnalysis})
	ctx := NewRollingContext(maxInt(40, o.Config.Pipeline.RollingContextSegments))
	if err := store.SaveContext(ctx.ToDict()); err != nil {
		return nil, err
	}
	// manifest 是初始化完成标志，必须最后原子落盘
	manifest["initialized"] = true
	if err := store.SaveManifest(manifest); err != nil {
		return nil, err
	}
	p := o.Config.Pipeline
	_ = store.LogEvent("run_initialized", map[string]any{
		"input_path": inputPath, "run_dir": store.RunDir, "title": doc.Title, "fmt": doc.Fmt,
		"source_lang": doc.SourceLang, "target_lang": doc.TargetLang,
		"chapters": len(doc.Chapters),
		"config": map[string]any{
			"review": p.Review, "polish": p.Polish,
			"backtranslate_sample": p.BacktranslateSample, "consistency_qa": p.ConsistencyQA,
			"book_understanding": p.BookUnderstanding, "review_concurrency": p.ReviewConcurrency,
			"review_output_retries": p.ReviewOutputRetries,
		},
	})
	return store, nil
}

// Run 翻译（only_chapter 调试入口）。
func (o *Orchestrator) Run(inputPath string, onlyChapter *int, progress ProgressFn) (*RunStore, error) {
	store, err := o.Prepare(inputPath, progress)
	if err != nil {
		return nil, err
	}
	unlock, err := store.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return o.RunLocked(store, onlyChapter, progress)
}

// PrepareForTranslation 准备模式（预扫，不翻正文）。
func (o *Orchestrator) PrepareForTranslation(inputPath string, progress ProgressFn) (*RunStore, error) {
	store, err := o.Prepare(inputPath, progress)
	if err != nil {
		return nil, err
	}
	unlock, err := store.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	m, err := store.LoadManifest()
	if err != nil {
		return nil, err
	}
	lang, _ := m["source_lang"].(string)
	if lang == "" {
		lang = o.Config.SourceLang
	}
	if err := o.ApplyLanguage(lang); err != nil {
		return nil, err
	}
	var synopsis string
	func() {
		defer func() {
			if r := recover(); r != nil {
				panic(r)
			}
		}()
		synopsis, err = o.BuildUnderstanding(store, progress)
	}()
	if err != nil {
		_, _ = o.FlushUsage(store, "prepare")
		return nil, err
	}
	_ = store.LogEvent("translation_prepared", map[string]any{
		"input_path": inputPath, "book_understanding": synopsis,
	})
	_, _ = o.FlushUsage(store, "prepare")
	return store, nil
}

// RunLocked 翻译主循环（须持锁）。
func (o *Orchestrator) RunLocked(store *RunStore, onlyChapter *int, progress ProgressFn) (*RunStore, error) {
	m, err := store.LoadManifest()
	if err != nil {
		return nil, err
	}
	lang, _ := m["source_lang"].(string)
	if lang == "" {
		lang = o.Config.SourceLang
	}
	if err := o.ApplyLanguage(lang); err != nil {
		return nil, err
	}
	chapterItems, _ := m["chapters"].([]any)
	indexSet := map[int]bool{}
	var available []int
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if idx, ok := cm["index"].(int64); ok {
			indexSet[int(idx)] = true
			available = append(available, int(idx))
		}
	}
	if onlyChapter != nil && !indexSet[*onlyChapter] {
		if len(available) == 0 {
			return nil, errors.New("章节编号 不存在；无可翻译章节")
		}
		return nil, fmt.Errorf("章节编号 %d 不存在；可用范围：0–%d", *onlyChapter, available[len(available)-1])
	}
	glossaryStore, err := glossary.OpenStore(store.GlossaryPath())
	if err != nil {
		return nil, err
	}
	defer glossaryStore.Close()
	contextRaw, err := store.LoadContext()
	if err != nil {
		return nil, err
	}
	if contextRaw == nil {
		contextRaw = map[string]any{}
	}
	context := RollingContextFromDict(contextRaw, maxInt(40, o.Config.Pipeline.RollingContextSegments))
	analysis, err := store.LoadAnalysis()
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		analysis = map[string]any{}
	}
	style := o.Analyzer.StyleBrief(mapToOMap(analysis))
	bookSynopsis, err := o.BuildUnderstanding(store, progress)
	if err != nil {
		return nil, err
	}
	var targets []int
	if onlyChapter != nil {
		targets = []int{*onlyChapter}
	} else {
		targets, err = store.PendingChapters()
		if err != nil {
			return nil, err
		}
	}
	var progressChapters []int
	if onlyChapter != nil {
		progressChapters = []int{*onlyChapter}
	} else {
		for _, item := range chapterItems {
			cm, _ := item.(map[string]any)
			if cm != nil {
				if idx, ok := cm["index"].(int64); ok {
					progressChapters = append(progressChapters, int(idx))
				}
			}
		}
	}
	total, done, err := o.progressCounts(store, progressChapters)
	if err != nil {
		return nil, err
	}
	history, sourceCorpus, err := loadTranslationInputs(store)
	if err != nil {
		return nil, err
	}
	var onlyChapterAny any
	if onlyChapter != nil {
		onlyChapterAny = *onlyChapter
	}
	targetsAny := make([]any, len(targets))
	for i, t := range targets {
		targetsAny[i] = t
	}
	_ = store.LogEvent("translate_run_started", map[string]any{
		"only_chapter": onlyChapterAny, "chapters": targetsAny, "total_segments": total,
	})
	defer func() {
		glossaryStore.Close()
		_, _ = o.FlushUsage(store, "translate")
	}()
	for _, ci := range targets {
		done, err = o.TranslateChapter(ci, store, glossaryStore, context, style, bookSynopsis,
			history, sourceCorpus, progress, done, total)
		if err != nil {
			return nil, err
		}
		if err := store.SaveContext(context.ToDict()); err != nil {
			return nil, err
		}
		if _, err := o.FlushUsage(store, "chapter"); err != nil {
			return nil, err
		}
	}
	pending, err := store.PendingChapters()
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		if err := o.TranslateTitles(store, glossaryStore, progress); err != nil {
			return nil, err
		}
	}
	callProgress(progress, total, total, "翻译完成")
	_ = store.LogEvent("translate_run_finished", map[string]any{"total_segments": total})
	return store, nil
}

// BuildUnderstanding 全书理解预扫（幂等）。
func (o *Orchestrator) BuildUnderstanding(store *RunStore, progress ProgressFn) (string, error) {
	if !o.Config.Pipeline.BookUnderstanding {
		_ = store.LogEvent("book_understanding_skipped", map[string]any{"reason": "disabled"})
		return "", nil
	}
	m, err := store.LoadManifest()
	if err != nil {
		return "", err
	}
	chapterItems, _ := m["chapters"].([]any)
	loaded := map[int]*ingest.Chapter{}
	var order []int
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := cm["index"].(int64)
		if !ok {
			continue
		}
		ch, err := store.LoadChapter(int(idx))
		if err != nil {
			return "", err
		}
		loaded[int(idx)] = ch
		order = append(order, int(idx))
	}
	type todoItem struct {
		ci  int
		src string
	}
	var todo []todoItem
	for _, ci := range order {
		ch := loaded[ci]
		if _, done := ch.Meta["source_digest"]; !done {
			var parts []string
			for _, s := range ch.TextSegments() {
				parts = append(parts, s.Source)
			}
			todo = append(todo, todoItem{ci, strings.Join(parts, "\n")})
		}
	}
	if len(todo) > 0 {
		workers := maxInt(1, o.Config.Pipeline.PrescanConcurrency)
		chaptersAny := make([]any, len(todo))
		for i, t := range todo {
			chaptersAny[i] = t.ci
		}
		_ = store.LogEvent("book_understanding_chapter_digest_started", map[string]any{
			"chapters": chaptersAny, "workers": workers,
		})
		callProgress(progress, 0, len(todo), "预扫章节梗概")
		type result struct {
			ci     int
			digest string
		}
		sem := make(chan struct{}, workers)
		results := make(chan result, len(todo))
		var wg sync.WaitGroup
		for _, t := range todo {
			wg.Add(1)
			go func(t todoItem) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				results <- result{t.ci, o.Synopsizer.DigestChapter(t.src)}
			}(t)
		}
		go func() {
			wg.Wait()
			close(results)
		}()
		nDone := 0
		for r := range results {
			ch := loaded[r.ci]
			ch.Meta["source_digest"] = r.digest
			if err := store.SaveChapter(ch); err != nil {
				return "", err
			}
			_ = store.LogEvent("book_understanding_chapter_digest_saved", map[string]any{
				"chapter": r.ci, "digest": r.digest,
			})
			nDone++
			callProgress(progress, nDone, len(todo), "预扫章节梗概")
		}
	}
	digests := make([]string, 0, len(order))
	for _, ci := range order {
		d, _ := loaded[ci].Meta["source_digest"].(string)
		digests = append(digests, d)
	}
	analysis, err := store.LoadAnalysis()
	if err != nil {
		return "", err
	}
	if analysis == nil {
		analysis = map[string]any{}
	}
	synopsis, _ := analysis["book_synopsis"].(string)
	if synopsis == "" {
		hasDigest := false
		for _, d := range digests {
			if d != "" {
				hasDigest = true
				break
			}
		}
		if hasDigest {
			callProgress(progress, 0, 0, "生成全书概览…")
			synopsis = o.Synopsizer.BookSynopsis(digests, o.Analyzer.StyleBrief(mapToOMap(analysis)))
			analysis["book_synopsis"] = synopsis
			if err := store.SaveAnalysis(analysis); err != nil {
				return "", err
			}
			_ = store.LogEvent("book_synopsis_saved", map[string]any{"synopsis": synopsis})
		}
	}
	return synopsis, nil
}

// TranslateChapter 单章翻译主循环。
func (o *Orchestrator) TranslateChapter(ci int, store *RunStore, glossaryStore *glossary.Store,
	context *RollingContext, style, bookSynopsis string, history map[[2]int]agents.TranslatedSegmentEvidence,
	sourceCorpus string, progress ProgressFn, done, total int) (int, error) {
	chapter, err := store.LoadChapter(ci)
	if err != nil {
		return done, err
	}
	textSegs := chapter.TextSegments()
	if len(textSegs) == 0 {
		if err := store.SetChapterStatus(ci, StatusDone); err != nil {
			return done, err
		}
		_ = store.LogEvent("chapter_skipped", map[string]any{"chapter": ci, "reason": "empty"})
		return done, nil
	}
	chapterDigest, _ := chapter.Meta["source_digest"].(string)
	batches := ResumeBatches(textSegs, o.Config.Segment.MaxCharsPerBatch)
	label := ChapterProgressLabel(chapter.Title, ci)
	callProgress(progress, done, total, label)
	glossaryCheckpoints := store.CompletedBatchGlossaryKeys(ci)
	termSnapshot, err := o.chapterTermSnapshot(glossaryStore, textSegs)
	if err != nil {
		return done, err
	}
	var btSamples [][2]string
	segBase := 0
	for _, b := range batches {
		batchStart := segBase
		glossaryKey := BatchGlossaryKey(batchStart, len(b))
		existingCount := 0
		for _, s := range b {
			if strings.TrimSpace(s.TargetText()) != "" {
				existingCount++
			}
		}
		if existingCount == len(b) {
			// 断点复用：整批已译
			o.AlignAnnotationsAfterBatch(ci, chapter, batchStart, len(b), store)
			var targets []string
			for _, s := range b {
				targets = append(targets, s.TargetText())
			}
			context.AddTargets(targets)
			syncContextChapterPrefix(context, textSegs, batchStart+len(b))
			var summary map[string]int
			if glossaryCheckpoints[glossaryKey] {
				summary = map[string]int{"inserted": 0, "conflict": 0, "unchanged": 0, "skipped": 1}
			} else {
				summary, err = o.extractBatchGlossary(glossaryStore, store, ci, batchStart, b, history, sourceCorpus)
				if err != nil {
					return done, err
				}
				glossaryCheckpoints[glossaryKey] = true
			}
			termSnapshot, err = o.chapterTermSnapshot(glossaryStore, textSegs)
			if err != nil {
				return done, err
			}
			_ = store.LogEvent("batch_skipped", map[string]any{
				"chapter": ci, "start_index": batchStart, "count": len(b),
				"reason": "already_translated", "glossary_extraction": summary,
				"segments": batchSegmentsField(b),
			})
			segBase += len(b)
			callProgress(progress, done, total, label)
			continue
		}
		// 正常路径
		ctxText := context.Render(o.Config.Pipeline.RollingContextSegments)
		targets, bt, err := o.processBatch(b, termSnapshot, ctxText, style, bookSynopsis, chapterDigest)
		if err != nil {
			return done, err
		}
		btSamples = append(btSamples, bt...)
		for i, s := range b {
			s.SetTarget(targets[i])
		}
		if err := store.SaveChapter(chapter); err != nil {
			return done, err
		}
		o.AlignAnnotationsAfterBatch(ci, chapter, batchStart, len(b), store)
		context.AddTargets(targets)
		syncContextChapterPrefix(context, textSegs, batchStart+len(b))
		_ = store.LogEvent("batch_translated", map[string]any{
			"chapter": ci, "start_index": batchStart, "count": len(b),
			"polished": o.Config.Pipeline.Polish,
			"punctuation_normalized": o.PunctuationEnabled(),
			"backtranslate_sample_count": len(bt),
			"segments":                  batchSegmentsField(b),
		})
		done += len(b)
		segBase += len(b)
		callProgress(progress, done, total, label)
		// 译文落盘后再抽取术语
		if _, err := o.extractBatchGlossary(glossaryStore, store, ci, batchStart, b, history, sourceCorpus); err != nil {
			return done, err
		}
		updateTranslationHistory(history, ci, batchStart, b)
		glossaryCheckpoints[glossaryKey] = true
		termSnapshot, err = o.chapterTermSnapshot(glossaryStore, textSegs)
		if err != nil {
			return done, err
		}
	}
	// 章末标点规范化
	if o.PunctuationEnabled() {
		translated := make([]string, len(textSegs))
		continuations := make([]bool, len(textSegs))
		for i, s := range textSegs {
			translated[i] = s.TargetText()
			continuations[i] = s.Cont
		}
		normalized, err := punct.NormalizeZHSegments(translated, continuations)
		if err == nil {
			for i, s := range textSegs {
				s.SetTarget(normalized[i])
			}
			retained := minInt(len(normalized), len(context.RecentTargets))
			copy(context.RecentTargets[len(context.RecentTargets)-retained:], normalized[len(normalized)-retained:])
		}
		updateTranslationHistory(history, ci, 0, textSegs)
	}
	// 章末术语兜底抽取
	var srcParts, tgtParts []string
	for _, s := range textSegs {
		srcParts = append(srcParts, s.Source)
		tgtParts = append(tgtParts, s.TargetText())
	}
	var historyList []agents.TranslatedSegmentEvidence
	for _, ev := range history {
		historyList = append(historyList, ev)
	}
	sort.SliceStable(historyList, func(i, j int) bool {
		if historyList[i].Chapter != historyList[j].Chapter {
			return historyList[i].Chapter < historyList[j].Chapter
		}
		return historyList[i].Segment < historyList[j].Segment
	})
	before := [2]int{ci, len(textSegs)}
	summary, err := o.Extractor.ExtractAndStore(glossaryStore,
		strings.Join(srcParts, "\n"), strings.Join(tgtParts, "\n"), ci, historyList, &before, sourceCorpus)
	if err != nil {
		return done, err
	}
	_ = store.LogEvent("chapter_glossary_extracted", map[string]any{"chapter": ci, "summary": summary})
	// 回译抽检
	var btIssues []map[string]any
	if len(btSamples) > 0 {
		srcs := make([]string, len(btSamples))
		tgts := make([]string, len(btSamples))
		for i, pair := range btSamples {
			srcs[i] = pair[0]
			tgts[i] = pair[1]
		}
		for _, om := range o.Backtrans.Check(srcs, tgts) {
			issue := omapToMap(om)
			issue["chapter"] = ci
			btIssues = append(btIssues, issue)
		}
		_ = store.LogEvent("chapter_backtranslation_checked", map[string]any{
			"chapter": ci, "sample_count": len(btSamples), "issue_count": len(btIssues), "issues": btIssues,
		})
	}
	chapter.Meta["backtranslation_issues"] = btIssues
	if err := store.SaveChapter(chapter); err != nil {
		return done, err
	}
	if err := store.SetChapterStatus(ci, StatusDone); err != nil {
		return done, err
	}
	_ = store.LogEvent("chapter_done", map[string]any{
		"chapter": ci, "title": chapter.Title, "segment_count": len(textSegs),
		"backtranslation_issue_count": len(btIssues),
	})
	return done, nil
}

func batchSegmentsField(b []*ingest.Segment) []map[string]any {
	out := make([]map[string]any, 0, len(b))
	for _, s := range b {
		var target any
		if s.Target != nil {
			target = *s.Target
		}
		out = append(out, map[string]any{
			"index": s.Index, "source": s.Source, "target": target,
		})
	}
	return out
}

// chapterTermSnapshot 本章术语快照（glossary_scope=chapter 裁剪）。
func (o *Orchestrator) chapterTermSnapshot(glossaryStore *glossary.Store, textSegs []*ingest.Segment) ([]*glossary.Term, error) {
	terms, err := glossaryStore.AllTerms()
	if err != nil {
		return nil, err
	}
	if o.Config.Pipeline.GlossaryScope != "chapter" {
		return terms, nil
	}
	var parts []string
	for _, s := range textSegs {
		parts = append(parts, s.Source)
	}
	hits := glossary.TermsIn(terms, strings.Join(parts, "\n"))
	hitSet := map[string]bool{}
	for _, t := range hits {
		hitSet[t.Source] = true
	}
	var out []*glossary.Term
	for _, t := range terms {
		if hitSet[t.Source] {
			out = append(out, t)
		}
	}
	return out, nil
}

func (o *Orchestrator) extractBatchGlossary(glossaryStore *glossary.Store, store *RunStore, chapter, startIndex int,
	batch []*ingest.Segment, history map[[2]int]agents.TranslatedSegmentEvidence, sourceCorpus string) (map[string]int, error) {
	var srcParts, tgtParts []string
	for _, s := range batch {
		srcParts = append(srcParts, s.Source)
		tgtParts = append(tgtParts, s.TargetText())
	}
	var historyList []agents.TranslatedSegmentEvidence
	for _, ev := range history {
		historyList = append(historyList, ev)
	}
	sort.SliceStable(historyList, func(i, j int) bool {
		if historyList[i].Chapter != historyList[j].Chapter {
			return historyList[i].Chapter < historyList[j].Chapter
		}
		return historyList[i].Segment < historyList[j].Segment
	})
	before := [2]int{chapter, startIndex}
	summary, err := o.Extractor.ExtractAndStore(glossaryStore,
		strings.Join(srcParts, "\n"), strings.Join(tgtParts, "\n"), chapter, historyList, &before, sourceCorpus)
	if err != nil {
		return nil, err
	}
	_ = store.LogEvent("batch_glossary_extracted", map[string]any{
		"chapter": chapter, "start_index": startIndex, "count": len(batch), "summary": summary,
	})
	return summary, nil
}

// processBatch 整批翻译（+润色+回译采样）。
func (o *Orchestrator) processBatch(batch []*ingest.Segment, terms []*glossary.Term, ctxText, style, bookSynopsis, chapterDigest string) ([]string, [][2]string, error) {
	sources := make([]string, len(batch))
	for i, s := range batch {
		sources[i] = s.Source
	}
	lites := make([]agents.GlossaryTermLite, len(terms))
	for i, t := range terms {
		lites[i] = agents.GlossaryTermLite{
			Source: t.Source, Target: t.Target, Reading: t.Reading,
			Type: t.Type, Gender: t.Gender, Aliases: t.Aliases,
		}
	}
	targets, err := o.Translator.TranslateBatch(sources, lites, style, ctxText, bookSynopsis, chapterDigest)
	if err != nil {
		return nil, nil, err
	}
	if o.Config.Pipeline.Polish {
		polished := o.Polisher.Polish(targets, terms, style)
		if len(polished) == len(targets) {
			targets = polished
		}
	}
	var btSamples [][2]string
	rate := o.Config.Pipeline.BacktranslateSample
	if rate > 0 {
		o.rngMu.Lock()
		for i, s := range batch {
			if o.rng.Float64() < rate {
				btSamples = append(btSamples, [2]string{s.Source, targets[i]})
			}
		}
		o.rngMu.Unlock()
	}
	return targets, btSamples, nil
}

// ---- 静态辅助 ----

// ResumeBatches 按预算分批后沿完成边界切开。
func ResumeBatches(segments []*ingest.Segment, maxChars int) [][]*ingest.Segment {
	raw := batchByChars(segments, maxChars)
	var out [][]*ingest.Segment
	var current []*ingest.Segment
	currentDone := -1
	flush := func() {
		if len(current) > 0 {
			out = append(out, current)
			current = nil
		}
	}
	for _, batch := range raw {
		for _, seg := range batch {
			isDone := strings.TrimSpace(seg.TargetText()) != ""
			if currentDone == -1 {
				currentDone = boolToInt(isDone)
			}
			if boolToInt(isDone) != currentDone {
				flush()
				currentDone = boolToInt(isDone)
			}
			current = append(current, seg)
		}
		flush()
		currentDone = -1
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func batchByChars(segments []*ingest.Segment, maxChars int) [][]*ingest.Segment {
	var out [][]*ingest.Segment
	var cur []*ingest.Segment
	curLen := 0
	for _, seg := range segments {
		slen := len([]rune(seg.Source))
		if len(cur) > 0 && curLen+slen > maxChars {
			out = append(out, cur)
			cur = nil
			curLen = 0
		}
		cur = append(cur, seg)
		curLen += slen
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func loadTranslationInputs(store *RunStore) (map[[2]int]agents.TranslatedSegmentEvidence, string, error) {
	history := map[[2]int]agents.TranslatedSegmentEvidence{}
	var sourceParts []string
	m, err := store.LoadManifest()
	if err != nil {
		return nil, "", err
	}
	chapterItems, _ := m["chapters"].([]any)
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ci, ok := cm["index"].(int64)
		if !ok {
			continue
		}
		ch, err := store.LoadChapter(int(ci))
		if err != nil {
			return nil, "", err
		}
		for _, seg := range ch.Segments {
			sourceParts = append(sourceParts, seg.Source)
			if strings.TrimSpace(seg.TargetText()) != "" {
				history[[2]int{int(ci), seg.Index}] = agents.TranslatedSegmentEvidence{
					Chapter: int(ci), Segment: seg.Index, Source: seg.Source, Target: seg.TargetText(),
				}
			}
		}
	}
	return history, strings.Join(sourceParts, "\n"), nil
}

func updateTranslationHistory(history map[[2]int]agents.TranslatedSegmentEvidence, chapter, startIndex int, segments []*ingest.Segment) {
	for offset, seg := range segments {
		if strings.TrimSpace(seg.TargetText()) != "" {
			history[[2]int{chapter, startIndex + offset}] = agents.TranslatedSegmentEvidence{
				Chapter: chapter, Segment: startIndex + offset,
				Source: seg.Source, Target: seg.TargetText(),
			}
		}
	}
}

func (o *Orchestrator) progressCounts(store *RunStore, chapterIndices []int) (int, int, error) {
	total := 0
	done := 0
	for _, ci := range chapterIndices {
		ch, err := store.LoadChapter(ci)
		if err != nil {
			return 0, 0, err
		}
		segs := ch.TextSegments()
		total += len(segs)
		for _, b := range ResumeBatches(segs, o.Config.Segment.MaxCharsPerBatch) {
			allDone := true
			for _, s := range b {
				if strings.TrimSpace(s.TargetText()) == "" {
					allDone = false
					break
				}
			}
			if allDone {
				done += len(b)
			}
		}
	}
	return total, done, nil
}

// syncContextChapterPrefix 把当前章已完成前缀同步回上下文尾部。
func syncContextChapterPrefix(context *RollingContext, segments []*ingest.Segment, end int) {
	if end < 0 {
		end = 0
	}
	if end > len(segments) {
		end = len(segments)
	}
	if end == 0 {
		return
	}
	prefix := segments[:end]
	var targets []string
	for _, s := range prefix {
		if strings.TrimSpace(s.TargetText()) == "" {
			return
		}
		targets = append(targets, s.TargetText())
	}
	retained := minInt(len(targets), len(context.RecentTargets))
	copy(context.RecentTargets[len(context.RecentTargets)-retained:], targets[len(targets)-retained:])
}

// completedLogicalStartsInRange 范围内已完逻辑段的起始下标（text_segments 基）。
func completedLogicalStartsInRange(segments []*ingest.Segment, start, count int) []int {
	if count <= 0 || len(segments) == 0 {
		return nil
	}
	lower := maxInt(0, start)
	upper := minInt(len(segments), lower+count)
	var out []int
	position := lower
	for position < upper {
		logicalStart := position
		for logicalStart > 0 && segments[logicalStart].Cont {
			logicalStart--
		}
		logicalEnd := position
		for logicalEnd+1 < len(segments) && segments[logicalEnd+1].Cont {
			logicalEnd++
		}
		if logicalEnd < upper {
			out = append(out, logicalStart)
		}
		position = maxInt(position+1, logicalEnd+1)
	}
	return out
}

// ChapterProgressLabel 章进度标签。
func ChapterProgressLabel(title string, index int) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return fmt.Sprintf("章节 %d", index+1)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func omapToMap(om *jsonx.OMap) map[string]any {
	out := map[string]any{}
	if om == nil {
		return out
	}
	for _, k := range om.Keys() {
		v, _ := om.Get(k)
		out[k] = v
	}
	return out
}

func mapToOMap(m map[string]any) *jsonx.OMap {
	om := jsonx.NewOMap()
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		om.Set(k, m[k])
	}
	return om
}

package pipeline

// steps.go run_steps / run_all / QA / report（主规格 §15.8 + §13.9）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wenyi/internal/agents"
	"wenyi/internal/glossary"
	"wenyi/internal/jsonx"
	"wenyi/internal/review"
)

// AssembleFunc 由 assemble 阶段（Phase 5 Node 组件）注入；返回产物路径。
var AssembleFunc func(store *RunStore, inputPath string, outPath *string, outFormat string,
	bilingual bool, order string, preserveSourceStyle bool, aboutPage bool, pdfEngine string) (string, error)

// RunReviewFunc 由 review 阶段（Phase 4）注入。
var RunReviewFunc func(o *Orchestrator, store *RunStore, terms []*glossary.Term, progress ProgressFn) (*review.Outcome, error)

// RunSteps 按步骤集执行（review-only 特判；translate 在锁外、其余在锁内）。
func (o *Orchestrator) RunSteps(inputPath string, steps []string, progress ProgressFn,
	outFormat string, outPath *string, pdfEngine string) (map[string]any, error) {
	stepSet := map[string]bool{}
	for _, s := range steps {
		stepSet[s] = true
	}
	runStepsInput := append([]string{}, steps...)
	sort.Strings(runStepsInput)
	if len(stepSet) == 1 && stepSet["review"] {
		store, err := o.RunReview(inputPath, progress)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"store": store, "output": nil, "outputs": []string{}, "report": nil,
			"review_issues": []map[string]any{}, "review_changes": []map[string]any{},
			"review_result": nil, "review_dir": "", "qa_issues": []map[string]any{},
		}, nil
	}
	var store *RunStore
	var err error
	if stepSet["translate"] {
		store, err = o.Run(inputPath, nil, progress)
	} else {
		store, err = o.Prepare(inputPath, progress)
	}
	if err != nil {
		return nil, err
	}
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
	unlock, err := store.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return o.finishStepsLocked(store, inputPath, stepSet, runStepsInput, progress, outFormat, outPath, pdfEngine)
}

// RunAll 全流程。
func (o *Orchestrator) RunAll(inputPath string, progress ProgressFn, outFormat string, outPath *string,
	doQA *bool, pdfEngine string) (map[string]any, error) {
	steps := []string{"translate", "report", "assemble"}
	if o.Config.Pipeline.Review {
		steps = append(steps, "review")
	}
	qa := o.Config.Pipeline.ConsistencyQA
	if doQA != nil {
		qa = *doQA
	}
	if qa {
		steps = append(steps, "qa")
	}
	return o.RunSteps(inputPath, steps, progress, outFormat, outPath, pdfEngine)
}

// RunReview 独立只读审校（Phase 4 实现 RunReviewFunc）。
func (o *Orchestrator) RunReview(inputPath string, progress ProgressFn) (*RunStore, error) {
	store, err := o.LocateExistingStore(inputPath, progress)
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
	if RunReviewFunc == nil {
		return nil, errors.New("Review 阶段尚未实现（阶段 4）")
	}
	terms, cleanup, err := glossary.LoadTermsReadonly(store.GlossaryPath())
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if _, err := RunReviewFunc(o, store, terms, progress); err != nil {
		return nil, err
	}
	return store, nil
}

func (o *Orchestrator) finishStepsLocked(store *RunStore, inputPath string, stepSet map[string]bool,
	runStepsInput []string, progress ProgressFn, outFormat string, outPath *string, pdfEngine string) (map[string]any, error) {
	_ = store.LogEvent("run_steps_started", map[string]any{"steps": toAnySlice(runStepsInput), "input_path": inputPath})
	var glossaryStore *glossary.Store
	var err error
	if stepSet["qa"] || stepSet["report"] {
		glossaryStore, err = glossary.OpenStore(store.GlossaryPath())
		if err != nil {
			return nil, err
		}
	}
	var reviewIssues []map[string]any
	var reviewChanges []map[string]any
	var reviewResult map[string]any
	reviewDir := ""
	var qaIssues []*jsonx.OMap
	result := map[string]any{}
	defer func() {
		if glossaryStore != nil {
			glossaryStore.Close()
		}
		_, _ = o.FlushUsage(store, "pipeline")
	}()
	if stepSet["review"] {
		if _, err := o.FlushUsage(store, "pipeline"); err != nil {
			return nil, err
		}
		if RunReviewFunc == nil {
			return nil, errors.New("Review 阶段尚未实现（阶段 4）")
		}
		var terms []*glossary.Term
		if glossaryStore != nil {
			terms, err = glossaryStore.AllTerms()
			if err != nil {
				return nil, err
			}
		} else {
			var cleanup func()
			terms, cleanup, err = glossary.LoadTermsReadonly(store.GlossaryPath())
			if err != nil {
				return nil, err
			}
			defer cleanup()
		}
		outcome, err := RunReviewFunc(o, store, terms, progress)
		if err != nil {
			return nil, err
		}
		reviewIssues = outcome.Issues
		reviewChanges = outcome.Changes
		reviewResult = outcome.Result
		reviewDir = outcome.RunDir
	}
	if stepSet["qa"] {
		if glossaryStore == nil {
			return nil, errors.New("QA 需要术语库")
		}
		callProgress(progress, 0, 0, "一致性 QA…")
		terms, err := glossaryStore.AllTerms()
		if err != nil {
			return nil, err
		}
		digests, err := o.buildConsistencyDigests(store)
		if err != nil {
			return nil, err
		}
		checker := agents.NewConsistencyChecker(o.Client, o.Config)
		omIssues := checker.CheckRaw(digests, terms)
		qaIssues = omIssues
		issueCount := len(omIssues)
		issuesField := make([]any, 0, len(omIssues))
		for _, om := range omIssues {
			issuesField = append(issuesField, omapToMap(om))
		}
		_ = store.LogEvent("consistency_qa_finished", map[string]any{
			"issue_count": issueCount, "issues": issuesField,
		})
	}
	if _, err := o.FlushUsage(store, "pipeline"); err != nil {
		return nil, err
	}
	var report map[string]any
	if stepSet["report"] {
		if glossaryStore == nil {
			return nil, errors.New("报告生成需要术语库")
		}
		callProgress(progress, 0, 0, "生成报告…")
		report, err = BuildReport(store, glossaryStore)
		if err != nil {
			return nil, err
		}
		var consistencyIssues []any
		for _, om := range qaIssues {
			consistencyIssues = append(consistencyIssues, omapToMap(om))
		}
		report["consistency_issues"] = consistencyIssues
		if err := store.SaveReport(report); err != nil {
			return nil, err
		}
		_ = store.LogEvent("report_saved", map[string]any{"path": store.ReportPath()})
	}
	var outputs []string
	if stepSet["assemble"] {
		if AssembleFunc == nil {
			return nil, errors.New("组装阶段尚未实现（阶段 5）")
		}
		callProgress(progress, 0, 0, "回填译文…")
		outCfg := o.Config.Output
		doMono, doBilingual := outCfg.Mono, outCfg.Bilingual
		if !doMono && !doBilingual {
			doMono = true
		}
		if doMono {
			path, err := AssembleFunc(store, inputPath, outPath, outFormat, false, "", false, outCfg.AboutPage, pdfEngine)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, path)
		}
		if doBilingual {
			var biOutPath *string
			if outPath != nil {
				bi := bilingualOutPath(*outPath)
				biOutPath = &bi
			}
			path, err := AssembleFunc(store, inputPath, biOutPath, outFormat, true,
				outCfg.BilingualOrder, outCfg.BilingualPreserveSourceStyle, outCfg.AboutPage, pdfEngine)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, path)
		}
		_ = store.LogEvent("assembled", map[string]any{
			"outputs": toAnySlice(outputs), "out_format": outFormat,
		})
	}
	qaCount := len(qaIssues)
	_ = store.LogEvent("run_steps_finished", map[string]any{
		"steps": toAnySlice(runStepsInput), "outputs": toAnySlice(outputs), "qa_issue_count": qaCount,
	})
	var output any
	if len(outputs) > 0 {
		output = outputs[0]
	}
	result["store"] = store
	result["output"] = output
	result["outputs"] = toAnySlice(outputs)
	result["report"] = report
	result["review_issues"] = reviewIssues
	result["review_changes"] = reviewChanges
	result["review_result"] = reviewResult
	result["review_dir"] = reviewDir
	var qaList []map[string]any
	for _, om := range qaIssues {
		qaList = append(qaList, omapToMap(om))
	}
	result["qa_issues"] = qaList
	return result, nil
}

func (o *Orchestrator) buildConsistencyDigests(store *RunStore) (string, error) {
	m, err := store.LoadManifest()
	if err != nil {
		return "", err
	}
	var inputs []agents.ChapterDigestInput
	chapterItems, _ := m["chapters"].([]any)
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idx, ok := cm["index"].(int64)
		if !ok {
			continue
		}
		status, _ := cm["status"].(string)
		title, _ := cm["title"].(string)
		ch, err := store.LoadChapter(int(idx))
		if err != nil {
			return "", err
		}
		var targets []string
		for _, s := range ch.TextSegments() {
			targets = append(targets, s.TargetText())
		}
		inputs = append(inputs, agents.ChapterDigestInput{
			Index: int(idx), Title: title, Done: status == StatusDone, Targets: targets,
		})
	}
	return agents.BuildChapterDigests(inputs), nil
}

// BuildReport QA 报告（主规格 §13.9，不调模型）。
func BuildReport(store *RunStore, glossaryStore *glossary.Store) (map[string]any, error) {
	m, err := store.LoadManifest()
	if err != nil {
		return nil, err
	}
	chapterItems, _ := m["chapters"].([]any)
	chaptersTotal := 0
	chaptersDone := 0
	type emptyTarget struct {
		Chapter int    `json:"chapter"`
		Index   int    `json:"index"`
		Source  string `json:"source"`
	}
	var emptyTargets []emptyTarget
	var backtranslationIssues []any
	for _, item := range chapterItems {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		chaptersTotal++
		idx, _ := cm["index"].(int64)
		status, _ := cm["status"].(string)
		if status == StatusDone {
			chaptersDone++
		}
		ch, err := store.LoadChapter(int(idx))
		if err != nil {
			return nil, err
		}
		for _, seg := range ch.TextSegments() {
			if strings.TrimSpace(seg.TargetText()) == "" {
				src := []rune(seg.Source)
				if len(src) > 60 {
					src = src[:60]
				}
				emptyTargets = append(emptyTargets, emptyTarget{
					Chapter: int(idx), Index: seg.Index, Source: string(src),
				})
			}
		}
		if raw, ok := ch.Meta["backtranslation_issues"].([]any); ok {
			backtranslationIssues = append(backtranslationIssues, raw...)
		}
	}
	stats, err := glossaryStore.Stats()
	if err != nil {
		return nil, err
	}
	conflicts, err := glossaryStore.OpenConflicts()
	if err != nil {
		return nil, err
	}
	report := map[string]any{
		"summary": map[string]any{
			"chapters_total":        chaptersTotal,
			"chapters_done":         chaptersDone,
			"terms":                 stats["terms"],
			"open_conflicts":        stats["open_conflicts"],
			"backtranslation_issues": len(backtranslationIssues),
			"empty_targets":         len(emptyTargets),
		},
	}
	conflictRecords := make([]map[string]any, 0, len(conflicts))
	for _, c := range conflicts {
		var chapter any
		if c.Chapter != nil {
			chapter = *c.Chapter
		}
		conflictRecords = append(conflictRecords, map[string]any{
			"source":          c.Source,
			"existing_target": c.ExistingTarget,
			"proposed_target": c.ProposedTarget,
			"chapter":         chapter,
		})
	}
	report["open_conflicts"] = conflictRecords
	report["backtranslation_issues"] = backtranslationIssues
	emptyField := make([]map[string]any, 0, len(emptyTargets))
	for _, e := range emptyTargets {
		emptyField = append(emptyField, map[string]any{
			"chapter": e.Chapter, "index": e.Index, "source": e.Source,
		})
	}
	report["empty_targets"] = emptyField
	if latest, err := store.LoadLatestReviewResult(); err == nil && latest != nil {
		issueCount := 0
		changeCount := 0
		if issues, ok := latest["issues"].([]any); ok {
			issueCount = len(issues)
		}
		if changes, ok := latest["changes"].([]any); ok {
			changeCount = len(changes)
		}
		reviewID, _ := latest["review_id"].(string)
		status, _ := latest["status"].(string)
		termination, _ := latest["termination"].(string)
		report["review"] = map[string]any{
			"review_id": reviewID, "status": status, "termination": termination,
			"issue_count": issueCount, "change_count": changeCount, "read_only": true,
		}
	}
	return report, nil
}

// bilingualOutPath stem 追加 "-bi"（主规格 §13.1）。
func bilingualOutPath(outPath string) string {
	ext := filepath.Ext(outPath)
	stem := strings.TrimSuffix(outPath, ext)
	return stem + "-bi" + ext
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

var _ = fmt.Sprintf
var _ = os.Getenv

// RunQA 独立一致性扫描（qa 命令用；绑定重试事件到书籍日志）。
func (o *Orchestrator) RunQA(store *RunStore, glossaryStore *glossary.Store) []map[string]any {
	o.BindLLMEvents(store)
	terms, err := glossaryStore.AllTerms()
	if err != nil {
		return nil
	}
	digests, err := o.buildConsistencyDigests(store)
	if err != nil {
		return nil
	}
	checker := agents.NewConsistencyChecker(o.Client, o.Config)
	omIssues := checker.CheckRaw(digests, terms)
	issuesField := make([]any, 0, len(omIssues))
	out := make([]map[string]any, 0, len(omIssues))
	for _, om := range omIssues {
		m := omapToMap(om)
		out = append(out, m)
		issuesField = append(issuesField, m)
	}
	_ = store.LogEvent("consistency_qa_finished", map[string]any{
		"issue_count": len(omIssues), "issues": issuesField,
	})
	return out
}

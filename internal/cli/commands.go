package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"wenyi/internal/config"
	"wenyi/internal/glossary"
	"wenyi/internal/pipeline"
)

func boolPtr(flag *bool) *bool { return flag }

type translateFlags struct {
	chapter          *int
	format           *string
	out              *string
	pdfEngine        *string
	polish           *bool
	review           *bool
	qa               *bool
	mono             *bool
	bilingual        *bool
}

func registerTranslate(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	f := &translateFlags{
		chapter: new(int), format: new(string), out: new(string), pdfEngine: new(string),
		polish: new(bool), review: new(bool), qa: new(bool), mono: new(bool), bilingual: new(bool),
	}
	cmd := &cobra.Command{
		Use:   "translate INPUT",
		Short: "翻译全书（断点续跑）并按配置收尾",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			chapterSet := cmd.Flags().Changed("chapter")
			formatSet := cmd.Flags().Changed("format")
			pdfEngineSet := cmd.Flags().Changed("pdf-engine")
			if chapterSet {
				var finishOpts []string
				if formatSet {
					finishOpts = append(finishOpts, "--format")
				}
				if cmd.Flags().Changed("out") {
					finishOpts = append(finishOpts, "--out")
				}
				if cmd.Flags().Changed("review") {
					finishOpts = append(finishOpts, "--review/--no-review")
				}
				if cmd.Flags().Changed("qa") {
					finishOpts = append(finishOpts, "--qa/--no-qa")
				}
				if cmd.Flags().Changed("mono") {
					finishOpts = append(finishOpts, "--mono/--no-mono")
				}
				if cmd.Flags().Changed("bilingual") {
					finishOpts = append(finishOpts, "--bilingual/--no-bilingual")
				}
				if len(finishOpts) > 0 {
					return fmt.Errorf("--chapter 只翻译并保存指定章节，不能同时使用收尾选项（%s）", strings.Join(finishOpts, "、"))
				}
			}
			outFormat := *f.format
			if outFormat == "" {
				outFormat = "epub"
			}
			if formatSet && !validateFormat(outFormat, errOut) {
				return errSilent(2)
			}
			pdfEngine := *f.pdfEngine
			if pdfEngine == "" {
				pdfEngine = "weasyprint"
			}
			if pdfEngineSet && !validatePDFEngine(pdfEngine, errOut) {
				return errSilent(2)
			}
			// CLI 覆盖写入 config
			if cmd.Flags().Changed("polish") {
				cfg.Pipeline.Polish = *f.polish
			}
			if cmd.Flags().Changed("review") {
				cfg.Pipeline.Review = *f.review
			}
			if cmd.Flags().Changed("qa") {
				cfg.Pipeline.ConsistencyQA = *f.qa
			}
			if cmd.Flags().Changed("mono") {
				cfg.Output.Mono = *f.mono
			}
			if cmd.Flags().Changed("bilingual") {
				cfg.Output.Bilingual = *f.bilingual
			}
			o, err := newOrchestrator(cfg)
			if err != nil {
				return err
			}
			var chapter *int
			if chapterSet {
				chapter = f.chapter
			}
			var outPath *string
			if cmd.Flags().Changed("out") {
				outPath = f.out
			}
			if chapterSet {
				if _, err := o.Run(input, chapter, WrapProgress(nil)); err != nil {
					return err
				}
				fmt.Fprintf(out, "已翻第 %d 章\n", *chapter)
				fmt.Fprintf(out, "状态目录：%s\n", "")
				return nil
			}
			result, err := o.RunAll(input, WrapProgress(nil), outFormat, outPath, nil, pdfEngine)
			if err != nil {
				return err
			}
			printTranslateSummary(out, result, cfg)
			return nil
		},
	}
	cmd.Flags().IntVar(f.chapter, "chapter", 0, "只翻译并保存指定章节")
	cmd.Flags().StringVar(f.format, "format", "", "输出格式（epub/txt/html/markdown/pdf）")
	cmd.Flags().StringVar(f.out, "out", "", "输出路径")
	cmd.Flags().StringVar(f.pdfEngine, "pdf-engine", "", "PDF 引擎（weasyprint/fpdf2/questpdf）")
	cmd.Flags().BoolVar(f.polish, "polish", cfg.Pipeline.Polish, "润色")
	cmd.Flags().BoolVar(f.review, "review", cfg.Pipeline.Review, "全书审校")
	cmd.Flags().BoolVar(f.qa, "qa", cfg.Pipeline.ConsistencyQA, "一致性 QA")
	cmd.Flags().BoolVar(f.mono, "mono", cfg.Output.Mono, "单语输出")
	cmd.Flags().BoolVar(f.bilingual, "bilingual", cfg.Output.Bilingual, "双语输出")
	root.AddCommand(cmd)
}

func printTranslateSummary(out io.Writer, result map[string]any, cfg *config.Config) {
	report, _ := result["report"].(map[string]any)
	if report != nil {
		if summary, ok := report["summary"].(map[string]any); ok {
			fmt.Fprintf(out, "完成：%v/%v 章，术语 %v，一致性问题 %v 项。\n",
				summary["chapters_done"], summary["chapters_total"], summary["terms"], len(anySlice(result["qa_issues"])))
		}
	}
	if outputs := anySlice(result["outputs"]); len(outputs) > 0 {
		fmt.Fprintf(out, "译文：%s\n", outputs[0])
	} else if output, ok := result["output"].(string); ok && output != "" {
		fmt.Fprintf(out, "译文：%s\n", output)
	}
	if reviewResult, ok := result["review_result"].(map[string]any); ok && reviewResult != nil {
		summary, _ := reviewResult["summary"].(map[string]any)
		fmt.Fprintf(out, "审校：%v（问题 %v 项，修改建议 %v 项）\n目录：%s\n",
			reviewResult["termination"], anyMapCount(summary, "issue_count"), anyMapCount(summary, "change_count"),
			result["review_dir"])
	}
}

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func anyMapCount(m map[string]any, key string) any {
	if m == nil {
		return 0
	}
	return m[key]
}

// silentExit 退出码透传（不打印）。
type silentExit struct{ code int }

func (e *silentExit) Error() string { return "" }

func errSilent(code int) error { return &silentExit{code: code} }

// exitCodeOf 从错误提取退出码。
func exitCodeOf(err error) int {
	if se, ok := err.(*silentExit); ok {
		return se.code
	}
	if ue, ok := err.(*usageError); ok {
		_ = ue
		return 2
	}
	return 1
}

func registerPrepare(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	cmd := &cobra.Command{
		Use:   "prepare INPUT",
		Short: "解析输入并完成初始化（分析/术语种子/预扫）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			o, err := newOrchestrator(cfg)
			if err != nil {
				return err
			}
			store, err := o.PrepareForTranslation(input, WrapProgress(nil))
			if err != nil {
				return err
			}
			m, _ := store.LoadManifest()
			chapters := 0
			if arr, ok := m["chapters"].([]any); ok {
				chapters = len(arr)
			}
			analysis, _ := store.LoadAnalysis()
			synopsis := ""
			if analysis != nil {
				synopsis, _ = analysis["book_synopsis"].(string)
			}
			fmt.Fprintf(out, "已解析 %d 章；预扫梗概完成；全书概览%v。\n", chapters, boolCn(synopsis != ""))
			fmt.Fprintf(out, "状态目录：%s\n", store.RunDir)
			return nil
		},
	}
	root.AddCommand(cmd)
}

func boolCn(b bool) string {
	if b {
		return "已生成"
	}
	return "未生成"
}

func registerReview(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	cmd := &cobra.Command{
		Use:   "review INPUT",
		Short: "全书只读审校（证据循环 + 影子修订）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			o, err := newOrchestrator(cfg)
			if err != nil {
				return err
			}
			if _, err := o.RunReview(input, WrapProgress(nil)); err != nil {
				return err
			}
			return nil
		},
	}
	root.AddCommand(cmd)
}

func registerQA(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	cmd := &cobra.Command{
		Use:   "qa INPUT",
		Short: "全书跨章一致性扫描",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			o, err := newOrchestrator(cfg)
			if err != nil {
				return err
			}
			store, err := o.LocateExistingStore(input, nil)
			if err != nil {
				return err
			}
			if err := applyStoreLanguages(o, store); err != nil {
				return err
			}
			g, err := glossary.OpenStore(store.GlossaryPath())
			if err != nil {
				return err
			}
			defer g.Close()
			issues := o.RunQA(store, g)
			for _, issue := range issues {
				fmt.Fprintf(out, "[%v] %v：%v（%v）\n", issue["type"], issue["detail"], issue["suggestion"], issue["where"])
			}
			return nil
		},
	}
	root.AddCommand(cmd)
}

func registerStatus(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	cmd := &cobra.Command{
		Use:   "status INPUT",
		Short: "查看翻译进度",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			store, err := RunstoreFor(cfg, input)
			if err != nil {
				return err
			}
			if !store.Exists() {
				fmt.Fprintln(errOut, "尚无翻译进度。请先运行 translate。")
				return errSilent(1)
			}
			m, err := store.LoadManifest()
			if err != nil {
				return err
			}
			for _, item := range anySlice(m["chapters"]) {
				cm, _ := item.(map[string]any)
				mark := "·"
				if cm["status"] == pipeline.StatusDone {
					mark = "✓"
				}
				fmt.Fprintf(out, "%s #%v %v\n", mark, cm["index"], cm["title"])
			}
			g, err := glossary.OpenStore(store.GlossaryPath())
			if err == nil {
				stats, _ := g.Stats()
				fmt.Fprintf(out, "术语 %v，未决冲突 %v\n", stats["terms"], stats["open_conflicts"])
				g.Close()
			}
			return nil
		},
	}
	root.AddCommand(cmd)
}

func registerReport(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	cmd := &cobra.Command{
		Use:   "report INPUT",
		Short: "生成 QA 报告",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			store, err := RunstoreFor(cfg, input)
			if err != nil {
				return err
			}
			if !store.Exists() {
				fmt.Fprintln(errOut, "尚无翻译进度。请先运行 translate。")
				return errSilent(1)
			}
			g, err := glossary.OpenStore(store.GlossaryPath())
			if err != nil {
				return err
			}
			defer g.Close()
			report, err := pipeline.BuildReport(store, g)
			if err != nil {
				return err
			}
			if err := store.SaveReport(report); err != nil {
				return err
			}
			if summary, ok := report["summary"].(map[string]any); ok {
				fmt.Fprintf(out, "章节：%v/%v；术语 %v（冲突 %v）；空译文 %v；回译问题 %v\n",
					summary["chapters_done"], summary["chapters_total"], summary["terms"],
					summary["open_conflicts"], summary["empty_targets"], summary["backtranslation_issues"])
			}
			return nil
		},
	}
	root.AddCommand(cmd)
}

func registerAssemble(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	var format, pdfEngine string
	var outPath string
	var mono, bilingual bool
	cmd := &cobra.Command{
		Use:   "assemble INPUT",
		Short: "从状态目录组装输出（不调模型）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if !requireInputFile(input, errOut) {
				return errSilent(1)
			}
			if cmd.Flags().Changed("format") && !validateFormat(format, errOut) {
				return errSilent(2)
			}
			if format == "" {
				format = "epub"
			}
			engine := pdfEngine
			if engine == "" {
				engine = "weasyprint"
			}
			if cmd.Flags().Changed("pdf-engine") && !validatePDFEngine(engine, errOut) {
				return errSilent(2)
			}
			if cmd.Flags().Changed("mono") {
				cfg.Output.Mono = mono
			}
			if cmd.Flags().Changed("bilingual") {
				cfg.Output.Bilingual = bilingual
			}
			o, err := newOrchestrator(cfg)
			if err != nil {
				return err
			}
			var outPtr *string
			if cmd.Flags().Changed("out") {
				outPtr = &outPath
			}
			result, err := o.RunSteps(input, []string{"assemble"}, WrapProgress(nil), format, outPtr, engine)
			if err != nil {
				return err
			}
			for _, p := range anySlice(result["outputs"]) {
				if s, ok := p.(string); ok {
					fmt.Fprintf(out, "译文：%s\n", s)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "输出路径")
	cmd.Flags().StringVar(&format, "format", "", "输出格式")
	cmd.Flags().StringVar(&pdfEngine, "pdf-engine", "", "PDF 引擎")
	cmd.Flags().BoolVar(&mono, "mono", cfg.Output.Mono, "单语输出")
	cmd.Flags().BoolVar(&bilingual, "bilingual", cfg.Output.Bilingual, "双语输出")
	root.AddCommand(cmd)
}

func registerGlossary(root *cobra.Command, cfg *config.Config, out, errOut io.Writer) {
	glossaryCmd := &cobra.Command{
		Use:   "glossary",
		Short: "术语库管理",
	}
	list := &cobra.Command{
		Use:   "list INPUT",
		Short: "列出术语",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, g, err := openGlossaryFor(cfg, args[0], errOut)
			if err != nil {
				return err
			}
			defer g.Close()
			terms, err := g.AllTerms()
			if err != nil {
				return err
			}
			for _, term := range terms {
				gender := term.Gender
				if gender != "" {
					gender = "，" + gender
				}
				fmt.Fprintf(out, "%s → %s（%s%s）\n", term.Source, term.Target, term.Type, gender)
			}
			return nil
		},
	}
	conflicts := &cobra.Command{
		Use:   "conflicts INPUT",
		Short: "列出未决译法冲突",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, g, err := openGlossaryFor(cfg, args[0], errOut)
			if err != nil {
				return err
			}
			defer g.Close()
			items, err := g.OpenConflicts()
			if err != nil {
				return err
			}
			for _, c := range items {
				fmt.Fprintf(out, "%s：现有 %s vs 提议 %s（第 %v 章）\n", c.Source, c.ExistingTarget, c.ProposedTarget, chapterOrDash(c.Chapter))
			}
			return nil
		},
	}
	resolve := &cobra.Command{
		Use:   "resolve INPUT SOURCE TARGET",
		Short: "裁决术语冲突",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, g, err := openGlossaryFor(cfg, args[0], errOut)
			if err != nil {
				return err
			}
			defer g.Close()
			hit, err := glossary.Resolve(g, args[1], args[2])
			if err != nil {
				return err
			}
			if !hit {
				fmt.Fprintf(errOut, "术语不存在：%s\n", args[1])
				return errSilent(1)
			}
			fmt.Fprintf(out, "已裁决：%s → %s\n", args[1], args[2])
			return nil
		},
	}
	glossaryCmd.AddCommand(list, conflicts, resolve)
	root.AddCommand(glossaryCmd)
}

func chapterOrDash(c *int) any {
	if c == nil {
		return "-"
	}
	return *c
}

func openGlossaryFor(cfg *config.Config, input string, errOut io.Writer) (*pipeline.RunStore, *glossary.Store, error) {
	if !requireInputFile(input, errOut) {
		return nil, nil, errSilent(1)
	}
	store, err := RunstoreFor(cfg, input)
	if err != nil {
		return nil, nil, err
	}
	if !store.Exists() {
		fmt.Fprintln(errOut, "尚无翻译进度。请先运行 translate。")
		return nil, nil, errSilent(1)
	}
	g, err := glossary.OpenStore(store.GlossaryPath())
	if err != nil {
		return nil, nil, err
	}
	return store, g, nil
}

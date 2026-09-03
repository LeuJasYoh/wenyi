// Package cli 实现引擎命令行（主规格 §4）。退出码：0 成功 / 1 业务错误 / 2 参数错误。
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"wenyi/internal/config"
	"wenyi/internal/llmfactory"
	"wenyi/internal/pipeline"
)

// Version 对应 package_version("trans-novel")。
const Version = "0.4.1"

// progressLine 由 --progress-line 设置（cli 包内共享）。
var progressLine bool

// stateDirOverride 由 --state-dir 设置；非空时覆盖 config。
var stateDirOverride string

// ProgressEnabled 报告是否开启机器可读进度通道。
func ProgressEnabled() bool { return progressLine }

// APIPreflightExempt 免凭据校验的命令（纯本地）。
var apiPreflightExempt = map[string]bool{
	"assemble": true, "glossary": true, "report": true, "status": true,
}

// Run 执行 CLI（args 不含程序名）；返回退出码。out/err 用于输出（测试注入）。
func Run(args []string, out, errOut io.Writer) int {
	cfgPath := "config.yaml"
	// 分派前解析 --config/-c（含 = 与粘连形式）
	if p := configPathFromArgs(args); p != "" {
		cfgPath = p
	}
	created, err := config.CreateDefaultFile(cfgPath)
	if err != nil {
		fmt.Fprintf(errOut, "错误：%v\n", err)
		return 1
	}
	_ = created

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(errOut, "错误：%v\n", err)
		return 1
	}

	root := &cobra.Command{
		Use:           "wenyi",
		Short:         "多智能体长篇小说翻译系统（无头引擎）",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetVersionTemplate("{{.Version}}\n")
	root.PersistentFlags().StringVarP(&cfgPath, "config", "c", cfgPath, "配置文件路径")
	// --progress-line：机器可读进度通道（分册 10 §1.8；默认关闭）
	root.PersistentFlags().BoolVar(&progressLine, "progress-line", false, "stdout 追加 JSON Lines 进度行（WebUI 后端使用）")
	// --state-dir：覆盖 config 的 paths.state_dir（WebUI 后端使用）
	root.PersistentFlags().StringVar(&stateDirOverride, "state-dir", "", "覆盖 paths.state_dir")
	// 全局 flags 解析完成后、命令执行前应用覆盖（cobra PersistentPreRun 语义）
	root.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		if stateDirOverride != "" {
			cfg.StateDir = stateDirOverride
		}
	}

	registerTranslate(root, cfg, out, errOut)
	registerPrepare(root, cfg, out, errOut)
	registerReview(root, cfg, out, errOut)
	registerQA(root, cfg, out, errOut)
	registerStatus(root, cfg, out, errOut)
	registerReport(root, cfg, out, errOut)
	registerAssemble(root, cfg, out, errOut)
	registerGlossary(root, cfg, out, errOut)

	root.SetArgs(args)
	if hasHelpFlag(args) {
		_ = root.Execute()
		return 0
	}
	// API 预检：除豁免命令与 help 外先校验凭据
	if hasVersionOrHelp(args) {
		_ = root.Execute()
		return 0
	}
	if len(args) > 0 && !apiPreflightExempt[args[0]] {
		client, err := llmfactory.BuildClient(cfg)
		if err == nil {
			err = client.ValidateCredentials()
		}
		if err != nil {
			fmt.Fprintf(errOut, "错误：%v\n", err)
			return 1
		}
	}
	if err := root.Execute(); err != nil {
		if _, silent := err.(*silentExit); !silent {
			fmt.Fprintln(errOut, err)
		}
		return exitCodeOf(err)
	}
	return 0
}

func isUsageError(err error) bool {
	// Cobra 对未知 flag/命令返回的使用错误 → 退出码 2
	var flagErr *interface{ Error() string }
	_ = flagErr
	msg := err.Error()
	return strings.Contains(msg, "unknown flag") || strings.Contains(msg, "unknown command") ||
		strings.Contains(msg, "unknown shorthand") || strings.Contains(msg, "invalid argument")
}

func hasVersionOrHelp(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "--version" || a == "-v" {
			return true
		}
	}
	return false
}

func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

func configPathFromArgs(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-c":
			if i+1 < len(args) {
				return args[i+1]
			}
		case len(a) > 9 && a[:9] == "--config=":
			return a[9:]
		case len(a) > 2 && a[:2] == "-c" && !strings.HasPrefix(a, "-c="):
			return a[2:]
		}
	}
	return ""
}

func requireInputFile(input string, errOut io.Writer) bool {
	if fi, err := os.Stat(input); err != nil || fi.IsDir() {
		fmt.Fprintf(errOut, "错误：输入文件不存在：%s\n", input)
		return false
	}
	return true
}

var validFormats = map[string]bool{"epub": true, "txt": true, "html": true, "markdown": true, "pdf": true}
var validPDFEngines = map[string]bool{"weasyprint": true, "fpdf2": true, "questpdf": true}

func validateFormat(format string, errOut io.Writer) bool {
	if !validFormats[format] {
		fmt.Fprintf(errOut, "错误：不支持的输出格式：%s（可选 epub/txt/html/markdown/pdf）\n", format)
		return false
	}
	return true
}

func validatePDFEngine(engine string, errOut io.Writer) bool {
	if !validPDFEngines[engine] {
		fmt.Fprintf(errOut, "错误：不支持的 PDF 引擎：%s（可选 weasyprint/fpdf2/questpdf）\n", engine)
		return false
	}
	return true
}

// RunstoreFor 定位状态目录（PDF 用文件名，其它解析取书名；create=false）。
func RunstoreFor(cfg *config.Config, input string) (*pipeline.RunStore, error) {
	ext := strings.ToLower(filepath.Ext(input))
	var title string
	if ext == ".pdf" {
		base := filepath.Base(input)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	} else {
		tmp, err := os.MkdirTemp("", "wenyi-locate-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		// 语言对定位无影响
		doc, err := loadDocumentForTitle(input, tmp)
		if err != nil {
			return nil, err
		}
		title = doc.Title
	}
	return pipeline.NewRunStore(filepath.Join(cfg.StateDir, pipeline.Slugify(title)), false)
}

func loadDocumentForTitle(input, tmp string) (titleDoc, error) {
	return loadTitleDoc(input, tmp)
}

type titleDoc struct {
	Title string
}

// printUsageError 打印参数错误（退出码 2）。
func usageErrorf(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// newOrchestrator 构建 orchestrator（供命令使用）。
func newOrchestrator(cfg *config.Config) (*pipeline.Orchestrator, error) {
	return pipeline.NewOrchestrator(cfg, nil)
}

// applyStoreLanguages 独立阶段命令从 manifest 恢复语言（主规格 §3.3）。
func applyStoreLanguages(o *pipeline.Orchestrator, store *pipeline.RunStore) error {
	m, err := store.LoadManifest()
	if err != nil {
		return err
	}
	lang, _ := m["source_lang"].(string)
	if lang == "" {
		lang = o.Config.SourceLang
	}
	return o.ApplyLanguage(lang)
}

func printUsage(out io.Writer, m map[string]int) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
}




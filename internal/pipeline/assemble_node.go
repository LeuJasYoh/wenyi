package pipeline

// assemble_node.go 阶段 5 接线：spawn Node 组装组件（架构分册 §3.2）。

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"wenyi/internal/ingest"
)

func init() {
	AssembleFunc = assembleViaNode
}

const pdfDllName = "Wenyi.Pdf.dll"

// assembleViaNode spawn node cli.js doc assemble；解析 OUTPUT: 行返回路径。
func assembleViaNode(store *RunStore, inputPath string, outPath *string, outFormat string,
	bilingual bool, order string, preserveSourceStyle bool, aboutPage bool, pdfEngine string) (string, error) {
	if outFormat == "pdf" {
		return renderPDFViaDotnet(store, outPath, bilingual, order)
	}
	args := []string{
		ingest.NodeCLIPath, "doc", "assemble",
		"--input", inputPath,
		"--state-dir", store.RunDir,
		"--format", outFormat,
	}
	if bilingual {
		args = append(args, "--bilingual", "--order", order)
	}
	if preserveSourceStyle {
		args = append(args, "--preserve-source-style")
	}
	if !aboutPage {
		args = append(args, "--no-about-page")
	}
	if outPath != nil {
		args = append(args, "--out", *outPath)
	}
	out, err := runCapture(stdOutLines, ingest.NodeCommand, args...)
	if err != nil {
		return "", err
	}
	for _, line := range out {
		if strings.HasPrefix(line, "OUTPUT: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "OUTPUT: ")), nil
		}
	}
	return "", fmt.Errorf("组装组件未返回输出路径")
}

// renderPDFViaDotnet spawn dotnet Wenyi.Pdf.dll render。
func renderPDFViaDotnet(store *RunStore, outPath *string, bilingual bool, order string) (string, error) {
	dll := discoverPDFDll()
	if dll == "" {
		return "", fmt.Errorf("PDF 输出需要 wenyi-pdf 组件（未找到 %s）", pdfDllName)
	}
	target := ""
	if outPath != nil {
		target = *outPath
	} else {
		target = filepath.Join(store.RunDir, "output", "book.pdf")
	}
	args := []string{dll, "render",
		"--state-dir", store.RunDir,
		"--out", target,
	}
	if bilingual {
		args = append(args, "--bilingual", "--order", order)
	}
	out, err := runCapture(stdOutLines, "dotnet", args...)
	if err != nil {
		return "", err
	}
	for _, line := range out {
		if strings.HasPrefix(line, "OUTPUT: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "OUTPUT: ")), nil
		}
	}
	return target, nil
}

func discoverPDFDll() string {
	if p := os.Getenv("WENYI_PDF_DLL"); p != "" {
		if fileExistsIng(p) {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, "..", "wenyi-pdf", pdfDllName),
			filepath.Join(dir, "wenyi-pdf", pdfDllName),
			filepath.Join(dir, pdfDllName),
		} {
			if fileExistsIng(cand) {
				return filepath.Clean(cand)
			}
		}
	}
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		cand := filepath.Join(dir, "pdf", "Wenyi.Pdf", "bin", "Release", "net10.0", "publish", pdfDllName)
		if fileExistsIng(cand) {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func fileExistsIng(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func stdOutLines(cmd *exec.Cmd) ([]string, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var lines []string
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := cmd.Wait(); err != nil {
		return lines, err
	}
	return lines, nil
}

func runCapture(capture func(*exec.Cmd) ([]string, error), name string, args ...string) ([]string, error) {
	cmd := exec.Command(name, args...)
	return capture(cmd)
}

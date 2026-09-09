// Package webserver 内嵌 WebUI 后端。本文件：Serve 入口（监听 + 自动开浏览器）。
package webserver

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// Serve 启动 HTTP 服务（阻塞）。返回错误仅在监听失败时。
func (s *Server) Serve(port int) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	out := s.opt.Stdout
	if out == nil {
		out = os.Stdout
	}
	fmt.Fprintf(out, "Wenyi WebUI 已启动：%s（配置：%s，状态目录：%s）\n", url, s.ConfigPath(), s.stateDir)
	if !s.opt.NoOpen && os.Getenv("WENYI_NO_OPEN") != "1" {
		openBrowser(url)
	}
	return http.Serve(ln, s.handler)
}

// openBrowser 跨平台尽力打开默认浏览器（失败静默，不阻塞启动）。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

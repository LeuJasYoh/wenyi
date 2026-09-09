// Package webserver 内嵌 WebUI 后端。本文件：静态资源（go:embed 内嵌 + 磁盘覆盖）。
package webserver

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed all:static
var embeddedUI embed.FS

// staticHandler 优先级：WENYI_UI_DIR / Options.UIDirOverride（开发态磁盘）→ 内嵌产物。
// UI 为 hash 路由单页应用，仅按路径原样匹配文件（index.html 显式回退根路径）。
func (s *Server) staticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + strings.ReplaceAll(r.URL.Path, "\\", "/"))
		name = strings.TrimPrefix(name, "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}

		if dir := s.uiDirOverride(); dir != "" {
			if serveDisk(w, r, dir, name) {
				return
			}
		}
		sub, err := fs.Sub(embeddedUI, "static")
		if err == nil {
			if f, err := sub.Open(name); err == nil {
				st, serr := f.Stat()
				if serr == nil && !st.IsDir() {
					body, _ := io.ReadAll(f)
					f.Close()
					serveBytes(w, r, name, body)
					return
				}
				f.Close()
			}
		}
		// 前端未构建（fresh clone）：根路径回落占位页
		if name == "index.html" {
			if f, err := embeddedUI.Open("static/placeholder.html"); err == nil {
				body, _ := io.ReadAll(f)
				f.Close()
				serveBytes(w, r, "index.html", body)
				return
			}
		}
		http.NotFound(w, r)
	})
}

func (s *Server) uiDirOverride() string {
	dir := s.opt.UIDirOverride
	if dir == "" {
		dir = os.Getenv("WENYI_UI_DIR")
	}
	if dir == "" {
		return ""
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}

func serveDisk(w http.ResponseWriter, r *http.Request, dir, name string) bool {
	full := filepath.Join(dir, filepath.FromSlash(name))
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		return false
	}
	serveBytes(w, r, name, func() []byte {
		b, _ := os.ReadFile(full)
		return b
	}())
	return true
}

func serveBytes(w http.ResponseWriter, r *http.Request, name string, body []byte) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html":
		w.Header().Set("content-type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("content-type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("content-type", "text/css; charset=utf-8")
	case ".svg":
		w.Header().Set("content-type", "image/svg+xml")
	case ".png":
		w.Header().Set("content-type", "image/png")
	case ".json":
		w.Header().Set("content-type", "application/json; charset=utf-8")
	case ".woff2":
		w.Header().Set("content-type", "font/woff2")
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(200)
		return
	}
	_, _ = w.Write(body)
}

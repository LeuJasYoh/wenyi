// Package webserver 内嵌 WebUI 后端。本文件：slugify（主规格 §11.1，与 pipeline/runstore.go 同规则）。
package webserver

import (
	"regexp"
	"strings"
)

var slugNonWord = regexp.MustCompile(`[^\w一-鿿぀-ヿ-]+`)

// Slugify 书名 → 目录名；空结果回退 "book"。
func Slugify(name string) string {
	s := slugNonWord.ReplaceAllString(name, "_")
	s = strings.Trim(s, "_")
	if s == "" {
		return "book"
	}
	return s
}

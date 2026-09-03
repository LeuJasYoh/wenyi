// wenyi 命令行入口（Go 引擎，主规格 §4）。
package main

import (
	"os"

	"wenyi/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

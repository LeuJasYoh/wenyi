// web.go 「wenyi web」子命令：单 exe 启动内嵌 WebUI（方案二：Go 后端 + go:embed 前端）。
package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"wenyi/internal/webserver"
)

func registerWeb(root *cobra.Command, out, errOut io.Writer) {
	var port int
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "web",
		Short: "启动 WebUI（内嵌前端，浏览器自动打开）",
		RunE: func(c *cobra.Command, _ []string) error {
			// 仅当用户显式 -c 时透传；否则由 webserver 按_exe 目录 config.json → config.yaml → CWD 链解析
			configFlag := ""
			if c.Flags().Changed("config") {
				configFlag, _ = c.Flags().GetString("config")
			}
			server, err := webserver.NewServer(webserver.Options{
				Port:           port,
				NoOpen:         noOpen,
				Version:        Version,
				ConfigPathFlag: configFlag,
				StateDirFlag:   stateDirOverride,
				Stdout:         out,
			})
			if err != nil {
				fmt.Fprintf(errOut, "错误：%v\n", err)
				return errSilent(1)
			}
			if err := server.Serve(port); err != nil {
				fmt.Fprintf(errOut, "错误：WebUI 启动失败：%v\n", err)
				return errSilent(1)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 8731, "HTTP 端口")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "不自动打开浏览器")
	root.AddCommand(cmd)
}

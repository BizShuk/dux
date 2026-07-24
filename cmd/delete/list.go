package delete

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/bizshuk/dux/du"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list [paths...]",
	Short: "show list with <full cmd>",
	Long:  `Display target deletion directories, descriptions, sizes, and their full shell delete commands without executing deletion.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithCancel(context.Background())
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sig
			cancel()
		}()
		defer cancel()

		targets := DefaultTargets
		if len(args) > 0 {
			targets = make([]TargetItem, len(args))
			for i, arg := range args {
				targets[i] = TargetItem{
					Path:        arg,
					Description: fmt.Sprintf("自訂目錄 (Custom path: %s)", arg),
				}
			}
		}

		return ExecuteList(ctx, os.Stdout, targets)
	},
}

func ExecuteList(ctx context.Context, out io.Writer, targets []TargetItem) error {
	fmt.Fprintln(out, "==================================================")
	fmt.Fprintln(out, " [列表模式 / List Mode] 檢視清理目標目錄與完整刪除指令")
	fmt.Fprintln(out, "==================================================")

	for i, item := range targets {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resolved := ExpandPath(item.Path)
		fullCmd := FormatFullCmd(resolved)
		info, err := os.Lstat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(out, "%d. %s\n   路徑: %s\n   指令: %s\n   狀態: 目錄不存在 (Not Found)\n\n", i+1, item.Description, resolved, fullCmd)
				continue
			}
			fmt.Fprintf(out, "%d. %s\n   路徑: %s\n   指令: %s\n   錯誤: 無法存取 (%v)\n\n", i+1, item.Description, resolved, fullCmd, err)
			continue
		}

		res, err := du.Usage(ctx, du.Options{
			Paths:        []string{resolved},
			MaxDepth:     0,
			ApparentSize: false,
		})

		var size uint64
		if err == nil && res != nil {
			size = res.GrandTotal.Size
		} else if info != nil {
			size = uint64(info.Size())
		}

		formattedSize := HumanSize(size)
		fmt.Fprintf(out, "%d. %s\n   路徑: %s\n   指令: %s\n   容量: %s (%d bytes)\n   狀態: 待清理 (Pending)\n\n", i+1, item.Description, resolved, fullCmd, formattedSize, size)
	}

	fmt.Fprintln(out, "--------------------------------------------------")
	fmt.Fprintln(out, "列表檢視完成。如需執行清理，可使用 'dux delete' 互動模式、'dux delete -y' 或 'dux delete tui'。")
	fmt.Fprintln(out, "--------------------------------------------------")

	return nil
}

package delete

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bizshuk/dux/du"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var forceYes bool

type TargetItem struct {
	Path        string
	Description string
}

var DefaultTargets = []TargetItem{
	{Path: "$HOME/Library/Developer/Xcode/DerivedData", Description: "Xcode DerivedData (建置快取 / Build cache)"},
	{Path: "$HOME/Library/Developer/Xcode/DeviceLogs", Description: "Xcode DeviceLogs (裝置日誌 / Device logs)"},
	{Path: "$HOME/Library/Developer/Xcode/iOS DeviceSupport", Description: "Xcode iOS DeviceSupport (裝置支援檔 / iOS device support)"},
	{Path: "$HOME/Library/Developer/Xcode/UserData", Description: "Xcode UserData (使用者自訂設定與範本 / Xcode user data)"},
	{Path: "$HOME/Library/Application Support/Antigravity IDE/logs", Description: "Antigravity IDE Logs (IDE 日誌 / IDE logs)"},
}

// ExpandPath replaces $HOME or ~ with the user's home directory.
func ExpandPath(p string) string {
	if strings.HasPrefix(p, "$HOME") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "$HOME"))
		}
	} else if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// FormatFullCmd returns the complete rm command line string.
func FormatFullCmd(resolvedPath string) string {
	return fmt.Sprintf("rm -rf %q", resolvedPath)
}

// HumanSize formats bytes into human-readable units (e.g. 5MB, 10GB, 1KB, 0B).
func HumanSize(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	val := float64(b) / float64(div)
	if val == float64(uint64(val)) {
		return fmt.Sprintf("%.0f%s", val, units[exp])
	}
	return fmt.Sprintf("%.1f%s", val, units[exp])
}

var Cmd = &cobra.Command{
	Use:   "delete [paths...]",
	Short: "Interactively confirm and delete Xcode caches or custom directories 1 by 1",
	Long: `Calculate disk space for target directories (default Xcode caches and UserData or specified paths)
and prompt for deletion 1 by 1 on a single line with default [N].

Use 'delete list' to view target directories, descriptions, sizes, and full shell commands without deleting.
Use 'delete tui' to launch interactive selection UI.
Use -y / --yes flag to delete directly without confirmation.`,
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

		return ExecuteDelete(ctx, os.Stdin, os.Stdout, targets, forceYes)
	},
}

func ExecuteDelete(ctx context.Context, in io.Reader, out io.Writer, targets []TargetItem, forceYes bool) error {
	reader := bufio.NewReader(in)

	var totalDeletedBytes uint64
	deletedCount := 0
	skippedCount := 0
	notFoundCount := 0

	dimStyle := lipgloss.NewStyle().Faint(true)

	fmt.Fprintln(out, "==================================================")
	if forceYes {
		fmt.Fprintln(out, " [直接刪除模式 / Direct Delete Mode] 開始清理目標目錄")
	} else {
		fmt.Fprintln(out, " [互動模式 / Interactive Mode] 請逐一確認是否進行刪除 (預設為 N)")
	}
	fmt.Fprintln(out, "==================================================")

	for i, item := range targets {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resolved := ExpandPath(item.Path)
		info, err := os.Lstat(resolved)
		exists := (err == nil)

		var size uint64
		if exists {
			res, uerr := du.Usage(ctx, du.Options{
				Paths:        []string{resolved},
				MaxDepth:     0,
				ApparentSize: false,
			})
			if uerr == nil && res != nil {
				size = res.GrandTotal.Size
			} else if info != nil {
				size = uint64(info.Size())
			}
		}

		icon := "📁 "
		sizeStr := HumanSize(size)
		if !exists {
			icon = "⚠️ "
			sizeStr = "0B"
		}

		dimDesc := dimStyle.Render(item.Description)

		if forceYes {
			fmt.Fprintf(out, "[%d/%d] %s%s, %s, %s\n", i+1, len(targets), icon, resolved, sizeStr, dimDesc)
			if exists {
				err := os.RemoveAll(resolved)
				if err != nil {
					fmt.Fprintf(out, "  -> 刪除失敗: %v\n", err)
					skippedCount++
				} else {
					fmt.Fprintf(out, "  -> 已刪除 (Deleted) - 釋放 %s\n", HumanSize(size))
					totalDeletedBytes += size
					deletedCount++
				}
			} else {
				fmt.Fprintln(out, "  -> 目錄不存在 (Not Found)")
				notFoundCount++
			}
		} else {
			// Single line prompt format: [1/4] ⚠️ /path, 0B, description [y/N]:
			fmt.Fprintf(out, "[%d/%d] %s%s, %s, %s [y/N]: ", i+1, len(targets), icon, resolved, sizeStr, dimDesc)

			input, readErr := reader.ReadString('\n')
			if readErr != nil && readErr != io.EOF {
				fmt.Fprintf(out, "  -> 讀取錯誤: %v, 已跳過\n", readErr)
				skippedCount++
				continue
			}

			trimmed := strings.ToLower(strings.TrimSpace(input))
			if trimmed == "y" || trimmed == "yes" {
				if !exists {
					fmt.Fprintln(out, "  -> 目錄不存在 (Not Found)")
					notFoundCount++
				} else {
					err := os.RemoveAll(resolved)
					if err != nil {
						fmt.Fprintf(out, "  -> 刪除失敗: %v\n", err)
						skippedCount++
					} else {
						fmt.Fprintf(out, "  -> 已刪除 (Deleted) - 釋放 %s\n", HumanSize(size))
						totalDeletedBytes += size
						deletedCount++
					}
				}
			} else {
				fmt.Fprintln(out, "  -> 已跳過 (Skipped)")
				skippedCount++
			}
		}
	}

	fmt.Fprintln(out, "--------------------------------------------------")
	fmt.Fprintf(out, "清理結果統計:\n")
	fmt.Fprintf(out, " - 成功刪除: %d 個目錄\n", deletedCount)
	fmt.Fprintf(out, " - 跳過未刪: %d 個目錄\n", skippedCount)
	fmt.Fprintf(out, " - 目錄不存在: %d 個目錄\n", notFoundCount)
	fmt.Fprintf(out, " - 總共釋放空間: %s (%d bytes)\n", HumanSize(totalDeletedBytes), totalDeletedBytes)
	fmt.Fprintln(out, "--------------------------------------------------")

	return nil
}

func init() {
	Cmd.Flags().BoolVarP(&forceYes, "yes", "y", false, "delete target directories without interactive confirmation")
	Cmd.AddCommand(listCmd)
	Cmd.AddCommand(tuiCmd)
}

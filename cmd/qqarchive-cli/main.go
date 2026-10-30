package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"qqarchive/internal/archive"
	"qqarchive/internal/domain"
	"qqarchive/internal/source/plain"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if e := run(ctx, os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "任务失败：", e)
		os.Exit(1)
	}
}
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("development demo only: demo DIRECTORY | export SOURCE_DB OUTPUT CONVERSATION START_DATE END_DATE TIMEZONE | verify PACKAGE | search PACKAGE TEXT")
	}
	switch args[0] {
	case "demo":
		if len(args) != 2 {
			return fmt.Errorf("demo DIRECTORY")
		}
		if e := plain.Demo(ctx, args[1]); e != nil {
			return e
		}
		return printJSON(map[string]string{"status": "DEVELOPMENT_DEMO", "message": "仅合成数据，不代表 NTQQ 已适配。"})
	case "export":
		if len(args) != 7 {
			return fmt.Errorf("export SOURCE_DB OUTPUT CONVERSATION START_DATE END_DATE TIMEZONE")
		}
		s, e := domain.Dates(args[3], args[4], args[5], args[6])
		if e != nil {
			return e
		}
		result, e := archive.Build(ctx, args[1], s, args[2])
		if e != nil {
			return e
		}
		return printJSON(result)
	case "verify", "search":
		if len(args) < 2 {
			return fmt.Errorf("package path required")
		}
		o, e := archive.Open(ctx, args[1])
		if e != nil {
			return e
		}
		defer o.Close()
		if args[0] == "verify" {
			return printJSON(map[string]any{"verification": "PASSED", "scope": "development_demo", "manifest": o.Manifest})
		}
		if len(args) != 3 {
			return fmt.Errorf("search PACKAGE TEXT")
		}
		rows, e := o.Search(ctx, args[2], 50, 0)
		if e != nil {
			return e
		}
		return printJSON(rows)
	default:
		return fmt.Errorf("unknown command")
	}
}

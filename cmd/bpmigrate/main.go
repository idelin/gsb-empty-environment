// bpmigrate 是断点迁移命令行工具。
//
// 用法:
//   bpmigrate -old old.go -new new.go -bp 12,27,40 [-json]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"bpmigrate"
)

func main() {
	oldPath := flag.String("old", "", "旧版本文件路径")
	newPath := flag.String("new", "", "新版本文件路径")
	bpFlag := flag.String("bp", "", "断点行号，逗号分隔（相对旧文件，1-based）")
	asJSON := flag.Bool("json", false, "以 JSON 输出")
	flag.Parse()

	if *oldPath == "" || *newPath == "" || *bpFlag == "" {
		flag.Usage()
		os.Exit(2)
	}

	oldSrc, err := ioutil.ReadFile(*oldPath)
	if err != nil {
		fatal(err)
	}
	newSrc, err := ioutil.ReadFile(*newPath)
	if err != nil {
		fatal(err)
	}

	var bps []bpmigrate.Breakpoint
	for i, s := range strings.Split(*bpFlag, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n < 1 {
			fatal(fmt.Errorf("无效断点行号: %q", s))
		}
		bps = append(bps, bpmigrate.Breakpoint{ID: i + 1, Line: n})
	}

	results := bpmigrate.Migrate(string(oldSrc), string(newSrc), bps)

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(results)
		return
	}
	for _, r := range results {
		fmt.Printf("断点 #%d (旧行 %d) -> %s", r.Breakpoint.ID, r.Breakpoint.Line, r.Status)
		if r.Status == bpmigrate.StatusMoved || r.Status == bpmigrate.StatusUnchanged || r.Status == bpmigrate.StatusSplit {
			fmt.Printf(" 新行 %d", r.Line)
		}
		fmt.Printf("\n  理由: %s\n", r.Reason)
		for _, c := range r.Candidates {
			fmt.Printf("  候选: 行 %d 置信度 %.2f %q\n", c.Line, c.Confidence, c.Text)
			for _, reason := range c.Reasons {
				fmt.Printf("    - %s\n", reason)
			}
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}

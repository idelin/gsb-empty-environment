// bpmigrate 是断点迁移工具：依据语句结构与邻域，把旧文件中的断点
// 映射到编辑后的新文件，输出唯一位置、候选列表或删除结论。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"bpmigrate/internal/migrate"
)

func main() {
	oldPath := flag.String("old", "", "修改前的源文件路径")
	newPath := flag.String("new", "", "修改后的源文件路径")
	line := flag.Int("line", 0, "断点在旧文件中的行号（1 起）")
	flag.Parse()

	if *oldPath == "" || *newPath == "" || *line <= 0 {
		fmt.Fprintln(os.Stderr, "用法: bpmigrate --old <旧文件> --new <新文件> --line <断点行号>")
		flag.PrintDefaults()
		os.Exit(2)
	}

	oldSrc, err := os.ReadFile(*oldPath)
	if err != nil {
		fatal(err)
	}
	newSrc, err := os.ReadFile(*newPath)
	if err != nil {
		fatal(err)
	}

	res, err := migrate.Migrate(string(oldSrc), string(newSrc), *line)
	if err != nil {
		fatal(err)
	}

	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))

	// 退出码：0 唯一，1 冲突，3 删除，便于脚本集成。
	switch res.Status {
	case migrate.StatusUnique:
	case migrate.StatusAmbiguous:
		os.Exit(1)
	case migrate.StatusDeleted:
		os.Exit(3)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(2)
}

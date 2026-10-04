// configreduce：保持依赖合法的失败配置缩减工具。
//
// 用法：
//   configreduce -oracle "<判定命令>" [-deps <依赖文件>] <配置项...>
//
// 判定程序契约（确定性）：配置项作为命令行参数追加给判定命令；
// 退出码 0 = 通过，1 = 目标测试失败，2 = 编译失败，其余视为执行错误。
//
// 依赖文件每行格式：<配置项>: <依赖1> <依赖2> ...
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

func main() {
	oracleCmd := flag.String("oracle", "", "判定程序命令，配置项作为参数追加；退出码 0=通过 1=目标测试失败 2=编译失败")
	depsFile := flag.String("deps", "", "依赖文件，每行格式: 配置项: 依赖1 依赖2 ...")
	flag.Parse()
	items := flag.Args()

	if *oracleCmd == "" || len(items) == 0 {
		fmt.Fprintln(os.Stderr, "用法: configreduce -oracle <判定命令> [-deps <依赖文件>] <配置项...>")
		flag.PrintDefaults()
		os.Exit(2)
	}

	deps, err := loadDeps(*depsFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取依赖文件失败: %v\n", err)
		os.Exit(2)
	}

	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		return runOracle(*oracleCmd, cfg)
	})
	reducer := NewReducer(deps, oracle)

	final, err := reducer.Reduce(items)
	if err != nil {
		fmt.Fprintf(os.Stderr, "缩减失败: %v\n", err)
		os.Exit(1)
	}

	stats := reducer.Stats()
	fmt.Println("=== 缩减结果 ===")
	fmt.Printf("最终配置（%d 项）: %s\n", len(final), strings.Join(final, " "))
	fmt.Printf("实际判定次数: %d（缓存命中: %d）\n", stats.Calls, stats.CacheHits)
	fmt.Printf("判定分布: 通过=%d, 目标测试失败=%d, 编译失败=%d\n",
		stats.Pass, stats.TestFail, stats.CompileFail)
	fmt.Println("1-极小性: 已保证（任何单项删除均不再复现目标测试失败）")
}

// runOracle 执行外部判定程序并映射退出码到判定结果。
func runOracle(cmdline string, cfg []string) (Verdict, error) {
	parts := strings.Fields(cmdline)
	if len(parts) == 0 {
		return Pass, fmt.Errorf("空的判定命令")
	}
	args := append(parts[1:], cfg...)
	cmd := exec.Command(parts[0], args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	err := cmd.Run()
	if err == nil {
		return Pass, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			switch ws.ExitStatus() {
			case 1:
				return FailTest, nil
			case 2:
				return FailCompile, nil
			}
			return Pass, fmt.Errorf("判定程序退出码异常: %d", ws.ExitStatus())
		}
	}
	return Pass, fmt.Errorf("无法执行判定程序: %v", err)
}

// loadDeps 解析依赖文件；空路径表示无依赖。
func loadDeps(path string) (map[string][]string, error) {
	deps := make(map[string][]string)
	if path == "" {
		return deps, nil
	}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for lineno, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			return nil, fmt.Errorf("第 %d 行缺少 ':'", lineno+1)
		}
		item := strings.TrimSpace(line[:colon])
		if item == "" {
			return nil, fmt.Errorf("第 %d 行配置项为空", lineno+1)
		}
		for _, d := range strings.Fields(line[colon+1:]) {
			deps[item] = append(deps[item], d)
		}
	}
	return deps, nil
}

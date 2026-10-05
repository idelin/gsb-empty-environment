// lockplan：锁文件更新规划器。
// 在新增依赖时精确求解：第一优先级尽量保留原版本，第二优先级减少下载字节；
// 输出每个被迫变化的原因路径，并删除已无根依赖的包。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"lockplan/internal/plan"
)

type addFlags []string

func (a *addFlags) String() string { return strings.Join(*a, ", ") }
func (a *addFlags) Set(v string) error {
	*a = append(*a, v)
	return nil
}

func loadJSON(path string, v interface{}) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

func parseDep(spec string) (plan.Dep, error) {
	idx := strings.Index(spec, "@")
	if idx < 0 {
		return plan.Dep{Name: spec, Constraint: "*"}, nil
	}
	if idx == 0 || idx == len(spec)-1 {
		return plan.Dep{}, fmt.Errorf("非法依赖声明 %q，应为 name@constraint", spec)
	}
	return plan.Dep{Name: spec[:idx], Constraint: spec[idx+1:]}, nil
}

func main() {
	var regPath, lockPath string
	var adds addFlags
	var verify, asJSON bool
	flag.StringVar(&regPath, "registry", "registry.json", "注册表 JSON 文件")
	flag.StringVar(&lockPath, "lock", "lock.json", "锁文件 JSON 文件")
	flag.Var(&adds, "add", "新增根依赖，形如 name@constraint（可重复）")
	flag.BoolVar(&verify, "verify", false, "用穷举法核验求解结果的最优性（仅适合小规模）")
	flag.BoolVar(&asJSON, "json", false, "以 JSON 输出计划")
	flag.Parse()

	var reg plan.Registry
	if err := loadJSON(regPath, &reg); err != nil {
		fmt.Fprintln(os.Stderr, "读取注册表失败:", err)
		os.Exit(1)
	}
	if err := reg.Finalize(); err != nil {
		fmt.Fprintln(os.Stderr, "注册表非法:", err)
		os.Exit(1)
	}
	var lock plan.Lock
	if err := loadJSON(lockPath, &lock); err != nil {
		fmt.Fprintln(os.Stderr, "读取锁文件失败:", err)
		os.Exit(1)
	}
	var extra []plan.Dep
	for _, a := range adds {
		d, err := parseDep(a)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		extra = append(extra, d)
	}

	sol, c, err := plan.Solve(&reg, &lock, extra, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "求解失败:", err)
		os.Exit(1)
	}
	p := plan.BuildPlan(&reg, &lock, extra, sol)

	if verify {
		best, count, ok, err := plan.BruteSolve(&reg, &lock, extra, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "穷举核验失败:", err)
			os.Exit(1)
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "穷举核验异常：求解器有解但穷举无可行解")
			os.Exit(1)
		}
		sc := fmt.Sprintf("%d/%d", c.Changes(), c.Bytes())
		bc := fmt.Sprintf("%d/%d", best.Changes(), best.Bytes())
		if sc != bc {
			fmt.Fprintf(os.Stderr, "穷举核验不通过：求解器 %s，穷举最优 %s\n", sc, bc)
			os.Exit(1)
		}
		fmt.Printf("穷举核验通过：%d 个可行解，最优代价 %s（变更/字节）\n", count, bc)
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(p)
		return
	}
	fmt.Printf("变更 %d 包，保留 %d 包，总下载 %d 字节\n", len(p.Changes), p.Kept, p.TotalDownload)
	for _, ch := range p.Changes {
		switch ch.Kind {
		case "add":
			fmt.Printf("  + %s@%s [%s, %d 字节]\n", ch.Name, ch.NewVersion, ch.NewSource, ch.Bytes)
		case "update":
			fmt.Printf("  ~ %s %s -> %s [%s, %d 字节]\n", ch.Name, ch.OldVersion, ch.NewVersion, ch.NewSource, ch.Bytes)
		case "source":
			fmt.Printf("  ~ %s@%s 源 %s -> %s [%d 字节]\n", ch.Name, ch.NewVersion, ch.OldSource, ch.NewSource, ch.Bytes)
		case "remove":
			fmt.Printf("  - %s@%s\n", ch.Name, ch.OldVersion)
		}
		fmt.Printf("    %s\n", ch.Reason)
	}
}

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

const usage = `grayalloc — 策略灰度分配器（禁止拆组的最小迁移精确配比求解）

用法:
  grayalloc -input groups.json -target <版本> (-ratio R -tol T | -min LO -max HI) [-cert proof.json]
  grayalloc -input groups.json -verify proof.json

说明:
  升级灰度时，把若干“不可分割的用户组”整体分配到目标版本，使目标版本上的
  总用户数落入精确区间 [LO, HI]（由 -ratio/-tol 或直接 -min/-max 指定），
  并最小化迁移用户数（版本发生变化的组内全部用户）。组数上限 20，
  求解为 2^n 完备枚举，结果保证全局最优。

  无可行解时输出不可达证明（默认写标准输出，-cert 指定文件），
  可用 -verify 独立校验该证明。

退出码: 0=找到最优解  2=不可达(已输出证明)  1=参数/校验错误
`

func main() {
	var (
		inputPath  = flag.String("input", "", "输入 JSON 文件路径")
		target     = flag.String("target", "", "目标版本名（灰度版本）")
		ratio      = flag.Float64("ratio", -1, "目标人数比例 (0..1)")
		tol        = flag.Float64("tol", 0, "比例容差")
		minUsers   = flag.Int64("min", -1, "目标版本最少人数")
		maxUsers   = flag.Int64("max", -1, "目标版本最多人数")
		certPath   = flag.String("cert", "", "不可达证明输出路径（默认标准输出）")
		verifyPath = flag.String("verify", "", "校验指定的不可达证明文件")
	)
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()

	if *inputPath == "" {
		fatal("missing -input")
	}
	in, err := loadInput(*inputPath)
	if err != nil {
		fatal(err.Error())
	}
	if err := validate(in); err != nil {
		fatal(err.Error())
	}

	// 校验模式
	if *verifyPath != "" {
		p, err := loadProof(*verifyPath)
		if err != nil {
			fatal(err.Error())
		}
		if err := VerifyProof(in.Groups, p); err != nil {
			fmt.Println("证明校验失败:", err)
			os.Exit(1)
		}
		fmt.Printf("证明校验通过：区间 [%d, %d] 内不存在可达人数（可达集合 %d 个值，摘要 %s）。\n",
			p.Lo, p.Hi, p.ReachableCnt, p.SHA256[:16])
		return
	}

	if *target == "" {
		fatal("missing -target")
	}

	total := TotalUsers(in.Groups)
	var iv Interval
	switch {
	case *minUsers >= 0 && *maxUsers >= 0:
		iv = Interval{Lo: *minUsers, Hi: *maxUsers}
	case *ratio >= 0:
		iv = IntervalFromRatio(total, *ratio, *tol)
	default:
		fatal("specify -ratio/-tol or -min/-max")
	}
	if iv.Hi > total {
		iv.Hi = total
	}
	if iv.Lo < 0 {
		iv.Lo = 0
	}
	if iv.Lo > iv.Hi {
		fmt.Printf("区间 [%d, %d] 为空（总用户数 %d），无可行解。\n", iv.Lo, iv.Hi, total)
		os.Exit(2)
	}

	sol, reachable, err := Solve(in.Groups, *target, iv)
	if err != nil {
		fatal(err.Error())
	}

	if sol == nil {
		proof := BuildProof(in.Groups, iv, reachable)
		data, _ := json.MarshalIndent(proof, "", "  ")
		if *certPath != "" {
			if err := ioutil.WriteFile(*certPath, data, 0644); err != nil {
				fatal(err.Error())
			}
			fmt.Printf("不可达：证明已写入 %s\n", *certPath)
		} else {
			fmt.Println(string(data))
		}
		fmt.Printf("不可达：区间 [%d, %d] 内不存在可达人数。\n", iv.Lo, iv.Hi)
		fmt.Printf("  最接近的可达人数：下方 %d，上方 %d（总用户 %d，组数 %d）。\n",
			proof.Predecessor, proof.Successor, total, len(in.Groups))
		if proof.ArithProof {
			fmt.Printf("  算术证明：所有组人数均为 %d 的倍数，而区间内无 %d 的倍数。\n",
				proof.GCD, proof.GCD)
		} else {
			fmt.Printf("  完备枚举证明：全部 2^%d 种按组分配均不可行（证书含完整可达集合，可用 -verify 校验）。\n",
				len(in.Groups))
		}
		os.Exit(2)
	}

	// 输出最优方案
	fmt.Printf("最优方案：目标版本 %q 人数 %d ∈ [%d, %d]（总用户 %d，比例 %.4f）\n",
		*target, sol.NewUsers, iv.Lo, iv.Hi, total, float64(sol.NewUsers)/float64(total))
	fmt.Printf("迁移用户数 %d（%d 个组），已枚举全部 2^%d 种分配，结果全局最优。\n",
		sol.MovedUsers, sol.MovedGroups, len(in.Groups))
	for i, g := range in.Groups {
		onTarget := sol.Mask&(1<<uint(i)) != 0
		cur := g.Version == *target
		switch {
		case onTarget && !cur:
			fmt.Printf("  迁移   %-16s %8d 用户  %s -> %s\n", g.Name, g.Users, g.Version, *target)
		case !onTarget && cur:
			fmt.Printf("  回退   %-16s %8d 用户  %s -> 非%s\n", g.Name, g.Users, g.Version, *target)
		}
	}
}

func loadInput(path string) (*Input, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

func loadProof(path string) (*Proof, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Proof
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func validate(in *Input) error {
	if len(in.Groups) == 0 {
		return fmt.Errorf("no groups")
	}
	if len(in.Groups) > MaxGroups {
		return fmt.Errorf("too many groups: %d > %d", len(in.Groups), MaxGroups)
	}
	seen := map[string]bool{}
	for _, g := range in.Groups {
		if g.Name == "" {
			return fmt.Errorf("group with empty name")
		}
		if seen[g.Name] {
			return fmt.Errorf("duplicate group name %q", g.Name)
		}
		seen[g.Name] = true
		if g.Users < 0 {
			return fmt.Errorf("group %q has negative users", g.Name)
		}
	}
	return nil
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}

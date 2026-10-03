package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	csp "example.com/backjumpingcsp"
)

func main() {
	problem := csp.NewTenVariableDemoProblem()
	result, err := csp.Solve(problem)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !result.Solved {
		fmt.Println("问题无解")
		return
	}

	fmt.Println("10 变量 CSP 冲突定向回跳演示")
	fmt.Println("约束：x8 仅在 x2=1 时有合法取值；x3 至 x7 与该冲突无关")
	for _, jump := range result.Stats.Backjumps {
		fmt.Printf("冲突集：{%s}\n", variableList(problem, jump.ConflictSet))
		fmt.Printf(
			"回跳：第 %d 步 -> 第 %d 步（跳过第 %s 步）\n",
			jump.From+1,
			jump.To+1,
			stepList(jump.Skipped),
		)
	}
	fmt.Printf("解：%s\n", assignmentList(problem, result.Assignment))
	fmt.Printf(
		"统计：赋值 %d 次，一致性检查 %d 次，拒绝取值 %d 个，回跳 %d 次\n",
		result.Stats.Assignments,
		result.Stats.ConsistencyChecks,
		result.Stats.RejectedValues,
		len(result.Stats.Backjumps),
	)
}

func variableList(problem csp.Problem, indexes []int) string {
	names := make([]string, 0, len(indexes))
	for _, index := range indexes {
		names = append(names, problem.Variables[index].Name)
	}
	return strings.Join(names, ", ")
}

func stepList(indexes []int) string {
	if len(indexes) == 0 {
		return "无"
	}
	steps := make([]string, 0, len(indexes))
	for _, index := range indexes {
		steps = append(steps, strconv.Itoa(index+1))
	}
	return strings.Join(steps, ", ")
}

func assignmentList(problem csp.Problem, assignment []int) string {
	parts := make([]string, 0, len(assignment))
	for index, value := range assignment {
		parts = append(parts, fmt.Sprintf("%s=%d", problem.Variables[index].Name, value))
	}
	return strings.Join(parts, ", ")
}

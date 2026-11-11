package pathmonitor_test

import (
	"fmt"

	"example.org/netsec/q0474/pathmonitor"
)

// ExampleMonitor 演示测试宿主如何断言“强认证失败只能进入受限恢复，
// 不得回落为普通登录成功”。
func ExampleMonitor() {
	states := []pathmonitor.State{
		{ID: "login", Level: pathmonitor.LevelAuthenticated},
		{ID: "strong", Level: pathmonitor.LevelStrong},
		{ID: "recovery", Level: 5, Success: true, Restricted: true},
		{ID: "session", Level: pathmonitor.LevelAuthenticated, Success: true},
	}
	edges := []pathmonitor.Edge{
		{From: "login", To: "strong", Kind: pathmonitor.EdgeNormal, Label: "upgrade"},
		{From: "strong", To: "recovery", Kind: pathmonitor.EdgeFailure, Label: "mfa-failed"},
		{From: "strong", To: "session", Kind: pathmonitor.EdgeFailure, Label: "fallback-login"},
	}
	monitor, err := pathmonitor.New(states, edges, "login")
	if err != nil {
		panic(err)
	}
	report := monitor.Check()

	for _, v := range report.ViolationsOf(pathmonitor.ViolationTaintedSuccess) {
		fmt.Println("violation:", v.Kind, "at", v.Exit)
	}
	for _, p := range report.Downgraded {
		fmt.Println("downgraded to:", p.Exit, "weakest:", p.WeakestLevel)
	}
	// Output:
	// violation: tainted-success at session
	// downgraded to: recovery weakest: 5
	// downgraded to: session weakest: 10
}

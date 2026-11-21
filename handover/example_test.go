package handover_test

import (
	"fmt"

	"prescreen.local/ddd-10451-v2/handover"
)

// 演示宿主如何基于抽象句柄表与角色声明生成、校验并提交交接计划。
func ExampleBuildPlan() {
	tab := &handover.Table{Handles: []handover.Handle{
		{Slot: 1, Gen: 11, Object: "config", Perm: handover.PermRead},
		{Slot: 2, Gen: 7, Object: "config", Perm: handover.PermRead | handover.PermWrite},
		{Slot: 3, Gen: 3, Object: "data", Perm: handover.PermRead},
	}}
	roles := []handover.Role{
		{
			Name:    "config-reader",
			Target:  20,
			Allowed: handover.Allow("config"),
			Need:    handover.PermRead, // 槽2带额外写权限，自动被排除
			Share:   handover.ShareWith(),
		},
		{
			Name:    "data-reader",
			Target:  21,
			Allowed: handover.Allow("data"),
			Need:    handover.PermRead,
			Share:   handover.ShareWith(),
		},
	}

	plan, err := handover.BuildPlan(tab, roles, 99)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	if err := plan.Validate(tab); err != nil {
		fmt.Println("invalid:", err)
		return
	}

	mem := handover.NewMemTable(tab)
	if err := plan.Commit(mem); err != nil {
		fmt.Println("commit:", err)
		return
	}

	h20, _ := mem.HandleAt(20)
	h21, _ := mem.HandleAt(21)
	fmt.Printf("slot20=%s %s inherited=%v\n", h20.Object, h20.Perm, !h20.Cloexec)
	fmt.Printf("slot21=%s %s inherited=%v\n", h21.Object, h21.Perm, !h21.Cloexec)
	// Output:
	// slot20=config r inherited=true
	// slot21=data r inherited=true
}

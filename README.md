# 备份恢复授权闭包

恢复一个目录会间接恢复它引用的秘密对象，目录级许可并不够。实现供Go恢复宿主调用的规划包，输入对象引用图与逐对象许可，计算可恢复闭包，任一必需依赖未授权则拒绝整个恢复；可选依赖显式留空，不悄悄拉取。用循环引用、共享秘密和撤销发生于计划与发布之间的场景验证最终复核。

## 行为约定

- 每个根对象以及每个被必需引用到达的对象，都单独检查 `Permissions`。目录授权不会传递给目录中的秘密对象。
- 任一必需对象缺失或未授权时，`PlanRestore` 不返回计划，整个恢复被拒绝。
- `Reference.Optional` 必须显式设为 `true` 才是可选引用；零值引用按必需处理。计划从不跟随可选引用，而是在 `OmittedOptional` 中明确列出这些边，恢复宿主应把它们置空。
- `Plan` 会复制对象图和许可快照。`Publish` 在发布前重新计算必需闭包，并逐对象检查当前许可；撤销、撤销后重授（revision 变化）或闭包变化都会拒绝发布。
- 循环引用和共享秘密按对象 ID 去重，每个对象只进入闭包一次。

## 使用示例

```go
graph := netsec.Graph{
	"dir:root": {References: []netsec.Reference{
		{Target: "secret:db"},
		{Target: "secret:preview", Optional: true},
	}},
	"secret:db":      {},
	"secret:preview": {},
}

permissions := netsec.Permissions{}
permissions.Grant("dir:root")
permissions.Grant("secret:db")

plan, err := netsec.PlanRestore(graph, permissions, "dir:root")
if err != nil {
	return err
}

// 可选依赖不会进入 Objects；manifest 明确要求把它恢复为空。
manifest, err := plan.Publish(permissions)
if err != nil {
	return err
}

for _, id := range manifest.Objects() {
	// 恢复宿主只恢复 manifest 中的对象。
	_ = id
}
```

`Manifest` 是最终复核后的唯一发布输入。恢复宿主应直接执行该清单，不应再次按原始图遍历或隐式拉取 `OmittedOptional` 中的对象。

`Publish` 保证的是复核时刻的授权状态。宿主应在持有许可状态锁时调用 `Publish`，并在释放锁前开始执行返回的清单，避免复核完成后又被撤销的竞态。

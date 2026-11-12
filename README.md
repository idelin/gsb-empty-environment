# 依赖可选边锁定差异

安全更新只改一项依赖，却可能因平台条件激活额外安装钩子。实现供Go构建宿主调用的锁定差分包，输入两版依赖图与最多16个布尔平台条件，边可带可选条件，节点标记安装期能力；求任一配置新增的能力及最小依赖路径。用配置全枚举核对未激活节点不能误报、共享依赖不能漏报。

## 语义

- `Compare(old, new)` 对两版图取平台条件并集，并穷举全部分配（最多 `2^16` 种）。
- 每个能力只有在**同一配置**下“新版可达、旧版不可达”时才算新增；旧版在别的配置下拥有同名能力不会掩盖新增。
- 每个新增能力返回一个见证配置 `Config`，以及新版图中从根到能力节点的最短依赖路径。等长路径按节点序列字典序稳定选择。
- 未激活节点（条件不成立或不可达）不会产生结果；同一共享节点被多条分支依赖时，只要在任一配置下形成新增能力，仍会报告。
- 输入图会校验根节点、重复节点、未知边端点、空平台名，以及平台变量总数不超过 16。

## 用法

```go
oldGraph := netsec.Graph{
	Root: "app",
	Nodes: []netsec.Node{
		{ID: "app"},
		{ID: "tool"},
	},
	Edges: []netsec.Edge{
		{From: "app", To: "tool", When: netsec.Flag("linux")},
	},
}

newGraph := netsec.Graph{
	Root: "app",
	Nodes: []netsec.Node{
		{ID: "app"},
		{ID: "tool", Capabilities: []string{"postinstall"}},
	},
	Edges: oldGraph.Edges,
}

report, err := netsec.Compare(oldGraph, newGraph)
if err != nil {
	panic(err)
}
for _, added := range report.Added {
	fmt.Println(added.Capability, added.PathString(), added.Config)
}
// postinstall app -> tool {linux=true}
```

条件可用 `Always`、`Never`、`Flag`、`NotFlag`、`When`、`Not`、`And`/`All`、`Or`/`Any` 组合；`Condition` 的零值等价于 `Always`。

## 验证

测试包含独立的配置全枚举 oracle：随机生成旧/新依赖图，用直接 BFS 逐配置计算新增能力及最短路径长度，再与 `Compare` 结果逐项核对；另有针对未激活节点误报、共享依赖漏报、条件淘汰和 16 条件上限的用例。

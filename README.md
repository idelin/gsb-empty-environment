# 反序列化对象能力隔离

`netsec` 是可直接嵌入 Go 宿主解码层的能力恢复包。它把攻击者控制的有界对象图转换为插件可用的对象图，并在能力边界原子执行租户、代次和撤销策略。

## 输入模型

`Graph` 使用节点下标作为身份，最多 `MaxGraphNodes`（1024）个节点。共享与环由重复的 `NodeID` 表示。

- `KindString`：字符串，数据在 `String`。
- `KindArray`：数组，子节点在 `Array`。
- `KindDict`：字典，子节点在 `Dict`。
- `KindObjectRef`：对象引用，目标在 `Target`；只能沿引用链指向字符串、数组或字典。
- `KindCapabilityRef`：能力引用，只携带 `Token`。

数组和字典可以直接包含任意节点。能力节点是终端，解码后只暴露租户、操作集合和代次，不暴露 bearer token。

## 使用方式

```go
registry := netsec.NewRegistry()
token, err := netsec.NewCapabilityToken()
if err != nil {
	return err
}
if err := registry.Register(token, "tenant-a", 12, "read", "write"); err != nil {
	return err
}

graph := netsec.Graph{
	Root: 0,
	Nodes: []netsec.Node{
		{Kind: netsec.KindDict, Dict: map[string]netsec.NodeID{
			"same-1": 1,
			"same-2": 1,
			"cap":    2,
		}},
		{Kind: netsec.KindString, String: "shared"},
		{Kind: netsec.KindCapabilityRef, Token: token},
	},
}

value, leases, err := netsec.Decode(
	graph,
	netsec.Principal{Tenant: "tenant-a", Generation: 12},
	registry,
)
if err != nil {
	return err
}
defer leases.Release()
```

`value.Dict["same-1"] == value.Dict["same-2"]`，重复能力 token 也只对应一个共享的 `Capability` 和一个租约。

## 安全不变量

- 解码先验证全部节点和引用，再一次性申请所有可达 token 的租约；失败返回 `nil` 对象和 `nil` 租约。
- `Registry.AcquireAll` 在同一把锁内检查全部 token 的租户、代次和撤销状态，再创建全部租约，不存在部分成功。
- 未知 token、跨租户 token、旧代次 token 和已撤销 token 统一返回 `ErrCapabilityDenied`，避免能力探测。
- `Revoke` 返回后，后续解码不能取得新租约；已经取得的租约仍保持有效。
- 重复 token 去重后只计一个租约；`LeaseSet.Release` 并发调用和重复调用均幂等。
- 经 `KindObjectRef` 的别名会解析到同一个 `*Value`，数组或字典环保持原有身份。

## 测试

```sh
go test -race ./...
```

测试覆盖跨租户、旧代次、悬空引用、对象引用环、1024 节点深环、正常共享对象、租约幂等释放，以及用可控闸门分别调度“撤销先于批量取租约”和“批量取租约先于撤销”的竞态。

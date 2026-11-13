# Kata代理请求字段级策略

Kata Containers 的宿主侧 shim 通过 ttrpc 调用虚拟机内的 kata-agent，请求不受信的镜像规格不应让 agent 接受宿主没有批准的创建与执行。为 Go 宿主写供 agent 请求拦截调用的策略包：输入请求方法名与载荷（创建容器请求里的运行规格片段：启动参数列表、环境变量、挂载、特权标志、能力集、设备）及调用方登记的规则集（本题规定为字段级匹配：精确值、前缀、正则、禁止出现的键），默认拒绝；CopyFile 的路径须在容器根之下，规范化后不含 .. 且不经符号链接穿出；ExecProcess 的启动参数须与创建时登记的参数之一相同；能力集不得超出创建时声明；特权标志为真拒绝；方法名不在规则集拒绝。用 CopyFile 穿越、未登记的执行、能力提升和合规创建验证。

## 接入方式

```go
rules := netsec.Rules{
	AllowedMethods: []string{
		netsec.MethodCreateContainer,
		netsec.MethodExecProcess,
		netsec.MethodCopyFile,
	},
	Methods: map[string]netsec.MethodRules{
		netsec.MethodCreateContainer: {
			Fields: map[string]netsec.FieldRule{
				"args": {Regex: []string{`^/bin/(sh|bash)$`}},
				"env":  {Prefix: []string{"PATH="}, ForbiddenKeys: []string{"LD_PRELOAD"}},
			},
		},
	},
}

policy := netsec.NewPolicy(rules)
decision := policy.Evaluate(netsec.Request{
	Method: netsec.MethodCreateContainer,
	Payload: netsec.Payload{
		ContainerID:  "container-1",
		Root:         "/var/lib/kata/container-1/rootfs",
		Args:         []string{"/bin/sh"},
		Capabilities: []string{"CAP_CHOWN"},
	},
})
if !decision.Allowed {
	log.Printf("kata-agent request denied: %s", decision)
	return decision.Err(netsec.MethodCreateContainer)
}
```

`Evaluate`、`Check` 和 `Authorize` 返回 `Decision`；`Validate` 返回可直接传给拦截器的
`error`。规则集没有登记的方法默认拒绝，载荷无法解析也默认拒绝。

## 规则语义

- `AllowedMethods` 或 `Methods` 中任一登记即可授权方法；两者都为空时全部拒绝。
- `Fields` 中的 `Exact`、`Prefix`、`Regex` 为字段级并集；字段存在受约束值时，每个值都必须命中其中之一。
- `ForbiddenKeys`/`DenyKeys` 在环境变量键、对象键和字段值上优先拒绝；`ForbiddenKeysAny` 扫描整个载荷。
- 支持从 `process`、`linux`、`spec`、`runtime_spec` 嵌套读取 OCI 字段，也支持直接传入 JSON map 或 JSON 字节。
- `CreateContainer` 成功后，`Args` 和 `Capabilities` 会登记到该 `ContainerID`。
- `ExecProcess` 的参数必须与创建登记的参数列表逐项完全一致，能力必须是创建能力集的子集。
- 任意请求的 `privileged: true` 都拒绝，即使规则集没有显式配置该字段。
- `CopyFile` 必须提供已登记的容器根或请求中的可信根；路径拒绝 `..`、URL 编码/Windows 风格穿越、NUL，并解析已有符号链接，确保最终仍在根内。

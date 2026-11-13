# 异常处理表把控制流导向函数中部

不可信模块提交异常处理元数据，若landing pad不受函数边界和指令边界约束，可把抛异常变成任意控制流跳转。给Go加载宿主实现题内异常表验证包：模块有不可变代码字节、可信解码器给出的指令区间和函数区间；函数区间互不重叠且完整由指令覆盖。异常项含函数ID、受保护半开区间[start,end)、landingPad、action链起点及personalityID；区间和landingPad均为模块相对地址。受保护区间须非空、落在对应函数且端点是指令边界；landingPad须是同函数内由宿主解码器标记的已登记landing指令的起点，不能位于受保护区间或函数入口前缀。action链节点含typeID、next索引和cleanup位，next只能向更大索引或终止，长度有预算，typeID须在函数类型表；同一函数保护区间可嵌套但不能交叉，嵌套时内层优先。personalityID从宿主白名单解析并绑定模块摘要，元数据不能自带函数指针。运行查询给抛出PC和typeID，返回最内层匹配项的landingPad及action证据；未匹配只可unwind。用逐地址参考测试跳入指令中部、跨函数区间、交叉表项、action环、伪personality和合法嵌套，验证失败不发布任何handler。

## 使用

```go
digest := netsec.ComputeModuleDigest(mod)
reg := netsec.NewPersonalityRegistry()
_ = reg.Register(digest, "host.eh.personality")

table, err := netsec.Compile(mod, untrustedMetadata, reg)
if err != nil {
	// Compile returns a nil table; no handler is published.
	return err
}

res := table.Resolve(throwingPC, typeID)
if res.Unwind {
	return unwind
}
enterAt(res.LandingPad, res.Actions, res.Personality)
```

## 验证边界

- `Module.Code` 在编译时复制。`Table` 发布后，调用方再修改输入切片不会影响查询结果。
- 指令区间必须非空、按地址不重叠且落在代码字节内。函数区间必须非空、互不重叠，并由连续指令完整覆盖。
- 受保护区间端点必须是同函数内的指令边界；landing pad 必须是宿主解码器标记的 landing 指令起点，且不能落在本项保护区间、函数入口地址或 `Function.EntryPrefixEnd` 之前的入口前缀。
- `ActionNode.Next` 仅允许 `NoAction` 或严格更大的索引；每条引用链还受 `Limits.MaxActionChain` 预算约束，所有节点 typeID 必须属于对应函数的类型表。
- 同函数保护区间只允许嵌套或相同范围，不允许交叉。查询按包含 PC 且 action 链匹配 typeID 的最内层区间选择 landing pad；无匹配时只返回 `Unwind`。
- personality 只按 `(模块摘要, personalityID)` 在宿主白名单中解析，元数据结构中没有任何函数指针。
- `Compile` 是全有或全无的：任一检查失败都返回 `nil` table 和带 `ErrorKind` 的 `ValidationError`。

测试覆盖逐地址参考查询、指令中部 landing pad、跨函数区间、交叉表项、action 环/超预算、伪 personality、合法嵌套以及失败不发布 handler。

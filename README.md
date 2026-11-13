# 动态符号抢占后的鉴权绕过

插件装入后可能抢占主程序调用的鉴权符号，签名正确的主程序因此调用到不可信实现。实现供Go加载审查宿主调用的符号绑定包，采用本题自定义规则，不声称实现某平台链接器：总计最多16个模块；模块有唯一ID、信任位、有序依赖及按(名称,版本)唯一的导出表，导出分强弱两类；全局模块列表由宿主给顺序。重定位点包含发起模块、名称、精确版本、是否允许外部抢占及是否要求可信目标。允许抢占时先查全局列表再查发起模块依赖的广度优先序列，不允许时先查发起模块；其余仍按上述顺序，去重保留首次，缺失依赖或重名同版本导出是输入错误。搜索完整序列后选第一个强定义，没有强定义才选首个弱定义，版本必须精确一致。攻击者只可把至多3个给定插件插入全局列表的任意位置，原模块相对次序不变；搜索使某要求可信的鉴权重定位最终绑定不可信导出的最少插件方案，并输出完整查找与胜出理由。输出所有等长方案前最多20个，按插件ID及位置排序，注明截断。覆盖弱定义被后方强定义抢占、依赖环、版本不同不命中和禁止抢占时本地强定义，预算耗尽返回未知。

## 包接口

模块路径为 `example.org/netsec/q0020`，包名为 `netsec`。

- `Export{Name, Version, Strong}`：模块内以 `(Name, Version)` 唯一的导出；`Strong=false` 表示弱定义。
- `Module{ID, Trusted, Dependencies, Exports}`：`Dependencies` 按声明顺序参与广度优先遍历，允许环。
- `Relocation{Origin, Name, Version, AllowPreemption, RequireTrusted}`：一次符号引用。
- `Resolve(modules, globalOrder, relocation)`：返回 `Binding`。它只对输入错误返回 `error`；未命中、可信要求不满足等结果通过字段表达。
- `Analyze(modules, globalOrder, plugins, relocation)`：无布局预算，返回 `Audit`。
- `AnalyzeBudget(modules, globalOrder, plugins, relocation, budget)`：`budget` 是检查的不同最终全局顺序数；`0` 表示不检查，负数表示无预算限制。

`plugins` 是模块 ID 列表（模块本身也包含在 `modules` 中），最多 3 个，且不得已在全局列表中。全部声明模块（含插件）最多 16 个。

## 查找规则

`AllowPreemption=true` 时的序列为：全局列表，然后从发起模块的有序依赖开始广度优先遍历。

`AllowPreemption=false` 时的序列为：发起模块，然后全局列表，再按同样的广度优先方式遍历依赖。

两种序列都按模块 ID 去重并保留第一次出现。发起模块是依赖遍历的根，即使它不在全局列表中，也不会因依赖环而再次成为依赖候选。完整序列扫描结束后，第一个强定义胜出；只有完全没有强定义时才选择第一个弱定义。名称与版本必须同时精确相等。

`Binding.Sequence` 与 `Binding.Lookup` 给出完整查找轨迹，`Binding.Selection` 和 `Binding.Explanation` 给出胜出方式与理由。`Binding.Report()` 输出可直接记录的文本。

## 插件搜索

`Analyze`/`AnalyzeBudget` 从零个插件开始逐层搜索，找到的第一层即最少插件数。只有 `RequireTrusted=true` 时，绑定到不可信模块才算违规；如果零插件配置本身已违规，结果就是零插件方案。

方案中的 `Insertion.Position` 是插件在插入后的全局列表中的零下标位置。每个方案的 `Insertions` 按 `(ModuleID, Position)` 排序；方案之间也按该键排序。`Audit.TotalPlans` 是全部等长方案数，`Audit.Plans` 最多给出前 20 个，`Audit.Truncated` 表示是否发生截断。`Plan.Binding` 保留该方案下的完整查找与胜出理由。

预算不足以完成当前层时返回 `AuditUnknown`，且不输出可能不完整的方案集合；穷举完成且无违规方案时返回 `AuditImpossible`。

## 最小示例

```go
modules := []netsec.Module{
	{ID: "app", Trusted: true, Exports: []netsec.Export{
		{Name: "auth", Version: "1.0", Strong: false},
	}},
	{ID: "vendor", Trusted: true, Exports: []netsec.Export{
		{Name: "auth", Version: "1.0", Strong: true},
	}},
}

binding, err := netsec.Resolve(modules, []string{"app", "vendor"}, netsec.Relocation{
	Origin: "app", Name: "auth", Version: "1.0", AllowPreemption: true,
})
if err != nil {
	log.Fatal(err)
}
// binding.ModuleID == "vendor"：弱定义之后出现强定义时，强定义胜出。
```

运行验证：`go test ./...`。
